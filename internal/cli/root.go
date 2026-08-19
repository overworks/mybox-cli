// Package cli defines the mybox command tree.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/config"
	"github.com/overworks/mybox-cli/internal/output"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/spf13/cobra"
)

// Version is overridden at build time with -ldflags "-X ...cli.Version=v1.2.3".
var Version = "dev"

// globals holds the flags every command shares, plus the lazily built client.
type globals struct {
	token   string
	profile string
	json    bool
	quiet   bool
	verbose bool
	noCache bool
	rate    string
	timeout time.Duration

	stdout io.Writer
	stderr io.Writer

	// cfg and client are resolved on first use so that commands which need
	// neither (version, completion, help) never touch the config file.
	cfg      *config.Config
	client   *api.Client
	cred     config.Credentials
	resolver *resolve.Resolver
}

// Printer builds an output printer from the global flags.
func (g *globals) Printer() *output.Printer {
	return &output.Printer{Out: g.stdout, Err: g.stderr, JSON: g.json, Quiet: g.quiet}
}

// Config loads the config file once per invocation.
func (g *globals) Config() (*config.Config, error) {
	if g.cfg == nil {
		cfg, err := config.Load()
		if err != nil {
			return nil, err
		}
		g.cfg = cfg
	}
	return g.cfg, nil
}

// Client builds an authenticated API client, resolving credentials on first use.
func (g *globals) Client() (*api.Client, error) {
	if g.client != nil {
		return g.client, nil
	}
	cfg, err := g.Config()
	if err != nil {
		return nil, err
	}
	cred, err := cfg.Resolve(g.token, g.profile)
	if err != nil {
		return nil, err
	}
	g.cred = cred

	limits, err := g.effectiveLimits(cred.Limits)
	if err != nil {
		return nil, err
	}
	g.warnUnknownLimits(cred.Limits)

	opts := api.Options{
		Token:     cred.Token,
		BaseURL:   cred.BaseURL,
		UserAgent: "mybox-cli/" + Version,
		Limits:    limits,
	}
	if g.verbose {
		// The client redacts the token itself; nothing secret reaches this hook.
		opts.Trace = func(s string) { fmt.Fprintln(g.stderr, "[http] "+s) }
	}

	c, err := api.New(opts)
	if err != nil {
		return nil, err
	}
	g.client = c
	return c, nil
}

// Resolver builds the path resolver, wiring in the per-account path cache
// unless --no-cache was given.
func (g *globals) Resolver() (*resolve.Resolver, error) {
	if g.resolver != nil {
		return g.resolver, nil
	}
	client, err := g.Client()
	if err != nil {
		return nil, err
	}
	cache := resolve.NewDisabledCache()
	if !g.noCache {
		cache = resolve.LoadCache(config.Fingerprint(g.cred.Token), resolve.DefaultTTL)
	}
	g.resolver = resolve.New(client, cache)
	return g.resolver, nil
}

// SaveCache persists anything the resolver learned. A failure here only costs
// API calls next time, so it is reported as a warning rather than an error.
func (g *globals) SaveCache() {
	if g.resolver == nil {
		return
	}
	if err := g.resolver.Cache().Save(); err != nil {
		g.Printer().Warn("could not save the path cache: %v", err)
	}
}

// invalidateAllPaths drops the whole path cache. Some operations (restoring from
// the trash) change the tree in ways the CLI cannot pinpoint, and a stale hit
// would be worse than a cold cache.
func (g *globals) invalidateAllPaths() {
	if g.resolver != nil {
		g.resolver.Cache().Clear()
		g.SaveCache()
		return
	}
	cache, err := g.pathCache()
	if err != nil {
		return
	}
	cache.Clear()
	_ = cache.Save()
}

// Context returns a context carrying the --timeout budget.
func (g *globals) Context(parent context.Context) (context.Context, context.CancelFunc) {
	if g.timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, g.timeout)
}

// NewRootCommand builds the full command tree.
func NewRootCommand(stdout, stderr io.Writer) *cobra.Command {
	g := &globals{stdout: stdout, stderr: stderr}

	root := &cobra.Command{
		Use:   "mybox",
		Short: "Work with Naver MYBOX from the command line",
		Long: `mybox is a command-line client for the Naver MYBOX Open API.

Files and folders are named by path (for example /문서/2026/report.pdf).
The MYBOX API has no path lookup, so mybox resolves a path by listing the
root, then each folder in turn, and caches what it learns. If you already
have a resource ID, the 'id:' prefix skips resolution entirely
(for example id:hV3sQ9pLzR2m).

To get started, create a token from MYBOX web > 설정 > 계정 및 개인 액세스
토큰 관리, then run 'mybox auth login'.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		// Global flags are validated before any command runs, so a malformed
		// --rate is reported as the flag mistake it is rather than surfacing as
		// whatever the command happened to need first (a missing token, say).
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			_, err := parseRate(g.rate)
			return err
		},
		// A command with no RunE makes cobra print help and succeed, even for a
		// typo'd subcommand. Handle the bare and unknown cases explicitly so a
		// mistyped command fails loudly.
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
				return usagef("unknown command %q; did you mean %q?", args[0], suggestions[0])
			}
			return usagef("unknown command %q; run 'mybox --help' to see what is available", args[0])
		},
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	pf := root.PersistentFlags()
	pf.StringVar(&g.token, "token", "", "personal access token; beats both the config file and $MYBOX_TOKEN")
	pf.StringVar(&g.profile, "profile", "", "profile to use")
	pf.BoolVar(&g.json, "json", false, "emit results as JSON")
	pf.BoolVarP(&g.quiet, "quiet", "q", false, "suppress incidental messages")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "log HTTP requests to stderr; the token is masked")
	pf.BoolVar(&g.noCache, "no-cache", false, "bypass the path resolution cache")
	pf.StringVar(&g.rate, "rate", "", rateFlagUsage)
	pf.DurationVar(&g.timeout, "timeout", 0, "time budget for the whole command (e.g. 30s, 2m)")

	root.AddCommand(
		newVersionCommand(g),
		newAuthCommand(g),
		newDfCommand(g),
		newLsCommand(g),
		newStatCommand(g),
		newSearchCommand(g),
		newTrashCommand(g),
		newMkdirCommand(g),
		newCpCommand(g),
		newMvCommand(g),
		newRenameCommand(g),
		newRmCommand(g),
		newStarCommand(g, true),
		newStarCommand(g, false),
		newUpCommand(g),
		newDownCommand(g),
		newCacheCommand(g),
		newDebugCommand(g),
	)
	return root
}

// Exit codes. They let scripts branch on why a command failed without parsing
// the message.
const (
	ExitOK          = 0
	ExitError       = 1
	ExitUsage       = 2
	ExitAuth        = 3
	ExitNotFound    = 4
	ExitRateLimited = 5
	ExitOutOfSpace  = 6
	ExitInterrupted = 130
)

// ExitCode maps an error to the process exit status.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	switch {
	case errors.Is(err, context.Canceled):
		return ExitInterrupted
	case errors.Is(err, config.ErrNoToken):
		return ExitAuth
	}

	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.IsUnauthorized(), apiErr.IsForbidden():
			return ExitAuth
		case apiErr.IsNotFound():
			return ExitNotFound
		case apiErr.IsRateLimited():
			return ExitRateLimited
		case apiErr.IsOutOfSpace():
			return ExitOutOfSpace
		}
	}

	var notFound *resolve.NotFoundError
	if errors.As(err, &notFound) {
		return ExitNotFound
	}

	var usageErr *UsageError
	if errors.As(err, &usageErr) {
		return ExitUsage
	}
	return ExitError
}

// UsageError marks a mistake in how the command was invoked, as opposed to a
// failure while doing the work.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usagef(format string, args ...any) error {
	return &UsageError{Err: fmt.Errorf(format, args...)}
}

// Execute runs the CLI and returns the process exit code. It prints the error
// itself so main stays a one-liner.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := NewRootCommand(stdout, stderr)
	root.SetArgs(args)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	if !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "mybox: "+err.Error())
	}
	if hint := hintFor(err); hint != "" {
		fmt.Fprintln(stderr, "  "+hint)
	}
	return ExitCode(err)
}

// hintFor turns the API's terse error codes into an actionable next step.
func hintFor(err error) string {
	if errors.Is(err, config.ErrNoToken) {
		return ""
	}
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return ""
	}
	switch {
	case apiErr.IsUnauthorized():
		return "the token is invalid or has expired; run 'mybox auth login' to replace it"
	case apiErr.IsForbidden():
		return "this account cannot reach that resource; password-protected and shared folders are not served by the Open API"
	case apiErr.IsRateLimited():
		return "call budget exhausted; try again shortly"
	case apiErr.IsConflict():
		return "something of that name already exists; pass --overwrite or choose another name"
	case apiErr.IsOutOfSpace():
		return "the MYBOX account is out of storage"
	}
	return ""
}
