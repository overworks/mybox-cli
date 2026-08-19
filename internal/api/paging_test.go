package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// pagedServer serves a listing split across the given pages, using the cursor
// query parameter to decide which page to return.
func pagedServer(t *testing.T, pages [][]string) (*Client, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		idx := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			fmt.Sscanf(c, "p%d", &idx)
		}
		if idx >= len(pages) {
			http.Error(w, `{"code":"PLAT-400","message":"BAD_REQUEST"}`, 400)
			return
		}
		body := `{"fileCount":0,"subFolderCount":0,"resources":[`
		for i, name := range pages[idx] {
			if i > 0 {
				body += ","
			}
			body += fmt.Sprintf(`{"name":%q,"resourceId":"id-%s","type":"file"}`, name, name)
		}
		body += `],"responseMetaData":{`
		if idx+1 < len(pages) {
			body += fmt.Sprintf(`"nextCursor":"p%d"`, idx+1)
		}
		body += `}}`
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return newTestClient(t, Options{BaseURL: srv.URL, Token: "mbx_pat_test"}), &calls
}

func TestIterResourcesFollowsCursors(t *testing.T) {
	c, calls := pagedServer(t, [][]string{{"a", "b"}, {"c"}, {"d", "e"}})

	var names []string
	for item, err := range c.IterResources(t.Context(), "FOLDER", ListOptions{Count: 2}) {
		if err != nil {
			t.Fatalf("iteration: %v", err)
		}
		names = append(names, item.Name)
	}

	want := []string{"a", "b", "c", "d", "e"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("names = %v, want %v", names, want)
	}
	if *calls != 3 {
		t.Errorf("requests = %d, want 3 (one per page)", *calls)
	}
}

func TestIterResourcesStopsOnLastPage(t *testing.T) {
	c, calls := pagedServer(t, [][]string{{"only"}})

	var n int
	for _, err := range c.IterResources(t.Context(), "", ListOptions{}) {
		if err != nil {
			t.Fatalf("iteration: %v", err)
		}
		n++
	}
	if n != 1 || *calls != 1 {
		t.Errorf("items = %d after %d requests, want 1 after 1", n, *calls)
	}
}

func TestIterResourcesStopsWhenCallerBreaks(t *testing.T) {
	c, calls := pagedServer(t, [][]string{{"a", "b"}, {"c"}, {"d"}})

	for item, err := range c.IterResources(t.Context(), "", ListOptions{}) {
		if err != nil {
			t.Fatalf("iteration: %v", err)
		}
		if item.Name == "a" {
			break
		}
	}
	// Breaking out must not keep fetching pages in the background.
	if *calls != 1 {
		t.Errorf("requests = %d, want 1 after an early break", *calls)
	}
}

func TestIterResourcesSurfacesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		io.WriteString(w, `{"code":"PLAT-403","message":"FORBIDDEN"}`)
	}))
	t.Cleanup(srv.Close)
	c := newTestClient(t, Options{BaseURL: srv.URL, Token: "mbx_pat_test"})

	var seen error
	var items int
	for _, err := range c.IterResources(t.Context(), "", ListOptions{}) {
		if err != nil {
			seen = err
			continue
		}
		items++
	}

	var apiErr *Error
	if !errors.As(seen, &apiErr) || !apiErr.IsForbidden() {
		t.Fatalf("error = %v, want a 403", seen)
	}
	if items != 0 {
		t.Errorf("yielded %d items alongside the error, want 0", items)
	}
}

// A page that repeats its own cursor would otherwise loop forever.
func TestIterPagesStopsOnRepeatedCursor(t *testing.T) {
	calls := 0
	seq := iterPages(func(cursor string) ([]int, string, error) {
		calls++
		if calls > 10 {
			t.Fatal("iterPages did not stop on a repeated cursor")
		}
		return []int{calls}, "same", nil
	})

	var n int
	for range seq {
		n++
	}
	if n != 2 {
		t.Errorf("items = %d, want 2 (first page, then the repeat is refused)", n)
	}
}

func TestIterPagesToleratesEmptyPage(t *testing.T) {
	pages := [][]int{{1}, {}, {2}}
	i := 0
	seq := iterPages(func(string) ([]int, string, error) {
		p := pages[i]
		i++
		next := ""
		if i < len(pages) {
			next = fmt.Sprint(i)
		}
		return p, next, nil
	})

	var got []int
	for v, err := range seq {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	// An empty middle page must not be mistaken for the end of the listing.
	if fmt.Sprint(got) != "[1 2]" {
		t.Errorf("got = %v, want [1 2]", got)
	}
}

func TestClampListCount(t *testing.T) {
	for _, tc := range []struct{ give, want int }{
		{0, 0}, {-5, 0}, {1, 1}, {100, 100}, {1000, 1000}, {1001, 1000}, {99999, 1000},
	} {
		if got := clampListCount(tc.give); got != tc.want {
			t.Errorf("clampListCount(%d) = %d, want %d", tc.give, got, tc.want)
		}
	}
}

func TestClampSearchCount(t *testing.T) {
	for _, tc := range []struct{ give, want int }{
		{0, 0}, {-1, 0}, {1, 20}, {19, 20}, {20, 20}, {200, 200}, {201, 200},
	} {
		if got := clampSearchCount(tc.give); got != tc.want {
			t.Errorf("clampSearchCount(%d) = %d, want %d", tc.give, got, tc.want)
		}
	}
}
