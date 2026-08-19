package transfer

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{HTTPClient: srv.Client(), UserAgent: "mybox-cli/test"}, srv
}

func TestDownloadStreamsContent(t *testing.T) {
	const payload = "회의록 내용"
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = io.WriteString(w, payload)
	})

	var buf bytes.Buffer
	n, err := c.Download(t.Context(), srv.URL, &buf)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if buf.String() != payload {
		t.Errorf("content = %q, want %q", buf.String(), payload)
	}
	if n != int64(len(payload)) {
		t.Errorf("n = %d, want %d", n, len(payload))
	}
}

func TestDownloadDetectsTruncation(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Promise more than we send, then hang up.
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "short")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler) // drop the connection mid-body
	})

	var buf bytes.Buffer
	// A truncated download that reported success would silently corrupt the file.
	if _, err := c.Download(t.Context(), srv.URL, &buf); err == nil {
		t.Fatal("want an error for a truncated download")
	}
}

func TestDownloadReportsStorageError(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, "expired token")
	})

	var buf bytes.Buffer
	_, err := c.Download(t.Context(), srv.URL, &buf)
	var se *StorageError
	if !errors.As(err, &se) {
		t.Fatalf("error = %T (%v), want *StorageError", err, err)
	}
	if se.Status != 403 || !strings.Contains(se.Error(), "expired token") {
		t.Errorf("error = %+v", se)
	}
}

// uploadOK is the storage host's success body. A 2xx without a resourceId is
// not a successful upload.
const uploadOK = `{"resourceId":"STORED1","name":"a.txt","fileSize":5}`

// capturedUpload is what the fake storage host saw.
type capturedUpload struct {
	Method      string
	ContentType string
	Length      int64
	TransferEnc []string
	Range       string
	Auth        string
	PartName    string
	PartFile    string
	PartBody    string
	RawBody     string
}

// storageHost stands in for the MYBOX storage tier, recording the framing of
// whatever it is sent.
func storageHost(t *testing.T, status int, body string, got *capturedUpload) (*Client, *httptest.Server) {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got.Method = r.Method
		got.ContentType = r.Header.Get("Content-Type")
		got.Length = r.ContentLength
		got.TransferEnc = r.TransferEncoding
		got.Range = r.Header.Get("Content-Range")
		got.Auth = r.Header.Get("Authorization")

		if strings.HasPrefix(got.ContentType, "multipart/form-data") {
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				t.Errorf("ParseMultipartForm: %v", err)
			} else if r.MultipartForm != nil {
				for name, files := range r.MultipartForm.File {
					got.PartName = name
					if len(files) > 0 {
						got.PartFile = files[0].Filename
						f, err := files[0].Open()
						if err == nil {
							raw, _ := io.ReadAll(f)
							got.PartBody = string(raw)
							f.Close()
						}
					}
				}
			}
		} else {
			raw, _ := io.ReadAll(r.Body)
			got.RawBody = string(raw)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

func TestUploadUsesTheVerifiedWireFormat(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	res, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("hello"), FileName: "a.txt",
		Size: 5, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Each of these was established against the live service; a change to any
	// one of them breaks uploads with a 400 or a 404.
	if got.Method != http.MethodPost {
		t.Errorf("method = %s, want POST (PUT is not routed)", got.Method)
	}
	if !strings.HasPrefix(got.ContentType, "multipart/form-data") {
		t.Errorf("Content-Type = %q, want multipart/form-data", got.ContentType)
	}
	if got.PartName != PartName {
		t.Errorf("part name = %q, want %q exactly", got.PartName, PartName)
	}
	if got.PartFile != "a.txt" || got.PartBody != "hello" {
		t.Errorf("part = %q / %q", got.PartFile, got.PartBody)
	}
	if res.ResourceID != "STORED1" {
		t.Errorf("ResourceID = %q", res.ResourceID)
	}
}

func TestUploadSendsContentLengthNotChunked(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("hello"), FileName: "a.txt",
		Size: 5, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// The storage host rejects chunked bodies with 400 "Invalid Data Format",
	// so the multipart envelope's length must be known before sending.
	if len(got.TransferEnc) != 0 {
		t.Errorf("Transfer-Encoding = %v, want none", got.TransferEnc)
	}
	if got.Length <= 5 {
		t.Errorf("Content-Length = %d, want the full envelope length", got.Length)
	}
}

func TestUploadSendsNoAuthorizationToStorage(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("hello"), FileName: "a.txt",
		Size: 5, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// The URL carries its own stoken; sending the personal access token to a
	// different host would expose it for nothing.
	if got.Auth != "" {
		t.Errorf("Authorization = %q, want it absent", got.Auth)
	}
}

func TestUploadResumeUsesBareContentRange(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, `{"resourceId":"R","name":"a.bin","fileSize":100}`, &got)

	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader(strings.Repeat("x", 60)),
		FileName: "a.bin", Size: 100, Offset: 40, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	// The storage host wants the bare start-end/total form, not RFC 9110's
	// "bytes 40-99/100".
	if got.Range != "40-99/100" {
		t.Errorf("Content-Range = %q, want 40-99/100 with no \"bytes \" prefix", got.Range)
	}
}

func TestUploadOmitsContentRangeAtZeroOffset(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("abc"), FileName: "a.txt",
		Size: 3, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.Range != "" {
		t.Errorf("Content-Range = %q, want it omitted for a fresh upload", got.Range)
	}
}

func TestUploadEscapesQuotesInFileNames(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	// An unescaped quote would break out of the Content-Disposition header.
	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("x"), FileName: `odd"name.txt`,
		Size: 1, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.PartName != PartName {
		t.Errorf("a quoted file name broke the part framing: part name = %q", got.PartName)
	}
}

func TestUploadRawStrategySendsBodyUnwrapped(t *testing.T) {
	var got capturedUpload
	raw, err := StrategyByName("post-raw")
	if err != nil {
		t.Fatal(err)
	}
	c, srv := storageHost(t, 200, uploadOK, &got)

	if _, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("hello"), FileName: "a.txt",
		Size: 5, Strategy: raw,
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.RawBody != "hello" || got.Length != 5 {
		t.Errorf("raw body = %q, length = %d", got.RawBody, got.Length)
	}
}

func TestUploadTreatsAMissingResourceIDAsFailure(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, `{"message":"ok"}`, &got)

	// A 2xx whose body names no stored file means the bytes did not become a
	// file; reporting success would hide a lost upload.
	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("x"), FileName: "a.txt",
		Size: 1, Strategy: DefaultStrategy,
	})
	if err == nil {
		t.Fatal("want an error when the response names no stored file")
	}
	if !strings.Contains(err.Error(), "did not name the stored file") {
		t.Errorf("error = %v", err)
	}
}

func TestUploadReportsStorageRejection(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 400, `{"message":"Invalid Data Format"}`, &got)

	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader("x"), FileName: "a", Size: 1,
		Strategy: DefaultStrategy,
	})
	var se *StorageError
	if !errors.As(err, &se) {
		t.Fatalf("error = %T (%v), want *StorageError", err, err)
	}
	if se.Status != 400 || !strings.Contains(se.Error(), "Invalid Data Format") {
		t.Errorf("error = %+v", se)
	}
}

func TestUploadRejectsAnOutOfRangeOffset(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	for _, tc := range []struct {
		name         string
		size, offset int64
	}{
		{"offset past the end", 10, 99},
		{"negative offset", 10, -1},
		{"negative size", -1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got = capturedUpload{}
			_, err := c.Upload(t.Context(), UploadRequest{
				URL: srv.URL, Body: strings.NewReader("x"), FileName: "a.bin",
				Size: tc.size, Offset: tc.offset, Strategy: DefaultStrategy,
			})
			if err == nil {
				t.Fatal("want an error for an impossible range")
			}
			// Left alone, the negative remainder becomes a negative
			// ContentLength, which net/http sends as chunked -- the one framing
			// the storage host rejects, with a 400 that names nothing useful.
			if got.Method != "" {
				t.Errorf("the request was sent anyway: %s, Transfer-Encoding %v",
					got.Method, got.TransferEnc)
			}
		})
	}
}

func TestUploadAcceptsAnOffsetAtTheExactEnd(t *testing.T) {
	var got capturedUpload
	c, srv := storageHost(t, 200, uploadOK, &got)

	// A fully delivered file has nothing left to send but is not an error: the
	// reservation can legitimately report an offset equal to the size.
	_, err := c.Upload(t.Context(), UploadRequest{
		URL: srv.URL, Body: strings.NewReader(""), FileName: "a.bin",
		Size: 10, Offset: 10, Strategy: DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got.Range != "10-9/10" {
		t.Errorf("Content-Range = %q", got.Range)
	}
}

func TestDefaultStrategyIsTheVerifiedOne(t *testing.T) {
	// A reordering of Strategies must not silently change what users send.
	if DefaultStrategy.Name != "post-multipart" {
		t.Errorf("DefaultStrategy = %q, want post-multipart", DefaultStrategy.Name)
	}
	if DefaultStrategy.Method != http.MethodPost || !DefaultStrategy.Multipart {
		t.Errorf("DefaultStrategy = %+v", DefaultStrategy)
	}
	if DefaultStrategy.PartName != "Filedata" {
		t.Errorf("part name = %q, want Filedata", DefaultStrategy.PartName)
	}
}

func TestStrategyByName(t *testing.T) {
	for _, s := range Strategies {
		got, err := StrategyByName(s.Name)
		if err != nil {
			t.Errorf("StrategyByName(%q): %v", s.Name, err)
		}
		if got.Name != s.Name {
			t.Errorf("got %q, want %q", got.Name, s.Name)
		}
	}

	_, err := StrategyByName("carrier-pigeon")
	if err == nil {
		t.Fatal("want an error for an unknown strategy")
	}
	// The message must list what is available.
	if !strings.Contains(err.Error(), "post-multipart") {
		t.Errorf("error %q should list the valid strategies", err)
	}
}

func TestResolveStrategyHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvStrategy, "post-raw")
	got, err := ResolveStrategy()
	if err != nil {
		t.Fatalf("ResolveStrategy: %v", err)
	}
	if got.Name != "post-raw" {
		t.Errorf("strategy = %q, want post-raw", got.Name)
	}

	t.Setenv(EnvStrategy, "")
	if got, _ = ResolveStrategy(); got.Name != DefaultStrategy.Name {
		t.Errorf("strategy = %q, want the default %q", got.Name, DefaultStrategy.Name)
	}

	t.Setenv(EnvStrategy, "nonsense")
	if _, err := ResolveStrategy(); err == nil {
		t.Error("an invalid environment override should be reported, not ignored")
	}
}

func TestRedactURLHidesStorageTokens(t *testing.T) {
	const url = "https://storage.example/v1/storage/upload?auth=4&stoken=SECRETVALUE"

	got := redactURL(url)
	if strings.Contains(got, "SECRETVALUE") {
		t.Errorf("redactURL leaked the token: %q", got)
	}
	if !strings.HasPrefix(got, "https://storage.example/v1/storage/upload") {
		t.Errorf("redactURL dropped the useful part: %q", got)
	}
	if got := redactURL("https://storage.example/plain"); got != "https://storage.example/plain" {
		t.Errorf("a URL without a query was altered: %q", got)
	}
}
