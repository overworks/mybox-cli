package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/overworks/mybox-cli/internal/transfer"
	"github.com/spf13/cobra"
)

func newDebugCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "debug",
		Short:  "Diagnostics",
		Hidden: true,
	}
	cmd.AddCommand(newUploadProbeCommand(g))
	return cmd
}

func newUploadProbeCommand(g *globals) *cobra.Command {
	var (
		dest    string
		keep    bool
		cleanup bool
	)

	cmd := &cobra.Command{
		Use:   "upload-probe local-file",
		Short: "Measure which upload wire format the storage host accepts",
		Long: `MYBOX documents the API that reserves an upload URL but not what to send to
that URL. The current default (post-multipart) was established against the
live service, so this command is not normally needed.

It exists for the case where Naver changes the format and uploads start
failing with 400 or 404: it tries each candidate in turn and reports which
one the storage host accepts.

Every attempt reserves a fresh URL and really transfers the file, so use a
small test file. Select whichever works with --strategy or
$MYBOX_UPLOAD_STRATEGY.`,
		Example: `  echo test > /tmp/probe.txt
  mybox debug upload-probe /tmp/probe.txt --dest /임시 --cleanup`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			local := args[0]
			info, err := os.Stat(local)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return usagef("name a file, not a directory")
			}
			if info.Size() > 1<<20 {
				return usagef("use a small probe file of 1MiB or less; this one is %s", bytesOf(info.Size()))
			}

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			target, err := r.ResolveFolder(ctx, dest)
			if err != nil {
				return err
			}

			tc := transfer.New("mybox-cli/"+Version, g.traceFunc())
			base := filepath.Base(local)
			results := make([]probeResult, 0, len(transfer.Strategies))

			for i, strat := range transfer.Strategies {
				// A distinct name per attempt keeps a successful upload from
				// being mistaken for a later strategy's success.
				name := fmt.Sprintf("mybox-probe-%d-%s-%s", i, strat.Name, base)
				res := probeResult{Strategy: strat.Name, FileName: name}

				started := time.Now()
				res.Err = probeOnce(ctx, r, tc, local, name, info.Size(), target.ID, strat)
				res.Elapsed = time.Since(started)
				results = append(results, res)

				if res.Err == nil {
					p.Info("accepted: %s", strat.Name)
					if cleanup {
						if err := probeCleanup(ctx, r, target, name); err != nil {
							p.Warn("could not clean up %s: %v", name, err)
						}
					} else if !keep {
						p.Info("  left behind: %s (pass --cleanup to remove it)", resolve.Join(target.Path, name))
					}
					break
				}
				p.Info("rejected: %s (%v)", strat.Name, res.Err)
			}

			if p.JSON {
				return p.EmitJSON(probeReport(results))
			}
			return reportProbe(g, results)
		},
	}

	f := cmd.Flags()
	f.StringVar(&dest, "dest", resolve.RootPath, "folder to upload the probe into")
	f.BoolVar(&keep, "keep", false, "keep a successful upload (the default)")
	f.BoolVar(&cleanup, "cleanup", false, "move a successful upload to the trash")
	return cmd
}

type probeResult struct {
	Strategy string
	FileName string
	Err      error
	Elapsed  time.Duration
}

func probeOnce(ctx context.Context, r *resolve.Resolver, tc *transfer.Client, local, name string, size int64, parentID string, strat transfer.Strategy) error {
	// Each attempt needs a fresh URL: the previous one may have been consumed
	// or invalidated by the failed attempt.
	ticket, err := r.Client().CreateUploadURL(ctx, api.UploadRequest{
		FileName: name,
		FileSize: size,
		ParentID: parentID,
	})
	if err != nil {
		return err
	}

	f, err := os.Open(local)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = tc.Upload(ctx, transfer.UploadRequest{
		URL:      ticket.UploadURL,
		Body:     f,
		FileName: name,
		Size:     size,
		Strategy: strat,
	})
	return err
}

func probeCleanup(ctx context.Context, r *resolve.Resolver, parent resolve.Target, name string) error {
	for item, err := range r.Client().IterResources(ctx, parent.ID, api.ListOptions{Count: api.MaxListPageSize}) {
		if err != nil {
			return err
		}
		if item.Name == name {
			return r.Client().DeleteResource(ctx, item.ResourceID)
		}
	}
	return errors.New("could not find the uploaded probe file")
}

func probeReport(results []probeResult) map[string]any {
	list := make([]map[string]any, 0, len(results))
	winner := ""
	for _, res := range results {
		entry := map[string]any{
			"strategy":  res.Strategy,
			"ok":        res.Err == nil,
			"elapsedMs": res.Elapsed.Milliseconds(),
		}
		if res.Err != nil {
			entry["error"] = res.Err.Error()
		} else if winner == "" {
			winner = res.Strategy
			entry["fileName"] = res.FileName
		}
		list = append(list, entry)
	}
	return map[string]any{"attempts": list, "working": winner}
}

func reportProbe(g *globals, results []probeResult) error {
	p := g.Printer()
	tw := p.Table()
	fmt.Fprintln(tw, "FORMAT\tRESULT\tTOOK\tNOTE")
	winner := ""
	for _, res := range results {
		status, note := "accepted", ""
		if res.Err != nil {
			status, note = "rejected", res.Err.Error()
		} else if winner == "" {
			winner = res.Strategy
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", res.Strategy, status, res.Elapsed.Round(time.Millisecond), note)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if winner == "" {
		return fmt.Errorf("no candidate was accepted; re-run with --verbose to see the responses")
	}
	p.Print("")
	p.Print("Accepted format: %s", winner)
	p.Print("  mybox up ... --strategy %s", winner)
	p.Print("  or export %s=%s", transfer.EnvStrategy, winner)
	return nil
}
