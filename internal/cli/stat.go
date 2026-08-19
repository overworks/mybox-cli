package cli

import (
	"fmt"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/output"
	"github.com/spf13/cobra"
)

func newStatCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stat path",
		Short: "Show a file or folder's properties",
		Example: `  mybox stat /문서/2026/회의록.pdf
  mybox stat --json id:hV3sQ9pLzR2m`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			target, err := r.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if target.IsRoot() {
				return usagef("the root has no properties of its own; use 'mybox df'")
			}

			item, err := r.Client().GetResource(ctx, target.ID)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.EmitJSON(item)
			}

			tw := p.Table()
			fmt.Fprintf(tw, "Name\t%s\n", item.Name)
			fmt.Fprintf(tw, "Kind\t%s\n", typeLabel(item))
			if target.Path != "" {
				fmt.Fprintf(tw, "Path\t%s\n", target.Path)
			}
			fmt.Fprintf(tw, "ID\t%s\n", item.ResourceID)
			fmt.Fprintf(tw, "Parent ID\t%s\n", orDash(item.ParentID))
			if !item.IsFolder() {
				fmt.Fprintf(tw, "Size\t%s (%d bytes)\n", output.Bytes(item.Size), item.Size)
			}
			if item.FileCount != nil {
				fmt.Fprintf(tw, "Files inside\t%d\n", *item.FileCount)
			}
			if item.SubFolderCount != nil {
				fmt.Fprintf(tw, "Folders inside\t%d\n", *item.SubFolderCount)
			}
			fmt.Fprintf(tw, "Created\t%s\n", p.Time(item.CreatedAt))
			fmt.Fprintf(tw, "Modified\t%s\n", p.Time(item.ModifiedAt))
			fmt.Fprintf(tw, "Accessed\t%s\n", p.Time(item.AccessedAt))
			fmt.Fprintf(tw, "Modified by\t%s\n", orDash(item.LastModifiedBy))
			fmt.Fprintf(tw, "Favourite\t%s\n", yesNo(item.IsFavorite))
			fmt.Fprintf(tw, "Hidden\t%s\n", yesNo(item.IsHidden))
			return tw.Flush()
		},
	}
}

func typeLabel(item *api.ResourceItem) string {
	if item.IsFolder() {
		return "folder"
	}
	if item.Category != "" {
		return fmt.Sprintf("file (%s)", item.Category)
	}
	return "file"
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
