package rpc

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
)

func TestExternalToolResultJSONUnion(t *testing.T) {
	var stringResult ExternalToolResult = ExternalToolStringResult("tool result")
	raw, err := json.Marshal(stringResult)
	if err != nil {
		t.Fatalf("marshal string result: %v", err)
	}
	if string(raw) != `"tool result"` {
		t.Fatalf("marshal string result = %s", raw)
	}

	decodedString, err := unmarshalExternalToolResult([]byte(`"tool result"`))
	if err != nil {
		t.Fatalf("unmarshal string result: %v", err)
	}
	decodedStringValue, ok := decodedString.(ExternalToolStringResult)
	if !ok || string(decodedStringValue) != "tool result" {
		t.Fatalf("unmarshal string result = %#v", decodedString)
	}

	var objectResult ExternalToolResult = &ExternalToolTextResultForLlm{TextResultForLlm: "expanded"}
	raw, err = json.Marshal(objectResult)
	if err != nil {
		t.Fatalf("marshal object result: %v", err)
	}
	if string(raw) != `{"textResultForLlm":"expanded"}` {
		t.Fatalf("marshal object result = %s", raw)
	}

	decodedObject, err := unmarshalExternalToolResult([]byte(`{"textResultForLlm":"expanded"}`))
	if err != nil {
		t.Fatalf("unmarshal object result: %v", err)
	}
	decodedObjectValue, ok := decodedObject.(*ExternalToolTextResultForLlm)
	if !ok || decodedObjectValue.TextResultForLlm != "expanded" {
		t.Fatalf("unmarshal object result = %#v", decodedObject)
	}
}

func TestFilterMappingJSONUnion(t *testing.T) {
	var mapping FilterMapping = FilterMappingEnumMap{"secret": ContentFilterModeHiddenCharacters}
	raw, err := json.Marshal(mapping)
	if err != nil {
		t.Fatalf("marshal filter mapping map: %v", err)
	}
	if string(raw) != `{"secret":"hidden_characters"}` {
		t.Fatalf("marshal filter mapping map = %s", raw)
	}

	decodedMap, err := unmarshalFilterMapping([]byte(`{"secret":"hidden_characters"}`))
	if err != nil {
		t.Fatalf("unmarshal filter mapping map: %v", err)
	}
	decodedMapValue, ok := decodedMap.(FilterMappingEnumMap)
	if !ok || decodedMapValue["secret"] != ContentFilterModeHiddenCharacters {
		t.Fatalf("unmarshal filter mapping map = %#v", decodedMap)
	}

	var enumValue FilterMapping = ContentFilterModeMarkdown
	raw, err = json.Marshal(enumValue)
	if err != nil {
		t.Fatalf("marshal filter mapping enum: %v", err)
	}
	if string(raw) != `"markdown"` {
		t.Fatalf("marshal filter mapping enum = %s", raw)
	}

	decodedEnum, err := unmarshalFilterMapping([]byte(`"markdown"`))
	if err != nil {
		t.Fatalf("unmarshal filter mapping enum: %v", err)
	}
	decodedEnumValue, ok := decodedEnum.(ContentFilterMode)
	if !ok || decodedEnumValue != ContentFilterModeMarkdown {
		t.Fatalf("unmarshal filter mapping enum = %#v", decodedEnum)
	}
}

func TestMCPServerConfigJSONUnion(t *testing.T) {
	var localConfig MCPServerConfig = &MCPServerConfigStdio{
		Args:    []string{"-v"},
		Command: "node",
	}
	raw, err := json.Marshal(localConfig)
	if err != nil {
		t.Fatalf("marshal local config: %v", err)
	}
	if string(raw) != `{"args":["-v"],"command":"node"}` {
		t.Fatalf("marshal local config = %s", raw)
	}

	decodedLocal, err := unmarshalMCPServerConfig([]byte(`{"args":["-v"],"command":"node"}`))
	if err != nil {
		t.Fatalf("unmarshal local config: %v", err)
	}
	decodedLocalValue, ok := decodedLocal.(*MCPServerConfigStdio)
	if !ok || decodedLocalValue.Command != "node" || len(decodedLocalValue.Args) != 1 || decodedLocalValue.Args[0] != "-v" {
		t.Fatalf("unmarshal local config = %#v", decodedLocal)
	}

	var httpConfig MCPServerConfig = &MCPServerConfigHTTP{URL: "https://example.com/mcp"}
	raw, err = json.Marshal(httpConfig)
	if err != nil {
		t.Fatalf("marshal HTTP config: %v", err)
	}
	if string(raw) != `{"url":"https://example.com/mcp"}` {
		t.Fatalf("marshal HTTP config = %s", raw)
	}

	decodedHTTP, err := unmarshalMCPServerConfig([]byte(`{"url":"https://example.com/mcp"}`))
	if err != nil {
		t.Fatalf("unmarshal HTTP config: %v", err)
	}
	decodedHTTPValue, ok := decodedHTTP.(*MCPServerConfigHTTP)
	if !ok || decodedHTTPValue.URL != "https://example.com/mcp" {
		t.Fatalf("unmarshal HTTP config = %#v", decodedHTTP)
	}

	decodedRaw, err := unmarshalMCPServerConfig([]byte(`{"name":"future"}`))
	if err != nil {
		t.Fatalf("unmarshal raw config: %v", err)
	}
	if _, ok := decodedRaw.(*RawMCPServerConfigData); !ok {
		t.Fatalf("unmarshal raw config = %T, want *RawMCPServerConfigData", decodedRaw)
	}
}

func TestTaskProgressUnmarshalsTaskAgentProgressVariants(t *testing.T) {
	agentProgress, err := unmarshalTaskProgress([]byte(`{"type":"agent","recentActivity":[],"latestIntent":"Summarizing"}`))
	if err != nil {
		t.Fatalf("unmarshal agent task progress: %v", err)
	}
	agentValue, ok := agentProgress.(*TaskAgentProgress)
	if !ok {
		t.Fatalf("agent task progress = %T, want *TaskAgentProgress", agentProgress)
	}
	if agentValue.LatestIntent == nil || *agentValue.LatestIntent != "Summarizing" {
		t.Fatalf("agent latest intent = %v, want Summarizing", agentValue.LatestIntent)
	}

	shellProgress, err := unmarshalTaskProgress([]byte(`{"type":"shell","recentOutput":"building","pid":123}`))
	if err != nil {
		t.Fatalf("unmarshal shell task progress: %v", err)
	}
	shellValue, ok := shellProgress.(*TaskShellProgress)
	if !ok {
		t.Fatalf("shell task progress = %T, want *TaskShellProgress", shellProgress)
	}
	if shellValue.RecentOutput != "building" {
		t.Fatalf("shell recent output = %q, want building", shellValue.RecentOutput)
	}
	if shellValue.Pid == nil || *shellValue.Pid != 123 {
		t.Fatalf("shell pid = %v, want 123", shellValue.Pid)
	}
}

func TestCommandsInvokeUnmarshalsSlashCommandInvocationResult(t *testing.T) {
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()

	client := jsonrpc2.NewClient(clientToServerWriter, serverToClientReader)
	server := jsonrpc2.NewClient(serverToClientWriter, clientToServerReader)
	server.SetRequestHandler("session.commands.invoke", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		var request struct {
			Input     string `json:"input"`
			Name      string `json:"name"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal(params, &request); err != nil {
			return nil, &jsonrpc2.Error{Code: -32602, Message: err.Error()}
		}
		if request.SessionID != "session-1" || request.Name != "help" || request.Input != "details" {
			return nil, &jsonrpc2.Error{Code: -32602, Message: "unexpected invoke request"}
		}
		return json.RawMessage(`{"kind":"text","text":"hello","markdown":true}`), nil
	})

	client.Start()
	server.Start()
	t.Cleanup(func() {
		client.Stop()
		server.Stop()
		_ = clientToServerWriter.Close()
		_ = clientToServerReader.Close()
		_ = serverToClientWriter.Close()
		_ = serverToClientReader.Close()
	})

	input := "details"
	result, err := NewSessionRPC(client, "session-1").Commands.Invoke(t.Context(), &CommandsInvokeRequest{
		Input: &input,
		Name:  "help",
	})
	if err != nil {
		t.Fatalf("invoke command: %v", err)
	}
	textResult, ok := result.(*SlashCommandTextResult)
	if !ok {
		t.Fatalf("invoke result = %T, want *SlashCommandTextResult", result)
	}
	if textResult.Text != "hello" {
		t.Fatalf("invoke result text = %q, want hello", textResult.Text)
	}
	if textResult.Markdown == nil || !*textResult.Markdown {
		t.Fatalf("invoke result markdown = %v, want true", textResult.Markdown)
	}
}

func TestQueuedCommandResultBoolDiscriminatorJSONUnion(t *testing.T) {
	stopProcessingQueue := true
	var handled QueuedCommandResult = &QueuedCommandHandled{StopProcessingQueue: &stopProcessingQueue}
	raw, err := json.Marshal(handled)
	if err != nil {
		t.Fatalf("marshal handled result: %v", err)
	}
	if string(raw) != `{"handled":true,"stopProcessingQueue":true}` {
		t.Fatalf("marshal handled result = %s", raw)
	}

	decodedHandled, err := unmarshalQueuedCommandResult([]byte(`{"handled":true,"stopProcessingQueue":true}`))
	if err != nil {
		t.Fatalf("unmarshal handled result: %v", err)
	}
	decodedHandledValue, ok := decodedHandled.(*QueuedCommandHandled)
	if !ok {
		t.Fatalf("unmarshal handled result = %T, want *QueuedCommandHandled", decodedHandled)
	}
	if decodedHandledValue.StopProcessingQueue == nil || !*decodedHandledValue.StopProcessingQueue {
		t.Fatalf("unmarshal handled stopProcessingQueue = %v, want true", decodedHandledValue.StopProcessingQueue)
	}

	var notHandled QueuedCommandResult = &QueuedCommandNotHandled{}
	raw, err = json.Marshal(notHandled)
	if err != nil {
		t.Fatalf("marshal not handled result: %v", err)
	}
	if string(raw) != `{"handled":false}` {
		t.Fatalf("marshal not handled result = %s", raw)
	}

	decodedNotHandled, err := unmarshalQueuedCommandResult([]byte(`{"handled":false}`))
	if err != nil {
		t.Fatalf("unmarshal not handled result: %v", err)
	}
	if _, ok := decodedNotHandled.(*QueuedCommandNotHandled); !ok {
		t.Fatalf("unmarshal not handled result = %T, want *QueuedCommandNotHandled", decodedNotHandled)
	}
}

func TestUIElicitationFieldValueJSONUnion(t *testing.T) {
	raw, err := json.Marshal(UIElicitationBooleanValue(true))
	if err != nil {
		t.Fatalf("marshal bool value: %v", err)
	}
	if string(raw) != `true` {
		t.Fatalf("marshal bool value = %s", raw)
	}

	var response UIElicitationResponse
	if err := json.Unmarshal([]byte(`{"action":"accept","content":{"choices":["a","b"]}}`), &response); err != nil {
		t.Fatalf("unmarshal response with string array value: %v", err)
	}
	decodedArray, ok := response.Content["choices"].(UIElicitationStringArrayValue)
	if !ok {
		t.Fatalf("unmarshal string array value = %T, want UIElicitationStringArrayValue", response.Content["choices"])
	}
	if len(decodedArray) != 2 || decodedArray[0] != "a" || decodedArray[1] != "b" {
		t.Fatalf("unmarshal string array value = %#v", decodedArray)
	}
}

func TestUIElicitationSchemaPropertyJSONUnion(t *testing.T) {
	var schema UIElicitationSchema
	if err := json.Unmarshal([]byte(`{
		"type":"object",
		"properties":{
			"confirmed":{"type":"boolean","default":true},
			"choice":{"type":"string","enum":["a","b"]},
			"freeform":{"type":"string","minLength":1},
			"count":{"type":"integer","minimum":0},
			"arrayChoice":{"type":"array","items":{"type":"string","enum":["a","b"]}},
			"arrayAnyOf":{"type":"array","items":{"anyOf":[{"const":"a","title":"A"}]}}
		},
		"required":["confirmed"]
	}`), &schema); err != nil {
		t.Fatalf("unmarshal elicitation schema: %v", err)
	}

	confirmed, ok := schema.Properties["confirmed"].(*UIElicitationSchemaPropertyBoolean)
	if !ok {
		t.Fatalf("confirmed property = %T, want *UIElicitationSchemaPropertyBoolean", schema.Properties["confirmed"])
	}
	if confirmed.Default == nil || !*confirmed.Default {
		t.Fatalf("confirmed default = %v, want true", confirmed.Default)
	}

	choice, ok := schema.Properties["choice"].(*UIElicitationStringEnumField)
	if !ok {
		t.Fatalf("choice property = %T, want *UIElicitationStringEnumField", schema.Properties["choice"])
	}
	if len(choice.Enum) != 2 || choice.Enum[0] != "a" || choice.Enum[1] != "b" {
		t.Fatalf("choice enum = %#v", choice.Enum)
	}

	freeform, ok := schema.Properties["freeform"].(*UIElicitationSchemaPropertyString)
	if !ok {
		t.Fatalf("freeform property = %T, want *UIElicitationSchemaPropertyString", schema.Properties["freeform"])
	}
	if freeform.MinLength == nil || *freeform.MinLength != 1 {
		t.Fatalf("freeform minLength = %v, want 1", freeform.MinLength)
	}

	count, ok := schema.Properties["count"].(*UIElicitationSchemaPropertyNumber)
	if !ok {
		t.Fatalf("count property = %T, want *UIElicitationSchemaPropertyNumber", schema.Properties["count"])
	}
	if count.Discriminator != UIElicitationSchemaPropertyNumberTypeInteger {
		t.Fatalf("count type = %q, want %q", count.Discriminator, UIElicitationSchemaPropertyNumberTypeInteger)
	}

	arrayChoice, ok := schema.Properties["arrayChoice"].(*UIElicitationArrayEnumField)
	if !ok {
		t.Fatalf("arrayChoice property = %T, want *UIElicitationArrayEnumField", schema.Properties["arrayChoice"])
	}
	if len(arrayChoice.Items.Enum) != 2 || arrayChoice.Items.Enum[0] != "a" || arrayChoice.Items.Enum[1] != "b" {
		t.Fatalf("arrayChoice items enum = %#v", arrayChoice.Items.Enum)
	}

	arrayAnyOf, ok := schema.Properties["arrayAnyOf"].(*UIElicitationArrayAnyOfField)
	if !ok {
		t.Fatalf("arrayAnyOf property = %T, want *UIElicitationArrayAnyOfField", schema.Properties["arrayAnyOf"])
	}
	if len(arrayAnyOf.Items.AnyOf) != 1 || arrayAnyOf.Items.AnyOf[0].Const != "a" || arrayAnyOf.Items.AnyOf[0].Title != "A" {
		t.Fatalf("arrayAnyOf items anyOf = %#v", arrayAnyOf.Items.AnyOf)
	}

	defaultValue := true
	encoded, err := json.Marshal(UIElicitationSchema{
		Type: UIElicitationSchemaTypeObject,
		Properties: map[string]UIElicitationSchemaProperty{
			"confirmed": &UIElicitationSchemaPropertyBoolean{Default: &defaultValue},
		},
	})
	if err != nil {
		t.Fatalf("marshal elicitation schema: %v", err)
	}
	var roundTrip UIElicitationSchema
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("unmarshal marshaled elicitation schema: %v", err)
	}
	if _, ok := roundTrip.Properties["confirmed"].(*UIElicitationSchemaPropertyBoolean); !ok {
		t.Fatalf("round-trip confirmed property = %T, want *UIElicitationSchemaPropertyBoolean", roundTrip.Properties["confirmed"])
	}
}
