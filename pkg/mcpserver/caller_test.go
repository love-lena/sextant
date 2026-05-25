package mcpserver

import (
	"testing"

	"github.com/google/uuid"
)

func TestCallerHasCap(t *testing.T) {
	agent := Caller{
		Kind:         CallerAgent,
		AgentUUID:    uuid.New(),
		Capabilities: []string{CapSendMessage, CapReadAgents},
	}
	if !agent.HasCap(CapSendMessage) {
		t.Errorf("agent should have %s", CapSendMessage)
	}
	if agent.HasCap(CapControlSpawn) {
		t.Errorf("agent should not have %s", CapControlSpawn)
	}
	// Empty cap = no gate.
	if !agent.HasCap("") {
		t.Errorf("empty cap should always allow")
	}

	op := Caller{Kind: CallerOperator}
	if !op.HasCap(CapControlSpawn) {
		t.Errorf("operator should pass every cap check")
	}
}

func TestCallerID(t *testing.T) {
	id := uuid.New()
	agent := Caller{Kind: CallerAgent, AgentUUID: id}
	if got := agent.ID(); got != id.String() {
		t.Errorf("agent ID = %q, want %s", got, id)
	}
	op := Caller{Kind: CallerOperator}
	if got := op.ID(); got != "operator" {
		t.Errorf("operator ID = %q, want %q", got, "operator")
	}
}

func TestCapForToolKnownTools(t *testing.T) {
	cases := map[string]string{
		ToolSendMessage: CapSendMessage,
		ToolBroadcast:   CapBroadcast,
		ToolListAgents:  CapReadAgents,
		ToolAgentStatus: CapReadAgents,
		ToolQueryAudit:  CapReadHistory,
		ToolSpawnAgent:  CapControlSpawn,
		ToolKillAgent:    CapControlKill,
		ToolArchiveAgent: CapControlArchive,
		ToolPromptAgent:  CapControlPrompt,
		ToolEmitEvent:   CapEmitEvent,
		ToolGetMetric:   CapReadMetrics,
	}
	for tool, want := range cases {
		if got := CapForTool(tool); got != want {
			t.Errorf("CapForTool(%q) = %q, want %q", tool, got, want)
		}
	}
	if got := CapForTool("unknown_tool"); got != "" {
		t.Errorf("CapForTool(unknown) = %q, want empty", got)
	}
}
