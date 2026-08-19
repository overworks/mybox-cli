package cli

import (
	"strings"
	"testing"

	"github.com/overworks/mybox-cli/internal/api"
)

func TestParseRate(t *testing.T) {
	for _, tc := range []struct {
		name string
		give string
		want map[api.Group]int
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{
			// A bare number is the whole-account allowance.
			"bare number covers every group",
			"240",
			map[api.Group]int{
				api.GroupDefault: 240, api.GroupSearch: 240,
				api.GroupDelete: 240, api.GroupRestore: 240,
			},
		},
		{
			"single group",
			"search=30",
			map[api.Group]int{api.GroupSearch: 30},
		},
		{
			// The shape a 180GB-or-larger plan actually needs: 240 everywhere
			// except search, which stays at its documented 30.
			"baseline then override",
			"240,search=30",
			map[api.Group]int{
				api.GroupDefault: 240, api.GroupSearch: 30,
				api.GroupDelete: 240, api.GroupRestore: 240,
			},
		},
		{
			"later entries win",
			"search=10,search=30",
			map[api.Group]int{api.GroupSearch: 30},
		},
		{
			"order matters: a bare number after an override resets it",
			"search=30,240",
			map[api.Group]int{
				api.GroupDefault: 240, api.GroupSearch: 240,
				api.GroupDelete: 240, api.GroupRestore: 240,
			},
		},
		{
			"several groups",
			"delete=240,restore=180",
			map[api.Group]int{api.GroupDelete: 240, api.GroupRestore: 180},
		},
		{
			"whitespace is tolerated",
			" 240 , search = 30 ",
			map[api.Group]int{
				api.GroupDefault: 240, api.GroupSearch: 30,
				api.GroupDelete: 240, api.GroupRestore: 240,
			},
		},
		{
			"empty fields are skipped",
			"240,,search=30,",
			map[api.Group]int{
				api.GroupDefault: 240, api.GroupSearch: 30,
				api.GroupDelete: 240, api.GroupRestore: 240,
			},
		},
		{
			// Zero disables client-side shaping and leaves the service to push back.
			"zero disables shaping",
			"0",
			map[api.Group]int{
				api.GroupDefault: 0, api.GroupSearch: 0,
				api.GroupDelete: 0, api.GroupRestore: 0,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRate(tc.give)
			if err != nil {
				t.Fatalf("parseRate(%q): %v", tc.give, err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("parseRate(%q) = %v, want %v", tc.give, got, tc.want)
			}
			for group, n := range tc.want {
				if got[group] != n {
					t.Errorf("parseRate(%q)[%s] = %d, want %d", tc.give, group, got[group], n)
				}
			}
		})
	}
}

func TestParseRateRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		give string
		want string
	}{
		{"많이", "is not a number"},
		{"search=많이", "is not a number"},
		{"upload=100", "unknown group"},
		{"240,typo=10", "unknown group"},
		{"search=", "is not a number"},
	} {
		_, err := parseRate(tc.give)
		if err == nil {
			t.Errorf("parseRate(%q) = nil error, want a failure", tc.give)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("parseRate(%q) error = %q, want it to mention %q", tc.give, err, tc.want)
		}
		// A bad flag is a usage mistake, not a runtime failure.
		if code := ExitCode(err); code != ExitUsage {
			t.Errorf("parseRate(%q) exit code = %d, want %d", tc.give, code, ExitUsage)
		}
	}
}

func TestParseRateErrorNamesTheValidGroups(t *testing.T) {
	_, err := parseRate("upload=100")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, name := range api.GroupNames() {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q should list group %q", err, name)
		}
	}
}

func TestEffectiveLimitsFlagBeatsConfig(t *testing.T) {
	g := &globals{rate: "search=30"}

	limits, err := g.effectiveLimits(map[string]int{"search": 10, "delete": 240})
	if err != nil {
		t.Fatalf("effectiveLimits: %v", err)
	}
	if limits[api.GroupSearch] != 30 {
		t.Errorf("search = %d, want the flag's 30", limits[api.GroupSearch])
	}
	// A group the flag does not mention keeps the configured value.
	if limits[api.GroupDelete] != 240 {
		t.Errorf("delete = %d, want the configured 240", limits[api.GroupDelete])
	}
}

func TestEffectiveLimitsWithoutEitherSourceIsNil(t *testing.T) {
	g := &globals{}

	limits, err := g.effectiveLimits(nil)
	if err != nil {
		t.Fatalf("effectiveLimits: %v", err)
	}
	// Nil means "use the documented defaults", which the limiter fills in.
	if limits != nil {
		t.Errorf("limits = %v, want nil", limits)
	}
}

func TestEffectiveLimitsFlagAloneWorks(t *testing.T) {
	g := &globals{rate: "240"}

	limits, err := g.effectiveLimits(nil)
	if err != nil {
		t.Fatalf("effectiveLimits: %v", err)
	}
	if limits[api.GroupDefault] != 240 {
		t.Errorf("default = %d, want 240", limits[api.GroupDefault])
	}
}

func TestUnknownLimitNames(t *testing.T) {
	got := unknownLimitNames(map[string]int{"search": 30, "uplaod": 10})
	if len(got) != 1 || got[0] != "uplaod" {
		t.Errorf("unknownLimitNames = %v, want [uplaod]", got)
	}
	if n := unknownLimitNames(map[string]int{"search": 30}); len(n) != 0 {
		t.Errorf("unknownLimitNames = %v, want none", n)
	}
}

func TestDescribeLimitsNamesEveryGroup(t *testing.T) {
	got := describeLimits(map[api.Group]int{api.GroupSearch: 30})

	for _, name := range api.GroupNames() {
		if !strings.Contains(got, name) {
			t.Errorf("describeLimits = %q, missing group %q", got, name)
		}
	}
	if !strings.Contains(got, "search=30") {
		t.Errorf("describeLimits = %q, want the override reflected", got)
	}
	// Groups the caller did not override fall back to the documented defaults.
	if !strings.Contains(got, "default=60") {
		t.Errorf("describeLimits = %q, want the default budget shown", got)
	}
}

func TestDescribeLimitsShowsDisabledShaping(t *testing.T) {
	got := describeLimits(map[api.Group]int{api.GroupDefault: 0})

	if !strings.Contains(got, "default=unlimited") {
		t.Errorf("describeLimits = %q, want a non-positive budget shown as unlimited", got)
	}
}
