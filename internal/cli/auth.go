package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/config"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newAuthCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage personal access tokens",
		Long: `Create a token from MYBOX web > 설정 > 계정 및 개인 액세스 토큰 관리,
then register it with 'mybox auth login'.

A token is shown once at creation and expires after the validity period you
chose (30, 60, 90 or 180 days). The config file is written with owner-only
permissions (0600).`,
	}
	cmd.AddCommand(
		newAuthLoginCommand(g),
		newAuthStatusCommand(g),
		newAuthLogoutCommand(g),
		newAuthListCommand(g),
	)
	return cmd
}

func newAuthLoginCommand(g *globals) *cobra.Command {
	var makeDefault bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Register a personal access token",
		Long: `Reads a personal access token and saves it to the config file.

On a terminal the input is not echoed. It can also be piped in:
  echo "$TOKEN" | mybox auth login`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			cfg, err := g.Config()
			if err != nil {
				return err
			}
			name := cfg.ActiveProfileName(g.profile)

			token := strings.TrimSpace(g.token)
			if token == "" {
				if token, err = readToken(g); err != nil {
					return err
				}
			}
			if token == "" {
				return usagef("the token is empty")
			}

			// Verify before saving, so a typo is caught now rather than on the
			// next command.
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			existing := cfg.Profiles[name]
			limits, err := g.effectiveLimits(existing.Limits)
			if err != nil {
				return err
			}
			// Surface a typo now, while the user is still looking at this
			// profile, rather than on some later command.
			g.warnUnknownLimits(existing.Limits)
			probe, err := api.New(api.Options{
				Token:     token,
				BaseURL:   config.ResolveBaseURL(existing.BaseURL),
				UserAgent: "mybox-cli/" + Version,
				Limits:    limits,
			})
			if err != nil {
				return err
			}
			st, err := probe.GetStorage(ctx)
			if err != nil {
				var apiErr *api.Error
				if errors.As(err, &apiErr) && apiErr.IsUnauthorized() {
					return fmt.Errorf("the token was rejected: %w", err)
				}
				return fmt.Errorf("could not verify the token: %w", err)
			}

			existing.Token = token
			cfg.SetProfile(name, existing)
			if makeDefault || cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}
			if err := cfg.Save(); err != nil {
				return err
			}

			p.Info("saved the token to profile %q (%s)", name, cfg.Path())
			p.Print("signed in · %s of %s used (%s)",
				bytesOf(st.UsedBytes), bytesOf(st.QuotaBytes), percentOf(st.UsedBytes, st.QuotaBytes))
			return nil
		},
	}
	cmd.Flags().BoolVar(&makeDefault, "set-default", false, "make this the default profile")
	return cmd
}

// readToken reads the token without echoing it when stdin is a terminal, and
// reads a single line when it is piped.
func readToken(g *globals) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(g.stderr, "Personal access token: ")
		raw, err := term.ReadPassword(fd)
		fmt.Fprintln(g.stderr)
		if err != nil {
			return "", fmt.Errorf("could not read the token: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("could not read a token from standard input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

func newAuthStatusCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check whether the token works",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			st, err := client.GetStorage(ctx)
			if err != nil {
				return err
			}

			limits, err := g.effectiveLimits(g.cred.Limits)
			if err != nil {
				return err
			}

			if p.JSON {
				resolved := map[string]int{}
				for group, n := range api.ResolveLimits(limits) {
					resolved[group.String()] = n
				}
				return p.EmitJSON(map[string]any{
					"valid":       true,
					"profile":     g.cred.Profile,
					"tokenSource": g.cred.Source,
					"token":       config.Redact(g.cred.Token),
					"quotaBytes":  st.QuotaBytes,
					"usedBytes":   st.UsedBytes,
					"rateLimits":  resolved,
				})
			}

			p.Print("Status      valid")
			p.Print("Token       %s (%s)", config.Redact(g.cred.Token), sourceLabel(g.cred.Source))
			if g.cred.Profile != "" {
				p.Print("Profile     %s", g.cred.Profile)
			}
			p.Print("Storage     %s of %s (%s)",
				bytesOf(st.UsedBytes), bytesOf(st.QuotaBytes), percentOf(st.UsedBytes, st.QuotaBytes))
			// The client shapes calls to the lowest documented allowance unless
			// told otherwise, so show what is actually in effect.
			p.Print("Rate limit  %s (per minute)", describeLimits(limits))
			return nil
		},
	}
}

func newAuthLogoutCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove a stored token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			cfg, err := g.Config()
			if err != nil {
				return err
			}
			name := cfg.ActiveProfileName(g.profile)
			if !cfg.RemoveProfile(name) {
				return fmt.Errorf("no profile named %q", name)
			}
			if err := cfg.Save(); err != nil {
				return err
			}
			p.Info("removed profile %q", name)

			// A token in the environment survives logout, and the user would
			// otherwise be confused when the next command still works.
			if os.Getenv(config.EnvToken) != "" {
				p.Warn("$%s is set, so commands remain authenticated", config.EnvToken)
			}
			return nil
		},
	}
}

func newAuthListCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List stored profiles",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			cfg, err := g.Config()
			if err != nil {
				return err
			}
			names := cfg.ProfileNames()

			if p.JSON {
				list := make([]map[string]any, 0, len(names))
				for _, name := range names {
					list = append(list, map[string]any{
						"name":      name,
						"token":     config.Redact(cfg.Profiles[name].Token),
						"isDefault": name == cfg.DefaultProfile,
					})
				}
				return p.EmitJSON(map[string]any{"path": cfg.Path(), "profiles": list})
			}

			if len(names) == 0 {
				p.Info("no profiles stored; run 'mybox auth login'")
				return nil
			}
			tw := p.Table()
			fmt.Fprintln(tw, "\tNAME\tTOKEN")
			for _, name := range names {
				marker := " "
				if name == cfg.DefaultProfile {
					marker = "*"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", marker, name, config.Redact(cfg.Profiles[name].Token))
			}
			return tw.Flush()
		},
	}
}

func sourceLabel(source string) string {
	switch source {
	case "flag":
		return "--token"
	case "env":
		return "$" + config.EnvToken
	default:
		return "config file"
	}
}
