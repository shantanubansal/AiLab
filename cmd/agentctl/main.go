// Command agentctl is the platform CLI. It wraps the v1 REST surface
// over a sdk-go client. Configuration comes from ~/.config/ailab/config.yaml
// (set via `agentctl login`) or from AILAB_API and AILAB_TOKEN env vars.
package main

import (
	"fmt"
	"os"

	"github.com/shantanubansal/AiLab/cmd/agentctl/cmd"
)

func main() {
	if err := cmd.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
