package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/overworks/mybox-cli/internal/transfer"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newDownCommand(g *globals) *cobra.Command {
	var (
		outDir    string
		overwrite bool
	)

	cmd := &cobra.Command{
		Use:     "down path...",
		Aliases: []string{"download", "get"},
		Short:   "Download files",
		Long: `Downloads files from MYBOX to your machine.

Pass -o - to write to standard output instead, for piping.
Downloads have a daily budget: 500 to 50,000 a day, depending on plan.`,
		Example: `  mybox down /문서/회의록.pdf
  mybox down /문서/회의록.pdf -o ~/Downloads/
  mybox down /문서/회의록.pdf -o - | less`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			toStdout := outDir == "-"
			if toStdout && len(args) > 1 {
				return usagef("-o - works with a single file only")
			}

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			tc := transfer.New("mybox-cli/"+Version, g.traceFunc())

			for _, arg := range args {
				target, err := r.ResolveFile(ctx, arg)
				if err != nil {
					return err
				}
				ticket, err := r.Client().CreateDownloadURL(ctx, target.ID)
				if err != nil {
					return fmt.Errorf("%s: %w", target.Describe(), err)
				}

				name := downloadName(target)
				if toStdout {
					if _, err := tc.Download(ctx, ticket.DownloadURL, g.stdout); err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					continue
				}

				dst := filepath.Join(outDir, name)
				n, err := g.downloadToFile(ctx, tc, ticket.DownloadURL, dst, name, overwrite)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				p.Info("downloaded %s (%s)", dst, transferSize(n))
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&outDir, "out", "o", ".", "directory to save into; - writes to standard output")
	f.BoolVar(&overwrite, "overwrite", false, "overwrite an existing local file")
	return cmd
}

// downloadToFile streams to a temporary file in the destination directory and
// renames it into place, so an interrupted download never leaves a truncated
// file where a complete one is expected.
func (g *globals) downloadToFile(ctx context.Context, tc *transfer.Client, url, dst, label string, overwrite bool) (int64, error) {
	if !overwrite {
		if _, err := os.Stat(dst); err == nil {
			return 0, fmt.Errorf("%s already exists; pass --overwrite to replace it", dst)
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, err
		}
	}

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(dir, ".mybox-download-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	// Removed unconditionally: a no-op once the rename has consumed it, and the
	// cleanup path when anything above fails.
	defer os.Remove(tmpName)

	var prog *transfer.Progress
	if g.showProgress() {
		prog = transfer.NewProgress(g.stderr, label, 0)
	}

	n, err := tc.Download(ctx, url, prog.Wrap(tmp))
	if err != nil {
		prog.Abort()
		tmp.Close()
		return n, err
	}
	prog.Done()

	if err := tmp.Close(); err != nil {
		return n, err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return n, fmt.Errorf("could not save the file: %w", err)
	}
	return n, nil
}

// downloadName picks the local file name for a downloaded resource.
func downloadName(t resolve.Target) string {
	if t.Item != nil && t.Item.Name != "" {
		return t.Item.Name
	}
	if t.Path != "" {
		_, name := resolve.Parent(t.Path)
		return name
	}
	return t.ID
}

// showProgress reports whether a live progress line makes sense: only on a
// terminal, and not when the user asked for quiet or machine-readable output.
func (g *globals) showProgress() bool {
	if g.quiet || g.json {
		return false
	}
	f, ok := g.stderr.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// traceFunc returns the transfer tracer for --verbose, or nil.
func (g *globals) traceFunc() func(string) {
	if !g.verbose {
		return nil
	}
	return func(s string) { fmt.Fprintln(g.stderr, "[storage] "+s) }
}

func transferSize(n int64) string { return bytesOf(n) }
