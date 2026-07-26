package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spencer-life/ai-tracker/ingest"
	coredb "github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spf13/cobra"
)

type reportFlags struct {
	rangeName, from, to, timezone, agent, provider, model, quality string
	includeEstimates, json                                         bool
	limit                                                          int
}

func bindReportFlags(cmd *cobra.Command, opts *reportFlags) {
	cmd.Flags().StringVar(&opts.rangeName, "range", "30d", "today, 7d, 30d, mtd, or custom")
	cmd.Flags().StringVar(&opts.from, "from", "", "range start (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&opts.to, "to", "", "exclusive range end (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&opts.timezone, "tz", time.Now().Location().String(), "IANA timezone")
	cmd.Flags().StringVar(&opts.agent, "agent", "", "filter by agent")
	cmd.Flags().StringVar(&opts.provider, "provider", "", "filter by provider")
	cmd.Flags().StringVar(&opts.model, "model", "", "filter by model")
	cmd.Flags().StringVar(&opts.quality, "quality", "", "reported, derived, estimated, or legacy")
	cmd.Flags().BoolVar(&opts.includeEstimates, "include-estimates", false, "include explicitly estimated usage")
	cmd.Flags().BoolVar(&opts.json, "json", false, "emit stable JSON")
	cmd.Flags().IntVar(&opts.limit, "limit", 50, "maximum sessions")
}

func (o reportFlags) filter(now time.Time) (coredb.QueryFilter, error) {
	loc, err := time.LoadLocation(o.timezone)
	if err != nil {
		return coredb.QueryFilter{}, fmt.Errorf("invalid timezone %q: %w", o.timezone, err)
	}
	now = now.In(loc)
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	to := dayStart.AddDate(0, 0, 1)
	var from time.Time
	switch strings.ToLower(o.rangeName) {
	case "today":
		from = dayStart
	case "7d":
		from = dayStart.AddDate(0, 0, -6)
	case "30d", "":
		from = dayStart.AddDate(0, 0, -29)
	case "mtd":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	case "custom":
		if o.from == "" {
			return coredb.QueryFilter{}, fmt.Errorf("--range custom requires --from")
		}
	default:
		return coredb.QueryFilter{}, fmt.Errorf("invalid range %q", o.rangeName)
	}
	if o.from != "" {
		from, err = parseUserTime(o.from, loc, false)
		if err != nil {
			return coredb.QueryFilter{}, err
		}
	}
	if o.to != "" {
		to, err = parseUserTime(o.to, loc, true)
		if err != nil {
			return coredb.QueryFilter{}, err
		}
	}
	if !from.Before(to) {
		return coredb.QueryFilter{}, fmt.Errorf("range start must be before end")
	}
	quality := coredb.Measurement(o.quality)
	if quality != "" && quality != coredb.MeasurementReported && quality != coredb.MeasurementDerived && quality != coredb.MeasurementEstimated && quality != coredb.MeasurementLegacy {
		return coredb.QueryFilter{}, fmt.Errorf("invalid quality %q", o.quality)
	}
	return coredb.QueryFilter{From: from, To: to, Timezone: loc, Agent: o.agent, Provider: o.provider, Model: o.model, Quality: quality, IncludeEstimates: o.includeEstimates, Limit: o.limit}, nil
}

func parseUserTime(value string, loc *time.Location, _ bool) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.In(loc), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid date %q; use YYYY-MM-DD or RFC3339", value)
}

func openRepository() (*ingest.Repository, func(), error) {
	dbConn, err := ingest.InitDB()
	if err != nil {
		return nil, nil, err
	}
	repo := ingest.NewRepository(dbConn)
	return repo, func() { _ = repo.Close() }, nil
}

func reportCommand(use, bucket string) *cobra.Command {
	opts := reportFlags{}
	short := "Show trustworthy " + use + " usage"
	if use == "usage" {
		short = "Show trustworthy usage"
	}
	cmd := &cobra.Command{Use: use, Short: short, RunE: func(cmd *cobra.Command, args []string) error { return runReport(cmd.Context(), opts, bucket) }}
	bindReportFlags(cmd, &opts)
	return cmd
}

func runReport(ctx context.Context, opts reportFlags, bucket string) error {
	filter, err := opts.filter(time.Now())
	if err != nil {
		return err
	}
	repo, closeFn, err := openRepository()
	if err != nil {
		return err
	}
	defer closeFn()
	summary, err := repo.Summary(ctx, filter)
	if err != nil {
		return err
	}
	series, err := repo.Series(ctx, filter, bucket)
	if err != nil {
		return err
	}
	if opts.json {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Summary coredb.Summary       `json:"summary"`
			Series  []coredb.SeriesPoint `json:"series"`
		}{summary, series})
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "RANGE\tSESSIONS\tTOKENS\tINPUT\tCACHE READ\tCACHE WRITE\tOUTPUT\tREASONING\tAPI-EQUIV COST\n")
	cost := "unavailable"
	if summary.CostMicros != nil {
		cost = fmt.Sprintf("$%.4f", float64(*summary.CostMicros)/1e6)
		pricedTotal := summary.CostCoverage.PricedTokens + summary.CostCoverage.UnpricedTokens
		if pricedTotal > 0 && summary.CostCoverage.UnpricedTokens > 0 {
			cost += fmt.Sprintf(" (%.1f%% priced)", 100*float64(summary.CostCoverage.PricedTokens)/float64(pricedTotal))
		}
	}
	_, _ = fmt.Fprintf(w, "%s to %s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", summary.RangeFrom.Format("2006-01-02"), summary.RangeTo.Format("2006-01-02"), summary.Sessions, summary.Tokens.Total, summary.Tokens.InputUncached, summary.Tokens.CacheRead, summary.Tokens.CacheWrite, summary.Tokens.Output, summary.Tokens.Reasoning, cost)
	if summary.Quality.Estimated > 0 {
		_, _ = fmt.Fprintf(w, "QUALITY\testimated tokens excluded unless --include-estimates\t%d\n", summary.Quality.Estimated)
	}
	return w.Flush()
}

func sessionsCommand() *cobra.Command {
	opts := reportFlags{}
	cmd := &cobra.Command{Use: "sessions", Short: "List recent source-backed sessions", RunE: func(cmd *cobra.Command, args []string) error {
		filter, err := opts.filter(time.Now())
		if err != nil {
			return err
		}
		repo, closeFn, err := openRepository()
		if err != nil {
			return err
		}
		defer closeFn()
		page, err := repo.ListSessions(cmd.Context(), filter)
		if err != nil {
			return err
		}
		if opts.json {
			return json.NewEncoder(os.Stdout).Encode(page)
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "UPDATED\tAGENT\tMODEL\tTOKENS\tQUALITY\tSESSION")
		for _, s := range page.Sessions {
			model := s.Model
			if model == "" {
				model = "unknown"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n", s.UpdatedAt.In(filter.Timezone).Format("2006-01-02 15:04"), s.Agent, model, s.Tokens.Total, s.Measurement, s.SourceSessionID)
		}
		return w.Flush()
	}}
	bindReportFlags(cmd, &opts)
	return cmd
}

func init() {
	rootCmd.AddCommand(reportCommand("usage", "day"), reportCommand("daily", "day"), reportCommand("weekly", "week"), reportCommand("monthly", "month"), sessionsCommand())
}
