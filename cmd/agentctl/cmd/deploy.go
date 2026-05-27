// deploy / undeploy commands for mode=server agents.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func deployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "deploy <agentId>",
		Short: "Bring up a long-running deployment for a mode=server agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			if err := cl.Deploy(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "queued")
			return nil
		},
	}
}

func undeployCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "undeploy <agentId>",
		Short: "Tear down a long-running deployment",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			if err := cl.Undeploy(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "queued")
			return nil
		},
	}
}
