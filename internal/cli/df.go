package cli

import (
	"fmt"

	"github.com/overworks/mybox-cli/internal/output"
	"github.com/spf13/cobra"
)

func newDfCommand(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:     "df",
		Aliases: []string{"storage"},
		Short:   "Show quota and per-category file counts",
		Long: `Reports total and used capacity, file counts by kind, the largest file
that can be uploaded, and the trash auto-delete interval.

Total capacity includes what has been shared out to other users and to mail;
used capacity likewise includes shared-out usage.`,
		Args: cobra.NoArgs,
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
			if p.JSON {
				return p.EmitJSON(st)
			}

			free := st.QuotaBytes - st.UsedBytes
			if free < 0 {
				free = 0
			}

			tw := p.Table()
			fmt.Fprintf(tw, "Used\t%s\t%s %s\n",
				output.Bytes(st.UsedBytes),
				output.Bar(st.UsedBytes, st.QuotaBytes, 20),
				output.Percent(st.UsedBytes, st.QuotaBytes))
			fmt.Fprintf(tw, "Free\t%s\t\n", output.Bytes(free))
			fmt.Fprintf(tw, "Total\t%s\t\n", output.Bytes(st.QuotaBytes))
			fmt.Fprintf(tw, "Max upload\t%s\t\n", output.Bytes(st.MaxFileBytes))
			fmt.Fprintf(tw, "Trash auto-delete\t%s\t\n", trashIntervalLabel(st.TrashAutoDeleteDays))
			if err := tw.Flush(); err != nil {
				return err
			}

			fc := st.FileCounts
			fmt.Fprintln(g.stdout)
			ct := p.Table()
			fmt.Fprintf(ct, "%d files\tdocument %d\timage %d\tvideo %d\n", fc.Total, fc.Document, fc.Image, fc.Video)
			fmt.Fprintf(ct, "\taudio %d\tarchive %d\texecutable %d\n", fc.Audio, fc.Archive, fc.Executable)
			fmt.Fprintf(ct, "\tother %d\t\t\n", fc.Etc)
			return ct.Flush()
		},
	}
}

// trashIntervalLabel renders the auto-delete interval, where 0 means "off".
func trashIntervalLabel(days int) string {
	if days <= 0 {
		return "off"
	}
	return fmt.Sprintf("%d days", days)
}

// bytesOf and percentOf keep the auth command's summary line short.
func bytesOf(n int64) string             { return output.Bytes(n) }
func percentOf(used, total int64) string { return output.Percent(used, total) }
