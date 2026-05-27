// runs subcommand tree: list / trigger / get / logs.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func runsCmd() *cobra.Command {
	c := &cobra.Command{Use: "runs", Short: "Manage runs"}
	c.AddCommand(runsListCmd(), runsTriggerCmd(), runsGetCmd(), runsLogsCmd(), runsWaitCmd())
	return c
}

func runsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <agentId>",
		Short: "List recent runs of an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			runs, err := cl.ListRuns(ctx, args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tSTATUS\tCREATED\tSTARTED\tENDED")
			for _, r := range runs {
				started, ended := "-", "-"
				if r.StartedAt != nil {
					started = r.StartedAt.Format(time.RFC3339)
				}
				if r.EndedAt != nil {
					ended = r.EndedAt.Format(time.RFC3339)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					r.ID, r.Status, r.CreatedAt.Format(time.RFC3339), started, ended)
			}
			return tw.Flush()
		},
	}
}

func runsTriggerCmd() *cobra.Command {
	var inputs string
	c := &cobra.Command{
		Use:   "trigger <agentId>",
		Short: "Trigger a manual run with optional inputs JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			var parsed map[string]any
			if inputs != "" {
				if err := json.Unmarshal([]byte(inputs), &parsed); err != nil {
					return fmt.Errorf("invalid --inputs JSON: %w", err)
				}
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			r, err := cl.TriggerRun(ctx, args[0], parsed)
			if err != nil {
				return err
			}
			return writeJSON(r)
		},
	}
	c.Flags().StringVar(&inputs, "inputs", "", "inputs JSON (e.g. '{\"key\":\"value\"}')")
	return c
}

func runsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <runId>",
		Short: "Get a run as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			r, err := cl.GetRun(ctx, args[0])
			if err != nil {
				return err
			}
			return writeJSON(r)
		},
	}
}

func runsLogsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <runId>",
		Short: "Stream pod logs for a run as plain lines",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			return cl.StreamLogs(ctx, args[0], func(line string) {
				fmt.Println(line)
			})
		},
	}
}

func runsWaitCmd() *cobra.Command {
	var timeout time.Duration
	var pollEvery time.Duration
	c := &cobra.Command{
		Use:   "wait <runId>",
		Short: "Poll a run until it reaches a terminal state; exit 0 if succeeded",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			deadline := time.Now().Add(timeout)
			for {
				r, err := cl.GetRun(ctx, args[0])
				if err != nil {
					return err
				}
				switch r.Status {
				case "succeeded":
					_ = writeJSON(r)
					return nil
				case "failed", "timed_out", "cancelled":
					_ = writeJSON(r)
					// Distinct non-zero so callers can tell terminal-error from poll-error.
					return fmt.Errorf("run ended: %s", r.Status)
				}
				if time.Now().After(deadline) {
					return fmt.Errorf("timed out after %s; last status: %s", timeout, r.Status)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(pollEvery):
				}
			}
		},
	}
	c.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "maximum time to wait")
	c.Flags().DurationVar(&pollEvery, "poll-every", 2*time.Second, "interval between status checks")
	return c
}
