package cli

import (
	"fmt"
	"strings"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/output"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/spf13/cobra"
)

func newLsCommand(g *globals) *cobra.Command {
	var (
		long  bool
		sort  string
		limit int
		all   bool
	)

	cmd := &cobra.Command{
		Use:     "ls [path]",
		Aliases: []string{"list"},
		Short:   "List a folder's contents",
		Long: `Lists the files and folders inside a folder, or the root if no path is given.

Results always come back folders first, then files, whatever --sort says.
Longer listings are paged through automatically.`,
		Example: `  mybox ls
  mybox ls /문서/2026
  mybox ls -l --sort modifiedAt,desc /문서
  mybox ls id:hV3sQ9pLzR2m`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			ref := resolve.RootPath
			if len(args) == 1 {
				ref = args[0]
			}
			target, err := r.ResolveFolder(ctx, ref)
			if err != nil {
				return err
			}

			opts := api.ListOptions{Sort: sort, Count: api.MaxListPageSize}
			items := make([]api.ResourceItem, 0, 64)
			for item, err := range r.Client().IterResources(ctx, target.ID, opts) {
				if err != nil {
					return err
				}
				if item.IsHidden && !all {
					continue
				}
				items = append(items, item)
				if limit > 0 && len(items) >= limit {
					break
				}
			}

			// Remember what we just listed: a following stat or rm on any of
			// these entries then costs no extra call.
			if target.Path != "" {
				for _, item := range items {
					r.Cache().Put(resolve.Join(target.Path, item.Name), item.ResourceID, item.Type)
				}
			}

			if p.JSON {
				return p.EmitJSON(items)
			}
			if len(items) == 0 {
				p.Info("%s is empty", target.Describe())
				return nil
			}
			return printResources(p, items, long)
		},
	}

	f := cmd.Flags()
	f.BoolVarP(&long, "long", "l", false, "show size, modification time and flags")
	f.StringVar(&sort, "sort", "", "sort as (name|createdAt|modifiedAt|accessedAt),(asc|desc)")
	f.IntVarP(&limit, "limit", "n", 0, "stop after this many entries; 0 means all")
	f.BoolVarP(&all, "all", "a", false, "include hidden entries")
	return cmd
}

// printResources renders a listing, in short or long form.
func printResources(p *output.Printer, items []api.ResourceItem, long bool) error {
	tw := p.Table()
	if !long {
		for _, item := range items {
			fmt.Fprintln(tw, displayName(item))
		}
		return tw.Flush()
	}

	fmt.Fprintln(tw, "\tSIZE\tMODIFIED\tNAME\tID")
	for _, item := range items {
		size := "-"
		if !item.IsFolder() {
			size = output.Bytes(item.Size)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			flags(item), size, p.Time(item.ModifiedAt), displayName(item), item.ResourceID)
	}
	return tw.Flush()
}

// displayName marks folders with a trailing separator, the way ls does.
func displayName(item api.ResourceItem) string {
	if item.IsFolder() {
		return item.Name + "/"
	}
	return item.Name
}

// flags renders the per-item markers: favourite and hidden.
func flags(item api.ResourceItem) string {
	var b strings.Builder
	if item.IsFavorite {
		b.WriteString("*")
	} else {
		b.WriteString(" ")
	}
	if item.IsHidden {
		b.WriteString("h")
	} else {
		b.WriteString(" ")
	}
	return b.String()
}
