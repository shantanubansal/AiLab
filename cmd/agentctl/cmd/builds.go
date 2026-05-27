// builds subcommand: create.

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func buildsCmd() *cobra.Command {
	c := &cobra.Command{Use: "builds", Short: "Manage builds"}
	c.AddCommand(buildsCreateCmd())
	return c
}

func buildsCreateCmd() *cobra.Command {
	var source string
	c := &cobra.Command{
		Use:   "create <agentId>",
		Short: "Queue a Kaniko build of a source repo / tarball",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if source == "" {
				return fmt.Errorf("--source is required")
			}
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			b, err := cl.CreateBuild(ctx, args[0], source)
			if err != nil {
				return err
			}
			return writeJSON(b)
		},
	}
	c.Flags().StringVar(&source, "source", "", "git URL or tarball URL")
	return c
}
