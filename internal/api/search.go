package api

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// Date field values for the search endpoints.
const (
	DateFieldCreated  = "created"
	DateFieldModified = "modified"
)

// FileSearchOptions are the query parameters of GET /search/resources/files.
//
// At least one of Query, Category or a date bound must be set; the API rejects a
// search with no criteria at all.
type FileSearchOptions struct {
	// Query is matched with spaces and extensions treated as AND terms.
	Query string
	// Category narrows by file kind; see Categories.
	Category string
	// ParentPath restricts the search to that folder's subtree.
	ParentPath string
	// DateField selects which timestamp StartDate/EndDate apply to. Empty means "created".
	DateField string
	// StartDate and EndDate are RFC3339 timestamps, e.g. "2026-01-01T00:00:00+09:00".
	StartDate string
	EndDate   string
	Count     int
	Cursor    string
}

func (o FileSearchOptions) validate() error {
	if o.Query == "" && o.Category == "" && o.StartDate == "" && o.EndDate == "" {
		return fmt.Errorf("searching files needs at least one of: a query, a category, a date range")
	}
	if o.Category != "" && !slices.Contains(Categories, o.Category) {
		return fmt.Errorf("unknown category %q; valid values are %v", o.Category, Categories)
	}
	return validateDateField(o.DateField)
}

func (o FileSearchOptions) query() url.Values {
	q := url.Values{}
	setNonEmpty(q, "q", o.Query)
	setNonEmpty(q, "category", o.Category)
	setNonEmpty(q, "parentPath", o.ParentPath)
	setNonEmpty(q, "dateField", o.DateField)
	setNonEmpty(q, "startDate", o.StartDate)
	setNonEmpty(q, "endDate", o.EndDate)
	setNonEmpty(q, "cursor", o.Cursor)
	if n := clampSearchCount(o.Count); n > 0 {
		q.Set("count", strconv.Itoa(n))
	}
	return q
}

// SearchFiles returns one page of file search results.
func (c *Client) SearchFiles(ctx context.Context, opts FileSearchOptions) (*FileSearchResult, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	var out FileSearchResult
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/search/resources/files",
		query:  opts.query(),
		group:  GroupSearch,
		out:    &out,
	})
	return &out, err
}

// IterFiles walks every file search result, following cursors until exhausted.
func (c *Client) IterFiles(ctx context.Context, opts FileSearchOptions) iter.Seq2[FileResource, error] {
	return iterPages(func(cursor string) ([]FileResource, string, error) {
		page := opts
		page.Cursor = cursor
		res, err := c.SearchFiles(ctx, page)
		if err != nil {
			return nil, "", err
		}
		return res.Resources, res.ResponseMetaData.NextCursor, nil
	})
}

// FolderSearchOptions are the query parameters of GET /search/resources/folders.
//
// At least one of Query, Path or a date bound must be set.
type FolderSearchOptions struct {
	Query string
	// Path pins the search to exactly that folder. The API ignores every other
	// criterion when it is set, which makes this the cheapest way to turn a
	// known folder path into a resource ID.
	Path string
	// ParentPath restricts the search to that folder's subtree.
	ParentPath string
	DateField  string
	StartDate  string
	EndDate    string
	Count      int
	Cursor     string
}

func (o FolderSearchOptions) validate() error {
	if o.Query == "" && o.Path == "" && o.StartDate == "" && o.EndDate == "" {
		return fmt.Errorf("searching folders needs at least one of: a query, --path, a date range")
	}
	return validateDateField(o.DateField)
}

func (o FolderSearchOptions) query() url.Values {
	q := url.Values{}
	setNonEmpty(q, "q", o.Query)
	setNonEmpty(q, "path", o.Path)
	setNonEmpty(q, "parentPath", o.ParentPath)
	setNonEmpty(q, "dateField", o.DateField)
	setNonEmpty(q, "startDate", o.StartDate)
	setNonEmpty(q, "endDate", o.EndDate)
	setNonEmpty(q, "cursor", o.Cursor)
	if n := clampSearchCount(o.Count); n > 0 {
		q.Set("count", strconv.Itoa(n))
	}
	return q
}

// SearchFolders returns one page of folder search results.
func (c *Client) SearchFolders(ctx context.Context, opts FolderSearchOptions) (*FolderSearchResult, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	var out FolderSearchResult
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/search/resources/folders",
		query:  opts.query(),
		group:  GroupSearch,
		out:    &out,
	})
	return &out, err
}

// IterFolders walks every folder search result, following cursors until exhausted.
func (c *Client) IterFolders(ctx context.Context, opts FolderSearchOptions) iter.Seq2[FolderResource, error] {
	return iterPages(func(cursor string) ([]FolderResource, string, error) {
		page := opts
		page.Cursor = cursor
		res, err := c.SearchFolders(ctx, page)
		if err != nil {
			return nil, "", err
		}
		return res.Resources, res.ResponseMetaData.NextCursor, nil
	})
}

func validateDateField(f string) error {
	switch f {
	case "", DateFieldCreated, DateFieldModified:
		return nil
	default:
		return fmt.Errorf("date field must be %q or %q, got %q", DateFieldCreated, DateFieldModified, f)
	}
}

func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
