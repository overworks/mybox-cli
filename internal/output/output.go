// Package output renders command results as either aligned tables for humans or
// JSON for scripts.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

// Printer writes command output. Commands construct one from the global flags
// and never touch os.Stdout directly, so --json and --quiet apply uniformly.
type Printer struct {
	Out io.Writer
	Err io.Writer
	// JSON switches every result to machine-readable output.
	JSON bool
	// Quiet suppresses incidental progress and confirmation messages. Results
	// and errors are still written.
	Quiet bool
	// Local is the time zone timestamps are rendered in. Nil means time.Local.
	Local *time.Location
}

// Table builds an aligned table writer. Callers write tab-separated rows and
// must call Flush.
func (p *Printer) Table() *tabwriter.Writer {
	return tabwriter.NewWriter(p.Out, 0, 0, 2, ' ', 0)
}

// Print writes a plain line of result output.
func (p *Printer) Print(format string, args ...any) {
	fmt.Fprintf(p.Out, format+"\n", args...)
}

// Info writes an incidental status message to stderr, honouring --quiet. It goes
// to stderr so that piping stdout to a file or jq stays clean.
func (p *Printer) Info(format string, args ...any) {
	if p.Quiet {
		return
	}
	fmt.Fprintf(p.Err, format+"\n", args...)
}

// Warn writes a warning to stderr. Warnings ignore --quiet because they report
// something the user probably needs to know.
func (p *Printer) Warn(format string, args ...any) {
	fmt.Fprintf(p.Err, "warning: "+format+"\n", args...)
}

// EmitJSON writes v as indented JSON.
func (p *Printer) EmitJSON(v any) error {
	enc := json.NewEncoder(p.Out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// Bytes renders a byte count in binary units, e.g. "1.0 GiB".
//
// Sizes are shown with one decimal place from KiB upwards; exact byte counts are
// printed unrounded so small files stay legible.
func Bytes(n int64) string {
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

// Time reformats an API timestamp for display. The API returns RFC3339 with a
// +09:00 offset; the value is shown in the printer's zone. Unparseable input is
// passed through untouched rather than hidden.
func (p *Printer) Time(s string) string {
	if s == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	loc := p.Local
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

// Percent renders used/total as a percentage, tolerating a zero total.
func Percent(used, total int64) string {
	if total <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(used)/float64(total)*100)
}

// Bar renders a fixed-width usage meter, e.g. "[####------]".
func Bar(used, total int64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := 0
	if total > 0 {
		filled = int(float64(used) / float64(total) * float64(width))
		filled = max(0, min(width, filled))
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}
