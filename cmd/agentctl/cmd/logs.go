// Top-level `agentctl logs <runId>` is an alias for `agentctl runs logs`.
// Surfaced here because tailing logs is by far the most common reason
// people reach for the CLI in the first place; making them remember
// the `runs` subgroup felt gratuitous.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func logsAliasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logs <runId>",
		Short: "Stream pod logs for a run (alias of `agentctl runs logs`)",
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
