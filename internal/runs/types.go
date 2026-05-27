// AgentRun and AgentDeployment custom resources.
//
// AgentRun maps to a single batch execution; the reconciler creates a k8s Job
// in the tenant namespace. AgentDeployment maps to a long-running MCP server
// or other request/response service; the reconciler creates a Deployment + Service.

package runs

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is shared by all CRDs in this package.
var GroupVersion = schema.GroupVersion{Group: "ailab.uipath.com", Version: "v1alpha1"}

// AddToScheme registers the types with a runtime.Scheme.
func AddToScheme(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&AgentRun{}, &AgentRunList{},
		&AgentDeployment{}, &AgentDeploymentList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// +kubebuilder:object:root=true

// AgentRun represents one execution of an agent.
type AgentRun struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentRunSpec   `json:"spec,omitempty"`
	Status AgentRunStatus `json:"status,omitempty"`
}

// AgentRunSpec is desired state — set by the api when a run is queued.
type AgentRunSpec struct {
	TenantID  string            `json:"tenantId"`
	AgentName string            `json:"agentName"`
	Image     string            `json:"image"`
	Inputs    string            `json:"inputs,omitempty"` // JSON string passed via stdin
	Env       map[string]string `json:"env,omitempty"`
	Secrets   []string          `json:"secrets,omitempty"`
	// SecretRef is the name of a k8s Secret in the same namespace whose
	// keys are EnvFrom-projected into the agent container. The api creates
	// this Secret from the tenant's named secrets before dispatching.
	SecretRef string `json:"secretRef,omitempty"`
	CPU       string `json:"cpu,omitempty"`
	Memory    string `json:"memory,omitempty"`
	Timeout   string `json:"timeout,omitempty"`
	TraceID   string `json:"traceId,omitempty"`
}

// AgentRunPhase is the observed phase.
type AgentRunPhase string

const (
	PhasePending   AgentRunPhase = "Pending"
	PhaseRunning   AgentRunPhase = "Running"
	PhaseSucceeded AgentRunPhase = "Succeeded"
	PhaseFailed    AgentRunPhase = "Failed"
)

// AgentRunStatus is observed state — set by the reconciler.
type AgentRunStatus struct {
	Phase     AgentRunPhase `json:"phase,omitempty"`
	JobName   string        `json:"jobName,omitempty"`
	StartedAt *metav1.Time  `json:"startedAt,omitempty"`
	EndedAt   *metav1.Time  `json:"endedAt,omitempty"`
	ExitCode  *int32        `json:"exitCode,omitempty"`
	Message   string        `json:"message,omitempty"`
}

// +kubebuilder:object:root=true

// AgentRunList is the list type for AgentRun.
type AgentRunList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRun `json:"items"`
}

// DeepCopyObject — required by runtime.Object. We hand-write a minimal copy
// rather than depend on controller-gen at this scaffolding stage.
func (in *AgentRun) DeepCopyObject() runtime.Object { c := *in; return &c }
func (in *AgentRunList) DeepCopyObject() runtime.Object {
	out := &AgentRunList{TypeMeta: in.TypeMeta, ListMeta: in.ListMeta}
	out.Items = append(out.Items, in.Items...)
	return out
}

// +kubebuilder:object:root=true

// AgentDeployment represents a long-running agent (e.g. an MCP server).
type AgentDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AgentDeploymentSpec   `json:"spec,omitempty"`
	Status AgentDeploymentStatus `json:"status,omitempty"`
}

// AgentDeploymentSpec is desired state for a long-running agent.
type AgentDeploymentSpec struct {
	TenantID    string            `json:"tenantId"`
	AgentName   string            `json:"agentName"`
	Image       string            `json:"image"`
	Port        int32             `json:"port"`
	HealthPath  string            `json:"healthPath,omitempty"`
	IdleTimeout string            `json:"idleTimeout,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Secrets     []string          `json:"secrets,omitempty"`
	// SecretRef is the name of a k8s Secret in the same namespace whose
	// keys are EnvFrom-projected into the agent container.
	SecretRef string `json:"secretRef,omitempty"`
}

// AgentDeploymentStatus is observed state.
type AgentDeploymentStatus struct {
	URL          string `json:"url,omitempty"`
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	Message      string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true

// AgentDeploymentList is the list type for AgentDeployment.
type AgentDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentDeployment `json:"items"`
}

func (in *AgentDeployment) DeepCopyObject() runtime.Object { c := *in; return &c }
func (in *AgentDeploymentList) DeepCopyObject() runtime.Object {
	out := &AgentDeploymentList{TypeMeta: in.TypeMeta, ListMeta: in.ListMeta}
	out.Items = append(out.Items, in.Items...)
	return out
}
