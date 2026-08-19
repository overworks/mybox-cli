package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/output"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/spf13/cobra"
)

func newTrashCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trash",
		Short: "Manage the trash",
		Long: `Trashed items cannot be named by path: the MYBOX API reports no path for
anything in the trash. Use the ID that 'mybox trash ls' shows, or a name,
which mybox looks up in the listing. An ambiguous name stops the command.`,
	}
	cmd.AddCommand(
		newTrashListCommand(g),
		newTrashRestoreCommand(g),
		newTrashRemoveCommand(g),
		newTrashEmptyCommand(g),
		newTrashAutoDeleteCommand(g),
	)
	return cmd
}

// findTrashItem locates a trash entry by resource ID or by name.
//
// The trash listing is the only way to address these items: the API exposes no
// path for them. An "id:" reference is used as-is; anything else is matched
// against the listing by exact name, and an ambiguous name is refused rather
// than resolved arbitrarily.
func findTrashItem(ctx context.Context, client *api.Client, ref string) (api.TrashedResourceItem, error) {
	if id, ok := strings.CutPrefix(ref, resolve.IDPrefix); ok {
		id = strings.TrimSpace(id)
		if id == "" {
			return api.TrashedResourceItem{}, usagef("%q carries no resource ID", ref)
		}
		return api.TrashedResourceItem{ResourceID: id, Name: id}, nil
	}

	name := strings.TrimPrefix(strings.TrimSpace(ref), "/")
	var matches []api.TrashedResourceItem
	for item, err := range client.IterTrash(ctx, api.ListOptions{Count: api.MaxListPageSize}) {
		if err != nil {
			return api.TrashedResourceItem{}, err
		}
		if item.Name == name || item.ResourceID == name {
			matches = append(matches, item)
		}
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return api.TrashedResourceItem{}, fmt.Errorf("nothing named %q in the trash", ref)
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, resolve.IDPrefix+m.ResourceID)
		}
		return api.TrashedResourceItem{}, fmt.Errorf(
			"the trash holds %d items named %q, so use 'id:' instead (%s)",
			len(matches), ref, strings.Join(ids, ", "))
	}
}

func newTrashRestoreCommand(g *globals) *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:   "restore target...",
		Short: "Restore trashed items to where they were",
		Long: `A target is either the ID shown by 'mybox trash ls' (with the id: prefix) or
a name. A name shared by several items cannot identify one, so use the ID.`,
		Example: `  mybox trash restore id:hV3sQ9pLzR2m
  mybox trash restore 회의록.pdf`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			for _, arg := range args {
				item, err := findTrashItem(ctx, client, arg)
				if err != nil {
					return err
				}
				if err := client.RestoreFromTrash(ctx, item.ResourceID, overwrite); err != nil {
					return fmt.Errorf("%s: %w", item.Name, err)
				}
				p.Info("restored %s", item.Name)
			}
			// A restored item reappears at a path the cache may have written off.
			g.invalidateAllPaths()
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing entry of the same name at the restore location")
	return cmd
}

func newTrashRemoveCommand(g *globals) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "rm target...",
		Aliases: []string{"delete", "purge"},
		Short:   "Permanently delete trashed items",
		Long:    "This cannot be undone.",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			// Resolve every target first so the confirmation names what will
			// actually be destroyed.
			items := make([]api.TrashedResourceItem, 0, len(args))
			for _, arg := range args {
				item, err := findTrashItem(ctx, client, arg)
				if err != nil {
					return err
				}
				items = append(items, item)
			}

			names := make([]string, 0, len(items))
			for _, item := range items {
				names = append(names, item.Name)
			}
			if err := confirm(g, fmt.Sprintf("This permanently deletes %s. It cannot be undone.", strings.Join(names, ", ")), yes); err != nil {
				return err
			}

			for _, item := range items {
				if err := client.PurgeTrashItem(ctx, item.ResourceID); err != nil {
					return fmt.Errorf("%s: %w", item.Name, err)
				}
				p.Info("permanently deleted %s", item.Name)
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newTrashEmptyCommand(g *globals) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "empty",
		Short: "Empty the trash",
		Long: `Permanently deletes every file and folder in the trash. This cannot be undone.

It removes everything, not just what mybox deleted — items trashed from the
web UI and the mobile apps go too.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			// Show the scale of the deletion before asking, so "empty the trash"
			// is not a blind command.
			items, err := collectTrash(ctx, client, api.DefaultTrashSort, 0)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				p.Info("the trash is already empty")
				return nil
			}
			if err := confirm(g, fmt.Sprintf("This permanently deletes %d items from the trash. It cannot be undone.", len(items)), yes); err != nil {
				return err
			}
			if err := client.EmptyTrash(ctx); err != nil {
				return err
			}
			p.Info("emptied the trash (%d items)", len(items))
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newTrashAutoDeleteCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "autodelete [days]",
		Short: "Read or set the trash auto-delete interval",
		Long: `With no argument this reports the current interval.
The accepted values are 0 (off), 5, 15, 30 and 50.`,
		Example: `  mybox trash autodelete
  mybox trash autodelete 30
  mybox trash autodelete 0`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			if len(args) == 0 {
				st, err := client.GetStorage(ctx)
				if err != nil {
					return err
				}
				if p.JSON {
					return p.EmitJSON(api.TrashAutoDelete{TrashAutoDeleteDays: st.TrashAutoDeleteDays})
				}
				p.Print("%s", trashIntervalLabel(st.TrashAutoDeleteDays))
				return nil
			}

			days, err := strconv.Atoi(args[0])
			if err != nil {
				return usagef("the interval must be a number, got %q", args[0])
			}
			res, err := client.SetTrashAutoDeleteDays(ctx, days)
			if err != nil {
				return err
			}
			if p.JSON {
				return p.EmitJSON(res)
			}
			p.Info("set the trash auto-delete interval to %s", trashIntervalLabel(res.TrashAutoDeleteDays))
			return nil
		},
	}
}

func newTrashListCommand(g *globals) *cobra.Command {
	var (
		sort  string
		limit int
	)

	cmd := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List what is in the trash",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			items, err := collectTrash(ctx, client, sort, limit)
			if err != nil {
				return err
			}

			if p.JSON {
				return p.EmitJSON(items)
			}
			if len(items) == 0 {
				p.Info("the trash is empty")
				return nil
			}
			tw := p.Table()
			fmt.Fprintln(tw, "SIZE\tDELETED\tNAME\tID")
			for _, item := range items {
				size := "-"
				if !item.IsFolder() {
					size = output.Bytes(item.Size)
				}
				name := item.Name
				if item.IsFolder() {
					name += "/"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", size, p.Time(item.DeletedAt), name, item.ResourceID)
			}
			return tw.Flush()
		},
	}

	f := cmd.Flags()
	f.StringVar(&sort, "sort", api.DefaultTrashSort,
		"sort as (deletedAt|name|type|location|size),(asc|desc)")
	f.IntVarP(&limit, "limit", "n", 0, "stop after this many entries; 0 means all")
	return cmd
}

// collectTrash walks the trash listing, honouring an optional cap.
func collectTrash(ctx context.Context, client *api.Client, sort string, limit int) ([]api.TrashedResourceItem, error) {
	opts := api.ListOptions{Sort: sort, Count: api.MaxListPageSize}
	items := make([]api.TrashedResourceItem, 0, 32)
	for item, err := range client.IterTrash(ctx, opts) {
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if limit > 0 && len(items) >= limit {
			break
		}
	}
	return items, nil
}
