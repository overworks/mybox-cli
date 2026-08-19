package resolve

import (
	"context"
	"fmt"
	"strings"

	"github.com/overworks/mybox-cli/internal/api"
)

// Target is a resolved file or folder.
type Target struct {
	// ID is the resource ID. It is empty for the root, which has none.
	ID string
	// Path is the absolute path, or "" when the reference was an id:.
	Path string
	// Type is api.TypeFile or api.TypeFolder. It is empty when the reference was
	// an id: and no lookup has been made.
	Type string
	// Item carries the full listing entry when resolution walked the tree.
	Item *api.ResourceItem
}

// IsRoot reports whether the target is the drive root.
func (t Target) IsRoot() bool { return t.ID == "" }

// IsFolder reports whether the target is known to be a folder. The root is one.
func (t Target) IsFolder() bool { return t.IsRoot() || t.Type == api.TypeFolder }

// Describe renders the target for messages, preferring the path.
func (t Target) Describe() string {
	if t.Path != "" {
		return t.Path
	}
	if t.ID == "" {
		return RootPath
	}
	return IDPrefix + t.ID
}

// NotFoundError reports that a path segment does not exist.
type NotFoundError struct {
	// Path is the full path that was requested.
	Path string
	// Missing is the segment that could not be found.
	Missing string
	// In is the path of the folder that was searched.
	In string
	// Suggestions are same-folder names that differ only by case or spacing.
	Suggestions []string
}

func (e *NotFoundError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: no such path", e.Path)
	if e.Missing != "" {
		fmt.Fprintf(&b, " (%s holds nothing named %q)", e.In, e.Missing)
	}
	if len(e.Suggestions) > 0 {
		fmt.Fprintf(&b, "\n  did you mean: %s", strings.Join(e.Suggestions, ", "))
	}
	return b.String()
}

// NotFolderError reports that a path traverses through a file.
type NotFolderError struct {
	Path    string
	Segment string
}

func (e *NotFolderError) Error() string {
	return fmt.Sprintf("%s: %q is a file, not a folder", e.Path, e.Segment)
}

// AmbiguousError reports that a folder holds more than one entry with the same
// name, so a path cannot identify one of them.
type AmbiguousError struct {
	Path string
	IDs  []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%s: %d resources share this name, so a path cannot identify one; use 'id:' instead (%s)",
		e.Path, len(e.IDs), strings.Join(e.IDs, ", "))
}

// Resolver turns references into targets, walking the folder tree and caching
// what it learns.
type Resolver struct {
	client *api.Client
	cache  *Cache
	// pageSize is the listing page size used while walking. The API caps it at
	// 1000, and asking for the maximum keeps deep trees to one call per level.
	pageSize int
}

// New builds a resolver. Passing a nil cache disables caching.
func New(client *api.Client, cache *Cache) *Resolver {
	if cache == nil {
		cache = NewDisabledCache()
	}
	return &Resolver{client: client, cache: cache, pageSize: api.MaxListPageSize}
}

// Cache exposes the underlying cache so commands can invalidate entries after a
// mutation and persist it once at the end.
func (r *Resolver) Cache() *Cache { return r.cache }

// Client exposes the API client, so a command that already holds a resolver does
// not need the client threaded to it separately.
func (r *Resolver) Client() *api.Client { return r.client }

// Resolve turns a user-supplied reference into a target.
func (r *Resolver) Resolve(ctx context.Context, ref string) (Target, error) {
	parsed, err := ParseRef(ref)
	if err != nil {
		return Target{}, err
	}
	return r.resolveRef(ctx, parsed)
}

// ResolveFolder resolves a reference and fails unless it names a folder.
//
// For an id: reference the type is unknown locally, so this costs one extra
// property lookup to confirm it.
func (r *Resolver) ResolveFolder(ctx context.Context, ref string) (Target, error) {
	t, err := r.Resolve(ctx, ref)
	if err != nil {
		return Target{}, err
	}
	if t.IsRoot() {
		return t, nil
	}
	if t.Type == "" {
		item, err := r.client.GetResource(ctx, t.ID)
		if err != nil {
			return Target{}, err
		}
		t.Type, t.Item = item.Type, item
	}
	if !t.IsFolder() {
		return Target{}, fmt.Errorf("%s is not a folder", t.Describe())
	}
	return t, nil
}

// ResolveFile resolves a reference and fails unless it names a file.
func (r *Resolver) ResolveFile(ctx context.Context, ref string) (Target, error) {
	t, err := r.Resolve(ctx, ref)
	if err != nil {
		return Target{}, err
	}
	if t.IsRoot() {
		return Target{}, fmt.Errorf("the root is not a file")
	}
	if t.Type == "" {
		item, err := r.client.GetResource(ctx, t.ID)
		if err != nil {
			return Target{}, err
		}
		t.Type, t.Item = item.Type, item
	}
	if t.Type != api.TypeFile {
		return Target{}, fmt.Errorf("%s is not a file", t.Describe())
	}
	return t, nil
}

// ResolveParent resolves the folder containing a path and returns the final
// segment's name. It is what create-style commands need: the destination folder
// plus the name to create in it.
//
// An id: reference has no known parent, so this reports an error rather than
// guessing.
func (r *Resolver) ResolveParent(ctx context.Context, ref string) (Target, string, error) {
	parsed, err := ParseRef(ref)
	if err != nil {
		return Target{}, "", err
	}
	if parsed.IsID() {
		return Target{}, "", fmt.Errorf("%s: a resource ID carries no parent; give a path instead", parsed)
	}
	if parsed.IsRoot() {
		return Target{}, "", fmt.Errorf("the root has no parent")
	}

	parentPath, name := Parent(parsed.Path)
	parent, err := r.resolveRef(ctx, Ref{Path: parentPath})
	if err != nil {
		return Target{}, "", err
	}
	if !parent.IsFolder() {
		return Target{}, "", &NotFolderError{Path: parsed.Path, Segment: parentPath}
	}
	return parent, name, nil
}

func (r *Resolver) resolveRef(ctx context.Context, ref Ref) (Target, error) {
	if ref.IsID() {
		return Target{ID: ref.ID}, nil
	}
	if ref.IsRoot() {
		return Target{Path: RootPath, Type: api.TypeFolder}, nil
	}

	if e, ok := r.cache.Get(ref.Path); ok {
		return Target{ID: e.ID, Path: ref.Path, Type: e.Type}, nil
	}

	segments := Segments(ref.Path)
	// Start from the deepest cached ancestor so a repeat lookup in a deep tree
	// costs one call rather than one per level.
	current := Target{Path: RootPath, Type: api.TypeFolder}
	start := 0
	for i := len(segments) - 1; i >= 0; i-- {
		prefix := "/" + strings.Join(segments[:i+1], "/")
		if e, ok := r.cache.Get(prefix); ok {
			if e.Type != api.TypeFolder {
				break // a cached file cannot be walked through
			}
			current = Target{ID: e.ID, Path: prefix, Type: e.Type}
			start = i + 1
			break
		}
	}

	for i := start; i < len(segments); i++ {
		name := segments[i]
		if !current.IsFolder() {
			return Target{}, &NotFolderError{Path: ref.Path, Segment: current.Path}
		}
		// Only the last segment may be a file; anything earlier must be a folder
		// for the walk to continue.
		wantFolder := i < len(segments)-1

		found, err := r.findChild(ctx, current, name, wantFolder)
		if err != nil {
			return Target{}, err
		}
		child := Target{
			ID:   found.ResourceID,
			Path: Join(current.Path, name),
			Type: found.Type,
			Item: found,
		}
		r.cache.Put(child.Path, child.ID, child.Type)
		current = child
	}
	return current, nil
}

// findChild scans a folder's listing for an exact name match.
//
// wantFolder narrows the match when the segment is an intermediate one, so a
// file that shares a name with a folder cannot derail the walk.
//
// The scan deliberately reads the listing to the end rather than stopping at the
// first hit. Stopping early would silently pick one of two same-named entries,
// and the caller may be about to delete or overwrite it. Pages hold up to 1000
// entries, so this is a single call for all but the largest folders; paying a
// few extra calls there is cheaper than deleting the wrong file.
func (r *Resolver) findChild(ctx context.Context, parent Target, name string, wantFolder bool) (*api.ResourceItem, error) {
	var matches []api.ResourceItem
	var nearMisses []string

	opts := api.ListOptions{Count: r.pageSize}
	for item, err := range r.client.IterResources(ctx, parent.ID, opts) {
		if err != nil {
			return nil, err
		}
		switch {
		case item.Name == name:
			if wantFolder && !item.IsFolder() {
				// Remember it: if nothing else matches, the user walked into a file.
				nearMisses = append(nearMisses, item.Name)
				continue
			}
			matches = append(matches, item)
		case strings.EqualFold(item.Name, name):
			nearMisses = append(nearMisses, item.Name)
		}
	}

	switch len(matches) {
	case 1:
		m := matches[0]
		return &m, nil
	case 0:
		if wantFolder && len(nearMisses) > 0 {
			for _, n := range nearMisses {
				if n == name {
					return nil, &NotFolderError{Path: Join(parent.Path, name), Segment: name}
				}
			}
		}
		return nil, &NotFoundError{
			Path:        Join(parent.Path, name),
			Missing:     name,
			In:          parent.Describe(),
			Suggestions: dedupe(nearMisses),
		}
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, IDPrefix+m.ResourceID)
		}
		return nil, &AmbiguousError{Path: Join(parent.Path, name), IDs: ids}
	}
}

func dedupe(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
