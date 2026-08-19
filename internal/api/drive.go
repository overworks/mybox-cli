package api

import (
	"context"
	"iter"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// GetStorage reports quota, usage, per-category file counts, the maximum
// uploadable file size and the trash auto-delete interval.
func (c *Client) GetStorage(ctx context.Context) (*Storage, error) {
	var out Storage
	err := c.do(ctx, request{method: http.MethodGet, path: "/drive/storage", group: GroupDefault, out: &out})
	return &out, err
}

// SetTrashAutoDeleteDays sets how long items stay in the trash. Only 0 (off), 5,
// 15, 30 and 50 are accepted; other values are rejected before any call is made.
func (c *Client) SetTrashAutoDeleteDays(ctx context.Context, days int) (*TrashAutoDelete, error) {
	if !slices.Contains(ValidTrashAutoDeleteDays, days) {
		return nil, invalidRequestf("trash auto-delete days must be one of %v, got %d", ValidTrashAutoDeleteDays, days)
	}
	var out TrashAutoDelete
	err := c.do(ctx, request{
		method: http.MethodPatch,
		path:   "/drive/storage",
		body:   TrashAutoDelete{TrashAutoDeleteDays: days},
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// ListRoot returns one page of the root listing.
func (c *Client) ListRoot(ctx context.Context, opts ListOptions) (*ResourceList, error) {
	var out ResourceList
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/drive/resources",
		query:  listQuery(opts),
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// ListFolder returns one page of a folder's listing.
func (c *Client) ListFolder(ctx context.Context, folderID string, opts ListOptions) (*ResourceList, error) {
	var out ResourceList
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/drive/folders/" + url.PathEscape(folderID) + "/resources",
		query:  listQuery(opts),
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// List returns one page of the listing for folderID, or of the root when
// folderID is empty. The root has no resource ID of its own, so an empty string
// is the canonical way to name it throughout this package.
func (c *Client) List(ctx context.Context, folderID string, opts ListOptions) (*ResourceList, error) {
	if folderID == "" {
		return c.ListRoot(ctx, opts)
	}
	return c.ListFolder(ctx, folderID, opts)
}

// IterResources walks every entry under folderID (or the root when folderID is
// empty), following cursors until the listing is exhausted.
func (c *Client) IterResources(ctx context.Context, folderID string, opts ListOptions) iter.Seq2[ResourceItem, error] {
	return iterPages(func(cursor string) ([]ResourceItem, string, error) {
		page := opts
		page.Cursor = cursor
		res, err := c.List(ctx, folderID, page)
		if err != nil {
			return nil, "", err
		}
		return res.Resources, res.ResponseMetaData.NextCursor, nil
	})
}

// GetResource returns a single file's or folder's properties. For a folder the
// response also carries FileCount and SubFolderCount.
func (c *Client) GetResource(ctx context.Context, resourceID string) (*ResourceItem, error) {
	var out ResourceItem
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/drive/resources/" + url.PathEscape(resourceID),
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

type createFolderBody struct {
	FolderName string `json:"folderName"`
	ParentID   string `json:"parentId,omitempty"`
}

// CreateFolder creates a folder under parentID, or in the root when parentID is empty.
func (c *Client) CreateFolder(ctx context.Context, name, parentID string) (*CreatedResource, error) {
	var out CreatedResource
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/folders",
		body:   createFolderBody{FolderName: name, ParentID: parentID},
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// CopyOptions configures CopyResource. All fields are optional: an empty Name
// keeps the original name and an empty ParentID copies into the root.
type CopyOptions struct {
	Name        string
	ParentID    string
	IsOverwrite bool
}

type copyBody struct {
	Name        string `json:"name,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	IsOverwrite bool   `json:"isOverwrite,omitempty"`
}

// CopyResource copies a file or folder and returns the copy's name and ID.
func (c *Client) CopyResource(ctx context.Context, resourceID string, opts CopyOptions) (*CreatedResource, error) {
	var out CreatedResource
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/resources/" + url.PathEscape(resourceID) + "/copy",
		body:   copyBody{Name: opts.Name, ParentID: opts.ParentID, IsOverwrite: opts.IsOverwrite},
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

type moveBody struct {
	ParentID    string `json:"parentId"`
	IsOverwrite bool   `json:"isOverwrite,omitempty"`
}

// MoveResource moves a file or folder into parentID. The endpoint answers 200
// with no body, so there is nothing to return on success.
func (c *Client) MoveResource(ctx context.Context, resourceID, parentID string, overwrite bool) error {
	return c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/resources/" + url.PathEscape(resourceID) + "/move",
		body:   moveBody{ParentID: parentID, IsOverwrite: overwrite},
		group:  GroupDefault,
	})
}

type renameBody struct {
	Name string `json:"name"`
}

// RenameResource renames a file or folder in place; the resource ID is unchanged.
// The new name must include the extension if the caller wants to keep it.
func (c *Client) RenameResource(ctx context.Context, resourceID, name string) (*RenameResult, error) {
	var out RenameResult
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/resources/" + url.PathEscape(resourceID) + "/rename",
		body:   renameBody{Name: name},
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// DeleteResource moves a file or folder to the trash. It is not a permanent delete.
func (c *Client) DeleteResource(ctx context.Context, resourceID string) error {
	return c.do(ctx, request{
		method: http.MethodDelete,
		path:   "/drive/resources/" + url.PathEscape(resourceID),
		group:  GroupDelete,
	})
}

// SetFavorite stars or unstars a file or folder. Both directions are idempotent:
// re-starting an already starred item still answers 200.
func (c *Client) SetFavorite(ctx context.Context, resourceID string, favorite bool) (*FavoriteResult, error) {
	action := "/unfavorite"
	if favorite {
		action = "/favorite"
	}
	var out FavoriteResult
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/resources/" + url.PathEscape(resourceID) + action,
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

// UploadRequest describes the file an upload URL is being issued for.
type UploadRequest struct {
	FileName string
	// FileSize in bytes. It is required even for an empty file; omitting it
	// makes the API fail.
	FileSize int64
	ParentID string
	// Overwrite replaces an existing file of the same name instead of failing.
	Overwrite bool
	// Resume asks for the offset of a previously interrupted upload. It must be
	// sent together with ModifiedTime.
	Resume bool
	// ModifiedTime is the local file's modification time, required when Resume is set.
	ModifiedTime string
}

type uploadBody struct {
	FileName     string `json:"fileName"`
	FileSize     int64  `json:"fileSize"`
	ParentID     string `json:"parentId,omitempty"`
	IsOverwrite  bool   `json:"isOverwrite,omitempty"`
	Resume       bool   `json:"resume,omitempty"`
	ModifiedTime string `json:"modifiedTime,omitempty"`
}

// CreateUploadURL issues a storage URL to upload a file to. It does not transfer
// any bytes; see internal/transfer for that.
func (c *Client) CreateUploadURL(ctx context.Context, req UploadRequest) (*UploadTicket, error) {
	if req.FileSize < 0 {
		return nil, invalidRequestf("file size cannot be negative, got %d", req.FileSize)
	}
	if req.Resume && req.ModifiedTime == "" {
		return nil, invalidRequestf("resuming also requires the file's modified time")
	}
	var out UploadTicket
	err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/drive/files",
		body: uploadBody{
			FileName:     req.FileName,
			FileSize:     req.FileSize,
			ParentID:     req.ParentID,
			IsOverwrite:  req.Overwrite,
			Resume:       req.Resume,
			ModifiedTime: req.ModifiedTime,
		},
		group: GroupDefault,
		out:   &out,
	})
	return &out, err
}

// CreateDownloadURL issues a single-use, short-lived URL for a file's contents.
// Each call counts against the account's daily download quota.
func (c *Client) CreateDownloadURL(ctx context.Context, fileID string) (*DownloadTicket, error) {
	var out DownloadTicket
	err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/drive/files/" + url.PathEscape(fileID) + "/download",
		group:  GroupDefault,
		out:    &out,
	})
	return &out, err
}

func listQuery(opts ListOptions) url.Values {
	q := url.Values{}
	if opts.Sort != "" {
		q.Set("sort", opts.Sort)
	}
	if n := clampListCount(opts.Count); n > 0 {
		q.Set("count", strconv.Itoa(n))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	return q
}
