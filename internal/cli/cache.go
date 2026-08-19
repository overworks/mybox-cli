package cli

import (
	"fmt"

	"github.com/overworks/mybox-cli/internal/config"
	"github.com/overworks/mybox-cli/internal/resolve"
	"github.com/spf13/cobra"
)

func newCacheCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the path resolution cache",
		Long: `The MYBOX API has no path lookup, so mybox resolves a path by listing the
root and then each folder in turn, and caches the result per account.

The cache exists to conserve the call budget — most APIs allow 60 a minute —
rather than to save time. Entries expire after ` + fmt.Sprint(int(resolve.DefaultTTL.Hours())) + ` hours.`,
	}
	cmd.AddCommand(newCacheInfoCommand(g), newCacheClearCommand(g))
	return cmd
}

func newCacheInfoCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "info",
		Short: "Show where the cache lives and how much it holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			cache, err := g.pathCache()
			if err != nil {
				return err
			}
			if p.JSON {
				return p.EmitJSON(map[string]any{
					"path":     cache.Path(),
					"entries":  cache.Len(),
					"ttlHours": int(resolve.DefaultTTL.Hours()),
					"enabled":  cache.Path() != "",
				})
			}
			if cache.Path() == "" {
				p.Print("caching is disabled")
				return nil
			}
			tw := p.Table()
			fmt.Fprintf(tw, "Location\t%s\n", cache.Path())
			fmt.Fprintf(tw, "Entries\t%d\n", cache.Len())
			fmt.Fprintf(tw, "Expires after\t%d hours\n", int(resolve.DefaultTTL.Hours()))
			return tw.Flush()
		},
	}
}

func newCacheClearCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Empty the cache",
		Long: `Use this when mybox still remembers where something used to be after you
moved it from the web UI or an app. Emptying the cache destroys no data; it
only makes the next command resolve paths again.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			cache, err := g.pathCache()
			if err != nil {
				return err
			}
			n := cache.Len()
			cache.Clear()
			if err := cache.Save(); err != nil {
				return err
			}
			p.Info("cleared %d cache entries", n)
			return nil
		},
	}
}

// pathCache loads the cache for the active account without building a resolver.
func (g *globals) pathCache() (*resolve.Cache, error) {
	cfg, err := g.Config()
	if err != nil {
		return nil, err
	}
	cred, err := cfg.Resolve(g.token, g.profile)
	if err != nil {
		return nil, err
	}
	if g.noCache {
		return resolve.NewDisabledCache(), nil
	}
	return resolve.LoadCache(config.Fingerprint(cred.Token), resolve.DefaultTTL), nil
}
