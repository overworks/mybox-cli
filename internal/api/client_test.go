package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// capture records what the fake server saw, so tests can assert on the wire
// format rather than on the client's internals.
type capture struct {
	Method     string
	Path       string
	RawQuery   string
	Body       string
	AuthHeader string
	UserAgent  string
}

// newServer stands up a fake API that answers every request with status and body
// and records the last request it received.
func newServer(t *testing.T, status int, body string) (*Client, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*got = capture{
			Method:     r.Method,
			Path:       r.URL.Path,
			RawQuery:   r.URL.RawQuery,
			Body:       strings.TrimSpace(string(raw)),
			AuthHeader: r.Header.Get("Authorization"),
			UserAgent:  r.Header.Get("User-Agent"),
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			io.WriteString(w, body)
		}
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "mbx_pat_test", UserAgent: "mybox-cli/test"})
	return c, got
}

// newTestClient builds a client with rate limiting and sleeping disabled so unit
// tests neither wait nor depend on wall-clock time.
func newTestClient(t *testing.T, opts Options) *Client {
	t.Helper()
	if opts.Limits == nil {
		opts.Limits = map[Group]int{GroupDefault: -1, GroupSearch: -1, GroupDelete: -1, GroupRestore: -1}
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.jitter = func() float64 { return 0 }
	return c
}

func (g *capture) assert(t *testing.T, method, path string) {
	t.Helper()
	if g.Method != method {
		t.Errorf("method = %q, want %q", g.Method, method)
	}
	if g.Path != path {
		t.Errorf("path = %q, want %q", g.Path, path)
	}
	if g.AuthHeader != "Bearer mbx_pat_test" {
		t.Errorf("Authorization = %q, want bearer token", g.AuthHeader)
	}
}

// assertJSONBody compares the request body to want as JSON, so field order and
// whitespace do not make the test brittle.
func (g *capture) assertJSONBody(t *testing.T, want string) {
	t.Helper()
	var gotV, wantV any
	if err := json.Unmarshal([]byte(g.Body), &gotV); err != nil {
		t.Fatalf("request body is not JSON (%q): %v", g.Body, err)
	}
	if err := json.Unmarshal([]byte(want), &wantV); err != nil {
		t.Fatalf("want is not JSON: %v", err)
	}
	gotN, _ := json.Marshal(gotV)
	wantN, _ := json.Marshal(wantV)
	if string(gotN) != string(wantN) {
		t.Errorf("body = %s, want %s", gotN, wantN)
	}
}

// --- fixtures copied verbatim from docs/api-reference.md -------------------

const storageJSON = `{
  "fileCounts": {"archive":5,"audio":8,"document":30,"etc":23,"executable":2,"image":40,"total":120,"video":12},
  "maxFileBytes": 53687091200,
  "quotaBytes": 32212254720,
  "trashAutoDeleteDays": 5,
  "usedBytes": 5368709120
}`

const resourceListJSON = `{
  "fileCount": 12,
  "resources": [
    {
      "accessedAt": "2026-08-11T09:00:00+09:00",
      "category": "image",
      "createdAt": "2026-08-11T09:00:00+09:00",
      "isFavorite": false,
      "isHidden": false,
      "lastModifiedBy": "mybox_user",
      "modifiedAt": "2026-08-11T09:00:00+09:00",
      "name": "회의록.pdf",
      "parentId": "Kd7ZmR2vT9xQ4nB6wL1yHc3pJ8sF5gA0uE",
      "resourceId": "hV3sQ9pLzR2mT7kXwB5nDcF8gJ4yA6uE0o",
      "size": 1048576,
      "type": "file"
    }
  ],
  "responseMetaData": {"nextCursor": "MjA"},
  "subFolderCount": 3
}`

const errorJSON = `{
  "code": "PLAT-404",
  "message": "NOT_FOUND",
  "requestId": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "timestamp": "2026-06-18T16:30:00+09:00"
}`

// --- endpoint tests --------------------------------------------------------

func TestGetStorage(t *testing.T) {
	c, got := newServer(t, 200, storageJSON)

	st, err := c.GetStorage(t.Context())
	if err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	got.assert(t, http.MethodGet, "/drive/storage")

	if st.QuotaBytes != 32212254720 {
		t.Errorf("QuotaBytes = %d, want 32212254720", st.QuotaBytes)
	}
	if st.UsedBytes != 5368709120 {
		t.Errorf("UsedBytes = %d, want 5368709120", st.UsedBytes)
	}
	if st.MaxFileBytes != 53687091200 {
		t.Errorf("MaxFileBytes = %d, want 53687091200", st.MaxFileBytes)
	}
	if st.TrashAutoDeleteDays != 5 {
		t.Errorf("TrashAutoDeleteDays = %d, want 5", st.TrashAutoDeleteDays)
	}
	if st.FileCounts.Total != 120 || st.FileCounts.Image != 40 || st.FileCounts.Executable != 2 {
		t.Errorf("FileCounts = %+v", st.FileCounts)
	}
}

func TestSetTrashAutoDeleteDays(t *testing.T) {
	c, got := newServer(t, 200, `{"trashAutoDeleteDays": 15}`)

	res, err := c.SetTrashAutoDeleteDays(t.Context(), 15)
	if err != nil {
		t.Fatalf("SetTrashAutoDeleteDays: %v", err)
	}
	got.assert(t, http.MethodPatch, "/drive/storage")
	got.assertJSONBody(t, `{"trashAutoDeleteDays":15}`)
	if res.TrashAutoDeleteDays != 15 {
		t.Errorf("TrashAutoDeleteDays = %d, want 15", res.TrashAutoDeleteDays)
	}
}

func TestSetTrashAutoDeleteDaysRejectsUndocumentedValue(t *testing.T) {
	c, got := newServer(t, 200, `{"trashAutoDeleteDays": 7}`)

	if _, err := c.SetTrashAutoDeleteDays(t.Context(), 7); err == nil {
		t.Fatal("want an error for an undocumented interval, got nil")
	}
	if got.Method != "" {
		t.Errorf("invalid interval reached the server as %s %s", got.Method, got.Path)
	}
}

func TestListRoot(t *testing.T) {
	c, got := newServer(t, 200, resourceListJSON)

	res, err := c.ListRoot(t.Context(), ListOptions{Sort: "name,asc", Count: 100, Cursor: "MjA"})
	if err != nil {
		t.Fatalf("ListRoot: %v", err)
	}
	got.assert(t, http.MethodGet, "/drive/resources")
	if want := "count=100&cursor=MjA&sort=name%2Casc"; got.RawQuery != want {
		t.Errorf("query = %q, want %q", got.RawQuery, want)
	}

	if len(res.Resources) != 1 {
		t.Fatalf("len(Resources) = %d, want 1", len(res.Resources))
	}
	r := res.Resources[0]
	if r.Name != "회의록.pdf" || r.ResourceID != "hV3sQ9pLzR2mT7kXwB5nDcF8gJ4yA6uE0o" || r.Size != 1048576 {
		t.Errorf("resource = %+v", r)
	}
	if r.IsFolder() {
		t.Error("IsFolder() = true for a file")
	}
	if res.FileCount != 12 || res.SubFolderCount != 3 {
		t.Errorf("counts = %d files / %d folders, want 12/3", res.FileCount, res.SubFolderCount)
	}
	if res.ResponseMetaData.NextCursor != "MjA" {
		t.Errorf("NextCursor = %q, want MjA", res.ResponseMetaData.NextCursor)
	}
}

func TestListCountIsClampedToAPIMaximum(t *testing.T) {
	c, got := newServer(t, 200, resourceListJSON)

	if _, err := c.ListRoot(t.Context(), ListOptions{Count: 5000}); err != nil {
		t.Fatalf("ListRoot: %v", err)
	}
	if want := "count=1000"; got.RawQuery != want {
		t.Errorf("query = %q, want %q", got.RawQuery, want)
	}
}

func TestListFolderEscapesResourceID(t *testing.T) {
	c, got := newServer(t, 200, resourceListJSON)

	if _, err := c.ListFolder(t.Context(), "a/b c", ListOptions{}); err != nil {
		t.Fatalf("ListFolder: %v", err)
	}
	// The ID must survive as a single path segment even when it contains a slash.
	got.assert(t, http.MethodGet, "/drive/folders/a/b c/resources")
	if got.RawQuery != "" {
		t.Errorf("query = %q, want empty when no options are set", got.RawQuery)
	}
}

func TestListDispatchesRootVersusFolder(t *testing.T) {
	c, got := newServer(t, 200, resourceListJSON)

	if _, err := c.List(t.Context(), "", ListOptions{}); err != nil {
		t.Fatalf("List(root): %v", err)
	}
	if got.Path != "/drive/resources" {
		t.Errorf("empty folder ID hit %q, want the root endpoint", got.Path)
	}

	if _, err := c.List(t.Context(), "FOLDER", ListOptions{}); err != nil {
		t.Fatalf("List(folder): %v", err)
	}
	if got.Path != "/drive/folders/FOLDER/resources" {
		t.Errorf("folder ID hit %q, want the folder endpoint", got.Path)
	}
}

func TestGetResource(t *testing.T) {
	c, got := newServer(t, 200, `{
	  "accessedAt":"2026-08-11T09:00:00+09:00","createdAt":"2026-08-11T09:00:00+09:00",
	  "fileCount":12,"isFavorite":false,"isHidden":false,"lastModifiedBy":"mybox_user",
	  "modifiedAt":"2026-08-11T09:00:00+09:00","name":"업무자료",
	  "parentId":"ROOT","resourceId":"FOLDER1","size":0,"subFolderCount":3,"type":"folder"}`)

	r, err := c.GetResource(t.Context(), "FOLDER1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	got.assert(t, http.MethodGet, "/drive/resources/FOLDER1")

	if !r.IsFolder() {
		t.Error("IsFolder() = false for a folder")
	}
	if r.FileCount == nil || *r.FileCount != 12 {
		t.Errorf("FileCount = %v, want 12", r.FileCount)
	}
	if r.SubFolderCount == nil || *r.SubFolderCount != 3 {
		t.Errorf("SubFolderCount = %v, want 3", r.SubFolderCount)
	}
}

func TestGetResourceOmitsFolderCountsForFiles(t *testing.T) {
	c, _ := newServer(t, 200, `{"name":"a.txt","resourceId":"F1","type":"file","size":1}`)

	r, err := c.GetResource(t.Context(), "F1")
	if err != nil {
		t.Fatalf("GetResource: %v", err)
	}
	if r.FileCount != nil || r.SubFolderCount != nil {
		t.Errorf("file carried folder counts: %v / %v", r.FileCount, r.SubFolderCount)
	}
}

func TestCreateFolder(t *testing.T) {
	c, got := newServer(t, 201, `{"name":"업무자료","resourceId":"Kd7ZmR2vT9xQ4nB6wL1yHc3pJ8sF5gA0uE"}`)

	res, err := c.CreateFolder(t.Context(), "업무자료", "PARENT")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/folders")
	got.assertJSONBody(t, `{"folderName":"업무자료","parentId":"PARENT"}`)
	if res.ResourceID != "Kd7ZmR2vT9xQ4nB6wL1yHc3pJ8sF5gA0uE" {
		t.Errorf("ResourceID = %q", res.ResourceID)
	}
}

func TestCreateFolderInRootOmitsParent(t *testing.T) {
	c, got := newServer(t, 201, `{"name":"업무자료","resourceId":"X"}`)

	if _, err := c.CreateFolder(t.Context(), "업무자료", ""); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	// An empty parentId must be omitted, not sent as "", so the API treats it as root.
	got.assertJSONBody(t, `{"folderName":"업무자료"}`)
}

func TestCopyResource(t *testing.T) {
	c, got := newServer(t, 201, `{"name":"사본.pdf","resourceId":"NEW"}`)

	res, err := c.CopyResource(t.Context(), "SRC", CopyOptions{Name: "사본.pdf", ParentID: "DST", IsOverwrite: true})
	if err != nil {
		t.Fatalf("CopyResource: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/resources/SRC/copy")
	got.assertJSONBody(t, `{"name":"사본.pdf","parentId":"DST","isOverwrite":true}`)
	if res.Name != "사본.pdf" {
		t.Errorf("Name = %q", res.Name)
	}
}

func TestCopyResourceWithDefaults(t *testing.T) {
	c, got := newServer(t, 201, `{"name":"a","resourceId":"NEW"}`)

	if _, err := c.CopyResource(t.Context(), "SRC", CopyOptions{}); err != nil {
		t.Fatalf("CopyResource: %v", err)
	}
	// Every field is optional; sending an empty object means "same name, into root".
	got.assertJSONBody(t, `{}`)
}

func TestMoveResourceAcceptsEmpty200Body(t *testing.T) {
	c, got := newServer(t, 200, "")

	if err := c.MoveResource(t.Context(), "SRC", "DST", true); err != nil {
		t.Fatalf("MoveResource: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/resources/SRC/move")
	got.assertJSONBody(t, `{"parentId":"DST","isOverwrite":true}`)
}

func TestRenameResource(t *testing.T) {
	c, got := newServer(t, 200, `{"name":"회의록.pdf"}`)

	res, err := c.RenameResource(t.Context(), "SRC", "회의록.pdf")
	if err != nil {
		t.Fatalf("RenameResource: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/resources/SRC/rename")
	got.assertJSONBody(t, `{"name":"회의록.pdf"}`)
	if res.Name != "회의록.pdf" {
		t.Errorf("Name = %q", res.Name)
	}
}

func TestDeleteResource(t *testing.T) {
	c, got := newServer(t, 204, "")

	if err := c.DeleteResource(t.Context(), "SRC"); err != nil {
		t.Fatalf("DeleteResource: %v", err)
	}
	got.assert(t, http.MethodDelete, "/drive/resources/SRC")
	if got.Body != "" {
		t.Errorf("body = %q, want empty", got.Body)
	}
}

func TestSetFavorite(t *testing.T) {
	for _, tc := range []struct {
		name     string
		favorite bool
		path     string
		body     string
	}{
		{"star", true, "/drive/resources/SRC/favorite", `{"isFavorite":true,"resourceId":"SRC"}`},
		{"unstar", false, "/drive/resources/SRC/unfavorite", `{"isFavorite":false,"resourceId":"SRC"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, got := newServer(t, 200, tc.body)

			res, err := c.SetFavorite(t.Context(), "SRC", tc.favorite)
			if err != nil {
				t.Fatalf("SetFavorite: %v", err)
			}
			got.assert(t, http.MethodPost, tc.path)
			if res.IsFavorite != tc.favorite {
				t.Errorf("IsFavorite = %v, want %v", res.IsFavorite, tc.favorite)
			}
		})
	}
}

func TestCreateUploadURL(t *testing.T) {
	c, got := newServer(t, 201, `{"offset":0,"uploadUrl":"https://storage.example/v1/storage/upload?auth=4&stoken=x"}`)

	res, err := c.CreateUploadURL(t.Context(), UploadRequest{
		FileName: "회의록.pdf", FileSize: 1048576, ParentID: "DST", Overwrite: true,
	})
	if err != nil {
		t.Fatalf("CreateUploadURL: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/files")
	got.assertJSONBody(t, `{"fileName":"회의록.pdf","fileSize":1048576,"parentId":"DST","isOverwrite":true}`)
	if res.UploadURL == "" || res.Offset != 0 {
		t.Errorf("ticket = %+v", res)
	}
}

func TestCreateUploadURLSendsZeroSizeExplicitly(t *testing.T) {
	c, got := newServer(t, 201, `{"offset":0,"uploadUrl":"https://storage.example/u"}`)

	if _, err := c.CreateUploadURL(t.Context(), UploadRequest{FileName: "empty.txt", FileSize: 0}); err != nil {
		t.Fatalf("CreateUploadURL: %v", err)
	}
	// The API errors when fileSize is absent, so an empty file must still send 0.
	got.assertJSONBody(t, `{"fileName":"empty.txt","fileSize":0}`)
}

func TestCreateUploadURLResumeRequiresModifiedTime(t *testing.T) {
	c, got := newServer(t, 201, `{"offset":0,"uploadUrl":"https://storage.example/u"}`)

	if _, err := c.CreateUploadURL(t.Context(), UploadRequest{FileName: "a", FileSize: 1, Resume: true}); err == nil {
		t.Fatal("want an error when resume is set without a modified time")
	}
	if got.Method != "" {
		t.Error("invalid resume request reached the server")
	}
}

func TestCreateDownloadURL(t *testing.T) {
	c, got := newServer(t, 200, `{"downloadUrl":"https://storage.example/v1/storage/download?auth=3","expiresIn":600}`)

	res, err := c.CreateDownloadURL(t.Context(), "FILE1")
	if err != nil {
		t.Fatalf("CreateDownloadURL: %v", err)
	}
	got.assert(t, http.MethodGet, "/drive/files/FILE1/download")
	if res.ExpiresIn != 600 || res.DownloadURL == "" {
		t.Errorf("ticket = %+v", res)
	}
}

// --- trash -----------------------------------------------------------------

func TestListTrash(t *testing.T) {
	c, got := newServer(t, 200, `{
	  "fileCount":12,"subFolderCount":3,
	  "resources":[{"accessedAt":"2026-08-11T09:00:00+09:00","category":"image",
	    "createdAt":"2026-08-11T09:00:00+09:00","deletedAt":"2026-08-11T10:00:00+09:00",
	    "lastModifiedBy":"mybox_user","modifiedAt":"2026-08-11T09:00:00+09:00",
	    "name":"회의록.pdf","parentId":"P","resourceId":"R","size":1048576,"type":"file"}],
	  "responseMetaData":{"nextCursor":"MjA"}}`)

	res, err := c.ListTrash(t.Context(), ListOptions{Sort: DefaultTrashSort})
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	got.assert(t, http.MethodGet, "/drive/trash")
	if want := "sort=deletedAt%2Cdesc"; got.RawQuery != want {
		t.Errorf("query = %q, want %q", got.RawQuery, want)
	}
	if len(res.Resources) != 1 || res.Resources[0].DeletedAt != "2026-08-11T10:00:00+09:00" {
		t.Errorf("resources = %+v", res.Resources)
	}
}

func TestRestoreFromTrash(t *testing.T) {
	c, got := newServer(t, 200, "")

	if err := c.RestoreFromTrash(t.Context(), "R", true); err != nil {
		t.Fatalf("RestoreFromTrash: %v", err)
	}
	got.assert(t, http.MethodPost, "/drive/trash/R/restore")
	got.assertJSONBody(t, `{"isOverwrite":true}`)
}

func TestPurgeTrashItem(t *testing.T) {
	c, got := newServer(t, 204, "")

	if err := c.PurgeTrashItem(t.Context(), "R"); err != nil {
		t.Fatalf("PurgeTrashItem: %v", err)
	}
	got.assert(t, http.MethodDelete, "/drive/trash/R")
}

func TestEmptyTrash(t *testing.T) {
	c, got := newServer(t, 204, "")

	if err := c.EmptyTrash(t.Context()); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	got.assert(t, http.MethodDelete, "/drive/trash")
}

// --- search ----------------------------------------------------------------

func TestSearchFiles(t *testing.T) {
	c, got := newServer(t, 200, `{
	  "resources":[{"category":"image","createdAt":"2026-06-26T15:04:05+09:00",
	    "modifiedAt":"2026-06-26T15:04:05+09:00","name":"사진.jpg",
	    "parentId":"P","parentPath":"/문서/","path":"/문서/사진.jpg",
	    "resourceId":"R","size":1048576}],
	  "responseMetaData":{"nextCursor":"Mjk5"}}`)

	res, err := c.SearchFiles(t.Context(), FileSearchOptions{
		Query: "1월 회의록", Category: "document", ParentPath: "/문서/",
		DateField: DateFieldModified, StartDate: "2026-01-01T00:00:00+09:00", Count: 50,
	})
	if err != nil {
		t.Fatalf("SearchFiles: %v", err)
	}
	got.assert(t, http.MethodGet, "/search/resources/files")

	for _, want := range []string{
		"q=1%EC%9B%94+%ED%9A%8C%EC%9D%98%EB%A1%9D",
		"category=document",
		"parentPath=%2F%EB%AC%B8%EC%84%9C%2F",
		"dateField=modified",
		"startDate=2026-01-01T00%3A00%3A00%2B09%3A00",
		"count=50",
	} {
		if !strings.Contains(got.RawQuery, want) {
			t.Errorf("query %q is missing %q", got.RawQuery, want)
		}
	}
	// Search is the only family that reports a path, which is what makes it
	// useful as a shortcut past tree walking.
	if res.Resources[0].Path != "/문서/사진.jpg" {
		t.Errorf("Path = %q", res.Resources[0].Path)
	}
}

func TestSearchFilesRejectsEmptyCriteria(t *testing.T) {
	c, got := newServer(t, 200, `{}`)

	if _, err := c.SearchFiles(t.Context(), FileSearchOptions{ParentPath: "/문서/"}); err == nil {
		t.Fatal("want an error when no search criterion is set")
	}
	if got.Method != "" {
		t.Error("criterion-less search reached the server")
	}
}

func TestSearchFilesRejectsUnknownCategory(t *testing.T) {
	c, _ := newServer(t, 200, `{}`)

	if _, err := c.SearchFiles(t.Context(), FileSearchOptions{Category: "spreadsheet"}); err == nil {
		t.Fatal("want an error for an unknown category")
	}
}

func TestSearchCountIsClampedToAPIRange(t *testing.T) {
	for _, tc := range []struct{ give, want string }{
		{"5", "count=20"},
		{"9999", "count=200"},
	} {
		c, got := newServer(t, 200, `{}`)
		n := 5
		if tc.give == "9999" {
			n = 9999
		}
		if _, err := c.SearchFiles(t.Context(), FileSearchOptions{Query: "x", Count: n}); err != nil {
			t.Fatalf("SearchFiles: %v", err)
		}
		if !strings.Contains(got.RawQuery, tc.want) {
			t.Errorf("count %d produced query %q, want %q", n, got.RawQuery, tc.want)
		}
	}
}

func TestSearchFolders(t *testing.T) {
	c, got := newServer(t, 200, `{
	  "resources":[{"createdAt":"2026-06-26T15:04:05+09:00","modifiedAt":"2026-06-26T15:04:05+09:00",
	    "name":"사진","parentId":"P","parentPath":"/문서/","path":"/문서/사진/","resourceId":"R"}],
	  "responseMetaData":{}}`)

	res, err := c.SearchFolders(t.Context(), FolderSearchOptions{Path: "/문서/사진/"})
	if err != nil {
		t.Fatalf("SearchFolders: %v", err)
	}
	got.assert(t, http.MethodGet, "/search/resources/folders")
	if res.Resources[0].ResourceID != "R" {
		t.Errorf("ResourceID = %q", res.Resources[0].ResourceID)
	}
	if res.ResponseMetaData.NextCursor != "" {
		t.Errorf("NextCursor = %q, want empty on the last page", res.ResponseMetaData.NextCursor)
	}
}

func TestSearchFoldersRejectsEmptyCriteria(t *testing.T) {
	c, _ := newServer(t, 200, `{}`)

	if _, err := c.SearchFolders(t.Context(), FolderSearchOptions{ParentPath: "/문서/"}); err == nil {
		t.Fatal("want an error when no search criterion is set")
	}
}

func TestSearchRejectsUnknownDateField(t *testing.T) {
	c, _ := newServer(t, 200, `{}`)

	if _, err := c.SearchFiles(t.Context(), FileSearchOptions{Query: "x", DateField: "accessed"}); err == nil {
		t.Fatal("want an error for an unsupported date field")
	}
}

// --- errors ----------------------------------------------------------------

func TestErrorEnvelopeIsParsed(t *testing.T) {
	c, _ := newServer(t, 404, errorJSON)

	_, err := c.GetResource(t.Context(), "MISSING")
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T (%v), want *api.Error", err, err)
	}
	if apiErr.Status != 404 || apiErr.Code != "PLAT-404" || apiErr.Message != "NOT_FOUND" {
		t.Errorf("error = %+v", apiErr)
	}
	if apiErr.RequestID != "f47ac10b-58cc-4372-a567-0e02b2c3d479" {
		t.Errorf("RequestID = %q", apiErr.RequestID)
	}
	if !apiErr.IsNotFound() {
		t.Error("IsNotFound() = false")
	}
	if want := "404 PLAT-404 NOT_FOUND (requestId: f47ac10b-58cc-4372-a567-0e02b2c3d479)"; apiErr.Error() != want {
		t.Errorf("Error() = %q, want %q", apiErr.Error(), want)
	}
}

func TestErrorPredicatesCoverEveryDocumentedStatus(t *testing.T) {
	for _, tc := range []struct {
		status    int
		code      string
		retryable bool
	}{
		{400, "PLAT-400", false},
		{401, "PLAT-401", false},
		{403, "PLAT-403", false},
		{404, "PLAT-404", false},
		{409, "PLAT-409", false},
		{422, "PLAT-422", false},
		{423, "PLAT-423", false},
		{429, "PLAT-429", true},
		{500, "PLAT-500", true},
		{502, "PLAT-502", true},
		{503, "PLAT-503", true},
		// Retrying an out-of-space error cannot succeed until the user frees space.
		{507, "PLAT-507", false},
	} {
		e := &Error{Status: tc.status, Code: tc.code}
		if e.Retryable() != tc.retryable {
			t.Errorf("status %d: Retryable() = %v, want %v", tc.status, e.Retryable(), tc.retryable)
		}
	}
}

func TestNonJSONErrorBodyIsPreserved(t *testing.T) {
	c, _ := newServer(t, 502, "<html>gateway down</html>")

	_, err := c.GetStorage(t.Context())
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is %T, want *api.Error", err)
	}
	if apiErr.Body != "<html>gateway down</html>" {
		t.Errorf("Body = %q", apiErr.Body)
	}
	if !strings.Contains(apiErr.Error(), "gateway down") {
		t.Errorf("Error() = %q, want it to include the raw body", apiErr.Error())
	}
}

// --- retries ---------------------------------------------------------------

// retryServer answers with each status in sequence, then repeats the last one.
func retryServer(t *testing.T, statuses []int, finalBody string) (*Client, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(calls.Add(1)) - 1
		status := statuses[min(n, len(statuses)-1)]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status < 300 {
			io.WriteString(w, finalBody)
		} else {
			io.WriteString(w, `{"code":"PLAT-`+http.StatusText(status)+`","message":"X"}`)
		}
	}))
	t.Cleanup(srv.Close)
	return newTestClient(t, Options{BaseURL: srv.URL, Token: "mbx_pat_test"}), &calls
}

func TestRetriesTransientFailuresThenSucceeds(t *testing.T) {
	c, calls := retryServer(t, []int{429, 503, 200}, storageJSON)

	if _, err := c.GetStorage(t.Context()); err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestRetriesAreBounded(t *testing.T) {
	c, calls := retryServer(t, []int{503}, "")

	if _, err := c.GetStorage(t.Context()); err == nil {
		t.Fatal("want an error after exhausting retries")
	}
	// One initial attempt plus defaultMaxRetries.
	if got := calls.Load(); got != defaultMaxRetries+1 {
		t.Errorf("attempts = %d, want %d", got, defaultMaxRetries+1)
	}
}

func TestClientErrorsAreNotRetried(t *testing.T) {
	c, calls := retryServer(t, []int{404}, "")

	if _, err := c.GetStorage(t.Context()); err == nil {
		t.Fatal("want an error")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (404 must not be retried)", got)
	}
}

func TestOutOfSpaceIsNotRetried(t *testing.T) {
	c, calls := retryServer(t, []int{507}, "")

	_, err := c.GetStorage(t.Context())
	var apiErr *Error
	if !errors.As(err, &apiErr) || !apiErr.IsOutOfSpace() {
		t.Fatalf("error = %v, want an out-of-space error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (507 must not be retried)", got)
	}
}

func TestRetryAfterHeaderIsHonoured(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			io.WriteString(w, `{"code":"PLAT-429","message":"TOO_MANY_REQUESTS"}`)
			return
		}
		io.WriteString(w, storageJSON)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "mbx_pat_test"})
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	if _, err := c.GetStorage(t.Context()); err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if len(slept) != 1 || slept[0] != 2*time.Second {
		t.Errorf("slept = %v, want exactly [2s] from the Retry-After header", slept)
	}
}

func TestRetryAfterParsing(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   time.Duration
		ok     bool
	}{
		{"", 0, false},
		{"3", 3 * time.Second, true},
		{" 3 ", 3 * time.Second, true},
		{"99999", maxBackoff, true},                // capped so a hostile header cannot stall us
		{"not-a-number", 0, false},                 // fall back to exponential backoff
		{"Mon, 01 Jan 2000 00:00:00 GMT", 0, true}, // a past date means "retry now"
	} {
		h := http.Header{}
		if tc.header != "" {
			h.Set("Retry-After", tc.header)
		}
		got, ok := retryAfter(h)
		if ok != tc.ok || got != tc.want {
			t.Errorf("retryAfter(%q) = %v, %v; want %v, %v", tc.header, got, ok, tc.want, tc.ok)
		}
	}
}

func TestContextCancellationStopsRetrying(t *testing.T) {
	c, calls := retryServer(t, []int{503}, "")
	ctx, cancel := context.WithCancel(t.Context())
	c.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}

	if _, err := c.GetStorage(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1", got)
	}
}

// --- misc ------------------------------------------------------------------

func TestTraceNeverLeaksTheToken(t *testing.T) {
	var lines []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, storageJSON)
	}))
	t.Cleanup(srv.Close)

	c := newTestClient(t, Options{
		BaseURL: srv.URL,
		Token:   "mbx_pat_supersecret",
		Trace:   func(s string) { lines = append(lines, s) },
	})
	if _, err := c.GetStorage(t.Context()); err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if len(lines) == 0 {
		t.Fatal("trace produced no output")
	}
	for _, l := range lines {
		if strings.Contains(l, "supersecret") {
			t.Errorf("trace leaked the token: %q", l)
		}
	}
}

func TestUserAgentIsSent(t *testing.T) {
	c, got := newServer(t, 200, storageJSON)

	if _, err := c.GetStorage(t.Context()); err != nil {
		t.Fatalf("GetStorage: %v", err)
	}
	if got.UserAgent != "mybox-cli/test" {
		t.Errorf("User-Agent = %q", got.UserAgent)
	}
}

func TestNewDefaultsToProductionBaseURL(t *testing.T) {
	c, err := New(Options{Token: "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.BaseURL() != DefaultBaseURL {
		t.Errorf("BaseURL() = %q, want %q", c.BaseURL(), DefaultBaseURL)
	}
}

func TestNewTrimsTrailingSlashFromBaseURL(t *testing.T) {
	c, err := New(Options{BaseURL: "https://example.test/v1/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.BaseURL() != "https://example.test/v1" {
		t.Errorf("BaseURL() = %q", c.BaseURL())
	}
}
