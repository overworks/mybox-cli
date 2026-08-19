package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Strategy describes how bytes are sent to a storage upload URL.
//
// Naver documents how to *reserve* an upload URL but not what to send to it.
// The default below is not a guess: it was established against the live service
// and is recorded in docs/api-reference.md. The type stays configurable so a
// change on Naver's side can be worked around without a new release.
type Strategy struct {
	// Name identifies the strategy for --strategy and MYBOX_UPLOAD_STRATEGY.
	Name string
	// Method is the HTTP verb. The storage host routes POST only; PUT, GET and
	// HEAD answer 404.
	Method string
	// Multipart wraps the body in multipart/form-data. Every other framing —
	// raw octet-stream, JSON, form-urlencoded, chunked, multipart/mixed — is
	// rejected with 400 "Invalid Data Format".
	Multipart bool
	// PartName is the form field name. The storage host accepts exactly one,
	// "Filedata", a legacy Flash-uploader convention; every other spelling,
	// casing included, is rejected with 400 "Param Not Exist".
	PartName string
	// PartContentType is the Content-Type of the multipart part itself.
	PartContentType string
}

// PartName is the only form field name the storage host accepts.
const PartName = "Filedata"

// Strategies are the wire formats the probe can try.
//
// Only the first is known to work. The others are kept so `mybox debug
// upload-probe` can re-establish the format if Naver ever changes it; each one
// is documented with how the service rejected it when it was last measured.
var Strategies = []Strategy{
	// Verified working.
	{Name: "post-multipart", Method: http.MethodPost, Multipart: true, PartName: PartName, PartContentType: "application/octet-stream"},
	// Rejected: 400 "Param Not Exist" — the part name is matched exactly.
	{Name: "post-multipart-file", Method: http.MethodPost, Multipart: true, PartName: "file", PartContentType: "application/octet-stream"},
	// Rejected: 400 "Invalid Data Format" — only multipart framing is accepted.
	{Name: "post-raw", Method: http.MethodPost},
	// Rejected: 404 — the storage host does not route PUT.
	{Name: "put-raw", Method: http.MethodPut},
	{Name: "put-multipart", Method: http.MethodPut, Multipart: true, PartName: PartName, PartContentType: "application/octet-stream"},
}

// DefaultStrategy is the format verified against the live service.
var DefaultStrategy = Strategies[0]

// EnvStrategy overrides the upload strategy by name.
const EnvStrategy = "MYBOX_UPLOAD_STRATEGY"

// StrategyByName looks up a strategy, listing the alternatives when it fails.
func StrategyByName(name string) (Strategy, error) {
	for _, s := range Strategies {
		if s.Name == name {
			return s, nil
		}
	}
	names := make([]string, 0, len(Strategies))
	for _, s := range Strategies {
		names = append(names, s.Name)
	}
	return Strategy{}, fmt.Errorf("unknown upload strategy %q; valid values are %s", name, strings.Join(names, ", "))
}

// ResolveStrategy picks the strategy to use, honouring EnvStrategy.
func ResolveStrategy() (Strategy, error) {
	if name := strings.TrimSpace(os.Getenv(EnvStrategy)); name != "" {
		return StrategyByName(name)
	}
	return DefaultStrategy, nil
}

// UploadRequest describes one upload attempt.
type UploadRequest struct {
	// URL is the storage URL issued by POST /drive/files.
	URL string
	// Body supplies the bytes to send, already positioned at Offset.
	Body io.Reader
	// FileName is used as the multipart part's filename.
	FileName string
	// Size is the total file size in bytes. It must match what the upload was
	// reserved with: sending fewer bytes answers 500 "File Storage Error".
	Size int64
	// Offset is where this attempt starts, for a resumed upload.
	Offset int64
	// Strategy selects the wire format.
	Strategy Strategy
}

// UploadResult is what the storage host reports once it has stored the file.
type UploadResult struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name"`
	FileSize   int64  `json:"fileSize"`
}

// Upload sends a file's contents to a storage upload URL.
//
// No Authorization header is sent: the URL authenticates itself through its
// stoken parameter, and the storage host is a different service from the API
// host the personal access token belongs to.
func (c *Client) Upload(ctx context.Context, req UploadRequest) (*UploadResult, error) {
	httpReq, err := c.buildUpload(ctx, req)
	if err != nil {
		return nil, err
	}

	c.tracef("%s %s (strategy=%s, offset=%d, size=%d)",
		req.Strategy.Method, redactURL(req.URL), req.Strategy.Name, req.Offset, req.Size)
	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("upload request failed: %w", err)
	}
	defer resp.Body.Close()
	c.tracef("<- %s", resp.Status)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, storageError("upload", resp)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("could not read the upload response: %w", err)
	}

	var out UploadResult
	// A 2xx without a resourceId means the bytes did not become a file. Treating
	// that as success would report an upload that silently did not happen.
	if err := json.Unmarshal(raw, &out); err != nil || out.ResourceID == "" {
		return nil, &StorageError{
			Status: resp.StatusCode,
			Message: fmt.Sprintf("the storage host accepted the upload (HTTP %d) but did not name the stored file: %s",
				resp.StatusCode, bodyOrEmpty(raw)),
		}
	}
	return &out, nil
}

func (c *Client) buildUpload(ctx context.Context, req UploadRequest) (*http.Request, error) {
	body := req.Body
	contentType := "application/octet-stream"
	remaining := req.Size - req.Offset
	length := remaining

	if req.Strategy.Multipart {
		boundary, err := randomBoundary()
		if err != nil {
			return nil, err
		}
		prefix := fmt.Sprintf(
			"--%s\r\nContent-Disposition: form-data; name=%q; filename=%q\r\nContent-Type: %s\r\n\r\n",
			boundary, req.Strategy.PartName, escapeFileName(req.FileName), req.Strategy.PartContentType)
		suffix := fmt.Sprintf("\r\n--%s--\r\n", boundary)

		// The envelope is assembled from readers rather than a pipe so its exact
		// length is known up front. Without a Content-Length, Go would fall back
		// to chunked transfer encoding, which the storage host rejects with
		// 400 "Invalid Data Format".
		body = io.MultiReader(strings.NewReader(prefix), req.Body, strings.NewReader(suffix))
		length = int64(len(prefix)) + remaining + int64(len(suffix))
		contentType = "multipart/form-data; boundary=" + boundary
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Strategy.Method, req.URL, body)
	if err != nil {
		return nil, err
	}
	httpReq.ContentLength = length
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("User-Agent", c.UserAgent)

	if req.Offset > 0 && req.Size > 0 {
		// Note the missing "bytes " prefix: the storage host wants the bare
		// start-end/total form, not the RFC 9110 spelling.
		httpReq.Header.Set("Content-Range",
			fmt.Sprintf("%d-%d/%d", req.Offset, req.Size-1, req.Size))
	}
	return httpReq, nil
}

func randomBoundary() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("could not generate a multipart boundary: %w", err)
	}
	return "mybox" + hex.EncodeToString(b[:]), nil
}

// escapeFileName keeps a quote or a newline in a file name from breaking out of
// the Content-Disposition header.
func escapeFileName(name string) string {
	return strings.NewReplacer(`"`, `\"`, "\r", "", "\n", "").Replace(name)
}

func bodyOrEmpty(raw []byte) string {
	if s := strings.TrimSpace(string(raw)); s != "" {
		return s
	}
	return "(empty response)"
}
