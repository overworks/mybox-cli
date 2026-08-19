package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/overworks/mybox-cli/internal/api"
	"github.com/overworks/mybox-cli/internal/output"
	"github.com/spf13/cobra"
)

// searchFlags are shared by the file and folder search subcommands.
type searchFlags struct {
	in        string
	since     string
	until     string
	dateField string
	limit     int
}

func (f *searchFlags) register(cmd *cobra.Command) {
	fl := cmd.Flags()
	fl.StringVar(&f.in, "in", "", "restrict the search to this folder's subtree")
	fl.StringVar(&f.since, "since", "", "start date (YYYY-MM-DD or RFC 3339)")
	fl.StringVar(&f.until, "until", "", "end date (YYYY-MM-DD or RFC 3339)")
	fl.StringVar(&f.dateField, "date-field", "", "which timestamp the dates apply to: created (default) or modified")
	fl.IntVarP(&f.limit, "limit", "n", 0, "stop after this many results; 0 means all")
}

// dates normalises the two date bounds.
func (f *searchFlags) dates() (start, end string, err error) {
	if start, err = normaliseDate(f.since, false); err != nil {
		return "", "", usagef("--since: %v", err)
	}
	if end, err = normaliseDate(f.until, true); err != nil {
		return "", "", usagef("--until: %v", err)
	}
	return start, end, nil
}

// normaliseDate accepts a bare date or a full RFC3339 timestamp and returns the
// RFC3339 form the API expects.
//
// A bare date is interpreted in KST, the zone MYBOX reports its timestamps in,
// so "--since 2026-01-01" means the same thing to the user and to the server
// regardless of where the CLI is running. endOfDay makes an upper bound
// inclusive of the whole day.
func normaliseDate(s string, endOfDay bool) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(time.RFC3339), nil
	}
	kst := time.FixedZone("KST", 9*60*60)
	t, err := time.ParseInLocation("2006-01-02", s, kst)
	if err != nil {
		return "", fmt.Errorf("%q is not a date (want YYYY-MM-DD or RFC 3339)", s)
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t.Format(time.RFC3339), nil
}

func newSearchCommand(g *globals) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search",
		Short: "Search for files or folders",
		Long: `Uses the MYBOX search index. Unlike a listing, results carry a full path,
which makes this much faster for finding something buried deep in the tree.

Search has a tighter budget than the other APIs: 10 to 30 calls a minute,
depending on plan.`,
	}
	cmd.AddCommand(newSearchFilesCommand(g), newSearchFoldersCommand(g))
	return cmd
}

func newSearchFilesCommand(g *globals) *cobra.Command {
	var (
		flags    searchFlags
		category string
	)

	cmd := &cobra.Command{
		Use:     "files [query]",
		Aliases: []string{"file", "f"},
		Short:   "Search for files",
		Long: `At least one of a query, a category or a date range is required.
Spaces and extensions in the query are treated as AND terms.`,
		Example: `  mybox search files 회의록
  mybox search files "1월 회의록 pdf"
  mybox search files --category image --in /사진
  mybox search files --since 2026-01-01 --until 2026-01-31 --date-field modified`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			start, end, err := flags.dates()
			if err != nil {
				return err
			}

			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			opts := api.FileSearchOptions{
				Query:      query,
				Category:   category,
				ParentPath: flags.in,
				DateField:  flags.dateField,
				StartDate:  start,
				EndDate:    end,
				Count:      api.MaxSearchPageSize,
			}
			if err := validateCategory(category); err != nil {
				return err
			}

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			results := make([]api.FileResource, 0, 32)
			for item, err := range client.IterFiles(ctx, opts) {
				if err != nil {
					return err
				}
				results = append(results, item)
				if flags.limit > 0 && len(results) >= flags.limit {
					break
				}
			}

			if p.JSON {
				return p.EmitJSON(results)
			}
			if len(results) == 0 {
				p.Info("no matches")
				return nil
			}
			tw := p.Table()
			fmt.Fprintln(tw, "SIZE\tMODIFIED\tPATH")
			for _, item := range results {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", output.Bytes(item.Size), p.Time(item.ModifiedAt), item.Path)
			}
			return tw.Flush()
		},
	}

	flags.register(cmd)
	cmd.Flags().StringVarP(&category, "category", "c", "",
		"file kind: image, video, audio, document, archive, executable, etc")
	return cmd
}

func newSearchFoldersCommand(g *globals) *cobra.Command {
	var (
		flags searchFlags
		path  string
	)

	cmd := &cobra.Command{
		Use:     "folders [query]",
		Aliases: []string{"folder", "dir", "d"},
		Short:   "Search for folders",
		Long: `At least one of a query, --path or a date range is required.

--path pins the search to exactly that folder and ignores every other criterion.`,
		Example: `  mybox search folders 프로젝트
  mybox search folders --in /문서
  mybox search folders --path /문서/2026`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.Printer()
			client, err := g.Client()
			if err != nil {
				return err
			}
			start, end, err := flags.dates()
			if err != nil {
				return err
			}

			query := ""
			if len(args) == 1 {
				query = args[0]
			}
			opts := api.FolderSearchOptions{
				Query:      query,
				Path:       path,
				ParentPath: flags.in,
				DateField:  flags.dateField,
				StartDate:  start,
				EndDate:    end,
				Count:      api.MaxSearchPageSize,
			}

			ctx, cancel := g.Context(cmd.Context())
			defer cancel()

			results := make([]api.FolderResource, 0, 32)
			for item, err := range client.IterFolders(ctx, opts) {
				if err != nil {
					return err
				}
				results = append(results, item)
				if flags.limit > 0 && len(results) >= flags.limit {
					break
				}
			}

			if p.JSON {
				return p.EmitJSON(results)
			}
			if len(results) == 0 {
				p.Info("no matches")
				return nil
			}
			tw := p.Table()
			fmt.Fprintln(tw, "MODIFIED\tPATH\tID")
			for _, item := range results {
				fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Time(item.ModifiedAt), item.Path, item.ResourceID)
			}
			return tw.Flush()
		},
	}

	flags.register(cmd)
	cmd.Flags().StringVar(&path, "path", "", "pin to exactly this folder; every other criterion is ignored")
	return cmd
}

func validateCategory(c string) error {
	if c == "" {
		return nil
	}
	for _, known := range api.Categories {
		if c == known {
			return nil
		}
	}
	return usagef("--category: unknown kind %q; valid values are %s",
		c, strings.Join(api.Categories, ", "))
}
