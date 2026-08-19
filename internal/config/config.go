// Package config stores CLI settings and access tokens on disk.
//
// The file lives at $MYBOX_CONFIG_HOME/config.json, falling back to
// $XDG_CONFIG_HOME/mybox/config.json and then ~/.config/mybox/config.json. It
// holds personal access tokens, so it is always written with 0600 permissions.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DefaultProfile is the profile used when none is named.
const DefaultProfile = "default"

// Environment variables recognised by this package.
const (
	EnvToken      = "MYBOX_TOKEN"
	EnvProfile    = "MYBOX_PROFILE"
	EnvConfigHome = "MYBOX_CONFIG_HOME"
	EnvAPIBase    = "MYBOX_API_BASE"
)

// Profile holds one account's settings.
type Profile struct {
	// Token is the personal access token. It is the only secret in this file.
	Token string `json:"token"`
	// BaseURL overrides the API root for this profile. Normally empty.
	BaseURL string `json:"baseUrl,omitempty"`
	// Limits overrides the per-minute call budgets, keyed by group name
	// ("default", "search", "delete", "restore"). Accounts on larger storage
	// plans get higher documented limits than the conservative built-in floors.
	Limits map[string]int `json:"limits,omitempty"`
}

// Config is the on-disk file.
type Config struct {
	DefaultProfile string             `json:"defaultProfile,omitempty"`
	Profiles       map[string]Profile `json:"profiles,omitempty"`

	// path records where this config was loaded from, so Save writes back to
	// the same place without the caller having to thread it through.
	path string
}

// Path reports the file this config was loaded from or will be saved to.
func (c *Config) Path() string { return c.path }

// DefaultPath returns the config file location for the current environment.
func DefaultPath() (string, error) {
	if home := os.Getenv(EnvConfigHome); home != "" {
		return filepath.Join(home, "config.json"), nil
	}
	dir, err := os.UserConfigDir() // honours XDG_CONFIG_HOME
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(dir, "mybox", "config.json"), nil
}

// Load reads the config file. A missing file is not an error: it yields an empty
// config bound to the path it would be created at.
func Load() (*Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads a config from an explicit path.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{path: path, Profiles: map[string]Profile{}}

	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Profiles == nil {
		cfg.Profiles = map[string]Profile{}
	}
	cfg.path = path
	return cfg, nil
}

// Save writes the config back to disk, creating the directory if needed.
//
// The file contains access tokens, so it is created 0600 and an existing file
// with looser permissions is tightened. Writing goes through a temporary file so
// an interrupted save cannot truncate a working config.
func (c *Config) Save() error {
	if c.path == "" {
		return errors.New("config has no path; load it first")
	}
	dir := filepath.Dir(c.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	raw = append(raw, '\n')

	tmp, err := os.CreateTemp(dir, ".config-*.json")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("set permissions on %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, c.path); err != nil {
		return fmt.Errorf("install %s: %w", c.path, err)
	}
	return nil
}

// ProfileNames lists the configured profiles in sorted order.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// SetProfile adds or replaces a profile.
func (c *Config) SetProfile(name string, p Profile) {
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	c.Profiles[resolveName(name)] = p
}

// RemoveProfile deletes a profile, reporting whether it existed.
func (c *Config) RemoveProfile(name string) bool {
	name = resolveName(name)
	if _, ok := c.Profiles[name]; !ok {
		return false
	}
	delete(c.Profiles, name)
	if c.DefaultProfile == name {
		c.DefaultProfile = ""
	}
	return true
}

// ActiveProfileName resolves which profile to use, preferring an explicit
// request, then MYBOX_PROFILE, then the config's default, then "default".
func (c *Config) ActiveProfileName(requested string) string {
	if requested != "" {
		return requested
	}
	if env := strings.TrimSpace(os.Getenv(EnvProfile)); env != "" {
		return env
	}
	if c.DefaultProfile != "" {
		return c.DefaultProfile
	}
	return DefaultProfile
}

// ErrNoToken is returned when no access token can be found anywhere.
var ErrNoToken = errors.New("no access token; run 'mybox auth login' or set $" + EnvToken)

// Credentials are the resolved settings for one invocation.
type Credentials struct {
	Token   string
	BaseURL string
	Limits  map[string]int
	// Profile names where the token came from, or "" when it came from a flag
	// or the environment.
	Profile string
	// Source describes the token's origin for diagnostics: "flag", "env" or "config".
	Source string
}

// Resolve determines which token and settings to use.
//
// Precedence for the token is flag, then MYBOX_TOKEN, then the active profile.
// The base URL follows the same order with MYBOX_API_BASE in the middle, so a
// token supplied by flag still picks up a profile's custom endpoint only when no
// override is present.
func (c *Config) Resolve(flagToken, flagProfile string) (Credentials, error) {
	name := c.ActiveProfileName(flagProfile)
	prof, hasProfile := c.Profiles[name]

	cred := Credentials{BaseURL: prof.BaseURL, Limits: prof.Limits}
	if hasProfile {
		cred.Profile = name
	}

	switch {
	case flagToken != "":
		cred.Token, cred.Source = flagToken, "flag"
	case os.Getenv(EnvToken) != "":
		cred.Token, cred.Source = os.Getenv(EnvToken), "env"
	case prof.Token != "":
		cred.Token, cred.Source = prof.Token, "config"
	default:
		// An explicitly named profile that does not exist is a mistake worth
		// reporting precisely, rather than a generic "no token".
		if flagProfile != "" && !hasProfile {
			return Credentials{}, fmt.Errorf("no profile named %q in %s", flagProfile, c.path)
		}
		return Credentials{}, ErrNoToken
	}

	cred.BaseURL = ResolveBaseURL(cred.BaseURL)
	return cred, nil
}

// ResolveBaseURL applies the MYBOX_API_BASE override to a profile's base URL,
// falling back to the profile's own value and then to the client default.
//
// It is separate from Resolve because a command can need the endpoint before it
// has a token to resolve: auth login builds a client to verify the token the
// user just typed. Without this it would ignore the override and talk to
// production, which makes it both untestable and wrong for anyone pointing at a
// different endpoint.
func ResolveBaseURL(profileBaseURL string) string {
	if base := strings.TrimSpace(os.Getenv(EnvAPIBase)); base != "" {
		return base
	}
	return profileBaseURL
}

func resolveName(name string) string {
	if name == "" {
		return DefaultProfile
	}
	return name
}
