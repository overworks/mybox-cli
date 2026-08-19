package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/spf13/cobra"
)

func newMkdirCommand(g *globals) *cobra.Command {
	var parents bool

	cmd := &cobra.Command{
		Use:   "mkdir path...",
		Short: "Create folders",
		Example: `  mybox mkdir /업무자료
  mybox mkdir -p /문서/2026/1월`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			for _, arg := range args {
				ref, err := resolve.ParseRef(arg)
				if err != nil {
					return err
				}
				if ref.IsID() {
					return usagef("mkdir needs a path, not a resource ID (%s)", arg)
				}
				if ref.IsRoot() {
					return usagef("the root already exists")
				}

				var created resolve.Target
				if parents {
					created, err = mkdirAll(ctx, r, ref.Path)
				} else {
					created, err = mkdirOne(ctx, r, ref.Path)
				}
				if err != nil {
					return err
				}
				p.Info("created %s", created.Path)
				if p.JSON {
					if err := p.EmitJSON(map[string]string{"path": created.Path, "resourceId": created.ID}); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&parents, "parents", "p", false, "create missing parents, and do not fail if it already exists")
	return cmd
}

// mkdirOne creates a single folder whose parent must already exist.
func mkdirOne(ctx context.Context, r *resolve.Resolver, path string) (resolve.Target, error) {
	parent, name, err := r.ResolveParent(ctx, path)
	if err != nil {
		return resolve.Target{}, err
	}
	res, err := r.Client().CreateFolder(ctx, name, parent.ID)
	if err != nil {
		return resolve.Target{}, err
	}
	target := resolve.Target{ID: res.ResourceID, Path: path, Type: api.TypeFolder}
	r.Cache().Put(path, target.ID, api.TypeFolder)
	return target, nil
}

// mkdirAll creates every missing folder along a path, like mkdir -p. An
// already-existing folder is not an error.
func mkdirAll(ctx context.Context, r *resolve.Resolver, path string) (resolve.Target, error) {
	segments := resolve.Segments(path)
	current := resolve.Target{Path: resolve.RootPath, Type: api.TypeFolder}
	// Once a level has been created, every level below it is known to be
	// missing. Looking them up anyway would burn one listing call per level
	// against a 60-per-minute budget to learn something we already know.
	creating := false

	for i, name := range segments {
		sub := "/" + joinSegments(segments[:i+1])

		if !creating {
			found, err := r.Resolve(ctx, sub)
			if err == nil {
				if !found.IsFolder() {
					return resolve.Target{}, &resolve.NotFolderError{Path: path, Segment: name}
				}
				current = found
				continue
			}
			var notFound *resolve.NotFoundError
			if !errors.As(err, &notFound) {
				return resolve.Target{}, err
			}
			creating = true
		}

		res, err := r.Client().CreateFolder(ctx, name, current.ID)
		if err != nil {
			return resolve.Target{}, err
		}
		current = resolve.Target{ID: res.ResourceID, Path: sub, Type: api.TypeFolder}
		r.Cache().Put(sub, current.ID, api.TypeFolder)
	}
	return current, nil
}

func joinSegments(segs []string) string {
	out := ""
	for i, s := range segs {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	return out
}

// destination describes where a copy or move should land.
type destination struct {
	// ParentID is the folder to place the item in ("" means the root).
	ParentID string
	// ParentPath is that folder's path, or "" when it was given as an id:.
	ParentPath string
	// Name is the new name, or "" to keep the source name.
	Name string
}

// resolveDestination interprets a destination reference.
//
// If it names an existing folder, the item goes inside it under its current
// name. Otherwise the last segment is taken as the new name and its parent as
// the target folder — the same rule cp and mv use on a local filesystem.
func resolveDestination(ctx context.Context, r *resolve.Resolver, ref string) (destination, error) {
	parsed, err := resolve.ParseRef(ref)
	if err != nil {
		return destination{}, err
	}
	if parsed.IsID() {
		return destination{ParentID: parsed.ID}, nil
	}
	if parsed.IsRoot() {
		return destination{}, nil
	}

	target, err := r.Resolve(ctx, ref)
	switch {
	case err == nil && target.IsFolder():
		return destination{ParentID: target.ID, ParentPath: target.Path}, nil
	case err == nil:
		// An existing file: treat it as an explicit rename target, which only
		// makes sense together with --overwrite.
		parent, name, perr := r.ResolveParent(ctx, ref)
		if perr != nil {
			return destination{}, perr
		}
		return destination{ParentID: parent.ID, ParentPath: parent.Path, Name: name}, nil
	}

	var notFound *resolve.NotFoundError
	if !errors.As(err, &notFound) {
		return destination{}, err
	}
	// A trailing slash means the user meant a folder, so a missing one is an
	// error rather than a new name.
	if parsed.TrailingSlash {
		return destination{}, err
	}
	parent, name, perr := r.ResolveParent(ctx, ref)
	if perr != nil {
		return destination{}, perr
	}
	return destination{ParentID: parent.ID, ParentPath: parent.Path, Name: name}, nil
}

func newCpCommand(g *globals) *cobra.Command {
	var (
		overwrite bool
		name      string
	)

	cmd := &cobra.Command{
		Use:   "cp source destination",
		Short: "Copy a file or folder within MYBOX",
		Long: `Copies a file or folder within MYBOX.
To move bytes between your machine and MYBOX, use 'mybox up' and 'mybox down'.

If the destination is an existing folder, the copy goes inside it under its
current name. Otherwise the last path segment becomes the new name.`,
		Example: `  mybox cp /문서/회의록.pdf /백업
  mybox cp /문서/회의록.pdf /백업/회의록-사본.pdf
  mybox cp /문서/회의록.pdf /백업 --name 사본.pdf`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			src, err := r.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if src.IsRoot() {
				return usagef("the root cannot be copied")
			}
			dst, err := resolveDestination(ctx, r, args[1])
			if err != nil {
				return err
			}
			if name != "" {
				dst.Name = name
			}

			res, err := r.Client().CopyResource(ctx, src.ID, api.CopyOptions{
				Name:        dst.Name,
				ParentID:    dst.ParentID,
				IsOverwrite: overwrite,
			})
			if err != nil {
				return err
			}

			if dst.ParentPath != "" {
				newPath := resolve.Join(dst.ParentPath, res.Name)
				r.Cache().Put(newPath, res.ResourceID, src.Type)
				p.Info("copied %s -> %s", src.Describe(), newPath)
			} else {
				p.Info("copied %s -> %s", src.Describe(), res.Name)
			}
			if p.JSON {
				return p.EmitJSON(res)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&overwrite, "overwrite", false, "replace an existing entry of the same name")
	f.StringVar(&name, "name", "", "name for the copy; include the extension")
	return cmd
}

func newMvCommand(g *globals) *cobra.Command {
	var overwrite bool

	cmd := &cobra.Command{
		Use:   "mv source destination",
		Short: "Move a file or folder",
		Long: `If the destination is an existing folder, the resource moves inside it.
Otherwise it moves and takes the last path segment as its new name.`,
		Example: `  mybox mv /문서/회의록.pdf /보관
  mybox mv /문서/회의록.pdf /보관/2026-회의록.pdf`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			src, err := r.Resolve(ctx, args[0])
			if err != nil {
				return err
			}
			if src.IsRoot() {
				return usagef("the root cannot be moved")
			}
			dst, err := resolveDestination(ctx, r, args[1])
			if err != nil {
				return err
			}

			// The API moves and renames through separate endpoints, so a move
			// that also renames is two calls. Rename first: if the move then
			// fails the item is still where the user left it, just renamed,
			// which is easier to notice and undo than the reverse.
			if dst.Name != "" && dst.Name != sourceName(src) {
				if _, err := r.Client().RenameResource(ctx, src.ID, dst.Name); err != nil {
					return err
				}
			}
			if err := r.Client().MoveResource(ctx, src.ID, dst.ParentID, overwrite); err != nil {
				return err
			}

			// The source path and everything under it now point somewhere else.
			if src.Path != "" {
				r.Cache().Invalidate(src.Path)
			}
			p.Info("moved %s -> %s", src.Describe(), describeDestination(dst, sourceName(src)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing entry of the same name")
	return cmd
}

func newRenameCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "rename path new-name",
		Aliases: []string{"ren"},
		Short:   "Rename a file or folder",
		Long: `Changes only the name; the location and the resource ID are kept.
Include the extension if you want to keep it.`,
		Example: `  mybox rename /문서/초안.pdf 최종.pdf`,
		Args:    cobra.ExactArgs(2),
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
				return usagef("the root cannot be renamed")
			}
			res, err := r.Client().RenameResource(ctx, target.ID, args[1])
			if err != nil {
				return err
			}

			if target.Path != "" {
				// Descendant paths change with the folder's name, so drop the
				// whole subtree rather than just this entry.
				r.Cache().Invalidate(target.Path)
				parent, _ := resolve.Parent(target.Path)
				r.Cache().Put(resolve.Join(parent, res.Name), target.ID, target.Type)
			}
			p.Info("renamed %s -> %s", target.Describe(), res.Name)
			if p.JSON {
				return p.EmitJSON(res)
			}
			return nil
		},
	}
}

func newRmCommand(g *globals) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:     "rm path...",
		Aliases: []string{"delete"},
		Short:   "Move files or folders to the trash",
		Long: `This moves resources to the trash rather than deleting them outright.
Use 'mybox trash restore' to undo it, or 'mybox trash rm' to delete for good.`,
		Example: `  mybox rm /문서/초안.pdf
  mybox rm /임시1 /임시2`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			// Resolve everything before deleting anything, so a typo in the
			// third argument does not leave the first two already gone.
			targets := make([]resolve.Target, 0, len(args))
			for _, arg := range args {
				t, err := r.Resolve(ctx, arg)
				if err != nil {
					return err
				}
				if t.IsRoot() {
					return usagef("the root cannot be deleted")
				}
				targets = append(targets, t)
			}

			// Deleting a folder takes its contents with it, so say so first.
			for _, t := range targets {
				if t.Type == api.TypeFolder {
					if err := confirm(g, fmt.Sprintf("This moves folder %s and everything inside it to the trash.", t.Describe()), yes); err != nil {
						return err
					}
				}
			}

			for _, t := range targets {
				if err := r.Client().DeleteResource(ctx, t.ID); err != nil {
					return fmt.Errorf("%s: %w", t.Describe(), err)
				}
				if t.Path != "" {
					r.Cache().Invalidate(t.Path)
				}
				p.Info("moved %s to the trash", t.Describe())
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip the confirmation prompt")
	return cmd
}

func newStarCommand(g *globals, favorite bool) *cobra.Command {
	use, short := "star path...", "Add to favourites"
	if !favorite {
		use, short = "unstar path...", "Remove from favourites"
	}

	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  "Both directions are idempotent; being already in that state is not an error.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			r, err := g.Resolver()
			if err != nil {
				return err
			}
			defer g.SaveCache()

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			results := make([]*api.FavoriteResult, 0, len(args))
			for _, arg := range args {
				t, err := r.Resolve(ctx, arg)
				if err != nil {
					return err
				}
				if t.IsRoot() {
					return usagef("the root cannot be favourited")
				}
				res, err := r.Client().SetFavorite(ctx, t.ID, favorite)
				if err != nil {
					return fmt.Errorf("%s: %w", t.Describe(), err)
				}
				results = append(results, res)
				if favorite {
					p.Info("added %s to favourites", t.Describe())
				} else {
					p.Info("removed %s from favourites", t.Describe())
				}
			}
			if p.JSON {
				return p.EmitJSON(results)
			}
			return nil
		},
	}
}

func sourceName(t resolve.Target) string {
	if t.Item != nil {
		return t.Item.Name
	}
	if t.Path == "" {
		return ""
	}
	_, name := resolve.Parent(t.Path)
	return name
}

func describeDestination(d destination, fallbackName string) string {
	name := d.Name
	if name == "" {
		name = fallbackName
	}
	if d.ParentPath != "" {
		return resolve.Join(d.ParentPath, name)
	}
	if d.ParentID == "" {
		return resolve.Join(resolve.RootPath, name)
	}
	return resolve.IDPrefix + d.ParentID + "/" + name
}
