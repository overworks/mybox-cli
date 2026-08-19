package transfer

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// Progress reports transfer progress on a single rewritten line. It is a plain
// io.Writer wrapper, so it composes with io.Copy.
//
// Callers should only install one when the output is a terminal; writing
// carriage returns into a pipe or log would produce noise.
type Progress struct {
	out   io.Writer
	label string
	total int64

	mu      sync.Mutex
	written int64
	last    time.Time
	started time.Time
	done    bool
	now     func() time.Time
}

// NewProgress builds a reporter. A total of zero or less means the size is
// unknown, and only the transferred amount is shown.
func NewProgress(out io.Writer, label string, total int64) *Progress {
	now := time.Now
	return &Progress{out: out, label: label, total: total, started: now(), now: now}
}

// Wrap returns a writer that reports progress as bytes pass through it.
func (p *Progress) Wrap(w io.Writer) io.Writer {
	if p == nil {
		return w
	}
	return &progressWriter{p: p, w: w}
}

type progressWriter struct {
	p *Progress
	w io.Writer
}

func (pw *progressWriter) Write(b []byte) (int, error) {
	n, err := pw.w.Write(b)
	pw.p.add(int64(n))
	return n, err
}

// Set records an absolute byte count. Uploads count bytes as they are read out
// of the file, so they report a total rather than an increment.
func (p *Progress) Set(n int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = n

	now := p.now()
	if now.Sub(p.last) < 100*time.Millisecond {
		return
	}
	p.last = now
	p.render(false)
}

func (p *Progress) add(n int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written += n

	// Redrawing on every chunk would spend more time formatting than copying.
	now := p.now()
	if now.Sub(p.last) < 100*time.Millisecond {
		return
	}
	p.last = now
	p.render(false)
}

// Done finishes the line, leaving a final summary in place.
func (p *Progress) Done() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done = true
	p.render(true)
	fmt.Fprintln(p.out)
}

// Abort clears the progress line without leaving a misleading summary behind.
func (p *Progress) Abort() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return
	}
	p.done = true
	fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", 72))
}

func (p *Progress) render(final bool) {
	elapsed := p.now().Sub(p.started)
	rate := ""
	if secs := elapsed.Seconds(); secs > 0.5 {
		rate = fmt.Sprintf("  %s/s", humanBytes(int64(float64(p.written)/secs)))
	}

	line := fmt.Sprintf("%s  %s", p.label, humanBytes(p.written))
	if p.total > 0 {
		pct := float64(p.written) / float64(p.total) * 100
		line = fmt.Sprintf("%s  %s / %s  %5.1f%%", p.label, humanBytes(p.written), humanBytes(p.total), pct)
	}
	line += rate
	if final {
		line += fmt.Sprintf("  (%s)", elapsed.Round(time.Millisecond*100))
	}

	// Pad to overwrite whatever the previous, possibly longer, line left behind.
	fmt.Fprintf(p.out, "\r%-72s", line)
}

// humanBytes mirrors output.Bytes. It is duplicated rather than imported to keep
// this package free of a dependency on the presentation layer.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 5; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
