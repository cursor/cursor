/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// DefineTool creates a Tool with automatic JSON schema generation from a typed handler function.
// The handler receives typed arguments (automatically unmarshaled from JSON) and the raw ToolInvocation.
// The handler can return any value - strings pass through directly, other types are JSON-serialized.
//
// Example:
//
//	type GetWeatherParams struct {
//	    City string `json:"city" jsonschema:"city name"`
//	    Unit string `json:"unit" jsonschema:"temperature unit (celsius or fahrenheit)"`
//	}
//
//	tool := copilot.DefineTool("get_weather", "Get weather for a city",
//	    func(params GetWeatherParams, inv copilot.ToolInvocation) (any, error) {
//	        return fmt.Sprintf("Weather in %s: 22°%s", params.City, params.Unit), nil
//	    })
func DefineTool[T any, U any](name, description string, handler func(T, ToolInvocation) (U, error)) Tool {
	var zero T
	schema := generateSchemaForType(reflect.TypeOf(zero))

	return Tool{
		Name:        name,
		Description: description,
		Parameters:  schema,
		Handler:     createTypedHandler(handler),
	}
}

// createTypedHandler wraps a typed handler function into the standard ToolHandler signature.
func createTypedHandler[T any, U any](handler func(T, ToolInvocation) (U, error)) ToolHandler {
	return func(inv ToolInvocation) (ToolResult, error) {
		var params T

		// Convert arguments to typed struct via JSON round-trip
		// Arguments is already map[string]any from JSON-RPC parsing
		jsonBytes, err := json.Marshal(inv.Arguments)
		if err != nil {
			return ToolResult{}, fmt.Errorf("failed to marshal arguments: %w", err)
		}

		if err := json.Unmarshal(jsonBytes, &params); err != nil {
			return ToolResult{}, fmt.Errorf("failed to unmarshal arguments into %T: %w", params, err)
		}

		result, err := handler(params, inv)
		if err != nil {
			return ToolResult{}, err
		}

		return normalizeResult(result)
	}
}

// normalizeResult converts any value to a ToolResult.
// Strings pass through directly, ToolResult passes through, and other types
// are JSON-serialized.
func normalizeResult(result any) (ToolResult, error) {
	if result == nil {
		return ToolResult{
			TextResultForLLM: "",
			ResultType:       "success",
		}, nil
	}

	// ToolResult passes through directly
	if tr, ok := result.(ToolResult); ok {
		return tr, nil
	}

	// Strings pass through directly
	if str, ok := result.(string); ok {
		return ToolResult{
			TextResultForLLM: str,
			ResultType:       "success",
		}, nil
	}

	// Everything else gets JSON-serialized
	jsonBytes, err := json.Marshal(result)
	if err != nil {
		return ToolResult{}, fmt.Errorf("failed to serialize result: %w", err)
	}

	return ToolResult{
		TextResultForLLM: string(jsonBytes),
		ResultType:       "success",
	}, nil
}

// ConvertMCPCallToolResult converts an MCP CallToolResult value (a map or struct
// with a "content" array and optional "isError" bool) into a ToolResult.
// Returns the converted ToolResult and true if the value matched the expected
// shape, or a zero ToolResult and false otherwise.
func ConvertMCPCallToolResult(value any) (ToolResult, bool) {
	m, ok := value.(map[string]any)
	if !ok {
		jsonBytes, err := json.Marshal(value)
		if err != nil {
			return ToolResult{}, false
		}

		if err := json.Unmarshal(jsonBytes, &m); err != nil {
			return ToolResult{}, false
		}
	}

	contentRaw, exists := m["content"]
	if !exists {
		return ToolResult{}, false
	}

	contentSlice, ok := contentRaw.([]any)
	if !ok {
		return ToolResult{}, false
	}

	// Verify every element has a string "type" field
	for _, item := range contentSlice {
		block, ok := item.(map[string]any)
		if !ok {
			return ToolResult{}, false
		}
		if _, ok := block["type"].(string); !ok {
			return ToolResult{}, false
		}
	}

	var textParts []string
	var binaryResults []ToolBinaryResult

	for _, item := range contentSlice {
		block := item.(map[string]any)
		blockType := block["type"].(string)

		switch blockType {
		case "text":
			if text, ok := block["text"].(string); ok {
				textParts = append(textParts, text)
			}
		case "image":
			data, _ := block["data"].(string)
			mimeType, _ := block["mimeType"].(string)
			if data == "" {
				continue
			}
			binaryResults = append(binaryResults, ToolBinaryResult{
				Data:     data,
				MIMEType: mimeType,
				Type:     "image",
			})
		case "resource":
			if resRaw, ok := block["resource"].(map[string]any); ok {
				if text, ok := resRaw["text"].(string); ok && text != "" {
					textParts = append(textParts, text)
				}
				if blob, ok := resRaw["blob"].(string); ok && blob != "" {
					mimeType, _ := resRaw["mimeType"].(string)
					if mimeType == "" {
						mimeType = "application/octet-stream"
					}
					uri, _ := resRaw["uri"].(string)
					binaryResults = append(binaryResults, ToolBinaryResult{
						Data:        blob,
						MIMEType:    mimeType,
						Type:        "resource",
						Description: uri,
					})
				}
			}
		}
	}

	resultType := "success"
	if isErr, ok := m["isError"].(bool); ok && isErr {
		resultType = "failure"
	}

	tr := ToolResult{
		TextResultForLLM: strings.Join(textParts, "\n"),
		ResultType:       resultType,
	}
	if len(binaryResults) > 0 {
		tr.BinaryResultsForLLM = binaryResults
	}
	return tr, true
}

// generateSchemaForType generates a JSON schema map from a Go type using reflection.
// Panics if schema generation fails, as this indicates a programming error.
func generateSchemaForType(t reflect.Type) map[string]any {
	if t == nil {
		return nil
	}

	// Handle pointer types
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// Use google/jsonschema-go to generate the schema
	schema, err := jsonschema.ForType(t, nil)
	if err != nil {
		panic(fmt.Sprintf("failed to generate schema for type %v: %v", t, err))
	}

	// Convert schema to map[string]any
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal schema for type %v: %v", t, err))
	}

	var schemaMap map[string]any
	if err := json.Unmarshal(schemaBytes, &schemaMap); err != nil {
		panic(fmt.Sprintf("failed to unmarshal schema for type %v: %v", t, err))
	}

	return schemaMap
}
