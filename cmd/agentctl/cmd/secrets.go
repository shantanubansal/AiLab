// secrets subcommand: list / set / delete.

package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func secretsCmd() *cobra.Command {
	c := &cobra.Command{Use: "secrets", Short: "Manage tenant secrets"}
	c.AddCommand(secretsListCmd(), secretsSetCmd(), secretsDeleteCmd())
	return c
}

func secretsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secrets (names only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			ss, err := cl.ListSecrets(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tCREATED\tUPDATED")
			for _, s := range ss {
				fmt.Fprintf(tw, "%s\t%s\t%s\n",
					s.Name, s.CreatedAt.Format(time.RFC3339), s.UpdatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

func secretsSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name> <value>",
		Short: "Create or update a secret",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			s, err := cl.SetSecret(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			return writeJSON(s)
		},
	}
}

func secretsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			if err := cl.DeleteSecret(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted")
			return nil
		},
	}
}
