package api

import (
	"context"
	"testing"
	"time"
)

// fakeClock lets limiter tests advance time explicitly instead of sleeping.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

// sleep advances the clock instead of blocking, mirroring what a real sleep
// would do to the bucket's refill maths.
func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
	return nil
}

func newTestLimiter(limits map[Group]int) (*Limiter, *fakeClock) {
	clock := newFakeClock()
	l := NewLimiter(limits)
	l.now = clock.Now
	l.sleep = clock.sleep
	return l, clock
}

func TestLimiterAllowsAFullBurstWithoutWaiting(t *testing.T) {
	l, clock := newTestLimiter(map[Group]int{GroupDefault: 60})

	// The bucket starts full, so ordinary short-lived commands never pay a delay.
	for i := range 60 {
		if err := l.Wait(t.Context(), GroupDefault); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept %v within the burst budget, want none", clock.slept)
	}
}

func TestLimiterThrottlesBeyondTheBurst(t *testing.T) {
	l, clock := newTestLimiter(map[Group]int{GroupDefault: 60})

	for range 60 {
		_ = l.Wait(t.Context(), GroupDefault)
	}
	if err := l.Wait(t.Context(), GroupDefault); err != nil {
		t.Fatal(err)
	}

	// 60/min refills one token per second, so the 61st call waits about a second.
	if len(clock.slept) != 1 {
		t.Fatalf("slept %v, want exactly one wait", clock.slept)
	}
	if d := clock.slept[0]; d < 900*time.Millisecond || d > 1100*time.Millisecond {
		t.Errorf("wait = %v, want ~1s", d)
	}
}

func TestLimiterGroupsAreIndependent(t *testing.T) {
	l, clock := newTestLimiter(map[Group]int{GroupDefault: 60, GroupSearch: 10})

	// Draining search must not eat into the default budget.
	for range 10 {
		_ = l.Wait(t.Context(), GroupSearch)
	}
	if len(clock.slept) != 0 {
		t.Fatalf("search burst slept %v, want none", clock.slept)
	}
	for range 60 {
		if err := l.Wait(t.Context(), GroupDefault); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.slept) != 0 {
		t.Errorf("default group slept %v after search was drained, want none", clock.slept)
	}
}

func TestLimiterSearchIsTighterThanDefault(t *testing.T) {
	l, clock := newTestLimiter(nil)

	// 11 search calls exceed the documented 10/min floor.
	for range 11 {
		_ = l.Wait(t.Context(), GroupSearch)
	}
	if len(clock.slept) != 1 {
		t.Fatalf("slept %v, want one wait after 11 search calls", clock.slept)
	}
	// 10/min refills one token every six seconds.
	if d := clock.slept[0]; d < 5*time.Second || d > 7*time.Second {
		t.Errorf("wait = %v, want ~6s", d)
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l, clock := newTestLimiter(map[Group]int{GroupDefault: 60})

	for range 60 {
		_ = l.Wait(t.Context(), GroupDefault)
	}
	clock.now = clock.now.Add(30 * time.Second) // 30 tokens back

	for i := range 30 {
		if err := l.Wait(t.Context(), GroupDefault); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept %v while refilled tokens were available, want none", clock.slept)
	}
}

func TestLimiterNonPositiveLimitDisablesShaping(t *testing.T) {
	l, clock := newTestLimiter(map[Group]int{GroupDefault: -1})

	for range 1000 {
		if err := l.Wait(t.Context(), GroupDefault); err != nil {
			t.Fatal(err)
		}
	}
	if len(clock.slept) != 0 {
		t.Errorf("slept %v with shaping disabled, want none", clock.slept)
	}
}

func TestLimiterRespectsContextCancellation(t *testing.T) {
	l, _ := newTestLimiter(map[Group]int{GroupDefault: 1})
	l.sleep = sleepCtx // use the real waiting path

	ctx, cancel := context.WithCancel(t.Context())
	if err := l.Wait(ctx, GroupDefault); err != nil { // consumes the only token
		t.Fatal(err)
	}
	cancel()
	if err := l.Wait(ctx, GroupDefault); err == nil {
		t.Error("Wait returned nil after the context was cancelled")
	}
}

func TestNilLimiterIsANoop(t *testing.T) {
	var l *Limiter
	if err := l.Wait(t.Context(), GroupDefault); err != nil {
		t.Errorf("nil limiter returned %v", err)
	}
}

func TestNewLimiterFillsGapsFromDefaults(t *testing.T) {
	l := NewLimiter(map[Group]int{GroupSearch: 30})

	if got := l.limits[GroupSearch]; got != 30 {
		t.Errorf("search limit = %d, want the override 30", got)
	}
	if got := l.limits[GroupDefault]; got != DefaultLimits[GroupDefault] {
		t.Errorf("default limit = %d, want the built-in %d", got, DefaultLimits[GroupDefault])
	}
}

func TestGroupString(t *testing.T) {
	for g, want := range map[Group]string{
		GroupDefault: "default",
		GroupSearch:  "search",
		GroupDelete:  "delete",
		GroupRestore: "restore",
	} {
		if got := g.String(); got != want {
			t.Errorf("Group(%d).String() = %q, want %q", g, got, want)
		}
	}
}

func TestGroupByName(t *testing.T) {
	for _, name := range GroupNames() {
		g, ok := GroupByName(name)
		if !ok {
			t.Errorf("GroupByName(%q) reported unknown", name)
			continue
		}
		if g.String() != name {
			t.Errorf("GroupByName(%q).String() = %q", name, g.String())
		}
	}

	if _, ok := GroupByName("upload"); ok {
		t.Error("GroupByName accepted an unknown group")
	}
	// An empty name must not fall through to the zero-value group.
	if _, ok := GroupByName(""); ok {
		t.Error("GroupByName accepted an empty name")
	}
}

func TestGroupNamesCoverEveryGroup(t *testing.T) {
	names := GroupNames()
	if len(names) != len(AllGroups) {
		t.Fatalf("GroupNames() = %v, want one per group (%d)", names, len(AllGroups))
	}
	// Every group must have a documented budget, or shaping silently defaults
	// to zero and blocks forever.
	for _, g := range AllGroups {
		if _, ok := DefaultLimits[g]; !ok {
			t.Errorf("group %s has no entry in DefaultLimits", g)
		}
	}
}

func TestResolveLimitsFillsGapsFromDefaults(t *testing.T) {
	got := ResolveLimits(map[Group]int{GroupSearch: 30})

	if len(got) != len(AllGroups) {
		t.Errorf("ResolveLimits returned %d groups, want %d", len(got), len(AllGroups))
	}
	if got[GroupSearch] != 30 {
		t.Errorf("search = %d, want the override 30", got[GroupSearch])
	}
	if got[GroupDefault] != DefaultLimits[GroupDefault] {
		t.Errorf("default = %d, want the documented %d", got[GroupDefault], DefaultLimits[GroupDefault])
	}
}

func TestResolveLimitsWithNoOverrides(t *testing.T) {
	got := ResolveLimits(nil)

	for _, g := range AllGroups {
		if got[g] != DefaultLimits[g] {
			t.Errorf("%s = %d, want %d", g, got[g], DefaultLimits[g])
		}
	}
}

func TestResolveLimitsDoesNotMutateDefaults(t *testing.T) {
	before := DefaultLimits[GroupDefault]

	got := ResolveLimits(map[Group]int{GroupDefault: 999})
	if got[GroupDefault] != 999 {
		t.Fatalf("override did not apply: %d", got[GroupDefault])
	}
	// A shared package-level map that callers can scribble on would make the
	// defaults depend on whatever ran first.
	if DefaultLimits[GroupDefault] != before {
		t.Errorf("DefaultLimits was mutated: %d, want %d", DefaultLimits[GroupDefault], before)
	}
}
