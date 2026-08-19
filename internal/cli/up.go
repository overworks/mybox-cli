package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/overworks/mybox-cli/internal/transfer"
	"github.com/spf13/cobra"
)

func newUpCommand(g *globals) *cobra.Command {
	var (
		overwrite bool
		resume    bool
		strategy  string
	)

	cmd := &cobra.Command{
		Use:     "up local-file... [folder]",
		Aliases: []string{"upload", "put"},
		Short:   "Upload local files",
		Long: `Uploads local files into a MYBOX folder, or into the root if none is given.

--resume continues an interrupted upload from wherever MYBOX still holds the
bytes. It cannot be combined with --overwrite: asking to overwrite tells MYBOX
to start the file again, which reports a resume offset of zero.

Naver does not document the upload wire format, but it has been established
against the live service and is what mybox uses by default (see
docs/api-reference.md). If the format changes and uploads start failing with
400 or 404, measure it again with 'mybox debug upload-probe' and select the
result with --strategy.`,
		Example: `  mybox up ./report.pdf /업무자료
  mybox up ./a.txt ./b.txt /업무자료 --overwrite
  mybox up ./archive.zip /백업 --resume`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			locals, destRef := splitUploadArgs(args)
			if len(locals) == 0 {
				return usagef("name at least one local file to upload")
			}

			strat, err := resolveUploadStrategy(strategy)
			if err != nil {
				return err
			}

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			dest, err := r.ResolveFolder(ctx, destRef)
			if err != nil {
				return err
			}

			// The account's per-file ceiling is worth checking once up front:
			// it turns a wasted upload into an immediate, clear refusal.
			st, err := r.Client().GetStorage(ctx)
			if err != nil {
				return err
			}

			tc := transfer.New("mybox-cli/"+Version, g.traceFunc())
			for _, local := range locals {
				if err := g.uploadOne(ctx, r, tc, local, dest, uploadOptions{
					Overwrite:    overwrite,
					Resume:       resume,
					Strategy:     strat,
					MaxFileBytes: st.MaxFileBytes,
				}); err != nil {
					return fmt.Errorf("%s: %w", local, err)
				}
				p.Info("uploaded %s -> %s", local, dest.Describe())
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&overwrite, "overwrite", false, "replace an existing file of the same name")
	f.BoolVar(&resume, "resume", false, "continue an interrupted upload; cannot be combined with --overwrite")
	f.StringVar(&strategy, "strategy", "", "override the upload wire format (default: post-multipart)")
	return cmd
}

type uploadOptions struct {
	Overwrite    bool
	Resume       bool
	Strategy     transfer.Strategy
	MaxFileBytes int64
}

func (g *globals) uploadOne(ctx context.Context, r *resolve.Resolver, tc *transfer.Client, local string, dest resolve.Target, opts uploadOptions) error {
	info, err := os.Stat(local)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("this is a directory; name a file instead")
	}
	if opts.MaxFileBytes > 0 && info.Size() > opts.MaxFileBytes {
		return fmt.Errorf("file is too large: %s, and this account allows at most %s", bytesOf(info.Size()), bytesOf(opts.MaxFileBytes))
	}

	name := filepath.Base(local)
	req := api.UploadRequest{
		FileName: name,
		FileSize: info.Size(),
		ParentID: dest.ID,
	}
	if opts.Resume {
		// MYBOX identifies an interrupted upload by the exact modifiedTime the
		// reservation carried, matched as a literal string and only ever in
		// KST. The same instant spelled +00:00 reads as a different file and
		// silently restarts the transfer, so convert rather than use local time.
		req.Resume = true
		req.ModifiedTime = uploadModifiedTime(info.ModTime())

		// Reserving with isOverwrite reports offset 0 — asking to overwrite
		// means starting the file over. The retained bytes survive, so the two
		// options are mutually exclusive per reservation, not permanently.
		if opts.Overwrite {
			g.Printer().Warn("%s: --overwrite cannot be combined with --resume, so it is being ignored", name)
		}
	} else {
		req.Overwrite = opts.Overwrite
	}

	ticket, err := r.Client().CreateUploadURL(ctx, req)
	if err != nil {
		return err
	}

	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	if ticket.Offset > 0 {
		if ticket.Offset > info.Size() {
			return fmt.Errorf("the server reported a resume offset of %d, past the file's own size of %d",
				ticket.Offset, info.Size())
		}
		if _, err := f.Seek(ticket.Offset, io.SeekStart); err != nil {
			return err
		}
		g.Printer().Info("%s: resuming from %s", name, bytesOf(ticket.Offset))
	}

	var body io.Reader = f
	var prog *transfer.Progress
	if g.showProgress() {
		prog = transfer.NewProgress(g.stderr, name, info.Size())
		body = &progressReader{r: f, prog: prog, sent: ticket.Offset}
	}

	res, err := tc.Upload(ctx, transfer.UploadRequest{
		URL:      ticket.UploadURL,
		Body:     body,
		FileName: name,
		Size:     info.Size(),
		Offset:   ticket.Offset,
		Strategy: opts.Strategy,
	})
	if err != nil {
		prog.Abort()
		return annotateUploadError(err, opts.Strategy)
	}
	prog.Done()

	// The storage host reports the byte count it stored. A mismatch on a fresh
	// upload means the file on MYBOX is not the file on disk, which is worth
	// failing over rather than reporting success. Resumed uploads are excluded:
	// whether the reported size covers the whole file or only this request's
	// bytes has not been established, and guessing wrong would reject every
	// successful resume.
	if ticket.Offset == 0 && res.FileSize != 0 && res.FileSize != info.Size() {
		return fmt.Errorf("stored size does not match: sent %s, stored %s",
			bytesOf(info.Size()), bytesOf(res.FileSize))
	}

	// The upload created or replaced an entry, so record where it landed.
	if dest.Path != "" {
		stored := res.Name
		if stored == "" {
			stored = name
		}
		r.Cache().Put(resolve.Join(dest.Path, stored), res.ResourceID, api.TypeFile)
	}
	return nil
}

// kst is the zone MYBOX reports and matches timestamps in.
var kst = time.FixedZone("KST", 9*60*60)

// uploadModifiedTime renders a file's modification time the way MYBOX matches it.
//
// MYBOX identifies an interrupted upload by the exact modifiedTime string its
// reservation carried, and only ever recognises the KST spelling. The same
// instant written as +00:00 reads as a different file, and the upload silently
// restarts from zero instead of resuming — no error, just a slow surprise. So
// the conversion happens here rather than relying on the host's zone.
func uploadModifiedTime(t time.Time) string {
	return t.In(kst).Format(time.RFC3339)
}

// annotateUploadError points the user at the probe when the storage host rejects
// the request, since the wire format is a guess rather than a documented fact.
func annotateUploadError(err error, s transfer.Strategy) error {
	var se *transfer.StorageError
	if !errors.As(err, &se) {
		return err
	}
	switch se.Status {
	case 400, 404, 405, 415, 501:
		return fmt.Errorf("%w\n  The storage host rejected the %q wire format. "+
			"Find one that works with 'mybox debug upload-probe <file>', then select it with --strategy.", err, s.Name)
	case 423:
		// The storage tier holds a brief lock right after a transfer dies.
		return fmt.Errorf("%w\n  A file stays locked for a few seconds after a transfer dies. Try again shortly.", err)
	}
	return err
}

// splitUploadArgs separates local files from the destination folder. With a
// single argument there is no destination, so the file goes to the root.
func splitUploadArgs(args []string) (locals []string, dest string) {
	if len(args) == 1 {
		return args, resolve.RootPath
	}
	return args[:len(args)-1], args[len(args)-1]
}

func resolveUploadStrategy(name string) (transfer.Strategy, error) {
	if name != "" {
		s, err := transfer.StrategyByName(name)
		if err != nil {
			return transfer.Strategy{}, &UsageError{Err: err}
		}
		return s, nil
	}
	return transfer.ResolveStrategy()
}

// progressReader reports progress as an upload body is consumed.
type progressReader struct {
	r    io.Reader
	prog *transfer.Progress
	sent int64
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	if n > 0 {
		pr.sent += int64(n)
		pr.prog.Set(pr.sent)
	}
	return n, err
}
