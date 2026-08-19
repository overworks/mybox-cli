package api

import "iter"

// DefaultPageSize matches the API default for the listing endpoints.
const DefaultPageSize = 100

// MaxListPageSize is the largest count the listing endpoints accept.
const MaxListPageSize = 1000

// Search endpoints use a different, much narrower range than listings.
const (
	MinSearchPageSize     = 20
	MaxSearchPageSize     = 200
	DefaultSearchPageSize = 20
)

// ListOptions are the query parameters shared by the listing endpoints.
//
// Sort takes the API's "field,direction" form (for example "name,asc"). Results
// always come back folders-first regardless of Sort.
type ListOptions struct {
	Sort   string
	Count  int
	Cursor string
}

// iterPages walks a cursor-paginated endpoint. fetch receives the cursor for the
// page to load ("" for the first) and returns that page's items plus the cursor
// for the next page ("" when exhausted).
//
// On error the sequence yields the zero item with that error and stops, so a
// caller that checks err on every step cannot silently truncate a listing.
func iterPages[T any](fetch func(cursor string) ([]T, string, error)) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		cursor := ""
		for {
			items, next, err := fetch(cursor)
			if err != nil {
				var zero T
				yield(zero, err)
				return
			}
			for _, it := range items {
				if !yield(it, nil) {
					return
				}
			}
			// A page can legitimately come back empty; only the cursor decides
			// whether more pages exist.
			if next == "" || next == cursor {
				return
			}
			cursor = next
		}
	}
}

// clampListCount normalises a caller-supplied page size for the listing endpoints.
func clampListCount(n int) int {
	switch {
	case n <= 0:
		return 0 // omit the parameter and take the server default
	case n > MaxListPageSize:
		return MaxListPageSize
	default:
		return n
	}
}

// clampSearchCount normalises a caller-supplied page size for the search endpoints,
// which reject anything outside [20, 200].
func clampSearchCount(n int) int {
	switch {
	case n <= 0:
		return 0
	case n < MinSearchPageSize:
		return MinSearchPageSize
	case n > MaxSearchPageSize:
		return MaxSearchPageSize
	default:
		return n
	}
}
