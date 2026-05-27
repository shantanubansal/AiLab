// Root command and shared helpers for agentctl.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	sdkgo "github.com/shantanubansal/AiLab/pkg/sdk-go"
)

// Root returns the root cobra command tree.
func Root() *cobra.Command {
	c := &cobra.Command{
		Use:   "agentctl",
		Short: "AiLab platform CLI",
		Long: `agentctl drives the AiLab platform API.

Configure once with 'agentctl login', or set AILAB_API and AILAB_TOKEN.`,
		SilenceUsage: true,
	}
	c.PersistentFlags().String("api", "", "platform API base URL (overrides config + env)")
	c.PersistentFlags().String("token", "", "bearer token (overrides config + env)")

	c.AddCommand(loginCmd())
	c.AddCommand(initCmd())
	c.AddCommand(agentsCmd())
	c.AddCommand(runsCmd())
	c.AddCommand(triggersCmd())
	c.AddCommand(deployCmd())
	c.AddCommand(undeployCmd())
	c.AddCommand(buildsCmd())
	c.AddCommand(secretsCmd())
	return c
}

// clientFromFlags merges flag, env, and config values to produce a sdkgo.Client.
func clientFromFlags(c *cobra.Command) (*sdkgo.Client, error) {
	apiFlag, _ := c.Flags().GetString("api")
	tokenFlag, _ := c.Flags().GetString("token")
	cfg, _ := LoadConfig()

	api := firstNonEmpty(apiFlag, os.Getenv("AILAB_API"), cfg.API, "http://localhost:8080")
	token := firstNonEmpty(tokenFlag, os.Getenv("AILAB_TOKEN"), cfg.Token)
	if token == "" {
		return nil, fmt.Errorf("no token: run 'agentctl login' or set AILAB_TOKEN")
	}
	return sdkgo.New(api, token), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// ctxFromCmd returns a context that cancels on SIGINT/SIGTERM.
func ctxFromCmd() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
