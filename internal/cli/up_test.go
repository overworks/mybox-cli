package cli

import (
	"strings"
	"testing"
	"time"
)

func TestUploadModifiedTimeIsAlwaysKST(t *testing.T) {
	// One instant, spelled in four zones. MYBOX matches the string literally and
	// only recognises the KST form, so every one of these must come out the same.
	instant := time.Date(2026, 1, 1, 18, 4, 5, 0, time.UTC)
	want := "2026-01-02T03:04:05+09:00"

	for _, zone := range []*time.Location{
		time.UTC,
		time.FixedZone("KST", 9*60*60),
		time.FixedZone("PST", -8*60*60),
		time.FixedZone("IST", 5*60*60+30*60),
	} {
		if got := uploadModifiedTime(instant.In(zone)); got != want {
			t.Errorf("uploadModifiedTime(%s) = %q, want %q", zone, got, want)
		}
	}
}

func TestUploadModifiedTimeEndsWithTheKSTOffset(t *testing.T) {
	// The failure this guards against is silent: a +00:00 spelling makes MYBOX
	// treat the reservation as a different file and restart the upload from
	// zero, with no error to notice.
	for _, at := range []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 12, 31, 15, 0, 0, 0, time.UTC),
	} {
		got := uploadModifiedTime(at)
		if !strings.HasSuffix(got, "+09:00") {
			t.Errorf("uploadModifiedTime(%s) = %q, want it to end with +09:00", at, got)
		}
	}
}

func TestUploadModifiedTimeCrossesTheDateLine(t *testing.T) {
	// 18:04 UTC is the next day in Seoul; getting this wrong would send a
	// timestamp MYBOX has never seen.
	got := uploadModifiedTime(time.Date(2026, 1, 1, 18, 4, 5, 0, time.UTC))
	if !strings.HasPrefix(got, "2026-01-02T") {
		t.Errorf("uploadModifiedTime = %q, want it to land on 2026-01-02", got)
	}
}
