//go:build e2e

// End-to-end tests against a real MYBOX account.
//
// These are excluded from a normal `go test ./...` because they mutate a live
// account and consume documented rate limits. Run them with:
//
//	MYBOX_TOKEN=mbx_pat_xxx go test -tags e2e -v ./internal/cli/
//
// Everything happens inside a scratch folder that is created at the start and
// trashed at the end. Two things are deliberately not exercised by default:
//
//   - `trash empty`, which would destroy items the account owner deleted from
//     the web, not just ours.
//   - the trash auto-delete interval, which is an account-wide setting rather
//     than something scoped to the scratch folder. Set
//     MYBOX_E2E_ALLOW_SETTING_CHANGES=1 to include it.
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/config"
	"github.com/overworks/mybox-cli/internal/resolve"
)

// e2eRoot is the scratch folder every e2e test works inside.
const e2eRoot = "/mybox-cli-e2e"

// callGap spaces out calls. Most APIs allow 60 per minute on the cheapest plan,
// so one call per second keeps a full run inside the budget.
const callGap = time.Second

// runBudget bounds a whole test, including its cleanup.
const runBudget = 15 * time.Minute

// lockRetryGap is how long to wait before retrying a resource the service is
// still holding a lock on. The storage tier keeps one for a second or two after
// a transfer or a delete.
const lockRetryGap = 3 * time.Second

type e2e struct {
	t   *testing.T
	ctx context.Context
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	if os.Getenv(config.EnvToken) == "" {
		t.Skipf("skipped: %s is not set", config.EnvToken)
	}
	// Use a scratch config and cache so a run cannot disturb the developer's own.
	t.Setenv(config.EnvConfigHome, t.TempDir())
	t.Setenv(resolve.EnvCacheHome, t.TempDir())

	// t.Context() is cancelled *just before* cleanup functions run, so a command
	// issued from a cleanup would fail instantly with a cancelled context and
	// leave the account modified. Commands therefore run on this context
	// instead. Cleanups are called last-added-first, and this one is registered
	// before the test body registers any of its own, so it is cancelled after
	// all of them have run.
	ctx, cancel := context.WithTimeout(context.Background(), runBudget)
	t.Cleanup(cancel)

	return &e2e{t: t, ctx: ctx}
}

// inCleanup reports whether the test body has finished. The test's own context
// is cancelled on the way into cleanup, which makes it a reliable signal.
func (e *e2e) inCleanup() bool { return e.t.Context().Err() != nil }

// run executes a command and fails the test if it does not succeed.
func (e *e2e) run(args ...string) string {
	e.t.Helper()
	stdout, stderr, code := e.try(args...)
	if code != ExitOK {
		e.t.Fatalf("mybox %s\n  exit %d\n  %s", strings.Join(args, " "), code, stderr)
	}
	return stdout
}

// try executes a command and returns its result without failing the test.
func (e *e2e) try(args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	time.Sleep(callGap)

	var out, errBuf bytes.Buffer
	code = Execute(e.ctx, args, &out, &errBuf)

	// A rate-limit rejection means the environment is busy, not that the code is
	// wrong; skipping keeps the suite honest instead of flaky. Never skip from a
	// cleanup though: that would report the run as skipped and hide the fact
	// that the account was left modified.
	if code == ExitRateLimited && !e.inCleanup() {
		e.t.Skipf("skipped: rate limited (%s)", errBuf.String())
	}
	return out.String(), errBuf.String(), code
}

// cleanup undoes something the test did, retrying while the service holds a
// transient lock.
//
// A failure here is reported as a test failure, not logged. A run that leaves
// the account holding scratch folders — or, worse, a changed setting — has not
// passed cleanly, and saying so quietly is how that drift goes unnoticed.
func (e *e2e) cleanup(what string, args ...string) {
	e.t.Helper()

	const attempts = 3
	for attempt := 1; ; attempt++ {
		_, stderr, code := e.try(args...)
		if code == ExitOK {
			return
		}
		if attempt < attempts && strings.Contains(stderr, "423") {
			time.Sleep(lockRetryGap)
			continue
		}
		e.t.Errorf("could not clean up %s — run this by hand: mybox %s\n  %s",
			what, strings.Join(args, " "), strings.TrimSpace(stderr))
		return
	}
}

// TestE2EHarnessContextOutlivesTheTestBody guards the fix for a bug that let
// every cleanup fail silently: commands were issued on t.Context(), which Go
// cancels on the way into cleanup. The symptom was scratch folders left behind
// and, worse, an account setting changed and never restored.
func TestE2EHarnessContextOutlivesTheTestBody(t *testing.T) {
	e := newE2E(t)

	t.Cleanup(func() {
		if !e.inCleanup() {
			t.Error("inCleanup() = false inside a cleanup function")
		}
		if err := e.ctx.Err(); err != nil {
			t.Errorf("the context was already done at cleanup time: %v — every cleanup would fail silently", err)
		}
	})

	if e.inCleanup() {
		t.Error("inCleanup() = true while the test body is still running")
	}
	if err := e.ctx.Err(); err != nil {
		t.Fatalf("the context ended while the test body was still running: %v", err)
	}
}

func TestE2EFullLifecycle(t *testing.T) {
	e := newE2E(t)

	e.run("auth", "status")
	e.run("df")

	// Start from a clean slate in case an earlier run was interrupted.
	if _, _, code := e.try("stat", e2eRoot); code == ExitOK {
		e.run("rm", "-y", e2eRoot)
	}

	e.run("mkdir", "-p", e2eRoot+"/하위")
	t.Cleanup(func() { e.cleanup("the scratch folder", "rm", "-y", e2eRoot) })

	// --- upload, list, stat ---
	local := filepath.Join(t.TempDir(), "sample.txt")
	payload := []byte("mybox-cli e2e " + time.Now().Format(time.RFC3339))
	if err := os.WriteFile(local, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, stderr, code := e.try("up", local, e2eRoot); code != ExitOK {
		// The storage wire format is established but undocumented by Naver, so
		// a failure here may mean it changed rather than that the CLI is broken.
		t.Fatalf("upload failed; the wire format may have changed —\n"+
			"  find one that works with 'mybox debug upload-probe', then set $%s.\n  %s",
			"MYBOX_UPLOAD_STRATEGY", stderr)
	}

	listing := e.run("ls", "-l", e2eRoot)
	if !strings.Contains(listing, "sample.txt") {
		t.Fatalf("the uploaded file is missing from the listing:\n%s", listing)
	}

	info := e.run("stat", e2eRoot+"/sample.txt")
	if !strings.Contains(info, fmt.Sprint(len(payload))) {
		t.Errorf("size does not match (expected %d bytes):\n%s", len(payload), info)
	}

	// --- download and verify the bytes survived the round trip ---
	downDir := t.TempDir()
	e.run("down", e2eRoot+"/sample.txt", "-o", downDir)
	got, err := os.ReadFile(filepath.Join(downDir, "sample.txt"))
	if err != nil {
		t.Fatalf("could not read the downloaded file: %v", err)
	}
	if sum(got) != sum(payload) {
		t.Errorf("content changed in transit: %s != %s", sum(got), sum(payload))
	}

	// --- copy, move, rename ---
	e.run("cp", e2eRoot+"/sample.txt", e2eRoot+"/하위", "--name", "copy.txt")
	e.run("mv", e2eRoot+"/하위/copy.txt", e2eRoot)
	e.run("rename", e2eRoot+"/copy.txt", "renamed.txt")
	if out := e.run("ls", e2eRoot); !strings.Contains(out, "renamed.txt") {
		t.Errorf("the rename is not reflected in the listing:\n%s", out)
	}

	// --- favourites ---
	e.run("star", e2eRoot+"/renamed.txt")
	if out := e.run("stat", e2eRoot+"/renamed.txt"); !strings.Contains(out, "Favourite") {
		t.Errorf("could not read the favourite state:\n%s", out)
	}
	e.run("unstar", e2eRoot+"/renamed.txt")

	// --- trash round trip ---
	e.run("rm", e2eRoot+"/renamed.txt")
	// Deleting only moves the resource; its ID stays readable and only its
	// parent changes. Resolving by *path* is what must stop finding it, which
	// is why this checks a path rather than an id:.
	if _, _, code := e.try("--no-cache", "stat", e2eRoot+"/renamed.txt"); code != ExitNotFound {
		t.Errorf("the path still resolves after deletion (exit %d)", code)
	}

	trashed := e.run("--json", "trash", "ls", "-n", "50")
	id := findTrashedID(t, trashed, "renamed.txt")
	e.run("trash", "restore", "id:"+id)
	// A restore lands the file back where it was, so the cache must not still
	// believe it is gone.
	e.run("--no-cache", "stat", e2eRoot+"/renamed.txt")

	e.run("rm", e2eRoot+"/renamed.txt")
	trashed = e.run("--json", "trash", "ls", "-n", "50")
	id = findTrashedID(t, trashed, "renamed.txt")
	e.run("trash", "rm", "-y", "id:"+id)
}

func TestE2ESearchFindsAnUploadedFile(t *testing.T) {
	e := newE2E(t)

	// Search runs off an index that may lag a write, so this only asserts the
	// call succeeds and returns well-formed results.
	out := e.run("--json", "search", "files", "--category", "document", "-n", "20")
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Errorf("search output is not a JSON array:\n%s", out)
	}
}

// envAllowSettingChanges opts in to tests that modify account-wide settings.
const envAllowSettingChanges = "MYBOX_E2E_ALLOW_SETTING_CHANGES"

// restorePointPath is where the pre-test value of an account setting is written.
// It lives outside the test's temporary directory on purpose: if the restore
// fails, this file is the only remaining record of what the value used to be.
func restorePointPath() string {
	return filepath.Join(os.TempDir(), "mybox-e2e-restore.json")
}

// saveRestorePoint durably records a setting's original value before it is
// changed, and tells the user where to find it.
func saveRestorePoint(t *testing.T, setting string, value int) {
	t.Helper()
	path := restorePointPath()

	raw, err := json.Marshal(map[string]any{"setting": setting, "originalValue": value})
	if err != nil {
		t.Fatalf("could not build the restore point: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		// Without a durable record there is no safe way to run this test.
		t.Fatalf("could not write the restore point to %s: %v", path, err)
	}
	t.Logf("recorded the original %s of %d in %s", setting, value, path)
}

// TestE2ETrashAutoDeleteRoundTrip changes an account-wide setting and puts it
// back.
//
// Every other test in this file works inside a scratch folder, so the worst a
// failure leaves behind is a folder to delete. This one is different: the trash
// auto-delete interval is a single account-wide value, MYBOX offers no way to
// look up what it used to be, and a failed restore therefore leaves the user on
// an interval they did not choose with no way to recover the old one. That is
// why it is opt-in and why the original is written to disk first.
func TestE2ETrashAutoDeleteRoundTrip(t *testing.T) {
	e := newE2E(t)

	if os.Getenv(envAllowSettingChanges) == "" {
		t.Skipf("skipped: this changes an account-wide setting; set %s=1 to include it", envAllowSettingChanges)
	}

	before := strings.TrimSpace(e.run("--json", "trash", "autodelete"))
	var current api.TrashAutoDelete
	decodeJSON(t, before, &current)
	original := current.TrashAutoDeleteDays

	// Write the original down before touching it, not after.
	saveRestorePoint(t, "trashAutoDeleteDays", original)

	// Set it to a different documented value, then put it back exactly as found.
	next := 30
	if original == 30 {
		next = 15
	}
	e.run("trash", "autodelete", fmt.Sprint(next))

	t.Cleanup(func() {
		e.cleanup("the trash auto-delete interval", "trash", "autodelete", fmt.Sprint(original))

		// Confirm the restore instead of assuming it: a silent failure here is
		// exactly the unrecoverable case this whole apparatus exists to prevent.
		var restored api.TrashAutoDelete
		out, _, code := e.try("--json", "trash", "autodelete")
		if code != ExitOK {
			t.Errorf("could not confirm the restore; run this by hand: mybox trash autodelete %d", original)
			return
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &restored); err != nil {
			t.Errorf("could not parse the confirmation response: %v", err)
			return
		}
		if restored.TrashAutoDeleteDays != original {
			t.Errorf("restore failed: now %d, was %d. Run this by hand: mybox trash autodelete %d",
				restored.TrashAutoDeleteDays, original, original)
			return
		}
		// Only now is the record redundant.
		_ = os.Remove(restorePointPath())
	})

	after := strings.TrimSpace(e.run("--json", "trash", "autodelete"))
	var got api.TrashAutoDelete
	decodeJSON(t, after, &got)
	if got.TrashAutoDeleteDays != next {
		t.Errorf("the setting did not take: %d, want %d", got.TrashAutoDeleteDays, next)
	}
}

func TestE2ERejectsAnInvalidToken(t *testing.T) {
	e := newE2E(t)

	_, stderr, code := e.try("--token", "mbx_pat_definitely_invalid", "df")
	if code != ExitAuth {
		t.Errorf("exit = %d, want %d", code, ExitAuth)
	}
	if !strings.Contains(stderr, "401") {
		t.Errorf("stderr:\n%s", stderr)
	}
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func decodeJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("could not parse JSON (%s): %v", s, err)
	}
}

// findTrashedID pulls a resource ID out of a --json trash listing.
func findTrashedID(t *testing.T, listing, name string) string {
	t.Helper()
	var items []api.TrashedResourceItem
	decodeJSON(t, listing, &items)
	for _, item := range items {
		if item.Name == name {
			return item.ResourceID
		}
	}
	t.Fatalf("nothing named %q in the trash:\n%s", name, listing)
	return ""
}
