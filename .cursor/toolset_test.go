/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestToolSet_emitsSourceQualifiedStrings(t *testing.T) {
	items := NewToolSet().
		AddBuiltIn("bash").
		AddBuiltIn("*").
		AddCustom("my_tool").
		AddCustom("*").
		AddMCP("github-list_issues").
		AddMCP("*").
		ToSlice()
	want := []string{
		"builtin:bash",
		"builtin:*",
		"custom:my_tool",
		"custom:*",
		"mcp:github-list_issues",
		"mcp:*",
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %v, want %v", items, want)
	}
}

func TestToolSet_addBuiltInVariadic(t *testing.T) {
	items := NewToolSet().AddBuiltIn("bash", "view").ToSlice()
	want := []string{"builtin:bash", "builtin:view"}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %v, want %v", items, want)
	}
}

func TestToolSet_toSliceReturnsDefensiveCopy(t *testing.T) {
	set := NewToolSet().AddBuiltIn("bash")
	a := set.ToSlice()
	a[0] = "builtin:tampered"
	if got := set.ToSlice(); !reflect.DeepEqual(got, []string{"builtin:bash"}) {
		t.Errorf("internal state mutated: %v", got)
	}
}

func TestToolSet_rejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name string
		fn   func()
	}{
		{"colon in builtin", func() { NewToolSet().AddBuiltIn("has:colon") }},
		{"space in mcp", func() { NewToolSet().AddMCP("has space") }},
		{"empty custom", func() { NewToolSet().AddCustom("") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Fatal("expected panic, got none")
				}
			}()
			c.fn()
		})
	}
}

func TestBuiltInToolsIsolated_membership(t *testing.T) {
	for _, banned := range []string{"bash", "edit", "grep", "web_fetch"} {
		if slices.Contains(BuiltInToolsIsolated, banned) {
			t.Errorf("isolated set must not contain %q", banned)
		}
	}
	for _, expected := range []string{"ask_user", "task_complete"} {
		if !slices.Contains(BuiltInToolsIsolated, expected) {
			t.Errorf("isolated set must contain %q", expected)
		}
	}
}

func TestNewClient_modeEmptyRejectsWithoutStorage(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic, got none")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T", r)
		}
		if !strings.Contains(strings.ToLower(msg), "empty") {
			t.Errorf("panic message should mention empty mode, got %q", msg)
		}
	}()
	NewClient(&ClientOptions{Mode: ModeEmpty})
}

func TestNewClient_modeEmptyAcceptsBaseDirectory(t *testing.T) {
	c := NewClient(&ClientOptions{
		Mode:          ModeEmpty,
		BaseDirectory: t.TempDir(),
	})
	if c.options.Mode != ModeEmpty {
		t.Errorf("expected ModeEmpty, got %q", c.options.Mode)
	}
}

func TestNewClient_modeEmptyAcceptsURIConnection(t *testing.T) {
	c := NewClient(&ClientOptions{
		Mode:       ModeEmpty,
		Connection: URIConnection{URL: "8080"},
	})
	if c.options.Mode != ModeEmpty {
		t.Errorf("expected ModeEmpty, got %q", c.options.Mode)
	}
}

func TestNewClient_modeCopilotCliIsDefault(t *testing.T) {
	c := NewClient(nil)
	if c.options.Mode != "" && c.options.Mode != ModeCopilotCli {
		t.Errorf("expected default mode to be empty/copilot-cli, got %q", c.options.Mode)
	}
}

func TestValidateToolFilterList_rejectsBareWildcard(t *testing.T) {
	err := validateToolFilterList("availableTools", []string{"builtin:bash", "*"})
	if err == nil {
		t.Fatal("expected error for bare wildcard")
	}
	if !strings.Contains(err.Error(), "bare wildcard") {
		t.Errorf("expected message about bare wildcard, got %q", err.Error())
	}
}

func TestValidateToolFilterList_allowsSourceQualifiedWildcards(t *testing.T) {
	if err := validateToolFilterList("availableTools", []string{"builtin:*", "mcp:*", "custom:*"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveToolFilterOptions_emptyModeRequiresAvailableTools(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	_, _, _, err := c.resolveToolFilterOptions(nil, nil)
	if err == nil {
		t.Fatal("expected error in empty mode without available tools")
	}
}

func TestResolveToolFilterOptions_setsExcludedPrecedence(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	_, _, precedence, err := c.resolveToolFilterOptions(nil, []string{"builtin:bash"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if precedence == nil || *precedence != "excluded" {
		t.Errorf("expected precedence 'excluded', got %v", precedence)
	}
}

func TestSystemMessageForMode_emptyModeStripsEnvContextWhenNil(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	got := c.systemMessageForMode(nil)
	if got == nil || got.Mode != "customize" {
		t.Fatalf("expected customize mode, got %+v", got)
	}
	if action, ok := got.Sections["environment_context"]; !ok || action.Action != SectionActionRemove {
		t.Errorf("expected environment_context: remove, got %+v", got.Sections)
	}
}

func TestSystemMessageForMode_emptyModePromotesAppendToCustomize(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	got := c.systemMessageForMode(&SystemMessageConfig{Mode: "append", Content: "extra"})
	if got.Mode != "customize" {
		t.Errorf("expected customize, got %q", got.Mode)
	}
	if got.Content != "extra" {
		t.Errorf("expected content preserved, got %q", got.Content)
	}
	if action, ok := got.Sections["environment_context"]; !ok || action.Action != SectionActionRemove {
		t.Errorf("expected environment_context removed")
	}
}

func TestSystemMessageForMode_emptyModePreservesReplace(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	in := &SystemMessageConfig{Mode: "replace", Content: "whole prompt"}
	got := c.systemMessageForMode(in)
	if got != in {
		t.Errorf("expected verbatim passthrough for replace, got %+v", got)
	}
}

func TestSystemMessageForMode_emptyModeRespectsCallerSection(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	in := &SystemMessageConfig{
		Mode: "customize",
		Sections: map[string]SectionOverride{
			"environment_context": {Action: SectionActionReplace, Content: "custom"},
		},
	}
	got := c.systemMessageForMode(in)
	if got != in {
		t.Errorf("expected caller's section override preserved verbatim")
	}
}

func TestSystemMessageForMode_copilotCliPassthrough(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	in := &SystemMessageConfig{Mode: "append", Content: "x"}
	got := c.systemMessageForMode(in)
	if got != in {
		t.Errorf("non-empty mode must not alter system message")
	}
}

func TestApplyConfigDefaultsForMode_emptyDefaultsTelemetryFalse(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.EnableSessionTelemetry == nil || *cfg.EnableSessionTelemetry != false {
		t.Errorf("expected telemetry default false in empty mode, got %v", cfg.EnableSessionTelemetry)
	}
}

func TestApplyConfigDefaultsForMode_emptyDefaultsExperimentalModeFalse(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.EnableExperimentalMode == nil || *cfg.EnableExperimentalMode != false {
		t.Errorf("expected experimental mode default false in empty mode, got %v", cfg.EnableExperimentalMode)
	}
}

func TestApplyConfigDefaultsForMode_copilotCliLeavesExperimentalModeNil(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.EnableExperimentalMode != nil {
		t.Errorf("non-empty mode must not default experimental mode")
	}
}

func TestApplyConfigDefaultsForMode_emptyHonorsCallerTelemetry(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	trueVal := true
	cfg := &SessionConfig{EnableSessionTelemetry: &trueVal}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.EnableSessionTelemetry == nil || *cfg.EnableSessionTelemetry != true {
		t.Errorf("caller-supplied telemetry must win")
	}
}

func TestApplyConfigDefaultsForMode_copilotCliLeavesNil(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.EnableSessionTelemetry != nil {
		t.Errorf("non-empty mode must not default telemetry")
	}
}

func TestApplyConfigDefaultsForMode_emptyDefaultsGranularFlags(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.SkipEmbeddingRetrieval == nil || *cfg.SkipEmbeddingRetrieval != true {
		t.Errorf("expected SkipEmbeddingRetrieval=true in empty mode, got %v", cfg.SkipEmbeddingRetrieval)
	}
	if cfg.EmbeddingCacheStorage == nil || *cfg.EmbeddingCacheStorage != *String("in-memory") {
		t.Errorf("expected EmbeddingCacheStorage=in-memory in empty mode, got %v", cfg.EmbeddingCacheStorage)
	}
	if cfg.EnableOnDemandInstructionDiscovery == nil || *cfg.EnableOnDemandInstructionDiscovery != false {
		t.Errorf("expected EnableOnDemandInstructionDiscovery=false in empty mode, got %v", cfg.EnableOnDemandInstructionDiscovery)
	}
	if cfg.EnableFileHooks == nil || *cfg.EnableFileHooks != false {
		t.Errorf("expected EnableFileHooks=false in empty mode, got %v", cfg.EnableFileHooks)
	}
	if cfg.EnableHostGitOperations == nil || *cfg.EnableHostGitOperations != false {
		t.Errorf("expected EnableHostGitOperations=false in empty mode, got %v", cfg.EnableHostGitOperations)
	}
	if cfg.EnableSessionStore == nil || *cfg.EnableSessionStore != false {
		t.Errorf("expected EnableSessionStore=false in empty mode, got %v", cfg.EnableSessionStore)
	}
	if cfg.EnableSkills == nil || *cfg.EnableSkills != false {
		t.Errorf("expected EnableSkills=false in empty mode, got %v", cfg.EnableSkills)
	}
	if cfg.Memory == nil || cfg.Memory.Enabled != false {
		t.Errorf("expected Memory.Enabled=false in empty mode, got %v", cfg.Memory)
	}
	if cfg.CustomAgentsLocalOnly == nil || !*cfg.CustomAgentsLocalOnly {
		t.Errorf("expected CustomAgentsLocalOnly=true in empty mode, got %v", cfg.CustomAgentsLocalOnly)
	}
}

func TestApplyConfigDefaultsForMode_emptyHonorsCallerGranularFlags(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	falseVal := false
	trueVal := true
	cfg := &SessionConfig{
		SkipEmbeddingRetrieval:             &falseVal,
		EmbeddingCacheStorage:              String("persistent"),
		EnableOnDemandInstructionDiscovery: &trueVal,
		EnableFileHooks:                    &trueVal,
		EnableHostGitOperations:            &trueVal,
		EnableSessionStore:                 &trueVal,
		EnableSkills:                       &trueVal,
		Memory:                             &MemoryConfiguration{Enabled: true},
		CustomAgentsLocalOnly:              &falseVal,
	}
	c.applyConfigDefaultsForMode(cfg)
	if *cfg.SkipEmbeddingRetrieval != false {
		t.Errorf("caller-supplied SkipEmbeddingRetrieval must win")
	}
	if *cfg.EmbeddingCacheStorage != *String("persistent") {
		t.Errorf("caller-supplied EmbeddingCacheStorage must win")
	}
	if *cfg.EnableOnDemandInstructionDiscovery != true {
		t.Errorf("caller-supplied EnableOnDemandInstructionDiscovery must win")
	}
	if *cfg.EnableFileHooks != true {
		t.Errorf("caller-supplied EnableFileHooks must win")
	}
	if *cfg.EnableHostGitOperations != true {
		t.Errorf("caller-supplied EnableHostGitOperations must win")
	}
	if *cfg.EnableSessionStore != true {
		t.Errorf("caller-supplied EnableSessionStore must win")
	}
	if *cfg.EnableSkills != true {
		t.Errorf("caller-supplied EnableSkills must win")
	}
	if cfg.Memory == nil || cfg.Memory.Enabled != true {
		t.Errorf("caller-supplied Memory must win")
	}
	if cfg.CustomAgentsLocalOnly == nil || *cfg.CustomAgentsLocalOnly {
		t.Errorf("caller-supplied CustomAgentsLocalOnly must win")
	}
}

func TestApplyConfigDefaultsForMode_copilotCliLeavesGranularFlagsNil(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.SkipEmbeddingRetrieval != nil {
		t.Errorf("non-empty mode must not default SkipEmbeddingRetrieval")
	}
	if cfg.EnableOnDemandInstructionDiscovery != nil {
		t.Errorf("non-empty mode must not default EnableOnDemandInstructionDiscovery")
	}
	if cfg.EnableFileHooks != nil {
		t.Errorf("non-empty mode must not default EnableFileHooks")
	}
	if cfg.EnableHostGitOperations != nil {
		t.Errorf("non-empty mode must not default EnableHostGitOperations")
	}
	if cfg.EnableSessionStore != nil {
		t.Errorf("non-empty mode must not default EnableSessionStore")
	}
	if cfg.EnableSkills != nil {
		t.Errorf("non-empty mode must not default EnableSkills")
	}
	if cfg.Memory != nil {
		t.Errorf("non-empty mode must not default Memory")
	}
	if cfg.CustomAgentsLocalOnly != nil {
		t.Errorf("non-empty mode must not default CustomAgentsLocalOnly")
	}
}

func TestApplyResumeDefaultsForMode_customAgentsLocalOnly(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})

	cfg := &ResumeSessionConfig{}
	c.applyResumeDefaultsForMode(cfg)
	if cfg.CustomAgentsLocalOnly == nil || !*cfg.CustomAgentsLocalOnly {
		t.Errorf("expected CustomAgentsLocalOnly=true in empty mode, got %v", cfg.CustomAgentsLocalOnly)
	}

	cfg = &ResumeSessionConfig{CustomAgentsLocalOnly: Bool(false)}
	c.applyResumeDefaultsForMode(cfg)
	if cfg.CustomAgentsLocalOnly == nil || *cfg.CustomAgentsLocalOnly {
		t.Errorf("caller-supplied CustomAgentsLocalOnly must win")
	}
}

func TestApplyConfigDefaultsForMode_emptyDefaultsMCPOAuthTokenStorage(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.MCPOAuthTokenStorage != "in-memory" {
		t.Errorf("expected MCPOAuthTokenStorage 'in-memory' in empty mode, got %q", cfg.MCPOAuthTokenStorage)
	}
}

func TestApplyConfigDefaultsForMode_emptyHonorsCallerMCPOAuthTokenStorage(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &SessionConfig{MCPOAuthTokenStorage: "persistent"}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.MCPOAuthTokenStorage != "persistent" {
		t.Errorf("caller-supplied MCPOAuthTokenStorage must win, got %q", cfg.MCPOAuthTokenStorage)
	}
}

func TestApplyConfigDefaultsForMode_copilotCliLeavesMCPOAuthTokenStorageEmpty(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	cfg := &SessionConfig{}
	c.applyConfigDefaultsForMode(cfg)
	if cfg.MCPOAuthTokenStorage != "" {
		t.Errorf("non-empty mode must not default MCPOAuthTokenStorage, got %q", cfg.MCPOAuthTokenStorage)
	}
}

func TestApplyResumeDefaultsForMode_emptyDefaultsExperimentalModeFalse(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeEmpty, BaseDirectory: t.TempDir()})
	cfg := &ResumeSessionConfig{}
	c.applyResumeDefaultsForMode(cfg)
	if cfg.EnableExperimentalMode == nil || *cfg.EnableExperimentalMode != false {
		t.Errorf("expected experimental mode default false in empty mode, got %v", cfg.EnableExperimentalMode)
	}
}

func TestApplyResumeDefaultsForMode_copilotCliLeavesExperimentalModeNil(t *testing.T) {
	c := NewClient(&ClientOptions{Mode: ModeCopilotCli})
	cfg := &ResumeSessionConfig{}
	c.applyResumeDefaultsForMode(cfg)
	if cfg.EnableExperimentalMode != nil {
		t.Errorf("non-empty mode must not default experimental mode")
	}
}
