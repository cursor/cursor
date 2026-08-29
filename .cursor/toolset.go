/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"fmt"
	"regexp"
)

// ClientMode controls the default surface presented to sessions created by the
// [Client]. The zero value is [ModeCopilotCli], matching the legacy CLI defaults.
//
// Set [ClientOptions.Mode] to [ModeEmpty] to opt in to multi-tenant safe
// defaults: no built-in tools by default (callers must specify
// [SessionConfig.AvailableTools] explicitly), no environment_context section
// in the system message, telemetry off, custom instructions and remote-custom
// agents disabled, etc.
type ClientMode string

const (
	// ModeCopilotCli is the default mode; sessions inherit the full Copilot
	// CLI experience (all built-in tools, host environment_context, etc.).
	ModeCopilotCli ClientMode = "copilot-cli"
	// ModeEmpty is the multi-tenant safe-default mode. Sessions start with
	// no built-in tools, no environment context, and various features
	// (custom instructions, remote agents, telemetry, plugins) off by
	// default. Callers can opt back in field-by-field.
	ModeEmpty ClientMode = "empty"
)

// ToolSet builds a list of source-qualified tool filter patterns
// (`builtin:*`, `mcp:<name>`, `custom:*`, ...) for use with
// [SessionConfig.AvailableTools] or [SessionConfig.ExcludedTools].
//
// Tools are classified by the runtime at registration time (not from name
// parsing), so AddBuiltIn("foo") matches only tools the runtime registered as
// built-in, even if an MCP server or custom-agent extension happens to
// register a tool with the same wire name.
//
// ToolSet's zero value is ready to use. Convert to []string via [ToolSet.ToSlice]
// before passing to [SessionConfig] fields, e.g.
// `(&ToolSet{}).AddBuiltIn(...).ToSlice()`.
type ToolSet struct {
	items []string
}

// NewToolSet returns an empty [ToolSet].
func NewToolSet() *ToolSet { return &ToolSet{} }

var toolNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// AddBuiltIn adds one or more built-in tool patterns. Pass a specific tool
// name (e.g. "bash") or "*" to match all built-in tools.
func (s *ToolSet) AddBuiltIn(names ...string) *ToolSet {
	for _, n := range names {
		validateToolName("builtin", n)
		s.items = append(s.items, "builtin:"+n)
	}
	return s
}

// AddCustom adds a custom-tool pattern. Matches tools registered via
// [SessionConfig.Tools] or via custom agents.
func (s *ToolSet) AddCustom(name string) *ToolSet {
	validateToolName("custom", name)
	s.items = append(s.items, "custom:"+name)
	return s
}

// AddMCP adds an MCP tool pattern. Matches tools advertised by any configured
// MCP server.
func (s *ToolSet) AddMCP(toolName string) *ToolSet {
	validateToolName("mcp", toolName)
	s.items = append(s.items, "mcp:"+toolName)
	return s
}

// ToSlice returns a defensive copy of the accumulated filter strings.
func (s *ToolSet) ToSlice() []string {
	out := make([]string, len(s.items))
	copy(out, s.items)
	return out
}

func validateToolName(kind, name string) {
	if name == "" {
		panic(fmt.Sprintf("invalid %s tool name: must not be empty", kind))
	}
	if name == "*" {
		return
	}
	if !toolNameRegex.MatchString(name) {
		panic(fmt.Sprintf(
			"invalid %s tool name %q: tool names must match /^[a-zA-Z0-9_-]+$/ or be the wildcard %q",
			kind, name, "*"))
	}
}

// BuiltInToolsIsolated lists built-in tools that operate only within the
// bounds of a single session — no host filesystem access outside the session,
// no cross-session state, no host environment access, no network. Safe to
// enable in [ModeEmpty] scenarios (e.g. multi-tenant servers) without leaking
// host capabilities.
//
// Contract: tools in this set MUST NOT be extended (even behind options or
// args) to read or write state outside the session boundary. Adding
// cross-session or host-state behavior to one of these tools is a breaking
// change that requires removing it from this set.
var BuiltInToolsIsolated = []string{
	"ask_user",
	"task_complete",
	"exit_plan_mode",
	"task",
	"read_agent",
	"write_agent",
	"list_agents",
	"send_inbox",
	"context_board",
	"skill",
}
