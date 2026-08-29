/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"context"
	"errors"
	"fmt"

	"github.com/github/copilot-sdk/go/rpc"
)

// validateNewClientForMode checks the cross-cutting requirements that
// [ModeEmpty] places on [ClientOptions]. Called from [NewClient].
func validateNewClientForMode(opts *ClientOptions) {
	if opts == nil || opts.Mode != ModeEmpty {
		return
	}
	// Empty mode requires durable, app-owned storage. Either:
	//   - the app supplied a BaseDirectory the runtime can write to,
	//   - the app supplied a SessionFS implementation,
	//   - or the app is connecting to an externally-managed runtime via
	//     URIConnection (in which case the host owns storage).
	if opts.BaseDirectory != "" {
		return
	}
	if opts.SessionFS != nil {
		return
	}
	if _, ok := opts.Connection.(URIConnection); ok {
		return
	}
	panic("Client is in Mode=ModeEmpty but neither BaseDirectory, SessionFS, nor a URIConnection was supplied. " +
		"Empty mode requires explicit, per-tenant storage; set ClientOptions.BaseDirectory or .SessionFS, " +
		"or connect to an externally-managed runtime via URIConnection.")
}

// validateToolFilterList rejects bare "*" entries with an actionable error
// pointing at the [ToolSet] builder. Called for both availableTools and
// excludedTools.
func validateToolFilterList(field string, list []string) error {
	for _, entry := range list {
		if entry == "*" {
			return fmt.Errorf(
				"invalid %s entry %q: there is no bare wildcard. "+
					"Use one or more of NewToolSet().AddBuiltIn(\"*\"), .AddMCP(\"*\"), or .AddCustom(\"*\") "+
					"to target a specific source",
				field, entry)
		}
	}
	return nil
}

// resolveToolFilterOptions validates the configured tool filters and applies
// empty-mode invariants. Returns the (possibly-mutated) request fields to set.
func (c *Client) resolveToolFilterOptions(availableTools, excludedTools []string) (
	[]string, []string, *rpc.OptionsUpdateToolFilterPrecedence, error,
) {
	if err := validateToolFilterList("availableTools", availableTools); err != nil {
		return nil, nil, nil, err
	}
	if err := validateToolFilterList("excludedTools", excludedTools); err != nil {
		return nil, nil, nil, err
	}
	if c.options.Mode == ModeEmpty && availableTools == nil {
		return nil, nil, nil, errors.New(
			"Client is in Mode=ModeEmpty but the session config did not specify AvailableTools. " +
				"Empty mode requires every session to explicitly opt into the tools it wants — " +
				"e.g. NewToolSet().AddBuiltIn(BuiltInToolsIsolated...).ToSlice()")
	}
	precedence := rpc.OptionsUpdateToolFilterPrecedenceExcluded
	return availableTools, excludedTools, &precedence, nil
}

// systemMessageForMode applies empty-mode environment_context stripping to
// the caller-supplied system message config. App values win (we only inject
// when the app hasn't already specified an environment_context override).
func (c *Client) systemMessageForMode(supplied *SystemMessageConfig) *SystemMessageConfig {
	if c.options.Mode != ModeEmpty {
		return supplied
	}
	removeAction := SectionOverride{Action: SectionActionRemove}
	if supplied == nil {
		return &SystemMessageConfig{
			Mode:     "customize",
			Sections: map[string]SectionOverride{"environment_context": removeAction},
		}
	}
	switch supplied.Mode {
	case "replace":
		return supplied
	case "customize":
		if _, ok := supplied.Sections["environment_context"]; ok {
			return supplied
		}
		out := *supplied
		out.Sections = make(map[string]SectionOverride, len(supplied.Sections)+1)
		for k, v := range supplied.Sections {
			out.Sections[k] = v
		}
		out.Sections["environment_context"] = removeAction
		return &out
	case "append", "":
		// Promote append/unspecified to customize so we can also strip
		// environment_context. The runtime appends Content to additional
		// instructions in both modes, so caller text is preserved verbatim.
		return &SystemMessageConfig{
			Mode:     "customize",
			Content:  supplied.Content,
			Sections: map[string]SectionOverride{"environment_context": removeAction},
		}
	default:
		return supplied
	}
}

// applyConfigDefaultsForMode fills in empty-mode defaults on the session
// config in place. App-supplied values win.
func (c *Client) applyConfigDefaultsForMode(config *SessionConfig) {
	if c.options.Mode != ModeEmpty {
		return
	}
	if config.EnableExperimentalMode == nil {
		f := false
		config.EnableExperimentalMode = &f
	}
	if config.EnableSessionTelemetry == nil {
		f := false
		config.EnableSessionTelemetry = &f
	}
	if config.SkipEmbeddingRetrieval == nil {
		t := true
		config.SkipEmbeddingRetrieval = &t
	}
	if config.EmbeddingCacheStorage == nil {
		inMemory := "in-memory"
		config.EmbeddingCacheStorage = &inMemory
	}
	if config.EnableOnDemandInstructionDiscovery == nil {
		f := false
		config.EnableOnDemandInstructionDiscovery = &f
	}
	if config.EnableFileHooks == nil {
		f := false
		config.EnableFileHooks = &f
	}
	if config.EnableHostGitOperations == nil {
		f := false
		config.EnableHostGitOperations = &f
	}
	if config.EnableSessionStore == nil {
		f := false
		config.EnableSessionStore = &f
	}
	if config.EnableSkills == nil {
		f := false
		config.EnableSkills = &f
	}
	if config.Memory == nil {
		config.Memory = &MemoryConfiguration{Enabled: false}
	}
	if config.MCPOAuthTokenStorage == "" {
		config.MCPOAuthTokenStorage = "in-memory"
	}
	if config.CustomAgentsLocalOnly == nil {
		localOnly := true
		config.CustomAgentsLocalOnly = &localOnly
	}
}

func (c *Client) applyResumeDefaultsForMode(config *ResumeSessionConfig) {
	if c.options.Mode != ModeEmpty {
		return
	}
	if config.EnableExperimentalMode == nil {
		f := false
		config.EnableExperimentalMode = &f
	}
	if config.EnableSessionTelemetry == nil {
		f := false
		config.EnableSessionTelemetry = &f
	}
	if config.SkipEmbeddingRetrieval == nil {
		t := true
		config.SkipEmbeddingRetrieval = &t
	}
	if config.EmbeddingCacheStorage == nil {
		inMemory := "in-memory"
		config.EmbeddingCacheStorage = &inMemory
	}
	if config.EnableOnDemandInstructionDiscovery == nil {
		f := false
		config.EnableOnDemandInstructionDiscovery = &f
	}
	if config.EnableFileHooks == nil {
		f := false
		config.EnableFileHooks = &f
	}
	if config.EnableHostGitOperations == nil {
		f := false
		config.EnableHostGitOperations = &f
	}
	if config.EnableSessionStore == nil {
		f := false
		config.EnableSessionStore = &f
	}
	if config.EnableSkills == nil {
		f := false
		config.EnableSkills = &f
	}
	if config.Memory == nil {
		config.Memory = &MemoryConfiguration{Enabled: false}
	}
	if config.MCPOAuthTokenStorage == "" {
		config.MCPOAuthTokenStorage = "in-memory"
	}
	if config.CustomAgentsLocalOnly == nil {
		localOnly := true
		config.CustomAgentsLocalOnly = &localOnly
	}
}

// updateSessionOptionsForMode applies the per-mode safe-defaults patch via
// session.options.update after create/resume succeeds. In empty mode the
// four overridable feature flags default to safe values; caller values win.
// installedPlugins=[] is unconditional in empty mode.
func (c *Client) updateSessionOptionsForMode(ctx context.Context, session *Session, base optBackInFields) error {
	patch := &rpc.SessionUpdateOptionsParams{}
	hasAny := false
	if c.options.Mode == ModeEmpty {
		if base.SkipCustomInstructions != nil {
			patch.SkipCustomInstructions = base.SkipCustomInstructions
		} else {
			t := true
			patch.SkipCustomInstructions = &t
		}
		if base.CustomAgentsLocalOnly != nil {
			patch.CustomAgentsLocalOnly = base.CustomAgentsLocalOnly
		} else {
			t := true
			patch.CustomAgentsLocalOnly = &t
		}
		if base.CoauthorEnabled != nil {
			patch.CoauthorEnabled = base.CoauthorEnabled
		} else {
			f := false
			patch.CoauthorEnabled = &f
		}
		if base.ManageScheduleEnabled != nil {
			patch.ManageScheduleEnabled = base.ManageScheduleEnabled
		} else {
			f := false
			patch.ManageScheduleEnabled = &f
		}
		patch.InstalledPlugins = []rpc.SessionInstalledPlugin{}
		hasAny = true
	} else {
		if base.SkipCustomInstructions != nil {
			patch.SkipCustomInstructions = base.SkipCustomInstructions
			hasAny = true
		}
		if base.CustomAgentsLocalOnly != nil {
			patch.CustomAgentsLocalOnly = base.CustomAgentsLocalOnly
			hasAny = true
		}
		if base.CoauthorEnabled != nil {
			patch.CoauthorEnabled = base.CoauthorEnabled
			hasAny = true
		}
		if base.ManageScheduleEnabled != nil {
			patch.ManageScheduleEnabled = base.ManageScheduleEnabled
			hasAny = true
		}
	}
	if !hasAny {
		return nil
	}
	if _, err := session.RPC.Options.Update(ctx, patch); err != nil {
		// The runtime session exists but the post-create options patch
		// failed — best-effort disconnect so we don't leak it (in empty
		// mode it would otherwise keep running with permissive defaults).
		_ = session.Disconnect()
		c.sessionsMux.Lock()
		delete(c.sessions, session.SessionID)
		c.sessionsMux.Unlock()
		return fmt.Errorf("failed to apply mode-specific session options: %w", err)
	}
	return nil
}

// optBackInFields is the subset of SessionConfig / ResumeSessionConfig shared
// by [Client.updateSessionOptionsForMode].
type optBackInFields struct {
	SkipCustomInstructions *bool
	CustomAgentsLocalOnly  *bool
	CoauthorEnabled        *bool
	ManageScheduleEnabled  *bool
}
