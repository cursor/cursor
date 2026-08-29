package copilot

import (
	"encoding/json"
	"testing"

	"github.com/github/copilot-sdk/go/rpc"
)

var _ rpc.SessionEvent = SessionEvent{}
var _ SessionEvent = rpc.SessionEvent{}
var _ rpc.SessionEventData = (*UserMessageData)(nil)
var _ SessionEventData = (*rpc.UserMessageData)(nil)
var _ rpc.EmbeddedTextResourceContents = EmbeddedTextResourceContents{}
var _ EmbeddedTextResourceContents = rpc.EmbeddedTextResourceContents{}

func TestSessionEventAgentIDRoundTripsKnownEvent(t *testing.T) {
	var event SessionEvent
	if err := json.Unmarshal([]byte(`{
		"id": "00000000-0000-0000-0000-000000000001",
		"timestamp": "2026-01-01T00:00:00Z",
		"parentId": null,
		"agentId": "agent-1",
		"type": "user.message",
		"data": {
			"content": "Hello"
		}
	}`), &event); err != nil {
		t.Fatalf("failed to unmarshal session event: %v", err)
	}

	if event.AgentID == nil || *event.AgentID != "agent-1" {
		t.Fatalf("expected agent ID to round-trip, got %v", event.AgentID)
	}
	if _, ok := event.Data.(*UserMessageData); !ok {
		t.Fatalf("expected user message data, got %T", event.Data)
	}
	if event.Type() != SessionEventTypeUserMessage {
		t.Fatalf("expected user message type, got %q", event.Type())
	}

	data, err := event.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal session event: %v", err)
	}

	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to unmarshal serialized session event: %v", err)
	}
	if serialized["agentId"] != "agent-1" {
		t.Fatalf("expected serialized agentId to round-trip, got %v", serialized["agentId"])
	}
}

func TestSessionEventTypeDerivedFromData(t *testing.T) {
	event := SessionEvent{
		Data: &UserMessageData{Content: "Hello"},
	}

	if event.Type() != SessionEventTypeUserMessage {
		t.Fatalf("expected user message type, got %q", event.Type())
	}

	data, err := event.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal session event: %v", err)
	}

	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to unmarshal serialized session event: %v", err)
	}
	if serialized["type"] != string(SessionEventTypeUserMessage) {
		t.Fatalf("expected serialized type to be derived from data, got %v", serialized["type"])
	}
}

func TestSessionEventAgentIDRoundTripsUnknownEvent(t *testing.T) {
	var event SessionEvent
	if err := json.Unmarshal([]byte(`{
		"id": "00000000-0000-0000-0000-000000000002",
		"timestamp": "2026-01-01T00:00:00Z",
		"parentId": null,
		"agentId": "future-agent",
		"type": "future.feature_from_server",
		"data": {
			"key": "value"
		}
	}`), &event); err != nil {
		t.Fatalf("failed to unmarshal session event: %v", err)
	}

	if event.AgentID == nil || *event.AgentID != "future-agent" {
		t.Fatalf("expected agent ID to round-trip, got %v", event.AgentID)
	}
	rawData, ok := event.Data.(*RawSessionEventData)
	if !ok {
		t.Fatalf("expected raw session event data, got %T", event.Data)
	}
	if event.Type() != "future.feature_from_server" {
		t.Fatalf("expected unknown event type to be derived from raw event type, got %q", event.Type())
	}
	if rawData.EventType != "future.feature_from_server" {
		t.Fatalf("expected raw event type to round-trip, got %q", rawData.EventType)
	}
	if rawData.Type() != event.Type() {
		t.Fatalf("expected raw data type to match event type, got %q", rawData.Type())
	}
	var rawPayload map[string]any
	if err := json.Unmarshal(rawData.Raw, &rawPayload); err != nil {
		t.Fatalf("failed to unmarshal raw payload: %v", err)
	}
	if rawPayload["key"] != "value" {
		t.Fatalf("expected raw payload to preserve data, got %v", rawPayload)
	}
	if _, ok := rawPayload["type"]; ok {
		t.Fatalf("expected raw payload to exclude event type, got %v", rawPayload)
	}

	data, err := event.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal session event: %v", err)
	}

	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to unmarshal serialized session event: %v", err)
	}
	if serialized["agentId"] != "future-agent" {
		t.Fatalf("expected serialized agentId to round-trip, got %v", serialized["agentId"])
	}
	if serialized["type"] != "future.feature_from_server" {
		t.Fatalf("expected serialized type to round-trip, got %v", serialized["type"])
	}
	serializedData, ok := serialized["data"].(map[string]any)
	if !ok {
		t.Fatalf("expected serialized data payload to be an object, got %T", serialized["data"])
	}
	if serializedData["key"] != "value" {
		t.Fatalf("expected serialized data payload to round-trip, got %v", serializedData)
	}
	if _, ok := serializedData["type"]; ok {
		t.Fatalf("expected serialized data to contain only the payload, got nested event object: %v", serializedData)
	}
}

func TestInternalSessionEventUsesRawFallback(t *testing.T) {
	var event SessionEvent
	if err := json.Unmarshal([]byte(`{
		"id": "00000000-0000-0000-0000-000000000003",
		"timestamp": "2026-01-01T00:00:00Z",
		"parentId": null,
		"type": "session.memory_changed",
		"data": {}
	}`), &event); err != nil {
		t.Fatalf("failed to unmarshal internal session event: %v", err)
	}

	if _, ok := event.Data.(*RawSessionEventData); !ok {
		t.Fatalf("expected internal event to use raw session event data, got %T", event.Data)
	}
	if event.Type() != "session.memory_changed" {
		t.Fatalf("expected internal event type to be preserved, got %q", event.Type())
	}
}

func TestRawSessionEventDataWithNilRawMarshalsAsNull(t *testing.T) {
	event := SessionEvent{
		Data: &RawSessionEventData{EventType: "future.event"},
	}

	data, err := event.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal session event: %v", err)
	}
	if !json.Valid(data) {
		t.Fatalf("expected valid JSON, got %s", data)
	}

	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to unmarshal serialized session event: %v", err)
	}
	if serialized["type"] != "future.event" {
		t.Fatalf("expected serialized type to round-trip, got %v", serialized["type"])
	}
	if serialized["data"] != nil {
		t.Fatalf("expected missing raw data to marshal as null, got %v", serialized["data"])
	}
}

func TestManagedSettingsResolvedProvenanceRoundTrips(t *testing.T) {
	sources := []ManagedSettingsResolvedSource{
		ManagedSettingsResolvedSourceServer,
		ManagedSettingsResolvedSourceDevice,
		ManagedSettingsResolvedSourceClient,
		ManagedSettingsResolvedSourceMixed,
		ManagedSettingsResolvedSourceNone,
	}
	expectedSources := []string{"server", "device", "client", "mixed", "none"}
	for i, source := range sources {
		if string(source) != expectedSources[i] {
			t.Fatalf("expected source %q, got %q", expectedSources[i], source)
		}
	}

	clientManaged := true
	resolved := SessionManagedSettingsResolvedData{
		BypassPermissionsDisabled: true,
		ClientManaged:             &clientManaged,
		DeviceManaged:             false,
		FailClosed:                false,
		ManagedKeys:               []string{"permissions"},
		ServerManaged:             false,
		Source:                    ManagedSettingsResolvedSourceClient,
	}
	data, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("failed to marshal managed settings resolution: %v", err)
	}

	var serialized map[string]any
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to inspect managed settings resolution: %v", err)
	}
	if serialized["source"] != "client" || serialized["clientManaged"] != true {
		t.Fatalf("expected client provenance, got %v", serialized)
	}

	var roundTripped SessionManagedSettingsResolvedData
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("failed to round-trip managed settings resolution: %v", err)
	}
	if roundTripped.Source != ManagedSettingsResolvedSourceClient ||
		roundTripped.ClientManaged == nil ||
		!*roundTripped.ClientManaged {
		t.Fatalf("expected client provenance to round-trip, got %#v", roundTripped)
	}

	resolved.Source = ManagedSettingsResolvedSourceMixed
	resolved.ClientManaged = nil
	data, err = json.Marshal(resolved)
	if err != nil {
		t.Fatalf("failed to marshal mixed managed settings resolution: %v", err)
	}
	serialized = nil
	if err := json.Unmarshal(data, &serialized); err != nil {
		t.Fatalf("failed to inspect mixed managed settings resolution: %v", err)
	}
	if serialized["source"] != "mixed" {
		t.Fatalf("expected mixed provenance, got %v", serialized["source"])
	}
	if _, ok := serialized["clientManaged"]; ok {
		t.Fatalf("expected absent clientManaged to be omitted, got %v", serialized)
	}
}
