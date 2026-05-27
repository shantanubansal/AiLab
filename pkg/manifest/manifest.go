// Package manifest defines uipath-agent.yaml — the contract users author against.
//
// A manifest describes how the platform should build, run, and expose an agent.
// It is intentionally narrow in v1: enough to support code agents, container
// agents, and MCP server hosting onto one container runtime.
package manifest

import (
	"errors"
	"fmt"
	"regexp"
)

// SchemaVersion is the only manifest schema version supported in v1.
const SchemaVersion = "v1"

// Mode controls the lifecycle of the agent on the platform.
type Mode string

const (
	// ModeJob is a batch run: started, runs to completion, produces output. Maps to AgentRun.
	ModeJob Mode = "job"

	// ModeServer is a long-running process exposed at a tenant subdomain. Maps to AgentDeployment.
	// Used for MCP servers and other request/response services.
	ModeServer Mode = "server"
)

// Runtime declares how the platform should produce a runnable container image.
type Runtime string

const (
	// RuntimePython builds a Python agent from a source tree with pyproject.toml.
	RuntimePython Runtime = "python"

	// RuntimeTypeScript builds a TypeScript agent from a source tree with package.json.
	RuntimeTypeScript Runtime = "typescript"

	// RuntimeContainer skips the builder entirely and uses a pre-built OCI image.
	RuntimeContainer Runtime = "container"
)

// Manifest is the top-level uipath-agent.yaml structure.
type Manifest struct {
	SchemaVersion string    `yaml:"schemaVersion" json:"schemaVersion"`
	Name          string    `yaml:"name" json:"name"`
	Mode          Mode      `yaml:"mode" json:"mode"`
	Runtime       Runtime   `yaml:"runtime" json:"runtime"`
	Image         string    `yaml:"image,omitempty" json:"image,omitempty"`
	Entrypoint    []string  `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	Inputs        Schema    `yaml:"inputs,omitempty" json:"inputs,omitempty"`
	Outputs       Schema    `yaml:"outputs,omitempty" json:"outputs,omitempty"`
	Env           []EnvVar  `yaml:"env,omitempty" json:"env,omitempty"`
	Secrets       []string  `yaml:"secrets,omitempty" json:"secrets,omitempty"`
	Resources     Resources `yaml:"resources,omitempty" json:"resources,omitempty"`
	Triggers      []Trigger `yaml:"triggers,omitempty" json:"triggers,omitempty"`
	Server        *Server   `yaml:"server,omitempty" json:"server,omitempty"`
}

// Schema is a thin JSON-Schema reference for inputs/outputs. v1 just round-trips it.
type Schema map[string]any

// EnvVar is a non-secret environment variable injected at run time.
type EnvVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"value"`
}

// Resources describes the run's CPU/memory/wall-clock budget. The platform may
// clamp these per tenant tier; what's declared here is the upper bound the
// agent expects to need.
type Resources struct {
	CPU      string `yaml:"cpu,omitempty" json:"cpu,omitempty"`           // e.g. "500m"
	Memory   string `yaml:"memory,omitempty" json:"memory,omitempty"`     // e.g. "1Gi"
	Timeout  string `yaml:"timeout,omitempty" json:"timeout,omitempty"`   // e.g. "10m"
	DiskSize string `yaml:"diskSize,omitempty" json:"diskSize,omitempty"` // e.g. "2Gi"
}

// TriggerKind enumerates the v1 trigger types.
type TriggerKind string

const (
	TriggerManual  TriggerKind = "manual"
	TriggerWebhook TriggerKind = "webhook"
	TriggerCron    TriggerKind = "cron"
)

// Trigger declares how runs of this agent are initiated. Manual is always implicit.
type Trigger struct {
	Kind    TriggerKind `yaml:"kind" json:"kind"`
	Name    string      `yaml:"name,omitempty" json:"name,omitempty"`
	Cron    string      `yaml:"cron,omitempty" json:"cron,omitempty"`       // when Kind == TriggerCron
	Webhook *Webhook    `yaml:"webhook,omitempty" json:"webhook,omitempty"` // when Kind == TriggerWebhook
}

// Webhook configures an inbound HTTP trigger.
type Webhook struct {
	// SignatureHeader is the header the platform uses for HMAC signing of the request body.
	// Defaults to X-AiLab-Signature.
	SignatureHeader string `yaml:"signatureHeader,omitempty" json:"signatureHeader,omitempty"`
}

// Server configures Mode == "server" specifics: port and health probe.
type Server struct {
	Port        int    `yaml:"port" json:"port"`
	HealthPath  string `yaml:"healthPath,omitempty" json:"healthPath,omitempty"`
	IdleTimeout string `yaml:"idleTimeout,omitempty" json:"idleTimeout,omitempty"` // controls scale-to-zero
}

// nameRe enforces DNS-1123 label syntax, since the name is reused as the
// pod/service/subdomain identifier.
var nameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// Validate returns the first invariant violation, or nil if the manifest is well-formed.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must be %q, got %q", SchemaVersion, m.SchemaVersion)
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("name %q must be a DNS-1123 label", m.Name)
	}
	switch m.Mode {
	case ModeJob, ModeServer:
	default:
		return fmt.Errorf("mode must be one of [job, server], got %q", m.Mode)
	}
	switch m.Runtime {
	case RuntimePython, RuntimeTypeScript, RuntimeContainer:
	default:
		return fmt.Errorf("runtime must be one of [python, typescript, container], got %q", m.Runtime)
	}
	if m.Runtime == RuntimeContainer && m.Image == "" {
		return errors.New("runtime: container requires image to be set")
	}
	if m.Runtime != RuntimeContainer && m.Image != "" {
		return errors.New("image may only be set when runtime: container")
	}
	if m.Mode == ModeServer {
		if m.Server == nil {
			return errors.New("mode: server requires a server block")
		}
		if m.Server.Port <= 0 || m.Server.Port > 65535 {
			return fmt.Errorf("server.port must be in 1..65535, got %d", m.Server.Port)
		}
	}
	for i, t := range m.Triggers {
		switch t.Kind {
		case TriggerManual, TriggerWebhook:
		case TriggerCron:
			if t.Cron == "" {
				return fmt.Errorf("triggers[%d]: cron required for kind=cron", i)
			}
		default:
			return fmt.Errorf("triggers[%d]: unknown kind %q", i, t.Kind)
		}
	}
	return nil
}
