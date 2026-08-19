package transfer

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// clockAt builds a progress reporter with a controllable clock, so rendering is
// deterministic instead of depending on how fast the test machine is.
func clockAt(out *bytes.Buffer, label string, total int64) (*Progress, *time.Time) {
	now := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	p := NewProgress(out, label, total)
	p.started = now
	p.now = func() time.Time { return now }
	return p, &now
}

func TestProgressReportsPercentWhenTotalIsKnown(t *testing.T) {
	var buf bytes.Buffer
	p, now := clockAt(&buf, "회의록.pdf", 1000)

	w := p.Wrap(io.Discard)
	*now = now.Add(time.Second)
	if _, err := w.Write(make([]byte, 500)); err != nil {
		t.Fatal(err)
	}
	p.Done()

	out := buf.String()
	if !strings.Contains(out, "50.0%") {
		t.Errorf("output should show the percentage:\n%q", out)
	}
	if !strings.Contains(out, "회의록.pdf") {
		t.Errorf("output should show the label:\n%q", out)
	}
}

func TestProgressWithoutATotalShowsOnlyTheAmount(t *testing.T) {
	var buf bytes.Buffer
	p, now := clockAt(&buf, "파일", 0)

	w := p.Wrap(io.Discard)
	*now = now.Add(time.Second)
	_, _ = w.Write(make([]byte, 2048))
	p.Done()

	out := buf.String()
	if strings.Contains(out, "%") {
		t.Errorf("a percentage was shown without a known total:\n%q", out)
	}
	if !strings.Contains(out, "2.0 KiB") {
		t.Errorf("output should show the transferred amount:\n%q", out)
	}
}

func TestProgressThrottlesRedraws(t *testing.T) {
	var buf bytes.Buffer
	p, _ := clockAt(&buf, "f", 1000)

	w := p.Wrap(io.Discard)
	// Many small writes inside one throttle window must not each redraw.
	for range 100 {
		_, _ = w.Write(make([]byte, 1))
	}
	if n := strings.Count(buf.String(), "\r"); n > 2 {
		t.Errorf("redrew %d times for 100 writes in the same instant, want at most 2", n)
	}
}

func TestProgressSetUsesAbsoluteCounts(t *testing.T) {
	var buf bytes.Buffer
	p, now := clockAt(&buf, "업로드", 1000)

	// Uploads count bytes read out of the file, so they report a running total.
	p.Set(250)
	*now = now.Add(time.Second)
	p.Set(750)
	p.Done()

	if !strings.Contains(buf.String(), "75.0%") {
		t.Errorf("Set should report an absolute position:\n%q", buf.String())
	}
}

func TestProgressAbortClearsTheLine(t *testing.T) {
	var buf bytes.Buffer
	p, now := clockAt(&buf, "파일", 1000)

	w := p.Wrap(io.Discard)
	*now = now.Add(time.Second)
	_, _ = w.Write(make([]byte, 500))
	buf.Reset()
	p.Abort()

	// A failed transfer must not leave "50%" sitting on screen as if it stalled.
	if strings.Contains(buf.String(), "%") {
		t.Errorf("Abort left progress text behind: %q", buf.String())
	}
	if !strings.HasPrefix(buf.String(), "\r") {
		t.Errorf("Abort should rewind the line: %q", buf.String())
	}
}

func TestProgressDoneIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	p, _ := clockAt(&buf, "f", 10)

	p.Done()
	before := buf.Len()
	p.Done()
	p.Abort()
	if buf.Len() != before {
		t.Error("Done/Abort after Done wrote more output")
	}
}

func TestNilProgressIsSafe(t *testing.T) {
	var p *Progress

	w := p.Wrap(io.Discard)
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatalf("write through a nil progress: %v", err)
	}
	p.Set(1)
	p.Done()
	p.Abort()
}
