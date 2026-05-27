// Command gateway is the ingress for long-running agents (MCP servers).
// It is a stub in v1 scaffolding — the real gateway routes
// https://<agent>.<tenant>.run.<domain> to the matching AgentDeployment with
// on-demand TLS and scale-to-zero.
package main

import "log"

func main() {
	log.Println("gateway: not yet implemented (see docs/PLAN.md item 7)")
}
