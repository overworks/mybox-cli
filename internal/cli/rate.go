package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/overworks/mybox-cli/internal/api"
)

// rateFlagUsage is the --rate help text. It is a variable so the group names
// stay in step with the api package rather than being retyped here.
var rateFlagUsage = "override the per-minute call budgets (e.g. 240 or 240,search=30); groups: " +
	strings.Join(api.GroupNames(), ", ")

// parseRate parses the --rate flag.
//
// The value is a comma-separated list where a bare number sets the budget for
// every group and "name=number" overrides one of them, applied left to right.
// A user on a 180GB-or-larger plan therefore writes "240,search=30": the
// documented per-API allowance is 240 a minute, but search stays at 30.
//
// A non-positive number turns client-side shaping off for the groups it covers,
// leaving the service's own limits to push back with 429s.
func parseRate(spec string) (map[api.Group]int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}

	out := map[api.Group]int{}
	for _, field := range strings.Split(spec, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}

		name, value, hasName := strings.Cut(field, "=")
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		if !hasName {
			name, value = "", name
		}

		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, usagef("--rate: %q is not a number", value)
		}

		if !hasName {
			// A bare number is a baseline for everything; a later "name=n"
			// still overrides it.
			for _, g := range api.AllGroups {
				out[g] = n
			}
			continue
		}

		g, ok := api.GroupByName(name)
		if !ok {
			return nil, usagef("--rate: unknown group %q; valid values are %s",
				name, strings.Join(api.GroupNames(), ", "))
		}
		out[g] = n
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// effectiveLimits combines the profile's configured limits with --rate, with the
// flag winning, and fills the rest in from the documented defaults.
func (g *globals) effectiveLimits(configured map[string]int) (map[api.Group]int, error) {
	merged := parseConfigLimits(configured)

	fromFlag, err := parseRate(g.rate)
	if err != nil {
		return nil, err
	}
	if merged == nil && fromFlag != nil {
		merged = map[api.Group]int{}
	}
	for group, n := range fromFlag {
		merged[group] = n
	}
	return merged, nil
}

// parseConfigLimits converts a profile's group names into api.Group keys.
//
// An unknown name is reported rather than ignored: a typo in a config file that
// silently leaves the conservative default in place is the kind of thing a user
// only discovers by wondering why nothing got faster.
func parseConfigLimits(m map[string]int) map[api.Group]int {
	if len(m) == 0 {
		return nil
	}
	out := make(map[api.Group]int, len(m))
	for name, n := range m {
		if group, ok := api.GroupByName(name); ok {
			out[group] = n
		}
	}
	return out
}

// unknownLimitNames lists configured group names the client does not recognise.
func unknownLimitNames(m map[string]int) []string {
	var unknown []string
	for name := range m {
		if _, ok := api.GroupByName(name); !ok {
			unknown = append(unknown, name)
		}
	}
	return unknown
}

// describeLimits renders the effective per-minute budgets for display.
func describeLimits(limits map[api.Group]int) string {
	resolved := api.ResolveLimits(limits)
	parts := make([]string, 0, len(api.AllGroups))
	for _, g := range api.AllGroups {
		n := resolved[g]
		if n <= 0 {
			parts = append(parts, fmt.Sprintf("%s=unlimited", g))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%d", g, n))
	}
	return strings.Join(parts, ", ")
}
