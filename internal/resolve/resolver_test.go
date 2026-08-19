package resolve

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/overworks/mybox-cli/internal/api"
)

// fakeDrive serves the listing endpoints from an in-memory tree so resolver
// tests exercise the real HTTP path without a network.
type fakeDrive struct {
	// children maps a folder ID ("" for the root) to its entries.
	children map[string][]api.ResourceItem
	// pageSize splits listings so cursor handling is exercised. 0 means one page.
	pageSize int
	// calls counts listing requests, which is how these tests verify caching.
	calls int
}

func (d *fakeDrive) add(parentID, id, name, typ string) {
	if d.children == nil {
		d.children = map[string][]api.ResourceItem{}
	}
	d.children[parentID] = append(d.children[parentID], api.ResourceItem{
		ResourceID: id, Name: name, ParentID: parentID, Type: typ,
	})
}

func (d *fakeDrive) start(t *testing.T) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.calls++

		folderID := ""
		if rest, ok := strings.CutPrefix(r.URL.Path, "/drive/folders/"); ok {
			folderID = strings.TrimSuffix(rest, "/resources")
		}
		items := d.children[folderID]

		from := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			fmt.Sscanf(c, "%d", &from)
		}
		to := len(items)
		next := ""
		if d.pageSize > 0 && from+d.pageSize < len(items) {
			to = from + d.pageSize
			next = fmt.Sprint(to)
		}
		if from > len(items) {
			from, to = len(items), len(items)
		}

		out := api.ResourceList{Resources: items[from:to]}
		out.ResponseMetaData.NextCursor = next
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)

	c, err := api.New(api.Options{
		BaseURL: srv.URL,
		Token:   "mbx_pat_test",
		Limits:  map[api.Group]int{api.GroupDefault: -1, api.GroupSearch: -1},
	})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return c
}

// sampleDrive is /문서/2026/회의록.pdf plus a few distractors.
func sampleDrive() *fakeDrive {
	d := &fakeDrive{}
	d.add("", "DOC", "문서", api.TypeFolder)
	d.add("", "PIC", "사진", api.TypeFolder)
	d.add("", "README", "읽어보기.txt", api.TypeFile)
	d.add("DOC", "Y2026", "2026", api.TypeFolder)
	d.add("DOC", "Y2025", "2025", api.TypeFolder)
	d.add("DOC", "NOTE", "메모.txt", api.TypeFile)
	d.add("Y2026", "MIN", "회의록.pdf", api.TypeFile)
	return d
}

func TestResolveWalksToAFile(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	got, err := r.Resolve(t.Context(), "/문서/2026/회의록.pdf")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "MIN" || got.Type != api.TypeFile || got.Path != "/문서/2026/회의록.pdf" {
		t.Errorf("target = %+v", got)
	}
	// One listing per level: root, 문서, 2026.
	if d.calls != 3 {
		t.Errorf("listing calls = %d, want 3", d.calls)
	}
}

func TestResolveRootNeedsNoCalls(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	got, err := r.Resolve(t.Context(), "/")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !got.IsRoot() || !got.IsFolder() {
		t.Errorf("root target = %+v", got)
	}
	if d.calls != 0 {
		t.Errorf("resolving the root made %d calls, want 0", d.calls)
	}
}

func TestResolveIDReferenceSkipsTheWalk(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	got, err := r.Resolve(t.Context(), "id:MIN")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "MIN" {
		t.Errorf("ID = %q, want MIN", got.ID)
	}
	if d.calls != 0 {
		t.Errorf("id: reference made %d calls, want 0", d.calls)
	}
}

func TestResolveFollowsCursors(t *testing.T) {
	d := sampleDrive()
	d.pageSize = 1 // force one page per entry
	r := New(d.start(t), NewDisabledCache())

	got, err := r.Resolve(t.Context(), "/문서/메모.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "NOTE" {
		t.Errorf("ID = %q, want NOTE", got.ID)
	}
}

func TestResolveMissingSegment(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	_, err := r.Resolve(t.Context(), "/문서/2027")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %T (%v), want *NotFoundError", err, err)
	}
	if nf.Missing != "2027" || nf.In != "/문서" {
		t.Errorf("error = %+v", nf)
	}
}

func TestResolveSuggestsCaseInsensitiveMatch(t *testing.T) {
	d := &fakeDrive{}
	d.add("", "R", "Reports", api.TypeFolder)
	r := New(d.start(t), NewDisabledCache())

	_, err := r.Resolve(t.Context(), "/reports")
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("error = %T, want *NotFoundError", err)
	}
	// Matching case-insensitively on its own would be a silent guess; suggest instead.
	if len(nf.Suggestions) != 1 || nf.Suggestions[0] != "Reports" {
		t.Errorf("Suggestions = %v, want [Reports]", nf.Suggestions)
	}
	if !strings.Contains(nf.Error(), "Reports") {
		t.Errorf("message %q should mention the suggestion", nf.Error())
	}
}

func TestResolveThroughAFileFails(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	_, err := r.Resolve(t.Context(), "/문서/메모.txt/안쪽")
	var nfe *NotFolderError
	if !errors.As(err, &nfe) {
		t.Fatalf("error = %T (%v), want *NotFolderError", err, err)
	}
	if nfe.Segment != "메모.txt" {
		t.Errorf("Segment = %q, want 메모.txt", nfe.Segment)
	}
}

func TestResolvePrefersFolderForIntermediateSegments(t *testing.T) {
	// A file and a folder share a name; only the folder can be walked through.
	d := &fakeDrive{}
	d.add("", "F_FILE", "자료", api.TypeFile)
	d.add("", "F_DIR", "자료", api.TypeFolder)
	d.add("F_DIR", "INNER", "안쪽.txt", api.TypeFile)
	r := New(d.start(t), NewDisabledCache())

	got, err := r.Resolve(t.Context(), "/자료/안쪽.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != "INNER" {
		t.Errorf("ID = %q, want INNER", got.ID)
	}
}

func TestResolveAmbiguousLeafIsReported(t *testing.T) {
	d := &fakeDrive{}
	d.add("", "A", "중복", api.TypeFile)
	d.add("", "B", "중복", api.TypeFile)
	r := New(d.start(t), NewDisabledCache())

	_, err := r.Resolve(t.Context(), "/중복")
	var amb *AmbiguousError
	if !errors.As(err, &amb) {
		t.Fatalf("error = %T (%v), want *AmbiguousError", err, err)
	}
	if len(amb.IDs) != 2 {
		t.Errorf("IDs = %v, want two candidates", amb.IDs)
	}
	// The message must tell the user the escape hatch.
	if !strings.Contains(amb.Error(), "id:") {
		t.Errorf("message %q should point at the id: form", amb.Error())
	}
}

func TestResolveUsesCacheOnRepeat(t *testing.T) {
	d := sampleDrive()
	client := d.start(t)
	cache := newMemCache()
	r := New(client, cache)

	if _, err := r.Resolve(t.Context(), "/문서/2026/회의록.pdf"); err != nil {
		t.Fatalf("first Resolve: %v", err)
	}
	first := d.calls

	if _, err := r.Resolve(t.Context(), "/문서/2026/회의록.pdf"); err != nil {
		t.Fatalf("second Resolve: %v", err)
	}
	if d.calls != first {
		t.Errorf("cached lookup made %d extra calls, want 0", d.calls-first)
	}
}

func TestResolveResumesFromDeepestCachedAncestor(t *testing.T) {
	d := sampleDrive()
	d.add("Y2026", "SUB", "하위", api.TypeFolder)
	d.add("SUB", "DEEP", "깊은.txt", api.TypeFile)
	client := d.start(t)
	cache := newMemCache()
	r := New(client, cache)

	if _, err := r.Resolve(t.Context(), "/문서/2026"); err != nil {
		t.Fatalf("warm-up Resolve: %v", err)
	}
	before := d.calls

	if _, err := r.Resolve(t.Context(), "/문서/2026/하위/깊은.txt"); err != nil {
		t.Fatalf("deep Resolve: %v", err)
	}
	// Root and 문서 are already known, so only 2026 and 하위 need listing.
	if got := d.calls - before; got != 2 {
		t.Errorf("made %d calls from the cached ancestor, want 2", got)
	}
}

func TestResolveDoesNotWalkThroughACachedFile(t *testing.T) {
	d := sampleDrive()
	cache := newMemCache()
	cache.Put("/문서/메모.txt", "NOTE", api.TypeFile)
	r := New(d.start(t), cache)

	_, err := r.Resolve(t.Context(), "/문서/메모.txt/안쪽")
	if err == nil {
		t.Fatal("want an error when walking through a cached file")
	}
}

func TestResolveParent(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	parent, name, err := r.ResolveParent(t.Context(), "/문서/2026/새파일.txt")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}
	if parent.ID != "Y2026" || name != "새파일.txt" {
		t.Errorf("parent = %+v, name = %q", parent, name)
	}
}

func TestResolveParentOfTopLevelIsRoot(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	parent, name, err := r.ResolveParent(t.Context(), "/새폴더")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}
	if !parent.IsRoot() || name != "새폴더" {
		t.Errorf("parent = %+v, name = %q", parent, name)
	}
	if d.calls != 0 {
		t.Errorf("resolving a top-level parent made %d calls, want 0", d.calls)
	}
}

func TestResolveParentRejectsIDReference(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	// An ID carries no path, so there is no parent to derive.
	if _, _, err := r.ResolveParent(t.Context(), "id:MIN"); err == nil {
		t.Fatal("want an error for an id: reference")
	}
}

func TestResolveFolderRejectsAFile(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	if _, err := r.ResolveFolder(t.Context(), "/문서/메모.txt"); err == nil {
		t.Fatal("want an error when a file is used where a folder is required")
	}
}

func TestResolveFileRejectsAFolder(t *testing.T) {
	d := sampleDrive()
	r := New(d.start(t), NewDisabledCache())

	if _, err := r.ResolveFile(t.Context(), "/문서"); err == nil {
		t.Fatal("want an error when a folder is used where a file is required")
	}
	if _, err := r.ResolveFile(t.Context(), "/"); err == nil {
		t.Fatal("want an error when the root is used where a file is required")
	}
}

// newMemCache builds a cache that never touches disk, for resolver tests.
func newMemCache() *Cache {
	c := LoadCache("test-fingerprint", DefaultTTL)
	c.path = "" // Save becomes a no-op
	return c
}
