package manifest

import "testing"

func TestValidate_OK_JobPython(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Name:          "hello-llm",
		Mode:          ModeJob,
		Runtime:       RuntimePython,
		Triggers:      []Trigger{{Kind: TriggerManual}, {Kind: TriggerCron, Cron: "*/5 * * * *"}},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_OK_ServerMCP(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Name:          "my-mcp",
		Mode:          ModeServer,
		Runtime:       RuntimeContainer,
		Image:         "ghcr.io/example/mcp:1.0",
		Server:        &Server{Port: 8080, HealthPath: "/healthz", IdleTimeout: "30s"},
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}
}

func TestValidate_BadName(t *testing.T) {
	m := &Manifest{SchemaVersion: SchemaVersion, Name: "Has_Underscore", Mode: ModeJob, Runtime: RuntimePython}
	if err := m.Validate(); err == nil {
		t.Fatal("expected name validation to fail")
	}
}

func TestValidate_ContainerRequiresImage(t *testing.T) {
	m := &Manifest{SchemaVersion: SchemaVersion, Name: "x", Mode: ModeJob, Runtime: RuntimeContainer}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error: container runtime without image")
	}
}

func TestValidate_ServerRequiresServerBlock(t *testing.T) {
	m := &Manifest{SchemaVersion: SchemaVersion, Name: "x", Mode: ModeServer, Runtime: RuntimePython}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error: server mode without server block")
	}
}

func TestValidate_CronTriggerRequiresExpression(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		Name:          "x",
		Mode:          ModeJob,
		Runtime:       RuntimePython,
		Triggers:      []Trigger{{Kind: TriggerCron}},
	}
	if err := m.Validate(); err == nil {
		t.Fatal("expected error: cron trigger without expression")
	}
}
