// Package resolve turns user-facing paths into MYBOX resource IDs.
//
// The MYBOX API has no path lookup: listings report a parent ID but never a
// path, and only the search endpoints return one. So a path like /문서/2026 is
// resolved by listing the root, finding "문서", listing it, finding "2026", and
// so on. Each step costs one API call against a documented 60-per-minute budget,
// which is why results are cached on disk.
package resolve

import (
	"fmt"
	"path"
	"strings"
)

// IDPrefix marks a reference that is already a resource ID and needs no lookup.
const IDPrefix = "id:"

// RootPath is the canonical form of the drive root.
const RootPath = "/"

// Ref is a parsed user-supplied reference to a file or folder.
type Ref struct {
	// ID is set when the reference used the id: form. Resolution is skipped.
	ID string
	// Path is the cleaned absolute path, set when ID is empty.
	Path string
	// TrailingSlash records that the user wrote a trailing separator, which
	// signals they meant a folder.
	TrailingSlash bool
}

// IsID reports whether the reference names a resource ID directly.
func (r Ref) IsID() bool { return r.ID != "" }

// IsRoot reports whether the reference names the drive root.
func (r Ref) IsRoot() bool { return r.ID == "" && r.Path == RootPath }

// String renders the reference the way the user wrote it, for error messages.
func (r Ref) String() string {
	if r.IsID() {
		return IDPrefix + r.ID
	}
	return r.Path
}

// ParseRef normalises a user-supplied reference.
//
// An empty string, "/" and "." all mean the root. Everything else is treated as
// an absolute path whether or not it starts with a separator, because the CLI
// has no notion of a working directory inside the drive.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)

	if rest, ok := strings.CutPrefix(s, IDPrefix); ok {
		id := strings.TrimSpace(rest)
		if id == "" {
			return Ref{}, fmt.Errorf("%q carries no resource ID", s)
		}
		return Ref{ID: id}, nil
	}

	trailing := strings.HasSuffix(s, "/") && s != "/"
	if s == "" || s == "." {
		return Ref{Path: RootPath}, nil
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	// Clean resolves "." and ".." and collapses repeated separators. Because the
	// input is absolute, ".." can never escape above the root.
	cleaned := path.Clean(s)
	return Ref{Path: cleaned, TrailingSlash: trailing}, nil
}

// Segments splits an absolute path into its components. The root yields none.
func Segments(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// Join builds an absolute path from a parent path and a child name.
func Join(parent, name string) string {
	if parent == "" || parent == RootPath {
		return "/" + name
	}
	return strings.TrimSuffix(parent, "/") + "/" + name
}

// Parent splits an absolute path into its parent path and final segment. For the
// root it returns the root and an empty name.
func Parent(p string) (parent, name string) {
	segs := Segments(p)
	if len(segs) == 0 {
		return RootPath, ""
	}
	name = segs[len(segs)-1]
	if len(segs) == 1 {
		return RootPath, name
	}
	return "/" + strings.Join(segs[:len(segs)-1], "/"), name
}

// IsAncestor reports whether ancestor is the same path as p or contains it.
// Used to decide which cache entries a move or rename invalidates.
func IsAncestor(ancestor, p string) bool {
	if ancestor == RootPath {
		return true
	}
	if ancestor == p {
		return true
	}
	return strings.HasPrefix(p, strings.TrimSuffix(ancestor, "/")+"/")
}
