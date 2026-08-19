package api

import (
	"context"
	"sync"
	"time"
)

// Group identifies a rate-limit bucket. MYBOX documents its limits per API
// family rather than per account, so each family gets its own bucket.
type Group int

const (
	// GroupDefault covers everything not called out separately: listing,
	// metadata, create, copy, move, rename, favorite, upload/download URL issuing.
	GroupDefault Group = iota
	// GroupSearch covers the two /search endpoints (10/min on the cheapest plan).
	GroupSearch
	// GroupDelete covers trashing and permanent deletion.
	GroupDelete
	// GroupRestore covers restoring from trash.
	GroupRestore
)

func (g Group) String() string {
	switch g {
	case GroupSearch:
		return "search"
	case GroupDelete:
		return "delete"
	case GroupRestore:
		return "restore"
	default:
		return "default"
	}
}

// AllGroups lists every rate-limit group, in the order they are presented to
// users. Iterating this rather than a map keeps output and errors stable.
var AllGroups = []Group{GroupDefault, GroupSearch, GroupDelete, GroupRestore}

// GroupNames lists the group names accepted in configuration and on the command
// line.
func GroupNames() []string {
	names := make([]string, 0, len(AllGroups))
	for _, g := range AllGroups {
		names = append(names, g.String())
	}
	return names
}

// GroupByName resolves a group name, reporting whether it is known.
func GroupByName(name string) (Group, bool) {
	for _, g := range AllGroups {
		if g.String() == name {
			return g, true
		}
	}
	return GroupDefault, false
}

// ResolveLimits merges an override map over DefaultLimits, so the result names
// every group. A non-positive value disables shaping for that group.
func ResolveLimits(overrides map[Group]int) map[Group]int {
	out := make(map[Group]int, len(AllGroups))
	for _, g := range AllGroups {
		out[g] = DefaultLimits[g]
	}
	for g, n := range overrides {
		out[g] = n
	}
	return out
}

// DefaultLimits is the per-minute call budget for each group.
//
// The documented limits vary by storage plan and the API gives us no way to
// discover which plan the account is on, so these are the *lowest* documented
// values. Users on larger plans can raise them via configuration.
var DefaultLimits = map[Group]int{
	GroupDefault: 60,
	GroupSearch:  10,
	GroupDelete:  60,
	GroupRestore: 180,
}

// bucket is a token bucket that starts full, so short bursts never wait and only
// sustained traffic is shaped down to the configured rate.
type bucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	perSec   float64
	last     time.Time
	now      func() time.Time // overridable in tests
}

func newBucket(perMinute int, now func() time.Time) *bucket {
	if now == nil {
		now = time.Now
	}
	c := float64(perMinute)
	return &bucket{
		capacity: c,
		tokens:   c,
		perSec:   c / 60,
		last:     now(),
		now:      now,
	}
}

// reserve returns how long the caller must wait before spending a token, and
// spends it. A zero duration means "go now".
func (b *bucket) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens = min(b.capacity, b.tokens+elapsed.Seconds()*b.perSec)
		b.last = now
	}
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	// Debt is carried in the token count, so concurrent callers queue in turn
	// rather than all waiting for the same instant.
	return time.Duration(-b.tokens / b.perSec * float64(time.Second))
}

// Limiter shapes outgoing calls to stay inside the documented per-minute budgets.
type Limiter struct {
	mu      sync.Mutex
	buckets map[Group]*bucket
	limits  map[Group]int
	now     func() time.Time
	sleep   func(context.Context, time.Duration) error
}

// NewLimiter builds a limiter. Groups missing from limits fall back to DefaultLimits.
// A non-positive limit disables shaping for that group.
func NewLimiter(limits map[Group]int) *Limiter {
	merged := make(map[Group]int, len(DefaultLimits))
	for g, n := range DefaultLimits {
		merged[g] = n
	}
	for g, n := range limits {
		merged[g] = n
	}
	return &Limiter{
		buckets: make(map[Group]*bucket, len(merged)),
		limits:  merged,
		now:     time.Now,
		sleep:   sleepCtx,
	}
}

// Wait blocks until the group has budget for one call, or ctx is done.
func (l *Limiter) Wait(ctx context.Context, g Group) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	n, ok := l.limits[g]
	if !ok {
		n = DefaultLimits[GroupDefault]
	}
	if n <= 0 {
		l.mu.Unlock()
		return nil
	}
	b := l.buckets[g]
	if b == nil {
		b = newBucket(n, l.now)
		l.buckets[g] = b
	}
	l.mu.Unlock()

	if d := b.reserve(); d > 0 {
		return l.sleep(ctx, d)
	}
	return ctx.Err()
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
