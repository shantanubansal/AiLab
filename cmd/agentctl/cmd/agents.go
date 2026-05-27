// agents subcommand tree: list / get / create / delete.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func agentsCmd() *cobra.Command {
	c := &cobra.Command{Use: "agents", Short: "Manage agents"}
	c.AddCommand(agentsListCmd(), agentsGetCmd(), agentsCreateCmd(), agentsDeleteCmd())
	return c
}

func agentsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List agents in the caller's tenant",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			agents, err := cl.ListAgents(ctx)
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tNAME\tMODE\tRUNTIME\tIMAGE\tCREATED")
			for _, a := range agents {
				img := "-"
				if a.Image != nil {
					img = *a.Image
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
					a.ID, a.Name, a.Mode, a.Runtime, img, a.CreatedAt.Format(time.RFC3339))
			}
			return tw.Flush()
		},
	}
}

func agentsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <agentId>",
		Short: "Get one agent as JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			a, err := cl.GetAgent(ctx, args[0])
			if err != nil {
				return err
			}
			return writeJSON(a)
		},
	}
}

func agentsCreateCmd() *cobra.Command {
	var (
		name, mode, runtime, image string
		manifestPath               string
	)
	c := &cobra.Command{
		Use:   "create",
		Short: "Create an agent from flags or from a uipath-agent.yaml/json manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			manifest := map[string]any{}
			if manifestPath != "" {
				raw, err := os.ReadFile(manifestPath)
				if err != nil {
					return err
				}
				// Accept JSON only; YAML support can layer on later via a yaml lib.
				if err := json.Unmarshal(raw, &manifest); err != nil {
					return fmt.Errorf("manifest must be JSON (got: %w)", err)
				}
			} else {
				manifest["schemaVersion"] = "v1"
				manifest["name"] = name
				manifest["mode"] = mode
				manifest["runtime"] = runtime
				if image != "" {
					manifest["image"] = image
				}
			}
			if strings.TrimSpace(fmt.Sprintf("%v", manifest["name"])) == "" {
				return fmt.Errorf("manifest name is required")
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			a, err := cl.CreateAgent(ctx, manifest)
			if err != nil {
				return err
			}
			return writeJSON(a)
		},
	}
	c.Flags().StringVar(&name, "name", "", "agent name (dns-1123)")
	c.Flags().StringVar(&mode, "mode", "job", "job | server")
	c.Flags().StringVar(&runtime, "runtime", "container", "python | typescript | container")
	c.Flags().StringVar(&image, "image", "", "OCI image (required when runtime=container)")
	c.Flags().StringVar(&manifestPath, "manifest", "", "path to a JSON manifest (overrides flags)")
	return c
}

func agentsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <agentId>",
		Short: "Delete an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cl, err := clientFromFlags(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := ctxFromCmd()
			defer cancel()
			if err := cl.DeleteAgent(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "deleted")
			return nil
		},
	}
}
