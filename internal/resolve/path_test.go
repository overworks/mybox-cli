package resolve

import (
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	for _, tc := range []struct {
		give     string
		wantID   string
		wantPath string
		trailing bool
	}{
		{"", "", "/", false},
		{".", "", "/", false},
		{"/", "", "/", false},
		{"/문서", "", "/문서", false},
		{"문서", "", "/문서", false}, // no working directory: bare names are absolute
		{"/문서/", "", "/문서", true},
		{"/문서//2026", "", "/문서/2026", false},
		{"/문서/./2026", "", "/문서/2026", false},
		{"/문서/2026/../2025", "", "/문서/2025", false},
		{"  /문서  ", "", "/문서", false},
		{"id:hV3sQ9pLzR2m", "hV3sQ9pLzR2m", "", false},
		{"id: hV3sQ9pLzR2m ", "hV3sQ9pLzR2m", "", false},
	} {
		got, err := ParseRef(tc.give)
		if err != nil {
			t.Errorf("ParseRef(%q): %v", tc.give, err)
			continue
		}
		if got.ID != tc.wantID || got.Path != tc.wantPath || got.TrailingSlash != tc.trailing {
			t.Errorf("ParseRef(%q) = %+v, want id=%q path=%q trailing=%v",
				tc.give, got, tc.wantID, tc.wantPath, tc.trailing)
		}
	}
}

func TestParseRefRejectsEmptyID(t *testing.T) {
	if _, err := ParseRef("id:"); err == nil {
		t.Fatal("want an error for an empty id: reference")
	}
}

func TestParseRefCannotEscapeAboveRoot(t *testing.T) {
	// A path built from user input must never address anything outside the drive.
	for _, give := range []string{"/../../etc", "../../etc", "/문서/../../.."} {
		got, err := ParseRef(give)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", give, err)
		}
		if strings.Contains(got.Path, "..") {
			t.Errorf("ParseRef(%q) = %q, which still escapes upward", give, got.Path)
		}
	}
}

func TestRefPredicates(t *testing.T) {
	r, _ := ParseRef("/")
	if !r.IsRoot() || r.IsID() {
		t.Errorf("root ref = %+v", r)
	}
	r, _ = ParseRef("id:X")
	if r.IsRoot() || !r.IsID() {
		t.Errorf("id ref = %+v", r)
	}
	if r.String() != "id:X" {
		t.Errorf("String() = %q", r.String())
	}
}

func TestSegments(t *testing.T) {
	for _, tc := range []struct {
		give string
		want string
	}{
		{"/", ""},
		{"", ""},
		{"/문서", "문서"},
		{"/문서/2026/회의록.pdf", "문서|2026|회의록.pdf"},
	} {
		got := strings.Join(Segments(tc.give), "|")
		if got != tc.want {
			t.Errorf("Segments(%q) = %q, want %q", tc.give, got, tc.want)
		}
	}
}

func TestJoin(t *testing.T) {
	for _, tc := range []struct{ parent, name, want string }{
		{"/", "문서", "/문서"},
		{"", "문서", "/문서"},
		{"/문서", "2026", "/문서/2026"},
		{"/문서/", "2026", "/문서/2026"},
	} {
		if got := Join(tc.parent, tc.name); got != tc.want {
			t.Errorf("Join(%q, %q) = %q, want %q", tc.parent, tc.name, got, tc.want)
		}
	}
}

func TestParent(t *testing.T) {
	for _, tc := range []struct{ give, wantParent, wantName string }{
		{"/", "/", ""},
		{"/문서", "/", "문서"},
		{"/문서/2026", "/문서", "2026"},
		{"/문서/2026/회의록.pdf", "/문서/2026", "회의록.pdf"},
	} {
		p, n := Parent(tc.give)
		if p != tc.wantParent || n != tc.wantName {
			t.Errorf("Parent(%q) = %q, %q; want %q, %q", tc.give, p, n, tc.wantParent, tc.wantName)
		}
	}
}

func TestIsAncestor(t *testing.T) {
	for _, tc := range []struct {
		ancestor, p string
		want        bool
	}{
		{"/", "/문서/2026", true},
		{"/문서", "/문서", true},
		{"/문서", "/문서/2026", true},
		{"/문서", "/문서2026", false}, // a name prefix is not a path prefix
		{"/문서/2026", "/문서", false},
		{"/사진", "/문서", false},
	} {
		if got := IsAncestor(tc.ancestor, tc.p); got != tc.want {
			t.Errorf("IsAncestor(%q, %q) = %v, want %v", tc.ancestor, tc.p, got, tc.want)
		}
	}
}
