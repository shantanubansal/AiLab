// Package events defines NATS JetStream subjects and payload types that
// services exchange. Subjects are stable; payloads may evolve via additive
// JSON fields. Consumers should ignore unknown fields.
package events

import "time"

// Subjects exchanged on the internal event bus.
const (
	SubjectRunRequested = "run.requested"
	SubjectRunStarted   = "run.started"
	SubjectRunCompleted = "run.completed"
	SubjectRunLog       = "run.log"

	SubjectBuildRequested = "build.requested"
	SubjectBuildCompleted = "build.completed"

	SubjectDeploymentRequested = "deployment.requested"
	SubjectDeploymentStopped   = "deployment.stopped"
)

// RunRequested fires after the api persists a Run row with status=pending.
// The controller consumes it to create an AgentRun CRD.
type RunRequested struct {
	TenantID  string         `json:"tenantId"`
	AgentID   string         `json:"agentId"`
	RunID     string         `json:"runId"`
	Image     string         `json:"image"`              // resolved, signed image reference
	Inputs    map[string]any `json:"inputs"`             // arbitrary input JSON, validated by api
	TraceID   string         `json:"traceId"`            // propagated to user code via AGENT_TRACE_ID
	SecretRef string         `json:"secretRef,omitempty"` // k8s Secret name in tenant ns, EnvFrom-mounted into pod
	At        time.Time      `json:"at"`
}

// RunStarted fires when the pod backing a run transitions to Running.
type RunStarted struct {
	TenantID string    `json:"tenantId"`
	AgentID  string    `json:"agentId"`
	RunID    string    `json:"runId"`
	At       time.Time `json:"at"`
}

// RunStatus enumerates the terminal state of a run.
type RunStatus string

const (
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusTimedOut  RunStatus = "timed_out"
	RunStatusCancelled RunStatus = "cancelled"
)

// RunCompleted fires when the controller observes a terminal Job phase. The
// outputs field is populated from the agent's stdout if it parsed as JSON.
type RunCompleted struct {
	TenantID  string         `json:"tenantId"`
	AgentID   string         `json:"agentId"`
	RunID     string         `json:"runId"`
	Status    RunStatus      `json:"status"`
	ExitCode  int            `json:"exitCode"`
	Outputs   map[string]any `json:"outputs,omitempty"`
	Error     string         `json:"error,omitempty"`
	StartedAt time.Time      `json:"startedAt"`
	EndedAt   time.Time      `json:"endedAt"`
}

// BuildRequested fires when a user pushes source for a code agent. The
// builder consumes it, runs Kaniko + Trivy + cosign, then publishes BuildCompleted.
type BuildRequested struct {
	TenantID string    `json:"tenantId"`
	AgentID  string    `json:"agentId"`
	BuildID  string    `json:"buildId"`
	SourceURL string   `json:"sourceUrl"` // git URL or pre-uploaded tarball URL
	At       time.Time `json:"at"`
}

// BuildStatus enumerates the terminal state of a build.
type BuildStatus string

const (
	BuildStatusSucceeded BuildStatus = "succeeded"
	BuildStatusFailed    BuildStatus = "failed"
	BuildStatusBlocked   BuildStatus = "blocked" // failed scan / signature
)

// DeploymentRequested is published when a mode=server agent should be
// brought up as a long-running Deployment + Service. The controller
// materializes it into an AgentDeployment CR.
type DeploymentRequested struct {
	TenantID   string    `json:"tenantId"`
	AgentID    string    `json:"agentId"`
	AgentName  string    `json:"agentName"`
	Image      string    `json:"image"`
	Port       int32     `json:"port"`
	HealthPath string    `json:"healthPath,omitempty"`
	SecretRef  string    `json:"secretRef,omitempty"` // k8s Secret name to EnvFrom-mount
	At         time.Time `json:"at"`
}

// DeploymentStopped is published when an agent's long-running deployment
// should be torn down (DELETE /deploy or agent deletion).
type DeploymentStopped struct {
	TenantID  string    `json:"tenantId"`
	AgentID   string    `json:"agentId"`
	AgentName string    `json:"agentName"`
	At        time.Time `json:"at"`
}

// BuildCompleted fires when the builder finishes. Image is only set when Status==succeeded.
type BuildCompleted struct {
	TenantID string      `json:"tenantId"`
	AgentID  string      `json:"agentId"`
	BuildID  string      `json:"buildId"`
	Status   BuildStatus `json:"status"`
	Image    string      `json:"image,omitempty"` // signed reference, e.g. registry/tenant/agent@sha256:...
	Error    string      `json:"error,omitempty"`
	EndedAt  time.Time   `json:"endedAt"`
}
