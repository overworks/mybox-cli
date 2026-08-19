package api

// ResourceType values returned in ResourceItem.Type.
const (
	TypeFile   = "file"
	TypeFolder = "folder"
)

// Category values accepted by the search API and returned in ResourceItem.Category.
var Categories = []string{"image", "video", "audio", "document", "archive", "executable", "etc"}

// ResourceItem is openapi.resourceItem: one file or folder in a listing.
//
// FileCount and SubFolderCount are only present when a folder is fetched via
// GetResource; listings leave them nil.
type ResourceItem struct {
	ResourceID     string `json:"resourceId"`
	Name           string `json:"name"`
	ParentID       string `json:"parentId"`
	Type           string `json:"type"`
	Size           int64  `json:"size"`
	Category       string `json:"category,omitempty"`
	CreatedAt      string `json:"createdAt"`
	ModifiedAt     string `json:"modifiedAt"`
	AccessedAt     string `json:"accessedAt"`
	LastModifiedBy string `json:"lastModifiedBy"`
	IsFavorite     bool   `json:"isFavorite"`
	IsHidden       bool   `json:"isHidden"`
	FileCount      *int   `json:"fileCount,omitempty"`
	SubFolderCount *int   `json:"subFolderCount,omitempty"`
}

// IsFolder reports whether the item is a folder.
func (r ResourceItem) IsFolder() bool { return r.Type == TypeFolder }

// TrashedResourceItem is openapi.trashedResourceItem. It drops isFavorite/isHidden
// and adds deletedAt compared to ResourceItem.
type TrashedResourceItem struct {
	ResourceID     string `json:"resourceId"`
	Name           string `json:"name"`
	ParentID       string `json:"parentId"`
	Type           string `json:"type"`
	Size           int64  `json:"size"`
	Category       string `json:"category,omitempty"`
	CreatedAt      string `json:"createdAt"`
	ModifiedAt     string `json:"modifiedAt"`
	AccessedAt     string `json:"accessedAt"`
	DeletedAt      string `json:"deletedAt"`
	LastModifiedBy string `json:"lastModifiedBy"`
}

// IsFolder reports whether the trashed item is a folder.
func (r TrashedResourceItem) IsFolder() bool { return r.Type == TypeFolder }

// ResponseMetaData carries the pagination cursor. An empty NextCursor means the
// listing is exhausted.
type ResponseMetaData struct {
	NextCursor string `json:"nextCursor,omitempty"`
}

// ResourceList is the response of the root and folder listing endpoints.
type ResourceList struct {
	FileCount        int              `json:"fileCount"`
	SubFolderCount   int              `json:"subFolderCount"`
	Resources        []ResourceItem   `json:"resources"`
	ResponseMetaData ResponseMetaData `json:"responseMetaData"`
}

// TrashList is the response of the trash listing endpoint.
type TrashList struct {
	FileCount        int                   `json:"fileCount"`
	SubFolderCount   int                   `json:"subFolderCount"`
	Resources        []TrashedResourceItem `json:"resources"`
	ResponseMetaData ResponseMetaData      `json:"responseMetaData"`
}

// FileCounts is openapi.fileCounts.
type FileCounts struct {
	Archive    int `json:"archive"`
	Audio      int `json:"audio"`
	Document   int `json:"document"`
	Etc        int `json:"etc"`
	Executable int `json:"executable"`
	Image      int `json:"image"`
	Total      int `json:"total"`
	Video      int `json:"video"`
}

// Storage is the response of GET /drive/storage.
//
// QuotaBytes includes capacity shared out to other users and to mail; UsedBytes
// likewise includes shared-out usage.
type Storage struct {
	FileCounts          FileCounts `json:"fileCounts"`
	MaxFileBytes        int64      `json:"maxFileBytes"`
	QuotaBytes          int64      `json:"quotaBytes"`
	TrashAutoDeleteDays int        `json:"trashAutoDeleteDays"`
	UsedBytes           int64      `json:"usedBytes"`
}

// TrashAutoDelete is the response of PATCH /drive/storage.
type TrashAutoDelete struct {
	TrashAutoDeleteDays int `json:"trashAutoDeleteDays"`
}

// ValidTrashAutoDeleteDays are the only values the API accepts. 0 turns auto-delete off.
var ValidTrashAutoDeleteDays = []int{0, 5, 15, 30, 50}

// CreatedResource is the 201 response shared by folder creation and copy.
type CreatedResource struct {
	Name       string `json:"name"`
	ResourceID string `json:"resourceId"`
}

// RenameResult is the response of the rename endpoint.
type RenameResult struct {
	Name string `json:"name"`
}

// FavoriteResult is the response of the favorite and unfavorite endpoints.
type FavoriteResult struct {
	IsFavorite bool   `json:"isFavorite"`
	ResourceID string `json:"resourceId"`
}

// UploadTicket is the 201 response of POST /drive/files. Offset is the byte to
// resume from; it is 0 for a fresh upload.
type UploadTicket struct {
	Offset    int64  `json:"offset"`
	UploadURL string `json:"uploadUrl"`
}

// DownloadTicket is the response of GET /drive/files/{fileId}/download. The URL
// is single-use and valid for ExpiresIn seconds (600 at the time of writing).
type DownloadTicket struct {
	DownloadURL string `json:"downloadUrl"`
	ExpiresIn   int    `json:"expiresIn"`
}

// FileResource is dtos.FileResource from the file search endpoint. Unlike the
// listing endpoints, search returns Path and ParentPath.
type FileResource struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
	ParentID   string `json:"parentId"`
	ParentPath string `json:"parentPath"`
	Path       string `json:"path"`
	Category   string `json:"category"`
	Size       int64  `json:"size"`
	CreatedAt  string `json:"createdAt"`
	ModifiedAt string `json:"modifiedAt"`
}

// FolderResource is dtos.FolderResource from the folder search endpoint.
type FolderResource struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
	ParentID   string `json:"parentId"`
	ParentPath string `json:"parentPath"`
	Path       string `json:"path"`
	CreatedAt  string `json:"createdAt"`
	ModifiedAt string `json:"modifiedAt"`
}

// FileSearchResult is the response of GET /search/resources/files.
type FileSearchResult struct {
	Resources        []FileResource   `json:"resources"`
	ResponseMetaData ResponseMetaData `json:"responseMetaData"`
}

// FolderSearchResult is the response of GET /search/resources/folders.
type FolderSearchResult struct {
	Resources        []FolderResource `json:"resources"`
	ResponseMetaData ResponseMetaData `json:"responseMetaData"`
}
