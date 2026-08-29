package copilot

import (
	"errors"
	"reflect"
	"testing"
)

func TestDefineTool(t *testing.T) {
	t.Run("creates tool with correct name and description", func(t *testing.T) {
		type Params struct {
			Query string `json:"query"`
		}

		tool := DefineTool("search", "Search for something",
			func(params Params, inv ToolInvocation) (any, error) {
				return "result", nil
			})

		if tool.Name != "search" {
			t.Errorf("Expected name 'search', got %q", tool.Name)
		}
		if tool.Description != "Search for something" {
			t.Errorf("Expected description 'Search for something', got %q", tool.Description)
		}
		if tool.Handler == nil {
			t.Error("Expected handler to be set")
		}
		if tool.Parameters == nil {
			t.Error("Expected parameters schema to be generated")
		}
	})

	t.Run("generates schema from struct tags", func(t *testing.T) {
		type Params struct {
			City string `json:"city"`
			Unit string `json:"unit"`
		}

		tool := DefineTool("get_weather", "Get weather",
			func(params Params, inv ToolInvocation) (any, error) {
				return "sunny", nil
			})

		schema := tool.Parameters
		if schema["type"] != "object" {
			t.Errorf("Expected schema type 'object', got %v", schema["type"])
		}

		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("Expected properties to be map, got %T", schema["properties"])
		}

		if _, ok := props["city"]; !ok {
			t.Error("Expected 'city' property in schema")
		}
		if _, ok := props["unit"]; !ok {
			t.Error("Expected 'unit' property in schema")
		}
	})

	t.Run("handler receives typed arguments", func(t *testing.T) {
		type Params struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		}

		var receivedParams Params
		tool := DefineTool("test", "Test tool",
			func(params Params, inv ToolInvocation) (any, error) {
				receivedParams = params
				return "ok", nil
			})

		inv := ToolInvocation{
			SessionID:  "session-1",
			ToolCallID: "call-1",
			ToolName:   "test",
			Arguments: map[string]any{
				"name":  "Alice",
				"count": float64(42), // JSON numbers are float64
			},
		}

		_, err := tool.Handler(inv)
		if err != nil {
			t.Fatalf("Handler returned error: %v", err)
		}

		if receivedParams.Name != "Alice" {
			t.Errorf("Expected name 'Alice', got %q", receivedParams.Name)
		}
		if receivedParams.Count != 42 {
			t.Errorf("Expected count 42, got %d", receivedParams.Count)
		}
	})

	t.Run("handler receives ToolInvocation", func(t *testing.T) {
		type Params struct{}

		var receivedInv ToolInvocation
		tool := DefineTool("test", "Test tool",
			func(params Params, inv ToolInvocation) (any, error) {
				receivedInv = inv
				return "ok", nil
			})

		inv := ToolInvocation{
			SessionID:  "session-123",
			ToolCallID: "call-456",
			ToolName:   "test",
			Arguments:  map[string]any{},
		}

		tool.Handler(inv)

		if receivedInv.SessionID != "session-123" {
			t.Errorf("Expected SessionID 'session-123', got %q", receivedInv.SessionID)
		}
		if receivedInv.ToolCallID != "call-456" {
			t.Errorf("Expected ToolCallID 'call-456', got %q", receivedInv.ToolCallID)
		}
	})

	t.Run("handler error is propagated", func(t *testing.T) {
		type Params struct{}

		tool := DefineTool("failing", "A failing tool",
			func(params Params, inv ToolInvocation) (any, error) {
				return nil, errors.New("something went wrong")
			})

		inv := ToolInvocation{
			Arguments: map[string]any{},
		}

		_, err := tool.Handler(inv)
		if err == nil {
			t.Fatal("Expected error, got nil")
		}
		if err.Error() != "something went wrong" {
			t.Errorf("Expected error 'something went wrong', got %q", err.Error())
		}
	})
}

func TestNormalizeResult(t *testing.T) {
	t.Run("nil returns empty success result", func(t *testing.T) {
		result, err := normalizeResult(nil)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.TextResultForLLM != "" {
			t.Errorf("Expected empty TextResultForLLM, got %q", result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected ResultType 'success', got %q", result.ResultType)
		}
	})

	t.Run("string passes through directly", func(t *testing.T) {
		result, err := normalizeResult("hello world")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.TextResultForLLM != "hello world" {
			t.Errorf("Expected 'hello world', got %q", result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected ResultType 'success', got %q", result.ResultType)
		}
	})

	t.Run("ToolResult passes through directly", func(t *testing.T) {
		input := ToolResult{
			TextResultForLLM: "custom result",
			ResultType:       "failure",
			Error:            "some error",
		}

		result, err := normalizeResult(input)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.TextResultForLLM != "custom result" {
			t.Errorf("Expected 'custom result', got %q", result.TextResultForLLM)
		}
		if result.ResultType != "failure" {
			t.Errorf("Expected ResultType 'failure', got %q", result.ResultType)
		}
		if result.Error != "some error" {
			t.Errorf("Expected Error 'some error', got %q", result.Error)
		}
	})

	t.Run("struct is JSON serialized", func(t *testing.T) {
		type Response struct {
			Status string `json:"status"`
			Count  int    `json:"count"`
		}

		result, err := normalizeResult(Response{Status: "ok", Count: 5})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		expected := `{"status":"ok","count":5}`
		if result.TextResultForLLM != expected {
			t.Errorf("Expected %q, got %q", expected, result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected ResultType 'success', got %q", result.ResultType)
		}
	})

	t.Run("map is JSON serialized", func(t *testing.T) {
		result, err := normalizeResult(map[string]any{
			"key": "value",
		})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		expected := `{"key":"value"}`
		if result.TextResultForLLM != expected {
			t.Errorf("Expected %q, got %q", expected, result.TextResultForLLM)
		}
	})

	t.Run("slice is JSON serialized", func(t *testing.T) {
		result, err := normalizeResult([]string{"a", "b", "c"})
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		expected := `["a","b","c"]`
		if result.TextResultForLLM != expected {
			t.Errorf("Expected %q, got %q", expected, result.TextResultForLLM)
		}
	})

	t.Run("returns error for unserializable value", func(t *testing.T) {
		// Channels cannot be JSON serialized
		ch := make(chan int)
		_, err := normalizeResult(ch)
		if err == nil {
			t.Fatal("Expected error for unserializable value, got nil")
		}
	})
}

func TestConvertMCPCallToolResult(t *testing.T) {
	t.Run("typed CallToolResult struct is converted", func(t *testing.T) {
		type Resource struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		}
		type ContentBlock struct {
			Type     string    `json:"type"`
			Resource *Resource `json:"resource,omitempty"`
		}
		type CallToolResult struct {
			Content []ContentBlock `json:"content"`
		}

		input := CallToolResult{
			Content: []ContentBlock{
				{
					Type:     "resource",
					Resource: &Resource{URI: "file:///report.txt", Text: "details"},
				},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.TextResultForLLM != "details" {
			t.Errorf("Expected 'details', got %q", result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected 'success', got %q", result.ResultType)
		}
	})

	t.Run("text-only CallToolResult is converted", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "hello"},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.TextResultForLLM != "hello" {
			t.Errorf("Expected 'hello', got %q", result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected 'success', got %q", result.ResultType)
		}
	})

	t.Run("multiple text blocks are joined with newline", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "line 1"},
				map[string]any{"type": "text", "text": "line 2"},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.TextResultForLLM != "line 1\nline 2" {
			t.Errorf("Expected 'line 1\\nline 2', got %q", result.TextResultForLLM)
		}
	})

	t.Run("isError maps to failure resultType", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{"type": "text", "text": "oops"},
			},
			"isError": true,
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.ResultType != "failure" {
			t.Errorf("Expected 'failure', got %q", result.ResultType)
		}
	})

	t.Run("image content becomes binaryResultsForLLM", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{"type": "image", "data": "base64data", "mimeType": "image/png"},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if len(result.BinaryResultsForLLM) != 1 {
			t.Fatalf("Expected 1 binary result, got %d", len(result.BinaryResultsForLLM))
		}
		if result.BinaryResultsForLLM[0].Data != "base64data" {
			t.Errorf("Expected data 'base64data', got %q", result.BinaryResultsForLLM[0].Data)
		}
		if result.BinaryResultsForLLM[0].MIMEType != "image/png" {
			t.Errorf("Expected mimeType 'image/png', got %q", result.BinaryResultsForLLM[0].MIMEType)
		}
	})

	t.Run("resource text goes to textResultForLLM", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{
					"type":     "resource",
					"resource": map[string]any{"uri": "file:///tmp/data.txt", "text": "file contents"},
				},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.TextResultForLLM != "file contents" {
			t.Errorf("Expected 'file contents', got %q", result.TextResultForLLM)
		}
	})

	t.Run("resource blob goes to binaryResultsForLLM", func(t *testing.T) {
		input := map[string]any{
			"content": []any{
				map[string]any{
					"type":     "resource",
					"resource": map[string]any{"uri": "file:///img.png", "blob": "blobdata", "mimeType": "image/png"},
				},
			},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if len(result.BinaryResultsForLLM) != 1 {
			t.Fatalf("Expected 1 binary result, got %d", len(result.BinaryResultsForLLM))
		}
		if result.BinaryResultsForLLM[0].Description != "file:///img.png" {
			t.Errorf("Expected description 'file:///img.png', got %q", result.BinaryResultsForLLM[0].Description)
		}
	})

	t.Run("non-CallToolResult map returns false", func(t *testing.T) {
		input := map[string]any{
			"key": "value",
		}

		_, ok := ConvertMCPCallToolResult(input)
		if ok {
			t.Error("Expected ConvertMCPCallToolResult to return false for non-CallToolResult map")
		}
	})

	t.Run("empty content array is converted", func(t *testing.T) {
		input := map[string]any{
			"content": []any{},
		}

		result, ok := ConvertMCPCallToolResult(input)
		if !ok {
			t.Fatal("Expected ConvertMCPCallToolResult to succeed")
		}
		if result.TextResultForLLM != "" {
			t.Errorf("Expected empty text, got %q", result.TextResultForLLM)
		}
		if result.ResultType != "success" {
			t.Errorf("Expected 'success', got %q", result.ResultType)
		}
	})
}

func TestGenerateSchemaForType(t *testing.T) {
	t.Run("generates schema for simple struct", func(t *testing.T) {
		type Simple struct {
			Name string `json:"name"`
			Age  int    `json:"age"`
		}

		schema := generateSchemaForType(reflect.TypeOf(Simple{}))

		if schema["type"] != "object" {
			t.Errorf("Expected type 'object', got %v", schema["type"])
		}

		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("Expected properties map, got %T", schema["properties"])
		}

		nameProp, ok := props["name"].(map[string]any)
		if !ok {
			t.Fatal("Expected 'name' property")
		}
		if nameProp["type"] != "string" {
			t.Errorf("Expected name type 'string', got %v", nameProp["type"])
		}

		ageProp, ok := props["age"].(map[string]any)
		if !ok {
			t.Fatal("Expected 'age' property")
		}
		if ageProp["type"] != "integer" {
			t.Errorf("Expected age type 'integer', got %v", ageProp["type"])
		}
	})

	t.Run("handles nested structs", func(t *testing.T) {
		type Address struct {
			City    string `json:"city"`
			Country string `json:"country"`
		}
		type Person struct {
			Name    string  `json:"name"`
			Address Address `json:"address"`
		}

		schema := generateSchemaForType(reflect.TypeOf(Person{}))

		props := schema["properties"].(map[string]any)
		addrProp, ok := props["address"].(map[string]any)
		if !ok {
			t.Fatal("Expected 'address' property")
		}

		// Nested struct should have properties
		addrProps, ok := addrProp["properties"].(map[string]any)
		if !ok {
			t.Fatal("Expected address to have properties")
		}
		if _, ok := addrProps["city"]; !ok {
			t.Error("Expected 'city' in address properties")
		}
	})

	t.Run("handles pointer types", func(t *testing.T) {
		type Params struct {
			Value string `json:"value"`
		}

		schema := generateSchemaForType(reflect.TypeOf(&Params{}))

		if schema["type"] != "object" {
			t.Errorf("Expected type 'object', got %v", schema["type"])
		}

		props := schema["properties"].(map[string]any)
		if _, ok := props["value"]; !ok {
			t.Error("Expected 'value' property")
		}
	})

	t.Run("handles nil type", func(t *testing.T) {
		schema := generateSchemaForType(nil)

		if schema != nil {
			t.Errorf("Expected nil schema for nil type, got %v", schema)
		}
	})

	t.Run("handles slices", func(t *testing.T) {
		type Params struct {
			Tags []string `json:"tags"`
		}

		schema := generateSchemaForType(reflect.TypeOf(Params{}))

		props := schema["properties"].(map[string]any)
		tagsProp, ok := props["tags"].(map[string]any)
		if !ok {
			t.Fatal("Expected 'tags' property")
		}

		// Schema library may return "array" or ["null", "array"] for slices
		tagType := tagsProp["type"]
		switch v := tagType.(type) {
		case string:
			if v != "array" {
				t.Errorf("Expected tags type 'array', got %v", v)
			}
		case []any:
			hasArray := false
			for _, item := range v {
				if item == "array" {
					hasArray = true
					break
				}
			}
			if !hasArray {
				t.Errorf("Expected tags type to include 'array', got %v", v)
			}
		default:
			t.Errorf("Expected tags type to be string or array, got %T: %v", tagType, tagType)
		}
	})
}
