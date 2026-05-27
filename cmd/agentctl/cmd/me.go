// whoami — prints the caller's tenant + user info from /v1/me.

package cmd

import "github.com/spf13/cobra"

func meCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the caller's tenant + user info from /v1/me",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			m, err := cl.Me(ctx)
			if err != nil {
				return err
			}
			return writeJSON(m)
		},
	}
}
