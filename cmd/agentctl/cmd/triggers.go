// triggers subcommand: list / create webhook | cron.

package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func triggersCmd() *cobra.Command {
	c := &cobra.Command{Use: "triggers", Short: "Manage triggers"}
	c.AddCommand(triggersListCmd(), triggersCreateCmd())
	return c
}

func triggersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <agentId>",
		Short: "List triggers for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			ts, err := cl.ListTriggers(ctx, args[0])
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tKIND\tNAME\tCRON\tCREATED")
			for _, t := range ts {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
					t.ID, t.Kind, t.Name, dashIfEmpty(t.Cron), t.CreatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

func triggersCreateCmd() *cobra.Command {
	c := &cobra.Command{Use: "create", Short: "Create a trigger"}
	c.AddCommand(triggersCreateWebhookCmd(), triggersCreateCronCmd())
	return c
}

func triggersCreateWebhookCmd() *cobra.Command {
	var name string
	c := &cobra.Command{
		Use:   "webhook <agentId>",
		Short: "Create a webhook trigger (prints the secret once)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			t, err := cl.CreateWebhookTrigger(ctx, args[0], name)
			if err != nil {
				return err
			}
			return writeJSON(t)
		},
	}
	c.Flags().StringVar(&name, "name", "", "trigger name (e.g. incoming)")
	return c
}

func triggersCreateCronCmd() *cobra.Command {
	var name, cron string
	c := &cobra.Command{
		Use:   "cron <agentId>",
		Short: "Create a cron trigger",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || cron == "" {
				return fmt.Errorf("--name and --cron are required")
			}
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			t, err := cl.CreateCronTrigger(ctx, args[0], name, cron)
			if err != nil {
				return err
			}
			return writeJSON(t)
		},
	}
	c.Flags().StringVar(&name, "name", "", "trigger name")
	c.Flags().StringVar(&cron, "cron", "", "cron expression, UTC (e.g. '*/5 * * * *')")
	return c
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
