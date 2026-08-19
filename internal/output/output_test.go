package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	for _, tc := range []struct {
		give int64
		want string
	}{
		{0, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1048576, "1.0 MiB"},
		{5368709120, "5.0 GiB"},
		{32212254720, "30.0 GiB"},
		{53687091200, "50.0 GiB"},
		{1 << 50, "1.0 PiB"},
	} {
		if got := Bytes(tc.give); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.give, got, tc.want)
		}
	}
}

func TestPercent(t *testing.T) {
	for _, tc := range []struct {
		used, total int64
		want        string
	}{
		{0, 100, "0.0%"},
		{50, 100, "50.0%"},
		{5368709120, 32212254720, "16.7%"},
		{10, 0, "-"}, // an unknown quota must not divide by zero
		{10, -1, "-"},
	} {
		if got := Percent(tc.used, tc.total); got != tc.want {
			t.Errorf("Percent(%d, %d) = %q, want %q", tc.used, tc.total, got, tc.want)
		}
	}
}

func TestBar(t *testing.T) {
	for _, tc := range []struct {
		used, total int64
		width       int
		want        string
	}{
		{0, 100, 10, "[----------]"},
		{50, 100, 10, "[#####-----]"},
		{100, 100, 10, "[##########]"},
		{150, 100, 10, "[##########]"}, // over quota must not overflow the bar
		{50, 0, 10, "[----------]"},
		{50, 100, 0, ""},
	} {
		if got := Bar(tc.used, tc.total, tc.width); got != tc.want {
			t.Errorf("Bar(%d, %d, %d) = %q, want %q", tc.used, tc.total, tc.width, got, tc.want)
		}
	}
}

func TestTimeRendersInTheConfiguredZone(t *testing.T) {
	kst := time.FixedZone("KST", 9*60*60)
	p := &Printer{Local: kst}

	if got := p.Time("2026-08-11T09:00:00+09:00"); got != "2026-08-11 09:00" {
		t.Errorf("Time() = %q", got)
	}

	utc := &Printer{Local: time.UTC}
	if got := utc.Time("2026-08-11T09:00:00+09:00"); got != "2026-08-11 00:00" {
		t.Errorf("Time() in UTC = %q, want the converted time", got)
	}
}

func TestTimePassesThroughUnparseableInput(t *testing.T) {
	p := &Printer{Local: time.UTC}

	// Showing the raw value beats hiding a format the API might change to.
	if got := p.Time("언젠가"); got != "언젠가" {
		t.Errorf("Time() = %q, want the input unchanged", got)
	}
	if got := p.Time(""); got != "" {
		t.Errorf("Time(\"\") = %q, want empty", got)
	}
}

func TestInfoRespectsQuietButWarnDoesNot(t *testing.T) {
	var out, errBuf bytes.Buffer
	p := &Printer{Out: &out, Err: &errBuf, Quiet: true}

	p.Info("숨겨질 메시지")
	p.Warn("보여야 할 경고")

	if strings.Contains(errBuf.String(), "숨겨질") {
		t.Errorf("Info printed under --quiet: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "보여야 할 경고") {
		t.Errorf("Warn was suppressed under --quiet: %q", errBuf.String())
	}
}

func TestInfoGoesToStderrSoStdoutStaysPipeable(t *testing.T) {
	var out, errBuf bytes.Buffer
	p := &Printer{Out: &out, Err: &errBuf}

	p.Info("진행 상황")
	p.Print("결과")

	if strings.Contains(out.String(), "진행 상황") {
		t.Errorf("status text leaked into stdout: %q", out.String())
	}
	if !strings.Contains(out.String(), "결과") {
		t.Errorf("result missing from stdout: %q", out.String())
	}
}

func TestEmitJSONDoesNotEscapeHTML(t *testing.T) {
	var out bytes.Buffer
	p := &Printer{Out: &out}

	if err := p.EmitJSON(map[string]string{"name": "a&b<c>"}); err != nil {
		t.Fatalf("EmitJSON: %v", err)
	}
	// Escaping would corrupt file names that contain & or angle brackets.
	if !strings.Contains(out.String(), "a&b<c>") {
		t.Errorf("EmitJSON escaped the value: %s", out.String())
	}
}
