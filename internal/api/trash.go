package api

import (
	"context"
	"iter"
	"net/http"
	"net/url"
)

// DefaultTrashSort is the ordering the API applies when none is requested.
const DefaultTrashSort = "deletedAt,desc"

// ListTrash returns one page of the trash listing.
func (c *Client) ListTrash(ctx context.Context, opts ListOptions) (*TrashList, error) {
	var out TrashList
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/drive/trash",
		query:  listQuery(opts),
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// IterTrash walks the whole trash listing, following cursors until exhausted.
func (c *Client) IterTrash(ctx context.Context, opts ListOptions) iter.Seq2[TrashedResourceItem, error] {
	return iterPages(func(cursor string) ([]TrashedResourceItem, string, error) {
		page := opts
		page.Cursor = cursor
		res, err := c.ListTrash(ctx, page)
		if err != nil {
			return nil, "", err
		}
		return res.Resources, res.ResponseMetaData.NextCursor, nil
	})
}

type restoreBody struct {
	IsOverwrite bool `json:"isOverwrite,omitempty"`
}

// RestoreFromTrash puts an item back where it was deleted from. The endpoint
// answers 200 with no body.
func (c *Client) RestoreFromTrash(ctx context.Context, resourceID string, overwrite bool) error {
	return c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/trash/" + url.PathEscape(resourceID) + "/restore",
		body:   restoreBody{IsOverwrite: overwrite},
		group:  GroupRestore,
	})
}

// PurgeTrashItem permanently deletes one item from the trash. This cannot be undone.
func (c *Client) PurgeTrashItem(ctx context.Context, resourceID string) error {
	return c.do(ctx, request{
		method: http.MethodDelete,
		path:   "/drive/trash/" + url.PathEscape(resourceID),
		group:  GroupDelete,
	})
}

// EmptyTrash permanently deletes everything in the trash. This cannot be undone.
func (c *Client) EmptyTrash(ctx context.Context) error {
	return c.do(ctx, request{
		method: http.MethodDelete,
		path:   "/drive/trash",
		group:  GroupDelete,
	})
}
