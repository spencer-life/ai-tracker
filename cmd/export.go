package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	coredb "github.com/spencer-life/ai-tracker/internal/db"
	"github.com/spf13/cobra"
)

type exportOptions struct {
	reportFlags
	csv bool
	out string
}

var exportCmd = func() *cobra.Command {
	opts := exportOptions{}
	cmd := &cobra.Command{Use: "export", Short: "Export the same filtered session data used by the dashboards", RunE: func(cmd *cobra.Command, args []string) error {
		filter, err := opts.filter(time.Now())
		if err != nil {
			return err
		}
		repo, closeFn, err := openRepository()
		if err != nil {
			return err
		}
		defer closeFn()
		filter.Limit = 250
		all := []coredb.Session{}
		for {
			page, err := repo.ListSessions(cmd.Context(), filter)
			if err != nil {
				return err
			}
			all = append(all, page.Sessions...)
			if page.NextCursor == "" {
				break
			}
			filter.Cursor = page.NextCursor
		}
		var data []byte
		if opts.csv {
			var b bytes.Buffer
			w := csv.NewWriter(&b)
			if err := w.Write([]string{"id", "agent", "provider", "sourceSessionId", "updatedAt", "model", "measurement", "inputUncached", "cacheRead", "cacheWrite", "output", "reasoning", "total", "costMicros"}); err != nil {
				return err
			}
			for _, s := range all {
				cost := ""
				if s.CostMicros != nil {
					cost = strconv.FormatInt(*s.CostMicros, 10)
				}
				row := []string{s.ID, s.Agent, s.Provider, s.SourceSessionID, s.UpdatedAt.Format(time.RFC3339Nano), s.Model, string(s.Measurement), strconv.FormatInt(s.Tokens.InputUncached, 10), strconv.FormatInt(s.Tokens.CacheRead, 10), strconv.FormatInt(s.Tokens.CacheWrite, 10), strconv.FormatInt(s.Tokens.Output, 10), strconv.FormatInt(s.Tokens.Reasoning, 10), strconv.FormatInt(s.Tokens.Total, 10), cost}
				if err := w.Write(row); err != nil {
					return err
				}
			}
			w.Flush()
			if err := w.Error(); err != nil {
				return err
			}
			data = b.Bytes()
		} else {
			if all == nil {
				all = []coredb.Session{}
			}
			data, err = json.MarshalIndent(all, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
		}
		if opts.out == "" {
			_, err = os.Stdout.Write(data)
			return err
		}
		return writePrivateExport(opts.out, data, opts.csv)
	}}
	bindReportFlags(cmd, &opts.reportFlags)
	cmd.Flags().BoolVar(&opts.csv, "csv", false, "export CSV instead of JSON")
	cmd.Flags().StringVar(&opts.out, "out", "", "output path; .zip writes a compressed export")
	return cmd
}()

func writePrivateExport(path string, data []byte, isCSV bool) error {
	if strings.HasSuffix(strings.ToLower(path), ".zip") {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		zw := zip.NewWriter(f)
		name := "sessions.json"
		if isCSV {
			name = "sessions.csv"
		}
		entry, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return err
		}
		if _, err = entry.Write(data); err != nil {
			_ = zw.Close()
			return err
		}
		if err = zw.Close(); err != nil {
			return err
		}
		return os.Chmod(path, 0o600)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secure export permissions: %w", err)
	}
	return nil
}

func init() { rootCmd.AddCommand(exportCmd) }
