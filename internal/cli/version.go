package cli

import (
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.Printer()
			if p.JSON {
				return p.EmitJSON(map[string]string{
					"version": Version,
					"go":      runtime.Version(),
					"os":      runtime.GOOS,
					"arch":    runtime.GOARCH,
				})
			}
			p.Print("mybox %s (%s %s/%s)", Version, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
