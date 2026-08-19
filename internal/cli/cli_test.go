package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/config"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/overworks/mybox-cli/internal/transfer"
)

// fakeAPI is a stand-in MYBOX server driven by a routing table, so each test
// declares only the responses it cares about.
type fakeAPI struct {
	t *testing.T
	// routes maps "METHOD /path" to a handler.
	routes map[string]http.HandlerFunc
	// requests records every request line the server saw, in order.
	requests []string
}

func newFakeAPI(t *testing.T) *fakeAPI {
	return &fakeAPI{t: t, routes: map[string]http.HandlerFunc{}}
}

func (f *fakeAPI) handle(method, path string, h http.HandlerFunc) *fakeAPI {
	f.routes[method+" "+path] = h
	return f
}

// json registers a route that answers with a fixed status and JSON body.
func (f *fakeAPI) json(method, path string, status int, body string) *fakeAPI {
	return f.handle(method, path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (f *fakeAPI) start() *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if h, ok := f.routes[r.Method+" "+r.URL.Path]; ok {
			h(w, r)
			return
		}
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"code":"PLAT-404","message":"NOT_FOUND"}`))
	}))
	f.t.Cleanup(srv.Close)
	return srv
}

// run executes the CLI against the fake server in an isolated environment and
// returns stdout, stderr and the exit code.
func (f *fakeAPI) run(args ...string) (stdout, stderr string, code int) {
	f.t.Helper()
	srv := f.start()

	f.t.Setenv(config.EnvAPIBase, srv.URL)
	f.t.Setenv(config.EnvToken, "mbx_pat_test")
	f.t.Setenv(config.EnvProfile, "")
	f.t.Setenv(config.EnvConfigHome, f.t.TempDir())
	f.t.Setenv(resolve.EnvCacheHome, f.t.TempDir())

	var out, errBuf bytes.Buffer
	code = Execute(f.t.Context(), args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

// listing builds a single-page listing response for the given entries.
func listing(items ...api.ResourceItem) string {
	out := api.ResourceList{Resources: items}
	for _, it := range items {
		if it.IsFolder() {
			out.SubFolderCount++
		} else {
			out.FileCount++
		}
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

func file(id, name string, size int64) api.ResourceItem {
	return api.ResourceItem{
		ResourceID: id, Name: name, Type: api.TypeFile, Size: size,
		CreatedAt:  "2026-08-11T09:00:00+09:00",
		ModifiedAt: "2026-08-11T09:00:00+09:00",
		AccessedAt: "2026-08-11T09:00:00+09:00",
	}
}

func folder(id, name string) api.ResourceItem {
	return api.ResourceItem{
		ResourceID: id, Name: name, Type: api.TypeFolder,
		CreatedAt:  "2026-08-11T09:00:00+09:00",
		ModifiedAt: "2026-08-11T09:00:00+09:00",
		AccessedAt: "2026-08-11T09:00:00+09:00",
	}
}

const storageBody = `{
  "fileCounts":{"archive":5,"audio":8,"document":30,"etc":23,"executable":2,"image":40,"total":120,"video":12},
  "maxFileBytes":53687091200,"quotaBytes":32212254720,"trashAutoDeleteDays":5,"usedBytes":5368709120}`

// --- df --------------------------------------------------------------------

func TestDfRendersUsage(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("df")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"5.0 GiB", "30.0 GiB", "16.7%", "5 days"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestDfJSONIsRawAPIShape(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("--json", "df")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	var st api.Storage
	if err := json.Unmarshal([]byte(stdout), &st); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if st.QuotaBytes != 32212254720 || st.FileCounts.Total != 120 {
		t.Errorf("decoded = %+v", st)
	}
}

// --- ls --------------------------------------------------------------------

func TestLsRoot(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200,
		listing(folder("DOC", "문서"), file("R", "읽어보기.txt", 1024)))

	stdout, _, code := f.run("ls")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	// Folders are marked so a listing reads like ls.
	if !strings.Contains(stdout, "문서/") {
		t.Errorf("folder not marked with a separator:\n%s", stdout)
	}
	if !strings.Contains(stdout, "읽어보기.txt") {
		t.Errorf("file missing:\n%s", stdout)
	}
}

func TestLsResolvesNestedPath(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(folder("DOC", "문서"))).
		json("GET", "/drive/folders/DOC/resources", 200, listing(folder("Y", "2026"))).
		json("GET", "/drive/folders/Y/resources", 200, listing(file("M", "회의록.pdf", 2048)))

	stdout, _, code := f.run("ls", "/문서/2026")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "회의록.pdf") {
		t.Errorf("output:\n%s", stdout)
	}
}

func TestLsLongFormShowsSizeAndID(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200,
		listing(file("FILEID", "회의록.pdf", 1048576)))

	stdout, _, code := f.run("ls", "-l")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"1.0 MiB", "FILEID", "회의록.pdf"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("long output missing %q:\n%s", want, stdout)
		}
	}
}

func TestLsHidesHiddenItemsUnlessAsked(t *testing.T) {
	hidden := file("H", "숨김.txt", 1)
	hidden.IsHidden = true
	body := listing(file("V", "보임.txt", 1), hidden)

	f := newFakeAPI(t).json("GET", "/drive/resources", 200, body)
	stdout, _, _ := f.run("ls")
	if strings.Contains(stdout, "숨김.txt") {
		t.Errorf("hidden item shown without --all:\n%s", stdout)
	}

	f2 := newFakeAPI(t).json("GET", "/drive/resources", 200, body)
	stdout2, _, _ := f2.run("ls", "--all")
	if !strings.Contains(stdout2, "숨김.txt") {
		t.Errorf("hidden item missing with --all:\n%s", stdout2)
	}
}

func TestLsLimitStopsEarly(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200,
		listing(file("A", "a.txt", 1), file("B", "b.txt", 1), file("C", "c.txt", 1)))

	stdout, _, _ := f.run("ls", "-n", "2")
	if strings.Contains(stdout, "c.txt") {
		t.Errorf("--limit did not cap the listing:\n%s", stdout)
	}
}

func TestLsEmptyFolderSaysSo(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing())

	stdout, stderr, code := f.run("ls")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Errorf("stdout should stay empty for scripts:\n%s", stdout)
	}
	if !strings.Contains(stderr, "is empty") {
		t.Errorf("stderr should explain the empty result:\n%s", stderr)
	}
}

func TestLsRejectsAFileAsTarget(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing(file("F", "메모.txt", 1)))

	_, stderr, code := f.run("ls", "/메모.txt")
	if code == ExitOK {
		t.Fatal("listing a file should fail")
	}
	if !strings.Contains(stderr, "is not a folder") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestLsMissingPathExitsNotFound(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing(folder("DOC", "문서")))

	_, stderr, code := f.run("ls", "/없는폴더")
	if code != ExitNotFound {
		t.Errorf("exit = %d, want %d", code, ExitNotFound)
	}
	if !strings.Contains(stderr, "no such path") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestLsSuggestsACaseVariant(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing(folder("R", "Reports")))

	_, stderr, _ := f.run("ls", "/reports")
	if !strings.Contains(stderr, "Reports") {
		t.Errorf("stderr should suggest the real name:\n%s", stderr)
	}
}

func TestLsAcceptsIDReferenceWithoutWalking(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources/DOC", 200, `{"resourceId":"DOC","name":"문서","type":"folder"}`).
		json("GET", "/drive/folders/DOC/resources", 200, listing(file("M", "회의록.pdf", 1)))

	stdout, _, code := f.run("ls", "id:DOC")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "회의록.pdf") {
		t.Errorf("output:\n%s", stdout)
	}
	// The root listing must never be fetched for an id: reference.
	for _, req := range f.requests {
		if req == "GET /drive/resources" {
			t.Error("an id: reference still walked from the root")
		}
	}
}

// --- stat ------------------------------------------------------------------

func TestStat(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("M", "회의록.pdf", 1048576))).
		json("GET", "/drive/resources/M", 200, `{
		  "resourceId":"M","name":"회의록.pdf","parentId":"ROOT","type":"file","size":1048576,
		  "category":"document","createdAt":"2026-08-11T09:00:00+09:00",
		  "modifiedAt":"2026-08-11T09:00:00+09:00","accessedAt":"2026-08-11T09:00:00+09:00",
		  "lastModifiedBy":"mybox_user","isFavorite":true,"isHidden":false}`)

	stdout, _, code := f.run("stat", "/회의록.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"회의록.pdf", "1.0 MiB", "1048576 bytes", "document", "/회의록.pdf"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestStatOnRootIsAUsageError(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("stat", "/")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "df") {
		t.Errorf("stderr should point at df:\n%s", stderr)
	}
}

// --- search ----------------------------------------------------------------

func TestSearchFiles(t *testing.T) {
	f := newFakeAPI(t).handle("GET", "/search/resources/files", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "회의록" {
			t.Errorf("q = %q, want 회의록", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[{"resourceId":"M","name":"회의록.pdf",
		  "path":"/문서/2026/회의록.pdf","parentPath":"/문서/2026/","size":1048576,
		  "modifiedAt":"2026-08-11T09:00:00+09:00","category":"document"}],
		  "responseMetaData":{}}`))
	})

	stdout, _, code := f.run("search", "files", "회의록")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	// The path is what makes search worth using over a tree walk.
	if !strings.Contains(stdout, "/문서/2026/회의록.pdf") {
		t.Errorf("output:\n%s", stdout)
	}
}

func TestSearchFilesNeedsACriterion(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("search", "files")
	if code == ExitOK {
		t.Fatal("a criterion-less search should fail")
	}
	if len(f.requests) != 0 {
		t.Errorf("an invalid search still hit the API: %v", f.requests)
	}
	if !strings.Contains(stderr, "at least one of") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestSearchRejectsUnknownCategory(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("search", "files", "--category", "spreadsheet")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "document") {
		t.Errorf("stderr should list the valid categories:\n%s", stderr)
	}
}

func TestSearchNormalisesBareDates(t *testing.T) {
	var gotStart, gotEnd string
	f := newFakeAPI(t).handle("GET", "/search/resources/files", func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("startDate")
		gotEnd = r.URL.Query().Get("endDate")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[],"responseMetaData":{}}`))
	})

	if _, _, code := f.run("search", "files", "--since", "2026-01-01", "--until", "2026-01-31"); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	// Bare dates are anchored to KST, the zone the API reports in.
	if gotStart != "2026-01-01T00:00:00+09:00" {
		t.Errorf("startDate = %q", gotStart)
	}
	// An upper bound should cover the whole day the user named.
	if !strings.HasPrefix(gotEnd, "2026-01-31T23:59:59") {
		t.Errorf("endDate = %q, want the end of the day", gotEnd)
	}
}

func TestSearchRejectsUnparseableDate(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("search", "files", "--since", "작년")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "--since") {
		t.Errorf("stderr should name the offending flag:\n%s", stderr)
	}
}

func TestSearchFolders(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/search/resources/folders", 200,
		`{"resources":[{"resourceId":"Y","name":"2026","path":"/문서/2026/",
		  "modifiedAt":"2026-08-11T09:00:00+09:00"}],"responseMetaData":{}}`)

	stdout, _, code := f.run("search", "folders", "2026")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "/문서/2026/") || !strings.Contains(stdout, "Y") {
		t.Errorf("output:\n%s", stdout)
	}
}

// --- trash -----------------------------------------------------------------

func TestTrashList(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/trash", 200, `{
	  "fileCount":1,"subFolderCount":0,
	  "resources":[{"resourceId":"T","name":"삭제됨.pdf","type":"file","size":2048,
	    "deletedAt":"2026-08-11T10:00:00+09:00","modifiedAt":"2026-08-11T09:00:00+09:00"}],
	  "responseMetaData":{}}`)

	stdout, _, code := f.run("trash", "ls")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"삭제됨.pdf", "2.0 KiB", "T"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestTrashListDefaultsToDeletionOrder(t *testing.T) {
	var gotSort string
	f := newFakeAPI(t).handle("GET", "/drive/trash", func(w http.ResponseWriter, r *http.Request) {
		gotSort = r.URL.Query().Get("sort")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":[],"responseMetaData":{}}`))
	})

	f.run("trash", "ls")
	if gotSort != api.DefaultTrashSort {
		t.Errorf("sort = %q, want %q", gotSort, api.DefaultTrashSort)
	}
}

// --- auth and errors -------------------------------------------------------

func TestAuthStatusRedactsTheToken(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("auth", "status")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(stdout, "mbx_pat_test") {
		t.Errorf("status printed the raw token:\n%s", stdout)
	}
	if !strings.Contains(stdout, "****") {
		t.Errorf("status should show a redacted token:\n%s", stdout)
	}
}

func TestUnauthorizedExitsWithAuthCode(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 401,
		`{"code":"PLAT-401","message":"UNAUTHORIZED","requestId":"abc"}`)

	_, stderr, code := f.run("df")
	if code != ExitAuth {
		t.Errorf("exit = %d, want %d", code, ExitAuth)
	}
	if !strings.Contains(stderr, "auth login") {
		t.Errorf("stderr should suggest re-authenticating:\n%s", stderr)
	}
	if !strings.Contains(stderr, "abc") {
		t.Errorf("stderr should carry the requestId for support:\n%s", stderr)
	}
}

func TestOutOfSpaceExitCode(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 507,
		`{"code":"PLAT-507","message":"INSUFFICIENT_STORAGE"}`)

	_, _, code := f.run("df")
	if code != ExitOutOfSpace {
		t.Errorf("exit = %d, want %d", code, ExitOutOfSpace)
	}
}

func TestRateLimitedExitCode(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 429,
		`{"code":"PLAT-429","message":"TOO_MANY_REQUESTS"}`)

	_, _, code := f.run("--timeout", "2s", "df")
	if code != ExitRateLimited {
		t.Errorf("exit = %d, want %d", code, ExitRateLimited)
	}
}

func TestVersionNeedsNoCredentials(t *testing.T) {
	f := newFakeAPI(t)

	stdout, _, code := f.run("version")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "mybox") {
		t.Errorf("output:\n%s", stdout)
	}
	if len(f.requests) != 0 {
		t.Errorf("version called the API: %v", f.requests)
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	f := newFakeAPI(t)

	_, _, code := f.run("frobnicate")
	if code != ExitError && code != ExitUsage {
		t.Errorf("exit = %d, want a failure", code)
	}
	if len(f.requests) != 0 {
		t.Errorf("an unknown command called the API: %v", f.requests)
	}
}

// --- mutations -------------------------------------------------------------

func TestMkdirInRoot(t *testing.T) {
	var body string
	f := newFakeAPI(t).handle("POST", "/drive/folders", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"업무자료","resourceId":"NEW"}`))
	})

	_, stderr, code := f.run("mkdir", "/업무자료")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// A top-level folder needs no parentId and no tree walk to find one.
	if !strings.Contains(body, `"folderName":"업무자료"`) || strings.Contains(body, "parentId") {
		t.Errorf("request body = %s", body)
	}
}

func TestMkdirParentsCreatesOnlyWhatIsMissing(t *testing.T) {
	var created []string
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(folder("DOC", "문서"))).
		json("GET", "/drive/folders/DOC/resources", 200, listing()).
		handle("POST", "/drive/folders", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				FolderName string `json:"folderName"`
				ParentID   string `json:"parentId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			created = append(created, req.FolderName)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"name":"` + req.FolderName + `","resourceId":"ID_` + req.FolderName + `"}`))
		})

	_, stderr, code := f.run("mkdir", "-p", "/문서/2026/1월")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// 문서 already exists, so only the two missing levels are created.
	if strings.Join(created, ",") != "2026,1월" {
		t.Errorf("created = %v, want [2026 1월]", created)
	}
}

func TestMkdirWithoutParentsFailsOnMissingParent(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing())

	_, _, code := f.run("mkdir", "/없는폴더/하위")
	if code != ExitNotFound {
		t.Errorf("exit = %d, want %d", code, ExitNotFound)
	}
}

func TestCopyIntoExistingFolder(t *testing.T) {
	var body string
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1), folder("DST", "백업"))).
		handle("POST", "/drive/resources/SRC/copy", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"name":"회의록.pdf","resourceId":"COPY"}`))
		})

	_, stderr, code := f.run("cp", "/회의록.pdf", "/백업")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// Copying into a folder keeps the original name, so no name is sent.
	if !strings.Contains(body, `"parentId":"DST"`) || strings.Contains(body, `"name"`) {
		t.Errorf("request body = %s", body)
	}
}

func TestCopyToNewNameUsesLastSegment(t *testing.T) {
	var body string
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1), folder("DST", "백업"))).
		json("GET", "/drive/folders/DST/resources", 200, listing()).
		handle("POST", "/drive/resources/SRC/copy", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"name":"사본.pdf","resourceId":"COPY"}`))
		})

	_, stderr, code := f.run("cp", "/회의록.pdf", "/백업/사본.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(body, `"name":"사본.pdf"`) || !strings.Contains(body, `"parentId":"DST"`) {
		t.Errorf("request body = %s", body)
	}
}

func TestMoveIntoFolder(t *testing.T) {
	var body string
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1), folder("DST", "보관"))).
		handle("POST", "/drive/resources/SRC/move", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			body = string(raw)
			w.WriteHeader(200)
		})

	_, stderr, code := f.run("mv", "/회의록.pdf", "/보관")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(body, `"parentId":"DST"`) {
		t.Errorf("request body = %s", body)
	}
	for _, req := range f.requests {
		if strings.HasSuffix(req, "/rename") {
			t.Error("a plain move should not call rename")
		}
	}
}

func TestMoveWithRenameCallsBothEndpoints(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1), folder("DST", "보관"))).
		json("GET", "/drive/folders/DST/resources", 200, listing()).
		json("POST", "/drive/resources/SRC/rename", 200, `{"name":"2026-회의록.pdf"}`).
		json("POST", "/drive/resources/SRC/move", 200, "")

	_, stderr, code := f.run("mv", "/회의록.pdf", "/보관/2026-회의록.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// The API has no combined move-and-rename, so both must be called, rename first.
	var order []string
	for _, req := range f.requests {
		if strings.HasSuffix(req, "/rename") || strings.HasSuffix(req, "/move") {
			order = append(order, req)
		}
	}
	if len(order) != 2 || !strings.HasSuffix(order[0], "/rename") {
		t.Errorf("call order = %v, want rename then move", order)
	}
}

func TestRename(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "초안.pdf", 1))).
		json("POST", "/drive/resources/SRC/rename", 200, `{"name":"최종.pdf"}`)

	_, stderr, code := f.run("rename", "/초안.pdf", "최종.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "최종.pdf") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestRemoveFileNeedsNoConfirmation(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "초안.pdf", 1))).
		json("DELETE", "/drive/resources/SRC", 204, "")

	_, stderr, code := f.run("rm", "/초안.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
}

func TestRemoveFolderRefusesWithoutConfirmation(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing(folder("DIR", "임시")))

	_, stderr, code := f.run("rm", "/임시")
	// stdin is not a terminal under test, so there is nobody to ask.
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "--yes") {
		t.Errorf("stderr should mention --yes:\n%s", stderr)
	}
	for _, req := range f.requests {
		if strings.HasPrefix(req, "DELETE") {
			t.Error("the folder was deleted despite the refusal")
		}
	}
}

func TestRemoveFolderProceedsWithYes(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(folder("DIR", "임시"))).
		json("DELETE", "/drive/resources/DIR", 204, "")

	_, stderr, code := f.run("rm", "-y", "/임시")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
}

func TestRemoveResolvesEverythingBeforeDeletingAnything(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/resources", 200, listing(file("A", "있음.txt", 1)))

	_, _, code := f.run("rm", "/있음.txt", "/없음.txt")
	if code != ExitNotFound {
		t.Errorf("exit = %d, want %d", code, ExitNotFound)
	}
	// The first file must survive a typo in the second argument.
	for _, req := range f.requests {
		if strings.HasPrefix(req, "DELETE") {
			t.Errorf("deleted %s despite a later argument failing to resolve", req)
		}
	}
}

func TestStarAndUnstar(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1))).
		json("POST", "/drive/resources/SRC/favorite", 200, `{"isFavorite":true,"resourceId":"SRC"}`).
		json("POST", "/drive/resources/SRC/unfavorite", 200, `{"isFavorite":false,"resourceId":"SRC"}`)

	if _, stderr, code := f.run("star", "/회의록.pdf"); code != ExitOK {
		t.Fatalf("star exit = %d: %s", code, stderr)
	}

	f2 := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("SRC", "회의록.pdf", 1))).
		json("POST", "/drive/resources/SRC/unfavorite", 200, `{"isFavorite":false,"resourceId":"SRC"}`)
	if _, stderr, code := f2.run("unstar", "/회의록.pdf"); code != ExitOK {
		t.Fatalf("unstar exit = %d: %s", code, stderr)
	}
}

// --- trash mutations -------------------------------------------------------

const trashOneItem = `{"fileCount":1,"subFolderCount":0,
  "resources":[{"resourceId":"T1","name":"삭제됨.pdf","type":"file","size":2048,
    "deletedAt":"2026-08-11T10:00:00+09:00"}],"responseMetaData":{}}`

func TestTrashRestoreByName(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/trash", 200, trashOneItem).
		json("POST", "/drive/trash/T1/restore", 200, "")

	_, stderr, code := f.run("trash", "restore", "삭제됨.pdf")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
}

func TestTrashRestoreByIDSkipsTheListing(t *testing.T) {
	f := newFakeAPI(t).json("POST", "/drive/trash/T1/restore", 200, "")

	_, stderr, code := f.run("trash", "restore", "id:T1")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	for _, req := range f.requests {
		if req == "GET /drive/trash" {
			t.Error("an id: reference still listed the trash")
		}
	}
}

func TestTrashRestoreRefusesAmbiguousName(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/trash", 200, `{
	  "resources":[
	    {"resourceId":"A","name":"중복.pdf","type":"file","deletedAt":"2026-08-11T10:00:00+09:00"},
	    {"resourceId":"B","name":"중복.pdf","type":"file","deletedAt":"2026-08-10T10:00:00+09:00"}],
	  "responseMetaData":{}}`)

	_, stderr, code := f.run("trash", "restore", "중복.pdf")
	if code == ExitOK {
		t.Fatal("an ambiguous name should not be resolved arbitrarily")
	}
	if !strings.Contains(stderr, "id:A") || !strings.Contains(stderr, "id:B") {
		t.Errorf("stderr should list both candidates:\n%s", stderr)
	}
}

func TestTrashRemoveRequiresConfirmation(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/trash", 200, trashOneItem)

	_, stderr, code := f.run("trash", "rm", "삭제됨.pdf")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "cannot be undone") {
		t.Errorf("stderr should warn that this is permanent:\n%s", stderr)
	}
	for _, req := range f.requests {
		if strings.HasPrefix(req, "DELETE") {
			t.Error("the item was purged despite the refusal")
		}
	}
}

func TestTrashEmptyShowsCountBeforeAsking(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/trash", 200, trashOneItem)

	_, stderr, code := f.run("trash", "empty")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "1 items") {
		t.Errorf("the prompt should say how much will be destroyed:\n%s", stderr)
	}
}

func TestTrashEmptyOnAnEmptyTrashDoesNothing(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/trash", 200, `{"resources":[],"responseMetaData":{}}`)

	_, stderr, code := f.run("trash", "empty")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "already empty") {
		t.Errorf("stderr:\n%s", stderr)
	}
	for _, req := range f.requests {
		if req == "DELETE /drive/trash" {
			t.Error("an already-empty trash was still emptied")
		}
	}
}

func TestTrashEmptyProceedsWithYes(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/trash", 200, trashOneItem).
		json("DELETE", "/drive/trash", 204, "")

	_, stderr, code := f.run("trash", "empty", "--yes")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
}

func TestTrashAutoDeleteGetAndSet(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)
	stdout, _, code := f.run("trash", "autodelete")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout, "5 days") {
		t.Errorf("output = %q", stdout)
	}

	f2 := newFakeAPI(t).json("PATCH", "/drive/storage", 200, `{"trashAutoDeleteDays":30}`)
	if _, stderr, code := f2.run("trash", "autodelete", "30"); code != ExitOK {
		t.Fatalf("set exit = %d: %s", code, stderr)
	}
}

func TestTrashAutoDeleteRejectsUndocumentedValue(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("trash", "autodelete", "7")
	if code == ExitOK {
		t.Fatal("an undocumented interval should be rejected")
	}
	if len(f.requests) != 0 {
		t.Errorf("the invalid value still reached the API: %v", f.requests)
	}
	if !strings.Contains(stderr, "50") {
		t.Errorf("stderr should list the allowed values:\n%s", stderr)
	}
}

func TestTrashAutoDeleteZeroTurnsItOff(t *testing.T) {
	f := newFakeAPI(t).json("PATCH", "/drive/storage", 200, `{"trashAutoDeleteDays":0}`)

	_, stderr, code := f.run("trash", "autodelete", "0")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if !strings.Contains(stderr, "off") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// --- transfers -------------------------------------------------------------

// withStorage adds fake storage endpoints and returns handles to what they saw.
// The API's download/upload URLs are rewritten to point back at the same test
// server, so the two-step "get a URL, then use it" flow is exercised whole.
type storageState struct {
	downloadBody []byte
	uploaded     []byte
	uploadMethod string
	uploadCT     string
	uploadPart   string
	uploadRange  string
	uploadStatus int
}

func (f *fakeAPI) withStorage(st *storageState) *fakeAPI {
	if st.uploadStatus == 0 {
		st.uploadStatus = 200
	}
	f.handle("GET", "/storage/download", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(st.downloadBody)
	})
	upload := func(w http.ResponseWriter, r *http.Request) {
		st.uploadMethod = r.Method
		st.uploadCT = r.Header.Get("Content-Type")
		st.uploadRange = r.Header.Get("Content-Range")

		if strings.HasPrefix(st.uploadCT, "multipart/form-data") {
			if err := r.ParseMultipartForm(8 << 20); err == nil && r.MultipartForm != nil {
				for name, files := range r.MultipartForm.File {
					st.uploadPart = name
					if len(files) > 0 {
						if fh, err := files[0].Open(); err == nil {
							st.uploaded, _ = io.ReadAll(fh)
							fh.Close()
						}
					}
				}
			}
		} else {
			st.uploaded, _ = io.ReadAll(r.Body)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(st.uploadStatus)
		if st.uploadStatus < 300 {
			// The host reports the size of the stored file, which for a resumed
			// upload is more than this request carried.
			stored := len(st.uploaded)
			if st.uploadRange != "" {
				if _, err := fmt.Sscanf(st.uploadRange, "%*d-%*d/%d", &stored); err != nil {
					stored = len(st.uploaded)
				}
			}
			_, _ = io.WriteString(w, `{"resourceId":"STORED","name":"stored","fileSize":`+
				strconv.Itoa(stored)+`}`)
		}
	}
	f.handle("POST", "/storage/upload", upload)
	f.handle("PUT", "/storage/upload", upload)
	return f
}

// selfURL rewrites a storage path into an absolute URL on the running test
// server. It is called from inside a handler, where the address is known.
func selfURL(r *http.Request, path string) string {
	return "http://" + r.Host + path
}

func TestDownloadWritesTheFile(t *testing.T) {
	st := &storageState{downloadBody: []byte("회의록 내용")}
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("M", "회의록.pdf", 11))).
		json("GET", "/drive/resources/M", 200, `{"resourceId":"M","name":"회의록.pdf","type":"file","size":11}`).
		handle("GET", "/drive/files/M/download", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"downloadUrl":"` + selfURL(r, "/storage/download") + `","expiresIn":600}`))
		}).
		withStorage(st)

	dir := t.TempDir()
	_, stderr, code := f.run("down", "/회의록.pdf", "-o", dir)
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}

	got, err := os.ReadFile(filepath.Join(dir, "회의록.pdf"))
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != "회의록 내용" {
		t.Errorf("content = %q", got)
	}
}

func TestDownloadToStdout(t *testing.T) {
	st := &storageState{downloadBody: []byte("파이프로")}
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("M", "a.txt", 8))).
		json("GET", "/drive/resources/M", 200, `{"resourceId":"M","name":"a.txt","type":"file","size":8}`).
		handle("GET", "/drive/files/M/download", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"downloadUrl":"` + selfURL(r, "/storage/download") + `","expiresIn":600}`))
		}).
		withStorage(st)

	stdout, _, code := f.run("down", "/a.txt", "-o", "-")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	if stdout != "파이프로" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestDownloadRefusesToOverwriteWithoutTheFlag(t *testing.T) {
	st := &storageState{downloadBody: []byte("new")}
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("M", "a.txt", 3))).
		json("GET", "/drive/resources/M", 200, `{"resourceId":"M","name":"a.txt","type":"file","size":3}`).
		handle("GET", "/drive/files/M/download", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"downloadUrl":"` + selfURL(r, "/storage/download") + `","expiresIn":600}`))
		}).
		withStorage(st)

	dir := t.TempDir()
	existing := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("down", "/a.txt", "-o", dir)
	if code == ExitOK {
		t.Fatal("an existing local file should not be clobbered silently")
	}
	if !strings.Contains(stderr, "--overwrite") {
		t.Errorf("stderr should name the flag:\n%s", stderr)
	}
	if got, _ := os.ReadFile(existing); string(got) != "old" {
		t.Errorf("the existing file was modified: %q", got)
	}
}

func TestDownloadLeavesNoPartialFileOnFailure(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(file("M", "a.txt", 3))).
		json("GET", "/drive/resources/M", 200, `{"resourceId":"M","name":"a.txt","type":"file","size":3}`).
		handle("GET", "/drive/files/M/download", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"downloadUrl":"` + selfURL(r, "/storage/download") + `","expiresIn":600}`))
		}).
		handle("GET", "/storage/download", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, "expired")
		})

	dir := t.TempDir()
	if _, _, code := f.run("down", "/a.txt", "-o", dir); code == ExitOK {
		t.Fatal("want a failure")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Neither the target name nor a stray temp file may survive a failure.
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("failed download left files behind: %v", names)
	}
}

func TestDownloadRejectsAFolder(t *testing.T) {
	f := newFakeAPI(t).
		json("GET", "/drive/resources", 200, listing(folder("D", "문서")))

	_, stderr, code := f.run("down", "/문서", "-o", t.TempDir())
	if code == ExitOK {
		t.Fatal("downloading a folder should fail")
	}
	if !strings.Contains(stderr, "is not a file") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestUploadSendsFileContents(t *testing.T) {
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		json("GET", "/drive/resources", 200, listing(folder("DST", "업무자료"))).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				FileName string `json:"fileName"`
				FileSize int64  `json:"fileSize"`
				ParentID string `json:"parentId"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.FileName != "보고서.pdf" || req.ParentID != "DST" {
				t.Errorf("upload request = %+v", req)
			}
			// "내용입" is nine bytes in UTF-8; the API needs the byte count,
			// not the character count.
			if req.FileSize != 9 {
				t.Errorf("fileSize = %d, want 9", req.FileSize)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":0,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "보고서.pdf")
	if err := os.WriteFile(local, []byte("내용입"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local, "/업무자료")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	if string(st.uploaded) != "내용입" {
		t.Errorf("uploaded = %q", st.uploaded)
	}
	// The verified wire format: POST, multipart, part named Filedata.
	if st.uploadMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", st.uploadMethod)
	}
	if !strings.HasPrefix(st.uploadCT, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", st.uploadCT)
	}
	if st.uploadPart != transfer.PartName {
		t.Errorf("part name = %q, want %q", st.uploadPart, transfer.PartName)
	}
}

func TestUploadStrategyFlagChangesTheWireFormat(t *testing.T) {
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":0,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local, "--strategy", "post-raw")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// The escape hatch must actually change the framing, so a future change on
	// Naver's side can be worked around without a release.
	if st.uploadCT != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want the raw framing", st.uploadCT)
	}
	if string(st.uploaded) != "x" {
		t.Errorf("body = %q", st.uploaded)
	}
}

func TestUploadRejectsUnknownStrategy(t *testing.T) {
	f := newFakeAPI(t)
	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local, "--strategy", "carrier-pigeon")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if len(f.requests) != 0 {
		t.Errorf("an invalid strategy still called the API: %v", f.requests)
	}
	if !strings.Contains(stderr, "post-multipart") {
		t.Errorf("stderr should list the alternatives:\n%s", stderr)
	}
}

func TestUploadRefusesAFileOverTheAccountLimit(t *testing.T) {
	// maxFileBytes is tiny, so even a small file is over the ceiling.
	f := newFakeAPI(t).json("GET", "/drive/storage", 200,
		`{"fileCounts":{},"maxFileBytes":4,"quotaBytes":1000,"trashAutoDeleteDays":5,"usedBytes":0}`)

	local := filepath.Join(t.TempDir(), "big.bin")
	if err := os.WriteFile(local, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local)
	if code == ExitOK {
		t.Fatal("want a failure for an oversized file")
	}
	if !strings.Contains(stderr, "too large") {
		t.Errorf("stderr:\n%s", stderr)
	}
	// The check must happen before an upload URL is requested.
	for _, req := range f.requests {
		if req == "POST /drive/files" {
			t.Error("an oversized file still requested an upload URL")
		}
	}
}

func TestUploadRejectsADirectory(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	_, stderr, code := f.run("up", t.TempDir())
	if code == ExitOK {
		t.Fatal("uploading a directory should fail")
	}
	if !strings.Contains(stderr, "directory") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestUploadRejectionPointsAtTheProbe(t *testing.T) {
	st := &storageState{uploadStatus: 405}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":0,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local)
	if code == ExitOK {
		t.Fatal("want a failure")
	}
	// The wire format is a guess, so a rejection must say how to find the right one.
	if !strings.Contains(stderr, "upload-probe") {
		t.Errorf("stderr should point at the probe:\n%s", stderr)
	}
}

func TestUploadResumesFromServerOffset(t *testing.T) {
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Resume       bool   `json:"resume"`
				ModifiedTime string `json:"modifiedTime"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if !req.Resume {
				t.Error("resume flag was not sent")
			}
			// The API rejects resume without a modification time.
			if req.ModifiedTime == "" {
				t.Error("modifiedTime was not sent alongside resume")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":3,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local, "--resume")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0: %s", code, stderr)
	}
	// Only the tail past the server's offset should be sent.
	if string(st.uploaded) != "3456789" {
		t.Errorf("uploaded = %q, want the bytes from offset 3", st.uploaded)
	}
	// The storage host wants the bare form, without RFC 9110's "bytes " prefix.
	if st.uploadRange != "3-9/10" {
		t.Errorf("Content-Range = %q, want 3-9/10", st.uploadRange)
	}
}

func TestUploadSendsModifiedTimeInKSTFromAnyHostZone(t *testing.T) {
	// Run as if the machine were in California. MYBOX matches modifiedTime as a
	// literal string and only recognises the KST spelling, so a host-zone
	// timestamp would make it treat this as a different file and restart the
	// upload from zero without reporting anything.
	t.Setenv("TZ", "America/Los_Angeles")

	var gotModified string
	var gotResume bool
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			var req struct {
				Resume       bool   `json:"resume"`
				ModifiedTime string `json:"modifiedTime"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			gotResume, gotModified = req.Resume, req.ModifiedTime
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":0,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := f.run("up", local, "--resume"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	if !gotResume {
		t.Error("resume flag was not sent")
	}
	if !strings.HasSuffix(gotModified, "+09:00") {
		t.Errorf("modifiedTime = %q, want the KST spelling regardless of host zone", gotModified)
	}
}

func TestUploadResumeIgnoresOverwrite(t *testing.T) {
	// Reserving with isOverwrite reports offset 0 — asking to overwrite means
	// starting the file again — so the two cannot be combined in one call.
	var gotOverwrite, sawOverwriteKey bool
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			raw, _ := io.ReadAll(r.Body)
			sawOverwriteKey = strings.Contains(string(raw), "isOverwrite")
			var req struct {
				IsOverwrite bool `json:"isOverwrite"`
			}
			_ = json.Unmarshal(raw, &req)
			gotOverwrite = req.IsOverwrite
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":0,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := f.run("up", local, "--resume", "--overwrite")
	if code != ExitOK {
		t.Fatalf("exit = %d: %s", code, stderr)
	}
	if gotOverwrite || sawOverwriteKey {
		t.Error("isOverwrite was sent alongside resume, which zeroes the offset")
	}
	// Silently dropping a flag the user typed would be worse than the conflict.
	if !strings.Contains(stderr, "--overwrite") {
		t.Errorf("the user should be told the flag was ignored:\n%s", stderr)
	}
}

func TestUploadRejectsImpossibleServerOffset(t *testing.T) {
	st := &storageState{}
	f := newFakeAPI(t).
		json("GET", "/drive/storage", 200, storageBody).
		handle("POST", "/drive/files", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"offset":999,"uploadUrl":"` + selfURL(r, "/storage/upload") + `"}`))
		}).
		withStorage(st)

	local := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(local, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seeking past the end would upload nothing and report success.
	_, stderr, code := f.run("up", local, "--resume")
	if code == ExitOK {
		t.Fatal("an offset beyond the file size should be refused")
	}
	if !strings.Contains(stderr, "resume offset") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

// --- rate limits -----------------------------------------------------------

func TestAuthStatusShowsEffectiveRateLimits(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("auth", "status")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	// Without --rate the client shapes to the lowest documented allowance, and
	// the user has no other way to find out what that is.
	if !strings.Contains(stdout, "default=60") || !strings.Contains(stdout, "search=10") {
		t.Errorf("status should show the effective budgets:\n%s", stdout)
	}
}

func TestRateFlagReachesTheClient(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("--rate", "240,search=30", "auth", "status")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{"default=240", "search=30", "delete=240", "restore=240"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status missing %q:\n%s", want, stdout)
		}
	}
}

func TestRateFlagInJSONOutput(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)

	stdout, _, code := f.run("--json", "--rate", "search=30", "auth", "status")
	if code != ExitOK {
		t.Fatalf("exit = %d, want 0", code)
	}
	var got struct {
		RateLimits map[string]int `json:"rateLimits"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	if got.RateLimits["search"] != 30 {
		t.Errorf("search = %d, want 30", got.RateLimits["search"])
	}
	// Groups the flag did not name still report their documented default.
	if got.RateLimits["default"] != 60 {
		t.Errorf("default = %d, want 60", got.RateLimits["default"])
	}
}

func TestInvalidRateFailsBeforeCallingTheAPI(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("--rate", "upload=100", "df")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if len(f.requests) != 0 {
		t.Errorf("an invalid --rate still called the API: %v", f.requests)
	}
	if !strings.Contains(stderr, "unknown group") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestInvalidRateIsReportedWithoutACredential(t *testing.T) {
	f := newFakeAPI(t)
	srv := f.start()
	t.Setenv(config.EnvAPIBase, srv.URL)
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvProfile, "")
	t.Setenv(config.EnvConfigHome, t.TempDir())
	t.Setenv(resolve.EnvCacheHome, t.TempDir())

	// A malformed flag is the user's most immediate mistake; reporting a
	// missing token first would send them off fixing the wrong thing.
	var out, errBuf bytes.Buffer
	code := Execute(t.Context(), []string{"--rate", "upload=100", "df"}, &out, &errBuf)
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errBuf.String(), "unknown group") {
		t.Errorf("stderr:\n%s", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "access token") {
		t.Errorf("the flag error was masked by a credential error:\n%s", errBuf.String())
	}
}

func TestInvalidRateIsCaughtOnCommandsThatNeedNoAPI(t *testing.T) {
	f := newFakeAPI(t)

	_, stderr, code := f.run("--rate", "typo=1", "version")
	if code != ExitUsage {
		t.Errorf("exit = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "unknown group") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func TestAuthLoginWarnsAboutUnknownLimitGroups(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)
	srv := f.start()

	t.Setenv(config.EnvConfigHome, t.TempDir())
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvProfile, "")
	t.Setenv(config.EnvAPIBase, srv.URL)
	t.Setenv(resolve.EnvCacheHome, t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetProfile(config.DefaultProfile, config.Profile{Limits: map[string]int{"serach": 30}})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	// auth login builds its own probe client, so it used to skip the warning
	// every other command gives -- the worst moment to stay quiet, since the
	// user is looking straight at this profile.
	var out, errBuf bytes.Buffer
	code := Execute(t.Context(), []string{"--token", "mbx_pat_test", "auth", "login"}, &out, &errBuf)
	if code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "serach") {
		t.Errorf("stderr should name the unrecognised group:\n%s", errBuf.String())
	}
	// auth login builds its own client, so it has to apply $MYBOX_API_BASE
	// itself. When it did not, this test silently authenticated against the
	// real MYBOX service instead of the fake one.
	if len(f.requests) == 0 {
		t.Error("auth login never reached the fake API; it ignored $" + config.EnvAPIBase)
	}
}

func TestUnknownConfigLimitGroupWarns(t *testing.T) {
	f := newFakeAPI(t).json("GET", "/drive/storage", 200, storageBody)
	srv := f.start()

	dir := t.TempDir()
	t.Setenv(config.EnvConfigHome, dir)
	t.Setenv(config.EnvToken, "")
	t.Setenv(config.EnvProfile, "")
	t.Setenv(config.EnvAPIBase, srv.URL)
	t.Setenv(resolve.EnvCacheHome, t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	cfg.SetProfile(config.DefaultProfile, config.Profile{
		Token:  "mbx_pat_test",
		Limits: map[string]int{"serach": 30}, // a plausible typo
	})
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}

	var out, errBuf bytes.Buffer
	if code := Execute(t.Context(), []string{"df"}, &out, &errBuf); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, errBuf.String())
	}
	// Silently ignoring the typo would leave the user wondering why raising the
	// limit changed nothing.
	if !strings.Contains(errBuf.String(), "serach") {
		t.Errorf("stderr should name the unrecognised group:\n%s", errBuf.String())
	}
}
