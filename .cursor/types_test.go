package copilot

import (
	"encoding/json"
	"testing"
)

func TestUserPromptTransformedHookOutput_PreservesEmptyReplacement(t *testing.T) {
	data, err := json.Marshal(UserPromptTransformedHookOutput{
		ModifiedTransformedPrompt: String(""),
	})
	if err != nil {
		t.Fatalf("failed to marshal hook output: %v", err)
	}
	if string(data) != `{"modifiedTransformedPrompt":""}` {
		t.Fatalf("expected empty replacement to be preserved, got %s", data)
	}
}

func TestProviderConfig_JSONIncludesHeaders(t *testing.T) {
	config := ProviderConfig{
		BaseURL: "https://example.com/provider",
		Headers: map[string]string{"Authorization": "Bearer provider-token"},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal provider config: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal provider config: %v", err)
	}

	if decoded["baseUrl"] != "https://example.com/provider" {
		t.Fatalf("expected baseUrl to round-trip, got %v", decoded["baseUrl"])
	}
	headers, ok := decoded["headers"].(map[string]any)
	if !ok {
		t.Fatalf("expected headers object, got %T", decoded["headers"])
	}
	if headers["Authorization"] != "Bearer provider-token" {
		t.Fatalf("expected Authorization header, got %v", headers["Authorization"])
	}
}

func TestSessionSendRequest_JSONIncludesRequestHeaders(t *testing.T) {
	req := sessionSendRequest{
		SessionID:      "session-1",
		Prompt:         "hello",
		RequestHeaders: map[string]string{"Authorization": "Bearer turn-token"},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal session send request: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal session send request: %v", err)
	}

	if decoded["prompt"] != "hello" {
		t.Fatalf("expected prompt to round-trip, got %v", decoded["prompt"])
	}
	headers, ok := decoded["requestHeaders"].(map[string]any)
	if !ok {
		t.Fatalf("expected requestHeaders object, got %T", decoded["requestHeaders"])
	}
	if headers["Authorization"] != "Bearer turn-token" {
		t.Fatalf("expected Authorization header, got %v", headers["Authorization"])
	}
}

func TestProviderConfig_JSONIncludesAllFields(t *testing.T) {
	cfg := ProviderConfig{
		BaseURL:         "https://example.com/provider",
		APIKey:          "test-key",
		Headers:         map[string]string{"Authorization": "Bearer provider-token"},
		ModelID:         "gpt-4o",
		WireModel:       "my-finetune-v3",
		MaxPromptTokens: 100000,
		MaxOutputTokens: 4096,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal ProviderConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ProviderConfig: %v", err)
	}

	if decoded["baseUrl"] != "https://example.com/provider" {
		t.Errorf("expected baseUrl to round-trip, got %v", decoded["baseUrl"])
	}
	if decoded["modelId"] != "gpt-4o" {
		t.Errorf("expected modelId 'gpt-4o', got %v", decoded["modelId"])
	}
	if decoded["wireModel"] != "my-finetune-v3" {
		t.Errorf("expected wireModel 'my-finetune-v3', got %v", decoded["wireModel"])
	}
	if decoded["maxPromptTokens"] != float64(100000) {
		t.Errorf("expected maxPromptTokens 100000, got %v", decoded["maxPromptTokens"])
	}
	if decoded["maxOutputTokens"] != float64(4096) {
		t.Errorf("expected maxOutputTokens 4096, got %v", decoded["maxOutputTokens"])
	}
	headers, ok := decoded["headers"].(map[string]any)
	if !ok {
		t.Fatalf("expected headers object, got %T", decoded["headers"])
	}
	if headers["Authorization"] != "Bearer provider-token" {
		t.Errorf("expected Authorization header, got %v", headers["Authorization"])
	}
}

func TestProviderConfig_JSONOmitsUnsetTokenFields(t *testing.T) {
	cfg := ProviderConfig{BaseURL: "https://example.com/provider"}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal ProviderConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ProviderConfig: %v", err)
	}

	for _, field := range []string{"modelId", "wireModel", "maxPromptTokens", "maxOutputTokens", "headers"} {
		if _, present := decoded[field]; present {
			t.Errorf("expected %q to be omitted when unset, got %v", field, decoded[field])
		}
	}
}

func TestCustomAgentConfig_JSONIncludesModel(t *testing.T) {
	cfg := CustomAgentConfig{
		Name:   "model-agent",
		Prompt: "You are a model agent.",
		Model:  "claude-haiku-4.5",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal CustomAgentConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CustomAgentConfig: %v", err)
	}

	if decoded["model"] != "claude-haiku-4.5" {
		t.Errorf("expected model 'claude-haiku-4.5', got %v", decoded["model"])
	}
	if decoded["name"] != "model-agent" {
		t.Errorf("expected name 'model-agent', got %v", decoded["name"])
	}
}

func TestCustomAgentConfig_JSONIncludesReasoningEffort(t *testing.T) {
	cfg := CustomAgentConfig{
		Name:            "reasoning-agent",
		Prompt:          "Think carefully.",
		ReasoningEffort: "high",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal CustomAgentConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CustomAgentConfig: %v", err)
	}

	if decoded["reasoningEffort"] != "high" {
		t.Errorf("expected reasoningEffort 'high', got %v", decoded["reasoningEffort"])
	}
}

func TestCustomAgentConfig_JSONIncludesEmptyTools(t *testing.T) {
	cfg := CustomAgentConfig{
		Name:   "no-tools-agent",
		Prompt: "You are an agent without tools.",
		Tools:  []string{},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal CustomAgentConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CustomAgentConfig: %v", err)
	}

	rawTools, present := decoded["tools"]
	if !present {
		t.Fatal("expected tools to be present for an empty non-nil slice")
	}
	tools, ok := rawTools.([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", rawTools)
	}
	if len(tools) != 0 {
		t.Fatalf("expected empty tools array, got %v", tools)
	}
}

func TestCustomAgentConfig_JSONOmitsNilTools(t *testing.T) {
	cfg := CustomAgentConfig{
		Name:   "all-tools-agent",
		Prompt: "You are an agent with default tools.",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal CustomAgentConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CustomAgentConfig: %v", err)
	}

	if _, present := decoded["tools"]; present {
		t.Errorf("expected tools to be omitted for nil slice, got %v", decoded["tools"])
	}
}

func TestToolResult_JSONIncludesToolReferences(t *testing.T) {
	result := ToolResult{
		TextResultForLLM: "found 2 tools",
		ResultType:       "success",
		ToolReferences:   []string{"get_weather", "check_status"},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal ToolResult: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ToolResult: %v", err)
	}

	rawRefs, present := decoded["toolReferences"]
	if !present {
		t.Fatal("expected toolReferences to be present")
	}
	refs, ok := rawRefs.([]any)
	if !ok {
		t.Fatalf("expected toolReferences array, got %T", rawRefs)
	}
	if len(refs) != 2 || refs[0] != "get_weather" || refs[1] != "check_status" {
		t.Errorf("unexpected toolReferences: %v", refs)
	}
}

func TestToolResult_JSONOmitsNilToolReferences(t *testing.T) {
	result := ToolResult{
		TextResultForLLM: "ok",
		ResultType:       "success",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal ToolResult: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ToolResult: %v", err)
	}

	if _, present := decoded["toolReferences"]; present {
		t.Errorf("expected toolReferences to be omitted for nil slice, got %v", decoded["toolReferences"])
	}
}

func TestCustomAgentConfig_JSONOmitsModelWhenEmpty(t *testing.T) {
	cfg := CustomAgentConfig{
		Name:   "no-model-agent",
		Prompt: "You are an agent without a model.",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal CustomAgentConfig: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CustomAgentConfig: %v", err)
	}

	if _, present := decoded["model"]; present {
		t.Errorf("expected model to be omitted when empty, got %v", decoded["model"])
	}
	if _, present := decoded["reasoningEffort"]; present {
		t.Errorf("expected reasoningEffort to be omitted when empty, got %v", decoded["reasoningEffort"])
	}
}

func TestTool_JSONIncludesEmptyParameters(t *testing.T) {
	tool := Tool{
		Name:       "accept_anything",
		Parameters: map[string]any{},
	}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("failed to marshal Tool: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal Tool: %v", err)
	}

	rawParameters, present := decoded["parameters"]
	if !present {
		t.Fatal("expected parameters to be present for an empty non-nil map")
	}
	parameters, ok := rawParameters.(map[string]any)
	if !ok {
		t.Fatalf("expected parameters object, got %T", rawParameters)
	}
	if len(parameters) != 0 {
		t.Fatalf("expected empty parameters object, got %v", parameters)
	}
}

func TestTool_JSONOmitsNilParameters(t *testing.T) {
	tool := Tool{Name: "no_parameters"}

	data, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("failed to marshal Tool: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal Tool: %v", err)
	}

	if _, present := decoded["parameters"]; present {
		t.Errorf("expected parameters to be omitted for nil map, got %v", decoded["parameters"])
	}
}

func TestCanvasDeclaration_JSONIncludesEmptyInputSchema(t *testing.T) {
	canvas := CanvasDeclaration{
		ID:          "empty-input",
		DisplayName: "Empty input",
		Description: "Accepts any input.",
		InputSchema: map[string]any{},
	}

	data, err := json.Marshal(canvas)
	if err != nil {
		t.Fatalf("failed to marshal CanvasDeclaration: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CanvasDeclaration: %v", err)
	}

	rawInputSchema, present := decoded["inputSchema"]
	if !present {
		t.Fatal("expected inputSchema to be present for an empty non-nil map")
	}
	inputSchema, ok := rawInputSchema.(map[string]any)
	if !ok {
		t.Fatalf("expected inputSchema object, got %T", rawInputSchema)
	}
	if len(inputSchema) != 0 {
		t.Fatalf("expected empty inputSchema object, got %v", inputSchema)
	}
}

func TestCanvasDeclaration_JSONOmitsNilInputSchema(t *testing.T) {
	canvas := CanvasDeclaration{
		ID:          "no-input-schema",
		DisplayName: "No input schema",
		Description: "Does not declare input.",
	}

	data, err := json.Marshal(canvas)
	if err != nil {
		t.Fatalf("failed to marshal CanvasDeclaration: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal CanvasDeclaration: %v", err)
	}

	if _, present := decoded["inputSchema"]; present {
		t.Errorf("expected inputSchema to be omitted for nil map, got %v", decoded["inputSchema"])
	}
}

func TestElicitationResult_JSONIncludesEmptyContent(t *testing.T) {
	result := ElicitationResult{
		Action:  ElicitationActionAccept,
		Content: map[string]any{},
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal ElicitationResult: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ElicitationResult: %v", err)
	}

	rawContent, present := decoded["content"]
	if !present {
		t.Fatal("expected content to be present for an empty non-nil map")
	}
	content, ok := rawContent.(map[string]any)
	if !ok {
		t.Fatalf("expected content object, got %T", rawContent)
	}
	if len(content) != 0 {
		t.Fatalf("expected empty content object, got %v", content)
	}
}

func TestElicitationResult_JSONOmitsNilContent(t *testing.T) {
	result := ElicitationResult{Action: ElicitationActionCancel}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal ElicitationResult: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal ElicitationResult: %v", err)
	}

	if _, present := decoded["content"]; present {
		t.Errorf("expected content to be omitted for nil map, got %v", decoded["content"])
	}
}
