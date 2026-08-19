//go:build e2e

// Verifies resuming an upload that was genuinely cut mid-flight.
//
// A short body cannot fake this: the storage host compares what arrives against
// the declared fileSize and answers 500 "File Storage Error". The request has to
// declare its full length and then have the socket die under it, which is what
// cuttingDialer arranges.
//
//	MYBOX_TOKEN=mbx_pat_xxx go test -tags e2e -run TestE2EResumeAfterInterruptedUpload -v ./internal/cli/
package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/config"
	"github.com/overworks/mybox-cli/internal/transfer"
)

const (
	// resumeFileSize is large enough that a cut lands well inside the body.
	resumeFileSize = 12 << 20
	// resumeCutAfter is roughly how much reaches the server before the socket dies.
	resumeCutAfter = 5 << 20

	// The storage tier holds a lock on the partial upload for a while after the
	// connection dies. The documented "about two seconds" is optimistic; poll.
	resumeLockRetries = 20
	resumeLockGap     = 3 * time.Second
)

func TestE2EResumeAfterInterruptedUpload(t *testing.T) {
	e := newE2E(t)

	token := os.Getenv(config.EnvToken)
	client, err := api.New(api.Options{Token: token, BaseURL: os.Getenv(config.EnvAPIBase)})
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ctx := e.ctx

	// Work inside the same scratch folder the rest of the suite uses.
	folder, err := client.CreateFolder(ctx, "resume-"+time.Now().Format("150405"), "")
	if err != nil {
		t.Fatalf("could not create the scratch folder: %v", err)
	}
	t.Cleanup(func() {
		if err := client.DeleteResource(e.ctx, folder.ResourceID); err != nil {
			t.Errorf("could not clean up the scratch folder — remove id:%s by hand: %v", folder.ResourceID, err)
		}
	})

	local := filepath.Join(t.TempDir(), "resume.bin")
	payload := make([]byte, resumeFileSize)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(local)
	if err != nil {
		t.Fatal(err)
	}

	// Both reservations must carry the identical modifiedTime: it is how MYBOX
	// recognises the retained bytes as belonging to this file.
	req := api.UploadRequest{
		FileName:     "resume.bin",
		FileSize:     info.Size(),
		ParentID:     folder.ResourceID,
		Resume:       true,
		ModifiedTime: uploadModifiedTime(info.ModTime()),
	}
	if !strings.HasSuffix(req.ModifiedTime, "+09:00") {
		t.Fatalf("modifiedTime = %q, want the KST spelling", req.ModifiedTime)
	}

	// --- leg one: start the upload for real, then cut the socket ---
	first, err := client.CreateUploadURL(ctx, req)
	if err != nil {
		t.Fatalf("the first reservation failed: %v", err)
	}

	f, err := os.Open(local)
	if err != nil {
		t.Fatal(err)
	}
	dialer := &cuttingDialer{after: resumeCutAfter}
	cutting := &transfer.Client{
		HTTPClient: &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}},
		UserAgent:  "mybox-cli/e2e",
	}
	_, err = cutting.Upload(ctx, transfer.UploadRequest{
		URL: first.UploadURL, Body: f, FileName: "resume.bin",
		Size: info.Size(), Strategy: transfer.DefaultStrategy,
	})
	f.Close()
	if err == nil {
		t.Fatal("the upload completed instead of being cut; this test depends on a real interruption")
	}
	t.Logf("cut on purpose after %s (%v)", bytesOf(dialer.written.Load()), err)

	// --- leg two: reserve again and see how much MYBOX kept ---
	offset := resumeOffsetOnceSettled(t, e, client, req)
	if offset <= 0 {
		t.Fatalf("MYBOX retained nothing from the interrupted upload (offset=%d)", offset)
	}
	if offset >= info.Size() {
		t.Fatalf("offset %d is not inside a file of %d bytes", offset, info.Size())
	}
	t.Logf("MYBOX retained %s of %s", bytesOf(offset), bytesOf(info.Size()))

	// --- leg three: send only the remainder through the real code path ---
	second, err := client.CreateUploadURL(ctx, req)
	if err != nil {
		t.Fatalf("the resume reservation failed: %v", err)
	}
	if second.Offset != offset {
		t.Logf("note: the offset moved from %d to %d", offset, second.Offset)
	}

	f, err = os.Open(local)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(second.Offset, 0); err != nil {
		t.Fatal(err)
	}

	tc := transfer.New("mybox-cli/e2e", nil)
	res, err := tc.Upload(ctx, transfer.UploadRequest{
		URL: second.UploadURL, Body: f, FileName: "resume.bin",
		Size: info.Size(), Offset: second.Offset, Strategy: transfer.DefaultStrategy,
	})
	if err != nil {
		t.Fatalf("resuming failed: %v", err)
	}
	if res.FileSize != info.Size() {
		t.Errorf("stored size = %d, want %d", res.FileSize, info.Size())
	}

	// --- verify the reassembled file byte for byte ---
	ticket, err := client.CreateDownloadURL(ctx, res.ResourceID)
	if err != nil {
		t.Fatalf("could not reserve a download: %v", err)
	}
	got := filepath.Join(t.TempDir(), "downloaded.bin")
	out, err := os.Create(got)
	if err != nil {
		t.Fatal(err)
	}
	n, err := tc.Download(ctx, ticket.DownloadURL, out)
	out.Close()
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if n != info.Size() {
		t.Fatalf("downloaded %d bytes, want %d", n, info.Size())
	}

	downloaded, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	// The whole point: the two legs must reassemble into the original file, not
	// merely into something of the right length.
	if want, have := sha256.Sum256(payload), sha256.Sum256(downloaded); want != have {
		t.Fatalf("content differs: %s != %s", hex.EncodeToString(want[:8]), hex.EncodeToString(have[:8]))
	}
	t.Logf("resume verified: only %s of %s re-sent, content matches",
		bytesOf(info.Size()-second.Offset), bytesOf(info.Size()))
}

// resumeOffsetOnceSettled reserves repeatedly until the lock the storage tier
// holds on the interrupted transfer clears, then reports the retained offset.
func resumeOffsetOnceSettled(t *testing.T, e *e2e, client *api.Client, req api.UploadRequest) int64 {
	t.Helper()

	for attempt := 1; attempt <= resumeLockRetries; attempt++ {
		ticket, err := client.CreateUploadURL(e.ctx, req)
		if err == nil {
			return ticket.Offset
		}

		var apiErr *api.Error
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusLocked {
			t.Logf("locked; retrying in %s (%d/%d)", resumeLockGap, attempt, resumeLockRetries)
			time.Sleep(resumeLockGap)
			continue
		}
		t.Fatalf("the resume reservation failed: %v", err)
	}

	t.Fatalf("the interrupted upload stayed locked for %s",
		time.Duration(resumeLockRetries)*resumeLockGap)
	return 0
}
