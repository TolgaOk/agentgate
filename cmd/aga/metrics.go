package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/TolgaOk/agentgate/internal/metrics"
)

func newMetricsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "metrics",
		Short: "Show usage metrics",
		RunE:  runMetrics,
	}
	cmd.Flags().String("since", "today", "Show usage since: today, 7d, 30d, or YYYY-MM-DD")
	return cmd
}

func runMetrics(cmd *cobra.Command, args []string) error {
	sinceFlag, _ := cmd.Flags().GetString("since")

	var since time.Time
	now := time.Now()
	switch sinceFlag {
	case "today", "":
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "7d":
		since = now.AddDate(0, 0, -7)
	case "30d":
		since = now.AddDate(0, 0, -30)
	default:
		t, err := time.Parse("2006-01-02", sinceFlag)
		if err != nil {
			return fmt.Errorf("invalid --since value %q (use: today, 7d, 30d, or YYYY-MM-DD)", sinceFlag)
		}
		since = t
	}

	metricsDir, err := dataDir()
	if err != nil {
		return err
	}
	store, err := metrics.NewStore(filepath.Join(metricsDir, "metrics.db"))
	if err != nil {
		return err
	}
	defer store.Close()

	days, err := store.Summary(context.Background(), since)
	if err != nil {
		return err
	}

	if len(days) == 0 {
		fmt.Println("No usage data.")
		return nil
	}

	var totalIn, totalOut, totalCalls int
	for _, d := range days {
		fmt.Printf("%s  %6d in  %6d out  %3d calls\n", d.Date, d.InputTokens, d.OutputTokens, d.CallCount)
		totalIn += d.InputTokens
		totalOut += d.OutputTokens
		totalCalls += d.CallCount
	}
	if len(days) > 1 {
		fmt.Printf("%-10s %6d in  %6d out  %3d calls\n", "total", totalIn, totalOut, totalCalls)
	}

	return nil
}
