package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tempConfig points the package at an isolated config file for one test.
func tempConfig(t *testing.T) string {
	t.Helper()
	// Point at a directory that does not exist yet, mirroring a first run where
	// the CLI has to create ~/.config/mybox itself.
	dir := filepath.Join(t.TempDir(), "mybox")
	t.Setenv(EnvConfigHome, dir)
	// Clear ambient credentials so a developer's real environment cannot change
	// what these tests observe.
	t.Setenv(EnvToken, "")
	t.Setenv(EnvProfile, "")
	t.Setenv(EnvAPIBase, "")
	return filepath.Join(dir, "config.json")
}

func TestLoadMissingFileYieldsEmptyConfig(t *testing.T) {
	path := tempConfig(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Path() != path {
		t.Errorf("Path() = %q, want %q", cfg.Path(), path)
	}
	if len(cfg.Profiles) != 0 {
		t.Errorf("Profiles = %v, want empty", cfg.Profiles)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	tempConfig(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.SetProfile("work", Profile{Token: "mbx_pat_work", Limits: map[string]int{"search": 30}})
	cfg.SetProfile(DefaultProfile, Profile{Token: "mbx_pat_personal"})
	cfg.DefaultProfile = "work"
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.DefaultProfile != "work" {
		t.Errorf("DefaultProfile = %q, want work", got.DefaultProfile)
	}
	if got.Profiles["work"].Token != "mbx_pat_work" {
		t.Errorf("work token = %q", got.Profiles["work"].Token)
	}
	if got.Profiles["work"].Limits["search"] != 30 {
		t.Errorf("work limits = %v", got.Profiles["work"].Limits)
	}
	if names := got.ProfileNames(); strings.Join(names, ",") != "default,work" {
		t.Errorf("ProfileNames() = %v, want sorted [default work]", names)
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	path := tempConfig(t)

	cfg, _ := Load()
	cfg.SetProfile(DefaultProfile, Profile{Token: "mbx_pat_secret"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The file holds a bearer token; anything group- or world-readable is a leak.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %04o, want 0600", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("directory permissions = %04o, want no group/world access", perm)
	}
}

func TestSaveLeavesNoTempFilesBehind(t *testing.T) {
	path := tempConfig(t)

	cfg, _ := Load()
	cfg.SetProfile(DefaultProfile, Profile{Token: "t"})
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".config-") {
			t.Errorf("left a temp file behind: %s", e.Name())
		}
	}
}

func TestSaveOverwritesExistingConfig(t *testing.T) {
	tempConfig(t)

	first, _ := Load()
	first.SetProfile(DefaultProfile, Profile{Token: "old"})
	if err := first.Save(); err != nil {
		t.Fatalf("first Save: %v", err)
	}

	second, _ := Load()
	second.SetProfile(DefaultProfile, Profile{Token: "new"})
	if err := second.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	got, _ := Load()
	if got.Profiles[DefaultProfile].Token != "new" {
		t.Errorf("token = %q, want new", got.Profiles[DefaultProfile].Token)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	path := tempConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("want an error for a malformed config")
	}
}

func TestSetProfileNormalisesEmptyName(t *testing.T) {
	tempConfig(t)

	cfg, _ := Load()
	cfg.SetProfile("", Profile{Token: "t"})
	if _, ok := cfg.Profiles[DefaultProfile]; !ok {
		t.Errorf("empty name did not land on %q: %v", DefaultProfile, cfg.Profiles)
	}
}

func TestRemoveProfile(t *testing.T) {
	tempConfig(t)

	cfg, _ := Load()
	cfg.SetProfile("work", Profile{Token: "t"})
	cfg.DefaultProfile = "work"

	if !cfg.RemoveProfile("work") {
		t.Error("RemoveProfile returned false for an existing profile")
	}
	if cfg.DefaultProfile != "" {
		t.Errorf("DefaultProfile = %q, want it cleared with the profile", cfg.DefaultProfile)
	}
	if cfg.RemoveProfile("work") {
		t.Error("RemoveProfile returned true for an already-removed profile")
	}
}

func TestActiveProfileNamePrecedence(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()

	if got := cfg.ActiveProfileName(""); got != DefaultProfile {
		t.Errorf("with nothing set: %q, want %q", got, DefaultProfile)
	}

	cfg.DefaultProfile = "configured"
	if got := cfg.ActiveProfileName(""); got != "configured" {
		t.Errorf("with a configured default: %q", got)
	}

	t.Setenv(EnvProfile, "fromenv")
	if got := cfg.ActiveProfileName(""); got != "fromenv" {
		t.Errorf("env should beat the configured default, got %q", got)
	}

	if got := cfg.ActiveProfileName("explicit"); got != "explicit" {
		t.Errorf("an explicit request should beat everything, got %q", got)
	}
}

func TestResolveTokenPrecedence(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()
	cfg.SetProfile(DefaultProfile, Profile{Token: "from-config"})

	cred, err := cfg.Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.Token != "from-config" || cred.Source != "config" {
		t.Errorf("cred = %+v, want the config token", cred)
	}

	t.Setenv(EnvToken, "from-env")
	if cred, _ = cfg.Resolve("", ""); cred.Token != "from-env" || cred.Source != "env" {
		t.Errorf("cred = %+v, want the env token to win over config", cred)
	}

	if cred, _ = cfg.Resolve("from-flag", ""); cred.Token != "from-flag" || cred.Source != "flag" {
		t.Errorf("cred = %+v, want the flag token to win over env", cred)
	}
}

func TestResolveWithoutAnyTokenFails(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()

	_, err := cfg.Resolve("", "")
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("error = %v, want ErrNoToken", err)
	}
	if !strings.Contains(err.Error(), "mybox auth login") {
		t.Errorf("error %q should tell the user how to fix it", err)
	}
}

func TestResolveReportsMissingNamedProfile(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()
	cfg.SetProfile(DefaultProfile, Profile{Token: "t"})

	// Asking for a profile that does not exist is a typo, not "please log in".
	_, err := cfg.Resolve("", "nope")
	if err == nil || !strings.Contains(err.Error(), `no profile named "nope"`) {
		t.Fatalf("error = %v, want a missing-profile message", err)
	}
}

func TestResolveCarriesProfileSettings(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()
	cfg.SetProfile("work", Profile{
		Token:   "t",
		BaseURL: "https://staging.example/v1",
		Limits:  map[string]int{"search": 30},
	})

	cred, err := cfg.Resolve("", "work")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL != "https://staging.example/v1" {
		t.Errorf("BaseURL = %q", cred.BaseURL)
	}
	if cred.Limits["search"] != 30 {
		t.Errorf("Limits = %v", cred.Limits)
	}
	if cred.Profile != "work" {
		t.Errorf("Profile = %q, want work", cred.Profile)
	}
}

func TestResolveEnvBaseURLWinsOverProfile(t *testing.T) {
	tempConfig(t)
	cfg, _ := Load()
	cfg.SetProfile(DefaultProfile, Profile{Token: "t", BaseURL: "https://profile.example/v1"})
	t.Setenv(EnvAPIBase, "https://env.example/v1")

	cred, err := cfg.Resolve("", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cred.BaseURL != "https://env.example/v1" {
		t.Errorf("BaseURL = %q, want the environment override", cred.BaseURL)
	}
}

func TestRedactKeepsTheTokenUnusable(t *testing.T) {
	for _, tc := range []struct{ give, want string }{
		{"", ""},
		{"abc", "****"},
		{"abcd", "****"},
		{"abcdefghij", "****ghij"},
		{"mbx_pat_1234567890abcdef", "mbx_pat_****cdef"},
		{"mbx_pat_ab", "mbx_pat_****"},
	} {
		if got := Redact(tc.give); got != tc.want {
			t.Errorf("Redact(%q) = %q, want %q", tc.give, got, tc.want)
		}
	}

	const secret = "mbx_pat_supersecretvalue"
	if got := Redact(secret); strings.Contains(got, "supersecret") {
		t.Errorf("Redact(%q) = %q, which still exposes the secret", secret, got)
	}
}

func TestFingerprintIsStableAndDistinct(t *testing.T) {
	a := Fingerprint("mbx_pat_a")
	b := Fingerprint("mbx_pat_b")

	if a != Fingerprint("mbx_pat_a") {
		t.Error("Fingerprint is not stable across calls")
	}
	if a == b {
		t.Error("different tokens produced the same fingerprint")
	}
	if len(a) != 16 {
		t.Errorf("fingerprint %q has length %d, want 16 hex chars", a, len(a))
	}
	if strings.Contains(a, "mbx_pat") {
		t.Errorf("fingerprint %q leaks the token", a)
	}
}
