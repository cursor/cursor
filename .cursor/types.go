package copilot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/github/copilot-sdk/go/rpc"
)

// connectionState is the internal client connection state.
type connectionState string

const (
	stateDisconnected connectionState = "disconnected"
	stateConnecting   connectionState = "connecting"
	stateConnected    connectionState = "connected"
	stateError        connectionState = "error"
)

// RuntimeConnection describes how a [Client] connects to the Copilot runtime.
//
// Construct one with a [StdioConnection], [TCPConnection], [URIConnection], or
// [InProcessConnection] literal and pass it via [ClientOptions.Connection]. When
// [ClientOptions.Connection] is nil, COPILOT_SDK_DEFAULT_CONNECTION may select
// "inprocess" or "stdio"; when unset, the default is an empty [StdioConnection].
type RuntimeConnection interface {
	runtimeConnection()
}

// childProcessConnection is implemented by the connection types that spawn a
// runtime child process ([StdioConnection] and [TCPConnection]). It exposes the
// per-connection environment so the client can resolve and validate it uniformly
// regardless of the specific child-process transport.
type childProcessConnection interface {
	RuntimeConnection
	connEnv() []string
}

// StdioConnection spawns a runtime child process and communicates over its
// stdin/stdout pipes. This is the default when no connection is configured.
type StdioConnection struct {
	// Path is the runtime executable. When empty, the bundled runtime is used.
	Path string
	// Args are extra command-line arguments inserted before SDK-managed args.
	Args []string
	// Env are the environment variables for the runtime process, each of the
	// form "KEY=VALUE". When set, these take precedence over
	// [ClientOptions.Env]; setting both is rejected. When nil, the client-level
	// env (or the current process environment) is used.
	Env []string
}

func (StdioConnection) runtimeConnection() {}

func (c StdioConnection) connEnv() []string { return c.Env }

// TCPConnection spawns a runtime child process that listens on a TCP socket
// and connects to it.
type TCPConnection struct {
	// Port is the TCP port the runtime listens on. 0 (the default) lets the
	// runtime pick a free port; the chosen port is then available via
	// [Client.RuntimePort] after [Client.Start] returns.
	Port int
	// ConnectionToken is an optional shared secret sent in the `connect`
	// handshake. When empty, a UUID is generated automatically so the
	// loopback listener is safe by default.
	ConnectionToken string
	// Path is the runtime executable. When empty, the bundled runtime is used.
	Path string
	// Args are extra command-line arguments inserted before SDK-managed args.
	Args []string
	// Env are the environment variables for the runtime process, each of the
	// form "KEY=VALUE". When set, these take precedence over
	// [ClientOptions.Env]; setting both is rejected. When nil, the client-level
	// env (or the current process environment) is used.
	Env []string
}

func (TCPConnection) runtimeConnection() {}

func (c TCPConnection) connEnv() []string { return c.Env }

// URIConnection connects to an already-running runtime at the given URL.
// The SDK does not spawn a process in this mode.
type URIConnection struct {
	// URL of the runtime. Accepts "port", "host:port", or a full URL such
	// as "http://host:port".
	URL string
	// ConnectionToken authenticates the connection; must match what the
	// remote runtime expects.
	ConnectionToken string
}

func (URIConnection) runtimeConnection() {}

// InProcessConnection hosts the Copilot runtime in-process by loading its native
// runtime library (a Rust cdylib) and driving JSON-RPC over the library's C ABI,
// instead of spawning a runtime child process.
//
// Because the runtime is loaded into the calling process, per-client
// environment, working directory, and telemetry cannot be represented and are
// rejected by [NewClient] (see [ClientOptions]). Set those via the host process
// environment instead, or use a child-process transport ([StdioConnection] /
// [TCPConnection]).
//
// Experimental: the in-process transport is experimental and its API and
// behavior may change in a future release. Build the application with the
// copilot_inprocess build tag to enable this transport.
type InProcessConnection struct {
}

func (InProcessConnection) runtimeConnection() {}

// ClientOptions configures the [Client].
type ClientOptions struct {
	// Connection describes how to connect to the Copilot runtime. When nil,
	// COPILOT_SDK_DEFAULT_CONNECTION may select "inprocess" or "stdio";
	// when unset, defaults to an empty [StdioConnection].
	Connection RuntimeConnection
	// WorkingDirectory is the working directory for the runtime process.
	// If empty, inherits the current process's working directory.
	WorkingDirectory string
	// BaseDirectory is the base directory for Copilot data (session state,
	// config, etc.). Sets the COPILOT_HOME environment variable on the
	// spawned runtime. When empty, the runtime defaults to ~/.copilot.
	// This does not affect where the Go SDK extracts the embedded CLI
	// binary; use embeddedcli.Config.Dir to control that install/cache
	// location.
	// Ignored when connecting to an existing runtime via [URIConnection].
	BaseDirectory string
	// LogLevel for the runtime. When empty (the default), the runtime
	// uses its own default level; the SDK does not pass --log-level.
	// Recognized values: "none", "error", "warning", "info", "debug", "all".
	LogLevel string
	// Env are the environment variables for the runtime process (default:
	// inherits from current process). Each entry is of the form "KEY=VALUE".
	// If Env contains duplicate keys, only the last value for each key is used.
	//
	// For child-process transports ([StdioConnection] / [TCPConnection]) the
	// per-connection Env, when set, takes precedence over this field; setting
	// both is rejected. Env is not supported with [InProcessConnection] (the
	// runtime shares this process's single environment block) and is rejected
	// by [NewClient].
	Env []string
	// GitHubToken is the GitHub token to use for authentication.
	// When provided, the token is passed to the runtime via environment
	// variable. This takes priority over other authentication methods.
	GitHubToken string
	// UseLoggedInUser controls whether to use the logged-in user for
	// authentication. When true, the runtime attempts to use stored OAuth
	// tokens or gh CLI auth. When false, only explicit tokens (GitHubToken
	// or environment variables) are used.
	// Default: true (but defaults to false when GitHubToken is provided).
	UseLoggedInUser *bool
	// OnListModels is a custom handler for listing available models.
	// When provided, [Client.ListModels] calls this handler instead of
	// querying the runtime. Useful in BYOK mode to return models available
	// from your custom provider.
	OnListModels func(ctx context.Context) ([]ModelInfo, error)
	// SessionFS configures a custom session filesystem provider.
	// When provided, the client registers as the session filesystem provider
	// on connection, routing session-scoped file I/O through per-session
	// handlers.
	SessionFS *SessionFSConfig
	// RequestHandler registers a connection-level LLM inference callback. When
	// non-nil, the client registers as the inference provider on connect, and
	// the runtime routes its model-layer HTTP and WebSocket traffic through
	// this handler instead of issuing the calls itself. Works for both CAPI
	// and BYOK sessions.
	RequestHandler *CopilotRequestHandler
	// OnGitHubTelemetry registers a connection-level callback (experimental)
	// that receives GitHub telemetry events the runtime forwards for sessions
	// opened by this client. When non-nil, every session created or resumed by
	// this client opts into telemetry forwarding (enableGitHubTelemetryForwarding).
	OnGitHubTelemetry func(notification *rpc.GitHubTelemetryNotification)
	// Telemetry configures OpenTelemetry integration for the runtime.
	// When non-nil, COPILOT_OTEL_ENABLED=true is set and any populated
	// fields are mapped to the corresponding environment variables.
	Telemetry *TelemetryConfig
	// SessionIdleTimeoutSeconds configures the server-wide session idle
	// timeout in seconds. Sessions without activity for this duration are
	// automatically cleaned up. Set to 0 or leave unset to disable.
	// Ignored when connecting to an existing runtime via [URIConnection].
	SessionIdleTimeoutSeconds int
	// EnableRemoteSessions enables remote session support (Mission Control
	// integration). When true, sessions in a GitHub repository working
	// directory are accessible from GitHub web and mobile.
	// Ignored when connecting to an existing runtime via [URIConnection].
	EnableRemoteSessions bool
	// Mode controls the default tool surface and feature flags presented to
	// sessions created by this client. The zero value ([ModeCopilotCli])
	// matches legacy CLI defaults. Set to [ModeEmpty] to opt in to
	// multi-tenant safe defaults — see [ClientMode] for details.
	//
	// When Mode is [ModeEmpty], NewClient requires either BaseDirectory,
	// SessionFS, or a [URIConnection] so the runtime has persistent storage
	// for session state.
	Mode ClientMode
}

// CloudSessionRepository is GitHub repository metadata associated with a cloud session.
type CloudSessionRepository struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
}

// CloudSessionOptions configures creation of a remote session in the cloud.
type CloudSessionOptions struct {
	Repository *CloudSessionRepository `json:"repository,omitempty"`
}

// TelemetryConfig configures OpenTelemetry integration for the Copilot CLI process.
type TelemetryConfig struct {
	// OTLPEndpoint is the OTLP HTTP endpoint URL for trace/metric export.
	// Sets OTEL_EXPORTER_OTLP_ENDPOINT.
	OTLPEndpoint string

	// OTLPProtocol is the OTLP HTTP protocol for all signals.
	// Sets OTEL_EXPORTER_OTLP_PROTOCOL.
	OTLPProtocol string

	// FilePath is the file path for JSON-lines trace output.
	// Sets COPILOT_OTEL_FILE_EXPORTER_PATH.
	FilePath string

	// ExporterType is the exporter backend type: "otlp-http" or "file".
	// Sets COPILOT_OTEL_EXPORTER_TYPE.
	ExporterType string

	// SourceName is the instrumentation scope name.
	// Sets COPILOT_OTEL_SOURCE_NAME.
	SourceName string

	// CaptureContent controls whether to capture message content (prompts, responses).
	// Sets OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT.
	CaptureContent *bool
}

// Bool returns a pointer to the given bool value.
// Use for option fields such as AutoStart, AutoRestart, or LogOptions.Ephemeral:
//
//	AutoStart: Bool(false)
//	Ephemeral: Bool(true)
func Bool(v bool) *bool {
	return &v
}

// String returns a pointer to the given string value.
// Use for setting optional string parameters in RPC calls.
func String(v string) *string {
	return &v
}

// Float64 returns a pointer to the given float64 value.
// Use for setting thresholds: BackgroundCompactionThreshold: Float64(0.80)
func Float64(v float64) *float64 {
	return &v
}

// Int returns a pointer to the given int value.
// Use for setting optional int parameters: MinLength: Int(1)
func Int(v int) *int {
	return &v
}

// Known system message section identifiers for the "customize" mode.
const (
	// SectionPreamble is the agent identity preamble and mode statement.
	SectionPreamble = "preamble"
	// SectionIdentity is the section group covering the identity preamble and its
	// sibling sub-sections (tone, tool efficiency, etc.).
	SectionIdentity = "identity"
	// SectionTone covers response style, conciseness rules, and output formatting preferences.
	SectionTone = "tone"
	// SectionToolEfficiency covers tool usage patterns, parallel calling, and batching guidelines.
	SectionToolEfficiency = "tool_efficiency"
	// SectionEnvironmentContext covers CWD, OS, git root, directory listing, and available tools.
	SectionEnvironmentContext = "environment_context"
	// SectionCodeChangeRules covers coding rules, linting/testing, ecosystem tools, and style.
	SectionCodeChangeRules = "code_change_rules"
	// SectionGuidelines covers tips, behavioral best practices, and behavioral guidelines.
	SectionGuidelines = "guidelines"
	// SectionSafety covers environment limitations, prohibited actions, and security policies.
	SectionSafety = "safety"
	// SectionToolInstructions covers per-tool usage instructions.
	SectionToolInstructions = "tool_instructions"
	// SectionCustomInstructions covers repository and organization custom instructions.
	SectionCustomInstructions = "custom_instructions"
	// SectionRuntimeInstructions targets runtime-provided context and instructions
	// (e.g. system notifications, memories, workspace context, mode-specific instructions,
	// content-exclusion policy).
	SectionRuntimeInstructions = "runtime_instructions"
	// SectionLastInstructions covers end-of-prompt instructions: parallel tool calling,
	// persistence, and task completion.
	SectionLastInstructions = "last_instructions"
)

// SectionOverrideAction represents the action to perform on a system message section.
type SectionOverrideAction string

const (
	// SectionActionReplace replaces section content entirely.
	SectionActionReplace SectionOverrideAction = "replace"
	// SectionActionRemove removes the section.
	SectionActionRemove SectionOverrideAction = "remove"
	// SectionActionAppend appends to existing section content.
	SectionActionAppend SectionOverrideAction = "append"
	// SectionActionPrepend prepends to existing section content.
	SectionActionPrepend SectionOverrideAction = "prepend"
	// SectionActionPreserve is a no-op marker that opts an individually-addressable
	// section out of a group-level "remove" (e.g. keep "tone" when removing the
	// "identity" group).
	SectionActionPreserve SectionOverrideAction = "preserve"
)

// SectionTransformFn is a callback that receives the current content of a system message section
// and returns the transformed content. Used with the "transform" action to read-then-write
// modify sections at runtime.
type SectionTransformFn func(currentContent string) (string, error)

// SectionOverride defines an override operation for a single system message section.
type SectionOverride struct {
	// Action is the operation to perform: "replace", "remove", "append", "prepend", or "transform".
	Action SectionOverrideAction `json:"action,omitempty"`
	// Content for the override. Optional for all actions. Ignored for "remove".
	Content string `json:"content,omitempty"`
	// Transform is a callback invoked when Action is "transform".
	// The runtime calls this with the current section content and uses the returned string.
	// Excluded from JSON serialization; the SDK registers it as an RPC callback internally.
	Transform SectionTransformFn `json:"-"`
}

// SystemMessageAppendConfig is append mode: use CLI foundation with optional appended content.
type SystemMessageAppendConfig struct {
	// Mode is optional, defaults to "append"
	Mode string `json:"mode,omitempty"`
	// Content provides additional instructions appended after SDK-managed sections
	Content string `json:"content,omitempty"`
}

// SystemMessageReplaceConfig is replace mode: use caller-provided system message entirely.
// Removes all SDK guardrails including security restrictions.
type SystemMessageReplaceConfig struct {
	// Mode must be "replace"
	Mode string `json:"mode"`
	// Content is the complete system message (required)
	Content string `json:"content"`
}

// SystemMessageConfig represents system message configuration for session creation.
//   - Append mode (default): SDK foundation + optional custom content
//   - Replace mode: Full control, caller provides entire system message
//   - Customize mode: Section-level overrides with graceful fallback
//
// In Go, use one struct and set fields appropriate for the desired mode.
type SystemMessageConfig struct {
	Mode     string                     `json:"mode,omitempty"`
	Content  string                     `json:"content,omitempty"`
	Sections map[string]SectionOverride `json:"sections,omitempty"`
}

// PermissionHandlerFunc executes a permission request.
// The handler should return a [rpc.PermissionDecision]. Returning an error
// causes the SDK to respond with [rpc.PermissionDecisionUserNotAvailable].
//
// Use the variant types directly:
//
//	&rpc.PermissionDecisionApproveOnce{}
//	&rpc.PermissionDecisionReject{Feedback: &feedback}
//	&rpc.PermissionDecisionUserNotAvailable{}
//	&rpc.PermissionDecisionNoResult{}  // decline to respond; another client may answer
type PermissionHandlerFunc func(request PermissionRequest, invocation PermissionInvocation) (rpc.PermissionDecision, error)

// PermissionInvocation provides context about a permission request
type PermissionInvocation struct {
	SessionID              string
	ManagedSettingsEnabled bool
}

// MCPAuthWwwAuthenticateParams contains parsed parameters from an MCP server's WWW-Authenticate response.
type MCPAuthWwwAuthenticateParams struct {
	ResourceMetadataURL *string `json:"resourceMetadataUrl,omitempty"`
	Scope               *string `json:"scope,omitempty"`
	Error               *string `json:"error,omitempty"`
}

// MCPAuthStaticClientConfig is static OAuth client configuration supplied by an MCP server.
type MCPAuthStaticClientConfig struct {
	ClientID     string  `json:"clientId"`
	ClientSecret *string `json:"clientSecret,omitempty"`
	GrantType    *string `json:"grantType,omitempty"`
	PublicClient *bool   `json:"publicClient,omitempty"`
}

// MCPAuthRequest describes an MCP OAuth request that the SDK host can satisfy with a token.
type MCPAuthRequest struct {
	RequestID             string                        `json:"requestId"`
	ServerName            string                        `json:"serverName"`
	ServerURL             string                        `json:"serverUrl"`
	Reason                MCPOauthRequestReason         `json:"reason"`
	WwwAuthenticateParams *MCPAuthWwwAuthenticateParams `json:"wwwAuthenticateParams,omitempty"`
	ResourceMetadata      *string                       `json:"resourceMetadata,omitempty"`
	StaticClientConfig    *MCPAuthStaticClientConfig    `json:"staticClientConfig,omitempty"`
}

// MCPAuthToken is host-provided OAuth token data for a pending MCP OAuth request.
type MCPAuthToken struct {
	AccessToken string  `json:"accessToken"`
	TokenType   *string `json:"tokenType,omitempty"`
	ExpiresIn   *int64  `json:"expiresIn,omitempty"`
}

// MCPAuthResult is the result returned by an MCP auth request handler.
type MCPAuthResult struct {
	Kind  string
	Token *MCPAuthToken
}

const (
	// MCPAuthResultKindToken indicates that the host provided token data.
	MCPAuthResultKindToken = "token"
	// MCPAuthResultKindCancelled indicates that the host declined the request.
	MCPAuthResultKindCancelled = "cancelled"
)

// MCPAuthResultToken returns a token result for an MCP OAuth request.
func MCPAuthResultToken(token *MCPAuthToken) *MCPAuthResult {
	return &MCPAuthResult{Kind: MCPAuthResultKindToken, Token: token}
}

// MCPAuthResultCancelled returns a cancelled result for an MCP OAuth request.
func MCPAuthResultCancelled() *MCPAuthResult {
	return &MCPAuthResult{Kind: MCPAuthResultKindCancelled}
}

// MCPAuthInvocation provides context about an MCP auth handler invocation.
type MCPAuthInvocation struct {
	SessionID string
}

// MCPAuthHandler handles MCP OAuth requests from the runtime.
type MCPAuthHandler func(request MCPAuthRequest, invocation MCPAuthInvocation) (*MCPAuthResult, error)

// UserInputRequest represents a request for user input from the agent
type UserInputRequest struct {
	Question      string
	Choices       []string
	AllowFreeform *bool
}

// UserInputResponse represents the user's response to an input request
type UserInputResponse struct {
	Answer      string
	WasFreeform bool
}

// UserInputHandler handles user input requests from the agent
// The handler should return a UserInputResponse. Returning an error fails the request.
type UserInputHandler func(request UserInputRequest, invocation UserInputInvocation) (UserInputResponse, error)

// UserInputInvocation provides context about a user input request
type UserInputInvocation struct {
	SessionID string
}

// ExitPlanModeRequest represents a request to exit plan mode and continue with a selected action.
type ExitPlanModeRequest struct {
	Summary           string   `json:"summary"`
	PlanContent       string   `json:"planContent,omitempty"`
	Actions           []string `json:"actions"`
	RecommendedAction string   `json:"recommendedAction"`
}

// ExitPlanModeResult is the response to an exit-plan-mode request.
type ExitPlanModeResult struct {
	Approved       bool   `json:"approved"`
	SelectedAction string `json:"selectedAction,omitempty"`
	Feedback       string `json:"feedback,omitempty"`
}

// ExitPlanModeInvocation provides context about an exit-plan-mode request.
type ExitPlanModeInvocation struct {
	SessionID string
}

// ExitPlanModeRequestHandler handles exit-plan-mode requests from the agent.
type ExitPlanModeRequestHandler func(request ExitPlanModeRequest, invocation ExitPlanModeInvocation) (ExitPlanModeResult, error)

// AutoModeSwitchRequest represents a request to switch to auto mode after an eligible rate limit.
type AutoModeSwitchRequest struct {
	ErrorCode         *string  `json:"errorCode,omitempty"`
	RetryAfterSeconds *float64 `json:"retryAfterSeconds,omitempty"`
}

// AutoModeSwitchInvocation provides context about an auto-mode-switch request.
type AutoModeSwitchInvocation struct {
	SessionID string
}

// AutoModeSwitchRequestHandler handles auto-mode-switch requests from the agent.
type AutoModeSwitchRequestHandler func(request AutoModeSwitchRequest, invocation AutoModeSwitchInvocation) (AutoModeSwitchResponse, error)

// PreToolUseHookInput is the input for a pre-tool-use hook
type PreToolUseHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	ToolName         string    `json:"toolName"`
	ToolArgs         any       `json:"toolArgs"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h PreToolUseHookInput) MarshalJSON() ([]byte, error) {
	type alias PreToolUseHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *PreToolUseHookInput) UnmarshalJSON(data []byte) error {
	type alias PreToolUseHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// PreToolUseHookOutput is the output for a pre-tool-use hook
type PreToolUseHookOutput struct {
	PermissionDecision       string `json:"permissionDecision,omitempty"` // "allow", "deny", "ask"
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	ModifiedArgs             any    `json:"modifiedArgs,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
	SuppressOutput           bool   `json:"suppressOutput,omitempty"`
}

// PreToolUseHandler handles pre-tool-use hook invocations
type PreToolUseHandler func(input PreToolUseHookInput, invocation HookInvocation) (*PreToolUseHookOutput, error)

// PostToolUseHookInput is the input for a post-tool-use hook
type PostToolUseHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	ToolName         string    `json:"toolName"`
	ToolArgs         any       `json:"toolArgs"`
	ToolResult       any       `json:"toolResult"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h PostToolUseHookInput) MarshalJSON() ([]byte, error) {
	type alias PostToolUseHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *PostToolUseHookInput) UnmarshalJSON(data []byte) error {
	type alias PostToolUseHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// PostToolUseHookOutput is the output for a post-tool-use hook
type PostToolUseHookOutput struct {
	ModifiedResult    any    `json:"modifiedResult,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	SuppressOutput    bool   `json:"suppressOutput,omitempty"`
}

// PostToolUseHandler handles post-tool-use hook invocations
type PostToolUseHandler func(input PostToolUseHookInput, invocation HookInvocation) (*PostToolUseHookOutput, error)

// PostToolUseFailureHookInput is the input for a post-tool-use-failure hook.
//
// Fires after a tool execution whose result was "failure". The CLI extracts
// the failure message from the tool result and passes it as the Error field
// (rather than passing the full result object).
type PostToolUseFailureHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	ToolName         string    `json:"toolName"`
	ToolArgs         any       `json:"toolArgs"`
	// Error is the failure message from the tool's result.
	Error string `json:"error"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h PostToolUseFailureHookInput) MarshalJSON() ([]byte, error) {
	type alias PostToolUseFailureHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *PostToolUseFailureHookInput) UnmarshalJSON(data []byte) error {
	type alias PostToolUseFailureHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// PostToolUseFailureHookOutput is the output for a post-tool-use-failure hook.
//
// Only AdditionalContext is consumed by the host CLI — it is appended as
// hidden guidance to the model alongside the failed tool result.
type PostToolUseFailureHookOutput struct {
	AdditionalContext string `json:"additionalContext,omitempty"`
}

// PostToolUseFailureHandler handles post-tool-use-failure hook invocations.
type PostToolUseFailureHandler func(input PostToolUseFailureHookInput, invocation HookInvocation) (*PostToolUseFailureHookOutput, error)

// UserPromptSubmittedHookInput is the input for a user-prompt-submitted hook
type UserPromptSubmittedHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	Prompt           string    `json:"prompt"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h UserPromptSubmittedHookInput) MarshalJSON() ([]byte, error) {
	type alias UserPromptSubmittedHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *UserPromptSubmittedHookInput) UnmarshalJSON(data []byte) error {
	type alias UserPromptSubmittedHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// UserPromptSubmittedHookOutput is the output for a user-prompt-submitted hook
type UserPromptSubmittedHookOutput struct {
	ModifiedPrompt    string `json:"modifiedPrompt,omitempty"`
	AdditionalContext string `json:"additionalContext,omitempty"`
	SuppressOutput    bool   `json:"suppressOutput,omitempty"`
}

// UserPromptSubmittedHandler handles user-prompt-submitted hook invocations
type UserPromptSubmittedHandler func(input UserPromptSubmittedHookInput, invocation HookInvocation) (*UserPromptSubmittedHookOutput, error)

// UserPromptTransformedHookInput is the input for a user-prompt-transformed hook.
type UserPromptTransformedHookInput struct {
	SessionID         string    `json:"sessionId"`
	Timestamp         time.Time `json:"-"`
	WorkingDirectory  string    `json:"cwd"`
	Prompt            string    `json:"prompt"`
	TransformedPrompt string    `json:"transformedPrompt"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h UserPromptTransformedHookInput) MarshalJSON() ([]byte, error) {
	type alias UserPromptTransformedHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *UserPromptTransformedHookInput) UnmarshalJSON(data []byte) error {
	type alias UserPromptTransformedHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// UserPromptTransformedHookOutput is the output for a user-prompt-transformed hook.
type UserPromptTransformedHookOutput struct {
	ModifiedTransformedPrompt *string `json:"modifiedTransformedPrompt,omitempty"`
}

// UserPromptTransformedHandler handles user-prompt-transformed hook invocations.
type UserPromptTransformedHandler func(input UserPromptTransformedHookInput, invocation HookInvocation) (*UserPromptTransformedHookOutput, error)

// SessionStartHookInput is the input for a session-start hook
type SessionStartHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	Source           string    `json:"source"` // "startup", "resume", "new"
	InitialPrompt    string    `json:"initialPrompt,omitempty"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h SessionStartHookInput) MarshalJSON() ([]byte, error) {
	type alias SessionStartHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *SessionStartHookInput) UnmarshalJSON(data []byte) error {
	type alias SessionStartHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// SessionStartHookOutput is the output for a session-start hook
type SessionStartHookOutput struct {
	AdditionalContext string         `json:"additionalContext,omitempty"`
	ModifiedConfig    map[string]any `json:"modifiedConfig,omitempty"`
}

// SessionStartHandler handles session-start hook invocations
type SessionStartHandler func(input SessionStartHookInput, invocation HookInvocation) (*SessionStartHookOutput, error)

// SessionEndHookInput is the input for a session-end hook
type SessionEndHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	Reason           string    `json:"reason"` // "complete", "error", "abort", "timeout", "user_exit"
	FinalMessage     string    `json:"finalMessage,omitempty"`
	Error            string    `json:"error,omitempty"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h SessionEndHookInput) MarshalJSON() ([]byte, error) {
	type alias SessionEndHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *SessionEndHookInput) UnmarshalJSON(data []byte) error {
	type alias SessionEndHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// SessionEndHookOutput is the output for a session-end hook
type SessionEndHookOutput struct {
	SuppressOutput bool     `json:"suppressOutput,omitempty"`
	CleanupActions []string `json:"cleanupActions,omitempty"`
	SessionSummary string   `json:"sessionSummary,omitempty"`
}

// SessionEndHandler handles session-end hook invocations
type SessionEndHandler func(input SessionEndHookInput, invocation HookInvocation) (*SessionEndHookOutput, error)

// ErrorOccurredHookInput is the input for an error-occurred hook
type ErrorOccurredHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	Error            string    `json:"error"`
	ErrorContext     string    `json:"errorContext"` // "model_call", "tool_execution", "system", "user_input"
	Recoverable      bool      `json:"recoverable"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h ErrorOccurredHookInput) MarshalJSON() ([]byte, error) {
	type alias ErrorOccurredHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *ErrorOccurredHookInput) UnmarshalJSON(data []byte) error {
	type alias ErrorOccurredHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// ErrorOccurredHookOutput is the output for an error-occurred hook
type ErrorOccurredHookOutput struct {
	SuppressOutput   bool   `json:"suppressOutput,omitempty"`
	ErrorHandling    string `json:"errorHandling,omitempty"` // "retry", "skip", "abort"
	RetryCount       int    `json:"retryCount,omitempty"`
	UserNotification string `json:"userNotification,omitempty"`
}

// ErrorOccurredHandler handles error-occurred hook invocations
type ErrorOccurredHandler func(input ErrorOccurredHookInput, invocation HookInvocation) (*ErrorOccurredHookOutput, error)

// AgentStopHookInput is the input for an agent-stop hook.
type AgentStopHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	StopReason       string    `json:"stopReason,omitempty"`
	TranscriptPath   string    `json:"transcriptPath,omitempty"`
	StopHookActive   bool      `json:"stop_hook_active,omitempty"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h AgentStopHookInput) MarshalJSON() ([]byte, error) {
	type alias AgentStopHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *AgentStopHookInput) UnmarshalJSON(data []byte) error {
	type alias AgentStopHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// AgentStopHookOutput is the output for an agent-stop hook.
type AgentStopHookOutput struct {
	Decision string `json:"decision,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// AgentStopHandler handles agent-stop hook invocations.
type AgentStopHandler func(input AgentStopHookInput, invocation HookInvocation) (*AgentStopHookOutput, error)

// PreMCPToolCallHookInput is the input for a pre-mcp-tool-call hook
type PreMCPToolCallHookInput struct {
	SessionID        string    `json:"sessionId"`
	Timestamp        time.Time `json:"-"`
	WorkingDirectory string    `json:"cwd"`
	ServerName       string    `json:"serverName"`
	ToolName         string    `json:"toolName"`
	Arguments        any       `json:"arguments,omitempty"`
	ToolCallID       string    `json:"toolCallId,omitempty"`
	Meta             any       `json:"_meta,omitempty"`
}

// MarshalJSON implements json.Marshaler, emitting Timestamp as Unix milliseconds.
func (h PreMCPToolCallHookInput) MarshalJSON() ([]byte, error) {
	type alias PreMCPToolCallHookInput
	return json.Marshal(&struct {
		Timestamp int64 `json:"timestamp"`
		alias
	}{Timestamp: h.Timestamp.UnixMilli(), alias: alias(h)})
}

// UnmarshalJSON implements json.Unmarshaler, parsing Timestamp from Unix milliseconds.
func (h *PreMCPToolCallHookInput) UnmarshalJSON(data []byte) error {
	type alias PreMCPToolCallHookInput
	aux := &struct {
		Timestamp int64 `json:"timestamp"`
		*alias
	}{alias: (*alias)(h)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	h.Timestamp = time.UnixMilli(aux.Timestamp)
	return nil
}

// PreMCPToolCallHookOutput is the output for a pre-mcp-tool-call hook
type PreMCPToolCallHookOutput struct {
	MetaToUse any `json:"metaToUse"`
}

// PreMCPToolCallHandler handles pre-mcp-tool-call hook invocations
type PreMCPToolCallHandler func(input PreMCPToolCallHookInput, invocation HookInvocation) (*PreMCPToolCallHookOutput, error)

// HookInvocation provides context about a hook invocation
type HookInvocation struct {
	SessionID string
}

// SessionHooks configures hook handlers for a session
type SessionHooks struct {
	OnPreToolUse            PreToolUseHandler
	OnPostToolUse           PostToolUseHandler
	OnPostToolUseFailure    PostToolUseFailureHandler
	OnUserPromptSubmitted   UserPromptSubmittedHandler
	OnUserPromptTransformed UserPromptTransformedHandler
	OnSessionStart          SessionStartHandler
	OnSessionEnd            SessionEndHandler
	OnErrorOccurred         ErrorOccurredHandler
	OnAgentStop             AgentStopHandler
	OnPreMCPToolCall        PreMCPToolCallHandler
}

// MCPServerConfig is implemented by MCP server configuration types.
// Only MCPStdioServerConfig and MCPHTTPServerConfig implement this interface.
type MCPServerConfig interface {
	mcpServerConfig()
}

// MCPStdioServerConfig configures a local/stdio MCP server.
//
// The Tools field controls which tools from the server are exposed:
//   - nil (omitted from the wire): all tools (CLI default)
//   - []string{"*"}: explicit "all tools"
//   - []string{}: no tools
//   - []string{"foo","bar"}: only those tools
type MCPStdioServerConfig struct {
	Tools            []string          `json:"tools,omitzero"`
	Timeout          int               `json:"timeout,omitempty"`
	Command          string            `json:"command"`
	Args             []string          `json:"args,omitzero"`
	Env              map[string]string `json:"env,omitzero"`
	WorkingDirectory string            `json:"cwd,omitempty"`
}

func (MCPStdioServerConfig) mcpServerConfig() {}

// MarshalJSON implements json.Marshaler, injecting the "type" discriminator.
func (c MCPStdioServerConfig) MarshalJSON() ([]byte, error) {
	type alias MCPStdioServerConfig
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{
		Type:  "stdio",
		alias: alias(c),
	})
}

// MCPHTTPServerConfig configures a remote MCP server (HTTP or SSE).
//
// See [MCPStdioServerConfig] for the semantics of the Tools field.
type MCPHTTPServerConfig struct {
	Tools   []string          `json:"tools,omitzero"`
	Timeout int               `json:"timeout,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitzero"`
}

func (MCPHTTPServerConfig) mcpServerConfig() {}

// MarshalJSON implements json.Marshaler, injecting the "type" discriminator.
func (c MCPHTTPServerConfig) MarshalJSON() ([]byte, error) {
	type alias MCPHTTPServerConfig
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{
		Type:  "http",
		alias: alias(c),
	})
}

// CustomAgentConfig configures a custom agent.
type CustomAgentConfig struct {
	// Name is the unique name of the custom agent
	Name string `json:"name"`
	// DisplayName is the display name for UI purposes
	DisplayName string `json:"displayName,omitempty"`
	// Description of what the agent does
	Description string `json:"description,omitempty"`
	// Tools is the list of tool names the agent can use. Nil omits the field
	// (all tools); an empty non-nil slice sends "tools": [] (no tools).
	Tools []string `json:"tools,omitzero"`
	// Prompt is the prompt content for the agent
	Prompt string `json:"prompt"`
	// MCPServers are MCP servers specific to this agent
	MCPServers map[string]MCPServerConfig `json:"mcpServers,omitempty"`
	// Infer indicates whether the agent should be available for model inference
	Infer *bool `json:"infer,omitempty"`
	// Skills is the list of skill names to preload into this agent's context at startup (opt-in; omit for none)
	Skills []string `json:"skills,omitempty"`
	// Model is the model identifier for this agent (e.g. "claude-haiku-4.5").
	// When set, the runtime will attempt to use this model for the agent,
	// falling back to the parent session model if unavailable.
	Model string `json:"model,omitempty"`
	// ReasoningEffort is the reasoning effort level for this agent's model.
	// When empty, the runtime resolves model configuration, then inherits the
	// parent effort only for the same model.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// DefaultAgentConfig configures the default agent (the built-in agent that handles turns when no custom agent is selected).
// Use ExcludedTools to hide specific tools from the default agent while keeping
// them available to custom sub-agents.
type DefaultAgentConfig struct {
	// ExcludedTools is a list of tool names to exclude from the default agent.
	// These tools remain available to custom sub-agents that reference them in their Tools list.
	ExcludedTools []string `json:"excludedTools,omitempty"`
}

// InfiniteSessionConfig configures infinite sessions with automatic context compaction
// and workspace persistence. When enabled, sessions automatically manage context window
// limits through background compaction and persist state to a workspace directory.
type InfiniteSessionConfig struct {
	// Enabled controls whether infinite sessions are enabled (default: true)
	Enabled *bool `json:"enabled,omitempty"`
	// BackgroundCompactionThreshold is the context utilization (0.0-1.0) at which
	// background compaction starts. Default: 0.80
	BackgroundCompactionThreshold *float64 `json:"backgroundCompactionThreshold,omitempty"`
	// BufferExhaustionThreshold is the context utilization (0.0-1.0) at which
	// the session blocks until compaction completes. Default: 0.95
	BufferExhaustionThreshold *float64 `json:"bufferExhaustionThreshold,omitempty"`
}

// MemoryConfiguration configures the memory feature for a session.
type MemoryConfiguration struct {
	// Enabled controls whether the memory feature is enabled for this session.
	Enabled bool `json:"enabled"`
}

// LargeToolOutputConfig configures handling of large tool outputs. When a tool
// produces output exceeding the configured size, the output is written to a
// temp file and a reference is returned to the model instead of the full
// payload.
type LargeToolOutputConfig struct {
	// Enabled controls whether large output handling is enabled. Default: true.
	Enabled *bool `json:"enabled,omitempty"`
	// MaxSizeBytes is the maximum size in bytes before output is written to a
	// temp file. Default: 50KB.
	MaxSizeBytes *int64 `json:"maxSizeBytes,omitempty"`
	// OutputDirectory is the directory to write temp files to. Defaults to the OS
	// temp directory.
	OutputDirectory string `json:"outputDir,omitempty"`
}

// ToolSearchConfig allows to configure tool search behavior.
// Tool search defers tools to keep the model's active tool set small.
// To override the tool-search tool's implementation, register a
// [Tool] named "tool_search_tool" with OverridesBuiltInTool set to true.
type ToolSearchConfig struct {
	// Controls whether tool search is enabled.
	Enabled *bool `json:"enabled,omitempty"`
	// DeferThreshold is the tool count above which MCP and external tools are
	// deferred behind tool search. When nil, the runtime default (30) applies.
	DeferThreshold *int `json:"deferThreshold,omitempty"`
}

// SessionFSCapabilities declares optional provider capabilities.
type SessionFSCapabilities struct {
	// Sqlite indicates whether the provider supports SQLite query/exists operations.
	Sqlite bool
}

// SessionFSConfig configures a custom session filesystem provider.
type SessionFSConfig struct {
	// InitialWorkingDirectory is the initial working directory for sessions.
	InitialWorkingDirectory string
	// SessionStatePath is the path within each session's filesystem where the runtime stores
	// session-scoped files such as events, checkpoints, and temp files.
	SessionStatePath string
	// Conventions identifies the path conventions used by this filesystem provider.
	Conventions rpc.SessionFSSetProviderConventions
	// Capabilities declares optional provider capabilities such as SQLite support.
	Capabilities *SessionFSCapabilities
}

// ExpFlagValue is a single ExP (Experiment Platform) flag value. ExP
// assignments resolve to a string, number (float64/int), bool, or nil.
type ExpFlagValue any

// ExpConfigEntry is a single configuration entry in a
// [CopilotExpAssignmentResponse]. Each entry carries an identifier and a bag of
// typed parameter values.
type ExpConfigEntry struct {
	// ID identifies the configuration entry. Serialized on the wire as "Id".
	ID string `json:"Id"`
	// Parameters holds parameter values keyed by parameter name.
	Parameters map[string]ExpFlagValue `json:"Parameters"`
}

// CopilotExpAssignmentResponse is ExP ("flight") assignment data, in the same
// JSON shape the Copilot CLI fetches from the experimentation service. Field
// names are PascalCase to match the on-the-wire contract consumed by the
// runtime.
type CopilotExpAssignmentResponse struct {
	// Features lists the enabled feature names.
	Features []string `json:"Features"`
	// Flights holds the assigned flights keyed by flight name.
	Flights map[string]string `json:"Flights"`
	// Configs holds configuration entries carrying typed parameter values.
	Configs []ExpConfigEntry `json:"Configs"`
	// ParameterGroups is an opaque parameter-group payload passed through
	// untouched. Optional.
	ParameterGroups any `json:"ParameterGroups,omitempty"`
	// FlightingVersion is the version of the flighting configuration. Optional.
	FlightingVersion *int `json:"FlightingVersion,omitempty"`
	// ImpressionID is the impression identifier for the assignment. Optional.
	// Serialized on the wire as "ImpressionId".
	ImpressionID *string `json:"ImpressionId,omitempty"`
	// AssignmentContext is the assignment context string forwarded to CAPI and
	// telemetry.
	AssignmentContext string `json:"AssignmentContext"`
}

// MarshalJSON normalizes the required collection fields so a zero-value
// response serializes them as JSON arrays/objects rather than null, which the
// runtime can otherwise treat as a malformed assignment payload and drop.
func (r CopilotExpAssignmentResponse) MarshalJSON() ([]byte, error) {
	type wire CopilotExpAssignmentResponse
	w := wire(r)
	if w.Features == nil {
		w.Features = []string{}
	}
	if w.Flights == nil {
		w.Flights = map[string]string{}
	}
	if w.Configs == nil {
		w.Configs = []ExpConfigEntry{}
	}
	return json.Marshal(w)
}

// MarshalJSON normalizes the required Parameters map so an entry serializes it
// as a JSON object rather than null.
func (e ExpConfigEntry) MarshalJSON() ([]byte, error) {
	type wire ExpConfigEntry
	w := wire(e)
	if w.Parameters == nil {
		w.Parameters = map[string]ExpFlagValue{}
	}
	return json.Marshal(w)
}

// GitHubMCPToolConfig configures the built-in GitHub MCP server.
//
// DisableFormDeferral only applies to the built-in GitHub MCP server and only
// has an effect when MCP Apps and form-backed GitHub tools are enabled.
type GitHubMCPToolConfig struct {
	EnableAllTools      *bool    `json:"enableAllTools,omitempty"`
	AdditionalToolsets  []string `json:"additionalToolsets,omitempty"`
	AdditionalTools     []string `json:"additionalTools,omitempty"`
	EnableInsidersMode  *bool    `json:"enableInsidersMode,omitempty"`
	DisableFormDeferral *bool    `json:"disableFormDeferral,omitempty"`
}

// SessionConfig configures a new session
type SessionConfig struct {
	// SessionID is an optional custom session ID
	SessionID string
	// ClientName identifies the application using the SDK.
	// Included in the User-Agent header for API requests.
	ClientName string
	// Model to use for this session
	Model string
	// ReasoningEffort level for models that support it.
	// Valid values: "low", "medium", "high", "xhigh", "max"
	// Only applies to models where capabilities.supports.reasoningEffort is true.
	ReasoningEffort string
	// ReasoningSummary mode for models that support configurable reasoning summaries.
	// Use ReasoningSummaryNone to suppress summary output regardless of whether reasoning is enabled.
	ReasoningSummary ReasoningSummary
	// ContextTier pins the session to a context window tier for models that support it.
	// Use ContextTierDefault or ContextTierLongContext for the currently known tiers.
	ContextTier ContextTier
	// ConfigDirectory overrides the default configuration directory location.
	// When specified, the session will use this directory for storing config and state.
	ConfigDirectory string
	// EnableConfigDiscovery enables runtime discovery of supported configuration.
	// Explicitly supplied configuration takes precedence over discovered values.
	// Nil leaves the runtime default unchanged; use Bool(false) to explicitly disable discovery.
	EnableConfigDiscovery *bool
	// SkipEmbeddingRetrieval, when non-nil, controls embedding-based retrieval
	// for this session. Use in multitenant deployments to prevent cross-session
	// information leakage through the shared embedding cache.
	SkipEmbeddingRetrieval *bool
	// EmbeddingCacheStorage controls how the embedding cache is stored for this session.
	// "persistent" caches on disk and shares across sessions/restarts.
	// "in-memory" caches in memory only and discards when the session ends.
	EmbeddingCacheStorage *string
	// OrganizationCustomInstructions provides organization-level custom instructions
	// to include in the system prompt. Allows hosts to inject organization-specific
	// guidance without relying on filesystem-based instruction discovery.
	OrganizationCustomInstructions *string
	// EnableOnDemandInstructionDiscovery, when non-nil, controls on-demand discovery
	// of instruction files (AGENTS.md, .github/copilot-instructions.md, etc.) after
	// successful file views.
	EnableOnDemandInstructionDiscovery *bool
	// EnableFileHooks, when non-nil, controls loading of file-based hooks from
	// .github/hooks/. This is separate from the Hooks callback parameter which
	// gates SDK hook event registration.
	EnableFileHooks *bool
	// EnableHostGitOperations, when non-nil, controls git operations on the host
	// filesystem (branch detection, file status, commit history). When false, no
	// git context is surfaced in the system prompt.
	EnableHostGitOperations *bool
	// EnableSessionStore, when non-nil, controls the cross-session store for search
	// and retrieval. When false, session content is not written to or read from the
	// shared session store.
	EnableSessionStore *bool
	// EnableSkills, when non-nil, controls skill loading (including builtin skills
	// and discovered skill directories). When false, no skills are loaded regardless
	// of SkillDirectories or EnableConfigDiscovery settings.
	EnableSkills *bool
	// Tools exposes caller-implemented tools to the CLI. A Tool with a nil Handler
	// is declaration-only; the consumer must resolve its calls via pending tool RPCs.
	Tools []Tool
	// SystemMessage configures system message customization
	SystemMessage *SystemMessageConfig
	// AvailableTools is a list of tool names to allow. When specified, only these tools will be available.
	// Takes precedence over ExcludedTools.
	AvailableTools []string
	// ExcludedTools is a list of tool names to disable. All other tools remain available.
	// Ignored if AvailableTools is specified.
	ExcludedTools []string
	// ExcludedBuiltInAgents is a list of built-in agent names to exclude from
	// the session. Excluded built-in agents are hidden from discovery and cannot
	// be selected or invoked unless a custom agent with the same name is
	// configured.
	ExcludedBuiltInAgents []string
	// OnPermissionRequest is an optional handler for permission requests from the server.
	// When nil, permission requests are surfaced as events and left pending for the
	// consumer to resolve via pending permission RPCs.
	OnPermissionRequest PermissionHandlerFunc
	// OnMCPAuthRequest is an optional handler for MCP OAuth requests from MCP servers.
	// When provided, the SDK can satisfy MCP server OAuth requests with host-provided
	// token data or cancellation.
	OnMCPAuthRequest MCPAuthHandler
	// OnUserInputRequest is a handler for user input requests from the agent (enables ask_user tool)
	OnUserInputRequest UserInputHandler
	// Hooks configures hook handlers for session lifecycle events
	Hooks *SessionHooks
	// WorkingDirectory is the working directory for the session.
	// Tool operations will be relative to this directory.
	WorkingDirectory string
	// AdditionalDirectories are directories the agent may access beyond WorkingDirectory.
	// Relative paths are resolved against WorkingDirectory. Re-supply them when resuming.
	AdditionalDirectories []string
	// Streaming enables streaming of assistant message and reasoning chunks.
	// When non-nil and true, assistant.message_delta and assistant.reasoning_delta
	// events with deltaContent are sent as the response is generated.
	// When nil, the runtime decides (currently defaults to non-streaming).
	Streaming *bool
	// IncludeSubAgentStreamingEvents includes sub-agent streaming events in the
	// event stream. When true, streaming delta events from sub-agents (e.g.,
	// assistant.message_delta, assistant.reasoning_delta, assistant.streaming_delta
	// with agentId set) are forwarded to this connection. When false, only
	// non-streaming sub-agent events and subagent.* lifecycle events are forwarded;
	// streaming deltas from sub-agents are suppressed. When nil, defaults to true.
	IncludeSubAgentStreamingEvents *bool
	// Provider configures a custom model provider (BYOK)
	Provider *ProviderConfig
	// Capi configures provider-scoped CAPI (Copilot API) session options.
	Capi *CapiSessionOptions
	// Providers configures named BYOK provider connections. Additive to Copilot
	// API auth (unlike Provider); combine with Models. Cannot be combined with Provider.
	//
	// Experimental: Providers is part of an experimental multi-provider BYOK
	// surface and may change or be removed in future SDK or CLI releases.
	Providers []NamedProviderConfig
	// Models adds BYOK model definitions to the session's selectable model list,
	// each referencing a Providers entry by name.
	//
	// Experimental: Models is part of an experimental multi-provider BYOK
	// surface and may change or be removed in future SDK or CLI releases.
	Models []ProviderModelConfig
	// EnableSessionTelemetry enables or disables internal session telemetry for this session.
	// When false, disables session telemetry. When nil (the default) or true,
	// telemetry is enabled for GitHub-authenticated sessions. When a custom
	// Provider (BYOK) is configured, session telemetry is always disabled
	// regardless of this setting. This is independent of the OpenTelemetry
	// configuration in ClientOptions.Telemetry.
	EnableSessionTelemetry *bool
	// EnableCitations enables native model citations for supported providers.
	//
	// Experimental: EnableCitations is part of an experimental model capability
	// surface and may change or be removed in future SDK or CLI releases.
	EnableCitations *bool
	// SessionLimits applies limits to this session's current accounting window.
	//
	// Experimental: SessionLimits is part of an experimental runtime accounting
	// surface and may change or be removed in future SDK or CLI releases.
	SessionLimits *rpc.SessionLimitsConfig
	// EnableExperimentalMode controls whether the session enables experimental
	// features. When nil, it defaults to false in [ModeEmpty]; otherwise the
	// runtime decides.
	EnableExperimentalMode *bool
	// SkipCustomInstructions, when non-nil, controls whether the runtime loads
	// custom instruction files. See also [ClientOptions.Mode] = [ModeEmpty].
	SkipCustomInstructions *bool
	// CustomAgentsLocalOnly, when non-nil, restricts custom agents to those
	// defined locally. See also [ClientOptions.Mode] = [ModeEmpty].
	CustomAgentsLocalOnly *bool
	// CoauthorEnabled, when non-nil, controls whether the `coauthor` tool is
	// exposed. See also [ClientOptions.Mode] = [ModeEmpty].
	CoauthorEnabled *bool
	// ManageScheduleEnabled, when non-nil, controls whether the
	// `manage_schedule` tool is exposed. See also [ClientOptions.Mode] =
	// [ModeEmpty].
	ManageScheduleEnabled *bool
	// ModelCapabilities overrides individual model capabilities resolved by the runtime.
	// Only non-nil fields are applied over the runtime-resolved capabilities.
	ModelCapabilities *rpc.ModelCapabilitiesOverride
	// MCPServers configures MCP servers for the session
	MCPServers map[string]MCPServerConfig
	// MCPOAuthTokenStorage controls how MCP OAuth tokens are stored for this session.
	// When empty, the runtime default ("in-memory") is used.
	MCPOAuthTokenStorage string
	// CustomAgents configures custom agents for the session
	CustomAgents []CustomAgentConfig
	// DefaultAgent configures the default agent (the built-in agent that handles turns when no custom agent is selected).
	// Use ExcludedTools to hide tools from the default agent while keeping them available to sub-agents.
	DefaultAgent *DefaultAgentConfig
	// Agent is the name of the custom agent to activate when the session starts.
	// Must match the Name of one of the agents in CustomAgents.
	Agent string
	// SkillDirectories is a list of directories to load skills from
	SkillDirectories []string
	// PluginDirectories is a list of local filesystem paths to Open Plugins-format
	// directories (https://open-plugins.com/) to load for this session.
	// Relative paths resolve against WorkingDirectory (or the runtime cwd if unset).
	// Treated as an explicit opt-in: plugin agents and rules load even when
	// EnableConfigDiscovery is false.
	PluginDirectories []string
	// InstructionDirectories is a list of additional directories to search for custom instruction files
	InstructionDirectories []string
	// DisabledSkills is a list of skill names to disable
	DisabledSkills []string
	// DisabledMCPServers is a list of exact MCP server names to disable for this session.
	// Disabled servers are not started or authenticated on create or cold resume.
	// A resident resume cannot stop servers that are already running.
	DisabledMCPServers []string
	// InfiniteSessions configures infinite sessions for persistent workspaces and automatic compaction.
	// When enabled (default), sessions automatically manage context limits and persist state.
	InfiniteSessions *InfiniteSessionConfig
	// LargeOutput configures handling of large tool outputs. When a tool produces
	// output exceeding the configured size, the output is written to a temp file
	// and a reference is returned to the model instead of the full payload.
	LargeOutput *LargeToolOutputConfig
	// ToolSearch overrides the runtime's built-in tool-search behavior, which
	// defers rarely used tools behind a searchable index. When nil, the runtime
	// default applies.
	ToolSearch *ToolSearchConfig
	// Memory configures the memory feature for the session. When omitted, the
	// runtime default applies.
	Memory *MemoryConfiguration
	// OnEvent is an optional event handler that is registered on the session before
	// the session.create RPC is issued. This guarantees that early events emitted
	// by the CLI during session creation (e.g. session.start) are delivered to the
	// handler. Equivalent to calling session.On(handler) immediately after creation,
	// but executes earlier in the lifecycle so no events are missed.
	OnEvent SessionEventHandler
	// CreateSessionFSProvider supplies a handler for session filesystem operations.
	// This takes effect only when ClientOptions.SessionFS is configured.
	CreateSessionFSProvider func(session *Session) SessionFSProvider
	// Commands registers slash-commands for this session. Each command appears as
	// /name in the CLI TUI for the user to invoke. The Handler is called when the
	// command is executed.
	Commands []CommandDefinition
	// OnElicitationRequest is a handler for elicitation requests from the server.
	// When provided, the server may call back to this client for form-based UI dialogs
	// (e.g. from MCP tools). Also enables the elicitation capability on the session.
	OnElicitationRequest ElicitationHandler
	// OnExitPlanModeRequest is a handler for exit-plan-mode requests from the server.
	// When provided, enables exitPlanMode.request callbacks for the session.
	OnExitPlanModeRequest ExitPlanModeRequestHandler
	// OnAutoModeSwitchRequest is a handler for auto-mode-switch requests from the server.
	// When provided, enables autoModeSwitch.request callbacks for the session.
	OnAutoModeSwitchRequest AutoModeSwitchRequestHandler
	// EnableMCPApps enables MCP Apps (SEP-1865) UI passthrough on this session.
	//
	// Experimental: EnableMCPApps is part of an experimental wire-protocol
	// surface (SEP-1865) and may change or be removed in a future release.
	//
	// When true AND the runtime has MCP Apps enabled (via the MCP_APPS feature
	// flag or COPILOT_MCP_APPS=true environment override), the runtime adds the
	// mcp-apps capability to the session, which causes it to advertise the
	// extensions.io.modelcontextprotocol/ui extension to MCP servers (so they
	// expose _meta.ui.resourceUri on tools) and to expose the
	// session.rpc.mcp.apps.{listTools,callTool,readResource,setHostContext,
	// getHostContext,diagnose} JSON-RPC methods.
	//
	// If the runtime gate is off, the opt-in is silently dropped server-side
	// (the runtime logs a warning); the session is created normally but the
	// MCP Apps surface is unavailable. Inspect the runtime's
	// capabilities.ui.mcpApps on the create/resume response to detect this.
	//
	// SDK consumers MUST set this to true only when they have an iframe renderer
	// that can display ui:// MCP App bundles. Setting it without a renderer will
	// cause MCP servers to register UI-enabled tool variants the consumer cannot
	// display.
	EnableMCPApps bool
	// GitHubMCPToolConfig configures the built-in GitHub MCP server.
	// DisableFormDeferral only applies to that server and only has an effect
	// when MCP Apps and form-backed GitHub tools are enabled.
	GitHubMCPToolConfig *GitHubMCPToolConfig
	// GitHubToken is an optional per-session GitHub token used for authentication.
	// When provided, the session authenticates as the token's owner instead of
	// using the global client-level auth.
	GitHubToken string `json:"-"`
	// RemoteSession controls per-session remote behavior:
	//   - "off" — local only, no remote export (default)
	//   - "export" — export session events to GitHub without enabling remote steering
	//   - "on" — export to GitHub AND enable remote steering
	RemoteSession rpc.RemoteSessionMode
	// Cloud creates a remote session in the cloud instead of a local session.
	// The optional repository is associated with the cloud session.
	Cloud *CloudSessionOptions
	// Canvases declares canvases this session provides. Sent over the wire on
	// `session.create`. CanvasHandler must be set when this is non-empty (the
	// SDK does not enforce this — declarations without a handler will surface
	// canvas RPCs that return a canvas_handler_unset error envelope).
	Canvases []CanvasDeclaration
	// RequestCanvasRenderer asks the host to enable canvas rendering for this session.
	RequestCanvasRenderer *bool
	// RequestExtensions asks the host to surface declared canvases as agent-visible extensions.
	RequestExtensions *bool
	// ExtensionSDKPath optionally overrides the bundled `@github/copilot-sdk` drop
	// injected into extension subprocesses. When set to an absolute path containing
	// a valid `copilot-sdk/` folder (with `index.js` and `extension.js` at the
	// root), the host injects the override into every forked extension; invalid or
	// missing paths fall back to the bundled SDK silently.
	ExtensionSDKPath *string
	// CanvasHandler receives inbound canvas.open / canvas.close / canvas.action.invoke
	// requests for this session. The SDK does not maintain a per-canvas registry;
	// the handler must dispatch on CanvasProviderOpenRequest.CanvasID itself.
	CanvasHandler CanvasHandler `json:"-"`
	// ExtensionInfo identifies the stable extension providing this session's canvases.
	ExtensionInfo *ExtensionInfo
	// CanvasProvider is the stable identity for a host/SDK connection that
	// supplies built-in canvases, so they survive reconnect and CLI restart.
	CanvasProvider *CanvasProviderIdentity
	// ExpAssignments injects ExP assignment ("flight") data for this session,
	// in the same JSON shape the Copilot CLI fetches from the experimentation
	// service (CopilotExpAssignmentResponse). When supplied, the runtime feeds
	// it into the same feature-flag path as CLI-fetched assignments and stamps
	// it onto telemetry and the CAPI request header. When absent, the session
	// does not block on ExP. Malformed payloads are dropped by the runtime
	// (fail-open).
	//
	// Internal: ExpAssignments is part of the SDK's internal API surface,
	// intended for trusted out-of-process integrators, and is not intended for
	// general external use.
	ExpAssignments *CopilotExpAssignmentResponse
	// EnableManagedSettings, when set to true, opts the runtime into
	// self-fetching enterprise managed settings (bypass-permissions policy) at
	// session bootstrap using the session's GitHubToken. Requires GitHubToken to
	// be set; if omitted, the runtime is expected to reject session creation
	// (fail-closed). Unset behaves exactly as before.
	EnableManagedSettings *bool
	// ManagedSettings supplies host-injected enterprise managed settings for
	// the session. Unlike EnableManagedSettings (which asks the runtime to
	// self-fetch account/org and device policy), this provides the managed
	// policy directly. The runtime validates it with the same
	// managed-permission parser it uses for fetched policy and composes it
	// restrictively with any self-fetched (server) and device-managed (MDM)
	// layers. It is startup-only and not persisted: re-supply it on resume,
	// where it replaces the prior injected layer (omitting it clears the
	// layer). It may be combined with EnableManagedSettings. Requires a runtime
	// whose RPC schema includes managedSettings.
	ManagedSettings *ManagedSettings
}

// ManagedSettings is host-injected enterprise managed settings for a session.
// The first supported contract is permissions-only; unknown sibling keys are
// rejected by the runtime. Serialized on the wire as managedSettings.
type ManagedSettings struct {
	// Permissions is the managed permission policy for the session.
	Permissions *ManagedSettingsPermissions `json:"permissions,omitempty"`
}

// DisableBypassPermissionsMode is the managed bypass-permissions policy.
type DisableBypassPermissionsMode = rpc.DisableBypassPermissionsMode

const (
	// DisableBypassPermissionsModeDisable turns off bypass-permissions mode.
	DisableBypassPermissionsModeDisable = rpc.DisableBypassPermissionsModeDisable
)

// ManagedSettingsPermissions is the permissions-only managed policy injected
// via ManagedSettings. Rule strings use the same vocabulary the runtime
// accepts for fetched managed policy (e.g. "Read(**)", "Shell(git push *)");
// malformed rules are rejected by the runtime at session creation.
type ManagedSettingsPermissions struct {
	// DisableBypassPermissionsMode, when set to "disable", turns off
	// bypass-permissions ("yolo") mode for the session. Deny-wins: no other
	// layer can re-enable it.
	DisableBypassPermissionsMode DisableBypassPermissionsMode `json:"disableBypassPermissionsMode,omitempty"`
	// Deny lists operations that must always be denied. Unioned across layers.
	Deny []string `json:"deny,omitzero"`
	// Ask lists operations that must prompt for approval. Unioned across layers.
	Ask []string `json:"ask,omitzero"`
	// Allow lists operations permitted without prompting. Every declared allow
	// list across managed layers must admit an operation for it to be allowed.
	Allow []string `json:"allow,omitzero"`
}

// ToolDefer controls whether a tool may be deferred (loaded lazily via tool
// search) rather than always pre-loaded.
type ToolDefer string

const (
	// ToolDeferAuto allows the tool to be deferred and surfaced through tool search.
	ToolDeferAuto ToolDefer = "auto"
	// ToolDeferNever forces the tool to always be pre-loaded.
	ToolDeferNever ToolDefer = "never"
)

type Tool struct {
	Name                 string         `json:"name"`
	Description          string         `json:"description,omitempty"`
	Parameters           map[string]any `json:"parameters,omitzero"`
	OverridesBuiltInTool bool           `json:"overridesBuiltInTool,omitempty"`
	SkipPermission       bool           `json:"skipPermission,omitempty"`
	// IsTerminal reports that a successful call to this tool ends the agent
	// turn: the runtime halts instead of feeding the result back to the model
	// for another round. A failed call leaves the loop running so the model can
	// read the error and retry.
	IsTerminal bool `json:"isTerminal,omitempty"`
	// Defer controls whether the tool may be deferred (loaded lazily via tool
	// search) rather than always pre-loaded. When empty, the runtime decides.
	Defer ToolDefer `json:"defer,omitempty"`
	// Metadata is opaque, host-defined metadata associated with the tool
	// definition. Keys are namespaced and not part of the stable public API;
	// values are not interpreted and may be recognized to inform host-specific
	// behavior. Unknown keys are preserved and round-tripped untouched.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Handler is optional. When nil, the SDK exposes the tool declaration but does
	// not automatically invoke it.
	Handler ToolHandler `json:"-"`
}

// ToolInvocation describes a tool call initiated by Copilot
type ToolInvocation struct {
	SessionID  string
	ToolCallID string
	ToolName   string
	Arguments  any

	// AvailableTools is a snapshot of the session's currently initialized
	// tools. The SDK populates it only when this invocation targets the
	// built-in tool-search tool ("tool_search_tool"), so a tool-search
	// override can rank/filter the live catalog -- including MCP tools
	// configured in settings -- without issuing its own RPC. It is nil for
	// every other tool invocation.
	AvailableTools []rpc.CurrentToolMetadata

	// TraceContext carries the W3C Trace Context propagated from the CLI's
	// execute_tool span.  Pass this to OpenTelemetry-aware code so that
	// child spans created inside the handler are parented to the CLI span.
	// When no trace context is available this will be context.Background().
	TraceContext context.Context
}

// ToolHandler executes a tool invocation.
// The handler should return a ToolResult. Returning an error marks the tool execution as a failure.
type ToolHandler func(invocation ToolInvocation) (ToolResult, error)

// ToolResult represents the result of a tool invocation.
type ToolResult struct {
	TextResultForLLM    string             `json:"textResultForLlm"`
	BinaryResultsForLLM []ToolBinaryResult `json:"binaryResultsForLlm,omitempty"`
	ResultType          string             `json:"resultType"`
	Error               string             `json:"error,omitempty"`
	SessionLog          string             `json:"sessionLog,omitempty"`
	ToolTelemetry       map[string]any     `json:"toolTelemetry,omitempty"`
	// ToolReferences lists names of tools returned by a tool-search tool.
	ToolReferences []string `json:"toolReferences,omitempty"`
}

// CommandContext provides context about a slash-command invocation.
type CommandContext struct {
	// SessionID is the session where the command was invoked.
	SessionID string
	// Command is the full command text (e.g. "/deploy production").
	Command string
	// CommandName is the command name without the leading / (e.g. "deploy").
	CommandName string
	// Args is the raw argument string after the command name.
	Args string
}

// CommandHandler is invoked when a registered slash-command is executed.
type CommandHandler func(ctx CommandContext) error

// CommandDefinition registers a slash-command. Name is shown in the CLI TUI
// as /name for the user to invoke.
type CommandDefinition struct {
	// Name is the command name (without leading /).
	Name string
	// Description is a human-readable description shown in command completion UI.
	Description string
	// Handler is invoked when the command is executed.
	Handler CommandHandler
}

// SessionCapabilities describes what features the host supports.
type SessionCapabilities struct {
	UI *UICapabilities `json:"ui,omitempty"`
}

// UICapabilities describes host UI feature support.
type UICapabilities struct {
	// Elicitation indicates whether the host supports interactive elicitation dialogs.
	Elicitation bool `json:"elicitation,omitempty"`
	// MCPApps indicates whether the runtime has accepted the session's MCP Apps
	// (SEP-1865) opt-in. True when the consumer set EnableMCPApps=true on
	// create/resume AND the runtime's MCP_APPS feature flag (or
	// COPILOT_MCP_APPS=true env override) is on. Otherwise false, indicating
	// the runtime silently dropped the opt-in.
	//
	// Experimental: MCPApps is part of an experimental wire-protocol surface
	// (SEP-1865) and may change or be removed in a future release.
	MCPApps bool `json:"mcpApps,omitempty"`
}

// ElicitationAction is the user response to an elicitation request.
type ElicitationAction = rpc.UIElicitationResponseAction

// Elicitation action values.
const (
	ElicitationActionAccept  ElicitationAction = rpc.UIElicitationResponseActionAccept
	ElicitationActionCancel  ElicitationAction = rpc.UIElicitationResponseActionCancel
	ElicitationActionDecline ElicitationAction = rpc.UIElicitationResponseActionDecline
)

// ElicitationFieldValue is a primitive value submitted for an elicitation form field.
// Supported values are string, numeric types, bool, []string, and []any containing strings.
type ElicitationFieldValue = any

// ElicitationResult is the user's response to an elicitation dialog.
type ElicitationResult struct {
	// Action is the user response: accept, decline, or cancel.
	Action ElicitationAction `json:"action"`
	// Content holds form values submitted by the user when Action is accept.
	Content map[string]ElicitationFieldValue `json:"content,omitzero"`
}

// ElicitationSchema describes the form fields for an elicitation request.
type ElicitationSchema struct {
	// Properties contains form field definitions keyed by field name.
	Properties map[string]any `json:"properties"`
	// Required lists field names that must be submitted.
	Required []string `json:"required,omitzero"`
}

// ElicitationContext describes an elicitation request from the server,
// combining the request data with session context. Mirrors the
// single-argument pattern of CommandContext.
type ElicitationContext struct {
	// SessionID is the identifier of the session that triggered the request.
	SessionID string
	// Message describes what information is needed from the user.
	Message string
	// RequestedSchema is a JSON Schema describing the form fields (form mode only).
	RequestedSchema *ElicitationSchema
	// Mode is "form" for structured input, "url" for browser redirect.
	Mode *ElicitationRequestedMode
	// ElicitationSource is the source that initiated the request (e.g. MCP server name).
	ElicitationSource *string
	// URL to open in the user's browser (url mode only).
	URL *string
}

// ElicitationHandler handles elicitation requests from the server (e.g. from MCP tools).
// It receives an ElicitationContext and must return an ElicitationResult.
// If the handler returns an error the SDK auto-cancels the request.
type ElicitationHandler func(ctx ElicitationContext) (ElicitationResult, error)

// UIInputOptions configures a text input field for the Input convenience method.
type UIInputOptions struct {
	// Title label for the input field.
	Title string
	// Description text shown below the field.
	Description string
	// MinLength is the minimum character length.
	MinLength *int
	// MaxLength is the maximum character length.
	MaxLength *int
	// Format is a semantic format hint: "email", "uri", "date", or "date-time".
	Format string
	// Default is the pre-populated value.
	Default string
}

// SessionUI provides convenience methods for showing elicitation dialogs to the user.
// Obtained via [Session.UI]. Methods error if the host does not support elicitation.
type SessionUI struct {
	session *Session
}

// ResumeSessionConfig configures options when resuming a session
type ResumeSessionConfig struct {
	// ClientName identifies the application using the SDK.
	// Included in the User-Agent header for API requests.
	ClientName string
	// Model to use for this session. Can change the model when resuming.
	Model string
	// Tools exposes caller-implemented tools to the CLI. A Tool with a nil Handler
	// is declaration-only; the consumer must resolve its calls via pending tool RPCs.
	Tools []Tool
	// SystemMessage configures system message customization
	SystemMessage *SystemMessageConfig
	// AvailableTools is a list of tool names to allow. When specified, only these tools will be available.
	// Takes precedence over ExcludedTools.
	AvailableTools []string
	// ExcludedTools is a list of tool names to disable. All other tools remain available.
	// Ignored if AvailableTools is specified.
	ExcludedTools []string
	// ExcludedBuiltInAgents is a list of built-in agent names to exclude from
	// the session. Excluded built-in agents are hidden from discovery and cannot
	// be selected or invoked unless a custom agent with the same name is
	// configured.
	ExcludedBuiltInAgents []string
	// Provider configures a custom model provider
	Provider *ProviderConfig
	// Capi configures provider-scoped CAPI (Copilot API) session options.
	Capi *CapiSessionOptions
	// Providers configures named BYOK provider connections. Additive to Copilot
	// API auth (unlike Provider); combine with Models. Cannot be combined with Provider.
	//
	// Experimental: Providers is part of an experimental multi-provider BYOK
	// surface and may change or be removed in future SDK or CLI releases.
	Providers []NamedProviderConfig
	// Models adds BYOK model definitions to the session's selectable model list,
	// each referencing a Providers entry by name.
	//
	// Experimental: Models is part of an experimental multi-provider BYOK
	// surface and may change or be removed in future SDK or CLI releases.
	Models []ProviderModelConfig
	// EnableSessionTelemetry enables or disables internal session telemetry for this session.
	// When false, disables session telemetry. When nil (the default) or true,
	// telemetry is enabled for GitHub-authenticated sessions. When a custom
	// Provider (BYOK) is configured, session telemetry is always disabled
	// regardless of this setting. This is independent of the OpenTelemetry
	// configuration in ClientOptions.Telemetry.
	EnableSessionTelemetry *bool
	// EnableCitations enables native model citations for supported providers.
	//
	// Experimental: EnableCitations is part of an experimental model capability
	// surface and may change or be removed in future SDK or CLI releases.
	EnableCitations *bool
	// SessionLimits applies limits to this session's current accounting window.
	//
	// Experimental: SessionLimits is part of an experimental runtime accounting
	// surface and may change or be removed in future SDK or CLI releases.
	SessionLimits *rpc.SessionLimitsConfig
	// EnableExperimentalMode controls whether the session enables experimental
	// features. When nil, it defaults to false in [ModeEmpty]; otherwise the
	// runtime decides.
	EnableExperimentalMode *bool
	// SkipCustomInstructions, when non-nil, controls whether the runtime loads
	// custom instruction files. See also [ClientOptions.Mode] = [ModeEmpty].
	SkipCustomInstructions *bool
	// CustomAgentsLocalOnly, when non-nil, restricts custom agents to those
	// defined locally. See also [ClientOptions.Mode] = [ModeEmpty].
	CustomAgentsLocalOnly *bool
	// CoauthorEnabled, when non-nil, controls whether the `coauthor` tool is
	// exposed. See also [ClientOptions.Mode] = [ModeEmpty].
	CoauthorEnabled *bool
	// ManageScheduleEnabled, when non-nil, controls whether the
	// `manage_schedule` tool is exposed. See also [ClientOptions.Mode] =
	// [ModeEmpty].
	ManageScheduleEnabled *bool
	// ModelCapabilities overrides individual model capabilities resolved by the runtime.
	// Only non-nil fields are applied over the runtime-resolved capabilities.
	ModelCapabilities *rpc.ModelCapabilitiesOverride
	// ReasoningEffort level for models that support it.
	// Valid values: "low", "medium", "high", "xhigh", "max"
	ReasoningEffort string
	// ReasoningSummary mode for models that support configurable reasoning summaries.
	// Use ReasoningSummaryNone to suppress summary output regardless of whether reasoning is enabled.
	ReasoningSummary ReasoningSummary
	// ContextTier pins the session to a context window tier for models that support it.
	// Use ContextTierDefault or ContextTierLongContext for the currently known tiers.
	ContextTier ContextTier
	// OnPermissionRequest is an optional handler for permission requests from the server.
	// When nil, permission requests are surfaced as events and left pending for the
	// consumer to resolve via pending permission RPCs.
	OnPermissionRequest PermissionHandlerFunc
	// OnMCPAuthRequest is an optional handler for MCP OAuth requests from MCP servers.
	// See SessionConfig.OnMCPAuthRequest.
	OnMCPAuthRequest MCPAuthHandler
	// OnUserInputRequest is a handler for user input requests from the agent (enables ask_user tool)
	OnUserInputRequest UserInputHandler
	// Hooks configures hook handlers for session lifecycle events
	Hooks *SessionHooks
	// WorkingDirectory is the working directory for the session.
	// Tool operations will be relative to this directory.
	WorkingDirectory string
	// AdditionalDirectories are directories the agent may access beyond WorkingDirectory.
	// Relative paths are resolved against WorkingDirectory. Re-supply them when resuming.
	AdditionalDirectories []string
	// ConfigDirectory overrides the default configuration directory location.
	ConfigDirectory string
	// EnableConfigDiscovery enables runtime discovery of supported configuration.
	// Explicitly supplied configuration takes precedence over discovered values.
	// Nil leaves the runtime default unchanged; use Bool(false) to explicitly disable discovery.
	EnableConfigDiscovery *bool
	// SkipEmbeddingRetrieval, when non-nil, controls embedding-based retrieval
	// for this session. Use in multitenant deployments to prevent cross-session
	// information leakage through the shared embedding cache.
	SkipEmbeddingRetrieval *bool
	// EmbeddingCacheStorage controls how the embedding cache is stored for this session.
	// "persistent" caches on disk and shares across sessions/restarts.
	// "in-memory" caches in memory only and discards when the session ends.
	EmbeddingCacheStorage *string
	// OrganizationCustomInstructions provides organization-level custom instructions
	// to include in the system prompt.
	OrganizationCustomInstructions *string
	// EnableOnDemandInstructionDiscovery, when non-nil, controls on-demand discovery
	// of instruction files after successful file views.
	EnableOnDemandInstructionDiscovery *bool
	// EnableFileHooks, when non-nil, controls loading of file-based hooks from
	// .github/hooks/. This is separate from the Hooks callback parameter which
	// gates SDK hook event registration.
	EnableFileHooks *bool
	// EnableHostGitOperations, when non-nil, controls git operations on the host
	// filesystem.
	EnableHostGitOperations *bool
	// EnableSessionStore, when non-nil, controls the cross-session store for search
	// and retrieval across sessions.
	EnableSessionStore *bool
	// EnableSkills, when non-nil, controls skill loading.
	EnableSkills *bool
	// Streaming enables streaming of assistant message and reasoning chunks.
	// When non-nil and true, assistant.message_delta and assistant.reasoning_delta
	// events with deltaContent are sent as the response is generated.
	// When nil, the runtime decides (currently defaults to non-streaming).
	Streaming *bool
	// IncludeSubAgentStreamingEvents includes sub-agent streaming events in the
	// event stream. When true, streaming delta events from sub-agents (e.g.,
	// assistant.message_delta, assistant.reasoning_delta, assistant.streaming_delta
	// with agentId set) are forwarded to this connection. When false, only
	// non-streaming sub-agent events and subagent.* lifecycle events are forwarded;
	// streaming deltas from sub-agents are suppressed. When nil, defaults to true.
	IncludeSubAgentStreamingEvents *bool
	// MCPServers configures MCP servers for the session
	MCPServers map[string]MCPServerConfig
	// MCPOAuthTokenStorage controls how MCP OAuth tokens are stored for this session.
	// When empty, the runtime default ("in-memory") is used.
	MCPOAuthTokenStorage string
	// CustomAgents configures custom agents for the session
	CustomAgents []CustomAgentConfig
	// DefaultAgent configures the default agent (the built-in agent that handles turns when no custom agent is selected).
	DefaultAgent *DefaultAgentConfig
	// Agent is the name of the custom agent to activate when the session starts.
	// Must match the Name of one of the agents in CustomAgents.
	Agent string
	// SkillDirectories is a list of directories to load skills from
	SkillDirectories []string
	// PluginDirectories is a list of local filesystem paths to Open Plugins-format
	// directories (https://open-plugins.com/) to load for this session.
	// Relative paths resolve against WorkingDirectory (or the runtime cwd if unset).
	// Treated as an explicit opt-in: plugin agents and rules load even when
	// EnableConfigDiscovery is false.
	PluginDirectories []string
	// InstructionDirectories is a list of additional directories to search for custom instruction files
	InstructionDirectories []string
	// DisabledSkills is a list of skill names to disable
	DisabledSkills []string
	// DisabledMCPServers is a list of exact MCP server names to disable for this session.
	// Disabled servers are not started or authenticated on create or cold resume.
	// A resident resume cannot stop servers that are already running.
	DisabledMCPServers []string
	// InfiniteSessions configures infinite sessions for persistent workspaces and automatic compaction.
	InfiniteSessions *InfiniteSessionConfig
	// LargeOutput configures handling of large tool outputs. When a tool produces
	// output exceeding the configured size, the output is written to a temp file
	// and a reference is returned to the model instead of the full payload.
	LargeOutput *LargeToolOutputConfig
	// ToolSearch overrides the runtime's built-in tool-search behavior, which
	// defers rarely used tools behind a searchable index. When nil, the runtime
	// default applies.
	ToolSearch *ToolSearchConfig
	// Memory configures the memory feature for the session. When omitted, the
	// runtime default applies.
	Memory *MemoryConfiguration
	// GitHubToken is an optional per-session GitHub token used for authentication.
	// When provided, the session authenticates as the token's owner instead of
	// using the global client-level auth.
	GitHubToken string `json:"-"`
	// RemoteSession controls per-session remote behavior.
	// See SessionConfig.RemoteSession for details.
	RemoteSession rpc.RemoteSessionMode
	// SuppressResumeEvent, when true, skips emitting the session.resume event.
	// Useful for reconnecting to a session without triggering resume-related side effects.
	SuppressResumeEvent bool
	// ContinuePendingWork, when non-nil, controls whether the runtime continues any
	// tool calls or permission prompts that were still pending when the session was
	// last suspended. Nil leaves the runtime default unchanged; use Bool(false) to
	// explicitly treat pending work as interrupted on resume.
	//
	// For permission requests, the runtime re-emits permission.requested so the
	// registered OnPermissionRequest handler can re-prompt; for external tool calls,
	// the consumer is expected to supply the result via the corresponding low-level
	// RPC method.
	ContinuePendingWork *bool
	// OnEvent is an optional event handler registered before the session.resume RPC
	// is issued, ensuring early events are delivered. See SessionConfig.OnEvent.
	OnEvent SessionEventHandler
	// CreateSessionFSProvider supplies a handler for session filesystem operations.
	// This takes effect only when ClientOptions.SessionFS is configured.
	CreateSessionFSProvider func(session *Session) SessionFSProvider
	// Commands registers slash-commands for this session. See SessionConfig.Commands.
	Commands []CommandDefinition
	// OnElicitationRequest is a handler for elicitation requests from the server.
	// See SessionConfig.OnElicitationRequest.
	OnElicitationRequest ElicitationHandler
	// OnExitPlanModeRequest is a handler for exit-plan-mode requests from the server.
	// See SessionConfig.OnExitPlanModeRequest.
	OnExitPlanModeRequest ExitPlanModeRequestHandler
	// OnAutoModeSwitchRequest is a handler for auto-mode-switch requests from the server.
	// See SessionConfig.OnAutoModeSwitchRequest.
	OnAutoModeSwitchRequest AutoModeSwitchRequestHandler
	// EnableMCPApps enables MCP Apps (SEP-1865) UI passthrough on resume.
	// See SessionConfig.EnableMCPApps.
	//
	// Experimental: EnableMCPApps is part of an experimental wire-protocol
	// surface (SEP-1865) and may change or be removed in a future release.
	EnableMCPApps bool
	// GitHubMCPToolConfig configures the built-in GitHub MCP server.
	// DisableFormDeferral only applies to that server and only has an effect
	// when MCP Apps and form-backed GitHub tools are enabled.
	GitHubMCPToolConfig *GitHubMCPToolConfig
	// Canvases declares canvases this session provides. Sent over the wire on
	// `session.resume`. See SessionConfig.Canvases.
	Canvases []CanvasDeclaration
	// OpenCanvases declares canvas instances the caller knows were open before
	// this resume so the runtime can re-attach them. Sent over the wire on
	// `session.resume` as `openCanvases`.
	OpenCanvases []rpc.OpenCanvasInstance
	// RequestCanvasRenderer asks the host to enable canvas rendering for this session.
	RequestCanvasRenderer *bool
	// RequestExtensions asks the host to surface declared canvases as agent-visible extensions.
	RequestExtensions *bool
	// ExtensionSDKPath optionally overrides the bundled `@github/copilot-sdk` drop
	// injected into extension subprocesses. See SessionConfig.ExtensionSDKPath.
	ExtensionSDKPath *string
	// CanvasHandler receives inbound canvas.* requests for this session. See SessionConfig.CanvasHandler.
	CanvasHandler CanvasHandler `json:"-"`
	// ExtensionInfo identifies the stable extension providing this session's canvases.
	ExtensionInfo *ExtensionInfo
	// CanvasProvider is the stable identity for a host/SDK connection that
	// supplies built-in canvases. See SessionConfig.CanvasProvider.
	CanvasProvider *CanvasProviderIdentity
	// ExpAssignments injects ExP assignment ("flight") data on resume. See
	// SessionConfig.ExpAssignments. Re-supply on resume so the runtime
	// re-applies the assignments after a CLI process restart.
	//
	// Internal: ExpAssignments is part of the SDK's internal API surface,
	// intended for trusted out-of-process integrators, and is not intended for
	// general external use.
	ExpAssignments *CopilotExpAssignmentResponse
	// EnableManagedSettings injects the same opt-in flag on resume. See
	// SessionConfig.EnableManagedSettings. Re-supply on resume so the runtime
	// re-applies the managed-settings self-fetch after a CLI process restart.
	EnableManagedSettings *bool
	// ManagedSettings re-injects host-provided managed settings on resume. See
	// SessionConfig.ManagedSettings. It must be re-supplied on resume: it
	// replaces the prior injected layer, and omitting it clears that layer so
	// warm and cold resume behave identically.
	ManagedSettings *ManagedSettings
}

// ProviderTokenArgs carries the context passed to a [BearerTokenProvider] callback
// when the runtime needs a fresh bearer token for a BYOK provider.
//
// Experimental: ProviderTokenArgs is part of the experimental managed-identity /
// bearer-token-provider surface and may change or be removed in future SDK or CLI
// releases.
type ProviderTokenArgs struct {
	// ProviderName is the name of the BYOK provider needing a token. For the
	// singular, whole-session [ProviderConfig] this is the implicit provider name
	// ("default"); for [NamedProviderConfig] entries it is
	// [NamedProviderConfig.Name].
	//
	// The callback closes over its own token scope/audience; the runtime is
	// provider-agnostic and forwards only the provider name.
	ProviderName string

	// SessionID is the id of the session that triggered this token request. A
	// client-level shared callback registered for many sessions can use this to
	// resolve the owning session and scope token acquisition or caching per
	// session.
	SessionID string
}

// BearerTokenProvider is a per-provider callback that resolves a bearer token on
// demand, returning the raw token string (without the "Bearer " prefix). The
// Copilot SDK itself takes no Azure dependency: the consumer supplies this
// callback backed by their own identity library (for example azidentity's
// DefaultAzureCredential.GetToken), and the runtime calls it once before each
// outbound model request. The runtime does no caching of its own, so the callback
// (or the identity library it wraps) owns token caching and refresh.
//
// Experimental: BearerTokenProvider is part of the experimental managed-identity /
// bearer-token-provider surface and may change or be removed in future SDK or CLI
// releases.
type BearerTokenProvider func(args ProviderTokenArgs) (string, error)

type ProviderConfig struct {
	// Type is the provider type: "openai", "azure", or "anthropic". Defaults to "openai".
	Type string `json:"type,omitempty"`
	// WireAPI is the API format (openai/azure only): "completions" or "responses". Defaults to "completions".
	WireAPI string `json:"wireApi,omitempty"`
	// Transport for OpenAI Responses requests: "http" or "websockets". Defaults to "http".
	// Set "websockets" to deliver Responses API requests over a persistent WebSocket
	// connection instead of HTTP. Applies to OpenAI-compatible providers using WireAPI "responses".
	Transport string `json:"transport,omitempty"`
	// BaseURL is the API endpoint URL
	BaseURL string `json:"baseUrl"`
	// APIKey is the API key. Optional for local providers like Ollama.
	APIKey string `json:"apiKey,omitempty"`
	// BearerToken for authentication. Sets the Authorization header directly.
	// Use this for services requiring bearer token auth instead of API key.
	// Takes precedence over APIKey when both are set.
	BearerToken string `json:"bearerToken,omitempty"`
	// Azure contains Azure-specific options
	Azure *AzureProviderOptions `json:"azure,omitempty"`
	// Headers are custom HTTP headers included in outbound provider requests.
	Headers map[string]string `json:"headers,omitempty"`
	// ModelID is the well-known model name used by the runtime to look up
	// agent configuration (tools, prompts, reasoning behavior) and default
	// token limits. Also used as the wire model when WireModel is not set.
	// Falls back to SessionConfig.Model.
	ModelID string `json:"modelId,omitempty"`
	// WireModel is the model name sent to the provider API for inference. Use
	// this when the provider's model name (e.g. an Azure deployment name or a
	// custom fine-tune name) differs from ModelID.
	// Falls back to ModelID, then SessionConfig.Model.
	WireModel string `json:"wireModel,omitempty"`
	// MaxPromptTokens overrides the resolved model's default max prompt tokens.
	// The runtime triggers conversation compaction before sending a request
	// when the prompt (system message, history, tool definitions, user
	// message) would exceed this limit.
	MaxPromptTokens int `json:"maxPromptTokens,omitempty"`
	// MaxOutputTokens overrides the resolved model's default max output
	// tokens. When hit, the model stops generating and returns a truncated
	// response.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// BearerTokenProvider resolves a bearer token on demand for this provider
	// (managed-identity / on-demand auth). When set, the SDK strips the callback
	// from the wire config and instead sends `hasBearerTokenProvider: true`; the
	// runtime calls back over the session-scoped `providerToken.getToken` RPC
	// before each outbound model request and applies the returned token as the
	// Authorization header. Never serialized.
	//
	// When set alongside APIKey/BearerToken, this callback takes precedence: the
	// runtime applies the token it returns as the Authorization: Bearer header for
	// each request and does not send the static credential.
	//
	// Experimental: part of the experimental managed-identity / bearer-token-provider
	// surface and may change or be removed in future SDK or CLI releases.
	BearerTokenProvider BearerTokenProvider `json:"-"`
}

// MarshalJSON serializes the provider config, deriving the wire-only
// `hasBearerTokenProvider` flag from the presence of [ProviderConfig.BearerTokenProvider].
// The non-serializable callback never crosses the RPC boundary; the runtime only
// learns that a token provider exists and forwards the provider name back when it
// needs a token.
func (p ProviderConfig) MarshalJSON() ([]byte, error) {
	type wire ProviderConfig
	aux := struct {
		wire
		HasBearerTokenProvider *bool `json:"hasBearerTokenProvider,omitempty"`
	}{wire: wire(p)}
	if p.BearerTokenProvider != nil {
		aux.HasBearerTokenProvider = Bool(true)
	}
	return json.Marshal(aux)
}

// CapiSessionOptions configures provider-scoped Copilot API (CAPI) session behavior.
//
// WebSocket transport is the default for the CAPI Responses API whenever the
// model advertises the ws:/responses endpoint. Set EnableWebSocketResponses to
// Bool(false) to force the HTTP Responses transport, which is useful behind
// proxies where WebSockets fail. This is equivalent to setting the
// COPILOT_CLI_DISABLE_WEBSOCKET_RESPONSES environment variable. These options
// are provider-scoped under the capi namespace because a single session can host
// multiple providers, such as CAPI and BYOK, so transport choice is provider-level.
type CapiSessionOptions struct {
	// EnableWebSocketResponses controls whether the CAPI Responses API uses
	// WebSocket transport. Enabled by default when the model advertises
	// ws:/responses support; set to Bool(false) to force HTTP Responses transport.
	EnableWebSocketResponses *bool `json:"enableWebSocketResponses,omitempty"`
}

// AzureProviderOptions contains Azure-specific provider configuration
type AzureProviderOptions struct {
	// APIVersion is the Azure API version. When empty, the runtime uses the GA
	// versionless v1 route.
	APIVersion string `json:"apiVersion,omitempty"`
}

// NamedProviderConfig is a named BYOK provider connection (transport +
// credentials), referenced by ProviderModelConfig entries via Name.
//
// Unlike the singular Provider (which makes the whole session BYOK and bypasses
// Copilot API authentication), named providers are additive: they coexist with
// Copilot API auth so models from CAPI and one or more BYOK providers can be
// mixed within a single session and across sub-agents. Combining Providers and
// Models with Provider is rejected.
//
// Experimental: NamedProviderConfig is part of an experimental multi-provider
// BYOK surface and may change or be removed in future SDK or CLI releases.
type NamedProviderConfig struct {
	// Name is the stable identifier referenced by ProviderModelConfig.Provider.
	// Must not contain "/".
	Name string `json:"name"`
	// Type is the provider type: "openai", "azure", or "anthropic". Defaults to "openai".
	Type string `json:"type,omitempty"`
	// WireAPI is the API format (openai/azure only): "completions" or "responses". Defaults to "completions".
	WireAPI string `json:"wireApi,omitempty"`
	// BaseURL is the API endpoint URL.
	BaseURL string `json:"baseUrl"`
	// APIKey is the API key. Optional for local providers like Ollama.
	APIKey string `json:"apiKey,omitempty"`
	// BearerToken for authentication. Sets the Authorization header directly.
	// Takes precedence over APIKey when both are set.
	BearerToken string `json:"bearerToken,omitempty"`
	// Azure contains Azure-specific options.
	Azure *AzureProviderOptions `json:"azure,omitempty"`
	// Headers are custom HTTP headers included in all outbound provider requests.
	Headers map[string]string `json:"headers,omitempty"`
	// BearerTokenProvider resolves a bearer token on demand for this provider
	// (managed-identity / on-demand auth). When set, the SDK strips the callback
	// from the wire config and instead sends `hasBearerTokenProvider: true`; the
	// runtime calls back over the session-scoped `providerToken.getToken` RPC
	// before each outbound model request and applies the returned token as the
	// Authorization header. Never serialized.
	//
	// When set alongside APIKey/BearerToken, this callback takes precedence: the
	// runtime applies the token it returns as the Authorization: Bearer header for
	// each request and does not send the static credential.
	//
	// Experimental: part of the experimental managed-identity / bearer-token-provider
	// surface and may change or be removed in future SDK or CLI releases.
	BearerTokenProvider BearerTokenProvider `json:"-"`
}

// MarshalJSON serializes the named provider config, deriving the wire-only
// `hasBearerTokenProvider` flag from the presence of
// [NamedProviderConfig.BearerTokenProvider]. The non-serializable callback never
// crosses the RPC boundary; the runtime only learns that a token provider exists
// and forwards the provider name back when it needs a token.
func (p NamedProviderConfig) MarshalJSON() ([]byte, error) {
	type wire NamedProviderConfig
	aux := struct {
		wire
		HasBearerTokenProvider *bool `json:"hasBearerTokenProvider,omitempty"`
	}{wire: wire(p)}
	if p.BearerTokenProvider != nil {
		aux.HasBearerTokenProvider = Bool(true)
	}
	return json.Marshal(aux)
}

// ProviderModelConfig is a BYOK model definition that references a
// NamedProviderConfig by name and is added to the session's selectable model
// list. The session-wide selection id is the provider-qualified "provider/id".
//
// Experimental: ProviderModelConfig is part of an experimental multi-provider
// BYOK surface and may change or be removed in future SDK or CLI releases.
type ProviderModelConfig struct {
	// ID is the provider-local model id, unique within its provider.
	ID string `json:"id"`
	// Provider is the name of the NamedProviderConfig that serves this model.
	Provider string `json:"provider"`
	// WireModel is the model name sent to the provider API for inference. Defaults to ID.
	WireModel string `json:"wireModel,omitempty"`
	// ModelID is the well-known base model id used for behavior/capability/config lookup. Defaults to ID.
	ModelID string `json:"modelId,omitempty"`
	// Name is the display name for model pickers. Defaults to the provider-qualified selection id.
	Name string `json:"name,omitempty"`
	// MaxPromptTokens is the maximum prompt/input tokens for the model.
	MaxPromptTokens int `json:"maxPromptTokens,omitempty"`
	// MaxContextWindowTokens is the maximum context window tokens for the model.
	MaxContextWindowTokens int `json:"maxContextWindowTokens,omitempty"`
	// MaxOutputTokens is the maximum output tokens for the model.
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	// Capabilities holds optional capability overrides for the synthesized model.
	Capabilities *rpc.ModelCapabilitiesOverride `json:"capabilities,omitempty"`
}

// ToolBinaryResult represents binary payloads returned by tools.
type ToolBinaryResult struct {
	Data        string `json:"data"`
	MIMEType    string `json:"mimeType"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// MessageOptions configures a message to send
type MessageOptions struct {
	// Prompt is the message to send
	Prompt string
	// Attachments are file or directory attachments
	Attachments []Attachment
	// Mode is the message delivery mode (default: "enqueue")
	Mode string
	// AgentMode is the UI mode the agent was in when this message was sent
	// (for example "plan" or "autopilot"). Defaults to the session's current
	// mode when empty.
	AgentMode AgentMode
	// RequestHeaders are custom per-turn HTTP headers for outbound model requests.
	RequestHeaders map[string]string
	// DisplayPrompt, if provided, is shown in the timeline instead of Prompt.
	DisplayPrompt string
}

// AgentMode is the UI mode the agent is in for a given turn. See
// [MessageOptions.AgentMode].
type AgentMode = rpc.SendAgentMode

// AgentMode values supported by the runtime.
const (
	AgentModeInteractive = rpc.SendAgentModeInteractive
	AgentModePlan        = rpc.SendAgentModePlan
	AgentModeAutopilot   = rpc.SendAgentModeAutopilot
	AgentModeShell       = rpc.SendAgentModeShell
)

// SessionEventHandler is a callback for session events
type SessionEventHandler func(event SessionEvent)

// ModelVisionLimits contains vision-specific limits
type ModelVisionLimits struct {
	SupportedMediaTypes []string `json:"supported_media_types"`
	MaxPromptImages     int      `json:"max_prompt_images"`
	MaxPromptImageSize  int      `json:"max_prompt_image_size"`
}

// ModelLimits contains model limits
type ModelLimits struct {
	MaxPromptTokens        *int               `json:"max_prompt_tokens,omitempty"`
	MaxContextWindowTokens *int               `json:"max_context_window_tokens,omitempty"`
	Vision                 *ModelVisionLimits `json:"vision,omitempty"`
}

// ModelSupports contains model support flags
type ModelSupports struct {
	Vision          bool `json:"vision"`
	ReasoningEffort bool `json:"reasoningEffort"`
}

// ModelCapabilities contains model capabilities and limits
type ModelCapabilities struct {
	Supports ModelSupports `json:"supports"`
	Limits   ModelLimits   `json:"limits"`
}

// Type aliases for model capabilities overrides, re-exported from the rpc
// package for ergonomic use without requiring a separate rpc import.
type (
	ModelCapabilitiesOverride             = rpc.ModelCapabilitiesOverride
	ModelCapabilitiesOverrideSupports     = rpc.ModelCapabilitiesOverrideSupports
	ModelCapabilitiesOverrideLimits       = rpc.ModelCapabilitiesOverrideLimits
	ModelCapabilitiesOverrideLimitsVision = rpc.ModelCapabilitiesOverrideLimitsVision
)

// ModelPolicy contains model policy state
type ModelPolicy struct {
	State string `json:"state"`
	Terms string `json:"terms"`
}

// ModelBilling contains model billing information
type ModelBilling struct {
	Multiplier  *float64                     `json:"multiplier,omitempty"`
	TokenPrices *rpc.ModelBillingTokenPrices `json:"tokenPrices,omitempty"`
}

// ModelInfo contains information about an available model
type ModelInfo struct {
	ID                        string            `json:"id"`
	Name                      string            `json:"name"`
	Capabilities              ModelCapabilities `json:"capabilities"`
	Policy                    *ModelPolicy      `json:"policy,omitempty"`
	Billing                   *ModelBilling     `json:"billing,omitempty"`
	SupportedReasoningEfforts []string          `json:"supportedReasoningEfforts,omitempty"`
	DefaultReasoningEffort    string            `json:"defaultReasoningEffort,omitempty"`
}

// SessionContext contains working directory context for a session
type SessionContext struct {
	// WorkingDirectory is the working directory where the session was created
	WorkingDirectory string `json:"cwd"`
	// GitRoot is the git repository root (if in a git repo)
	GitRoot string `json:"gitRoot,omitempty"`
	// Repository is the GitHub repository in "owner/repo" format
	Repository string `json:"repository,omitempty"`
	// Branch is the current git branch
	Branch string `json:"branch,omitempty"`
}

// SessionListFilter contains filter options for listing sessions
type SessionListFilter struct {
	// WorkingDirectory filters by exact working directory match
	WorkingDirectory string `json:"cwd,omitempty"`
	// GitRoot filters by git root
	GitRoot string `json:"gitRoot,omitempty"`
	// Repository filters by repository (owner/repo format)
	Repository string `json:"repository,omitempty"`
	// Branch filters by branch
	Branch string `json:"branch,omitempty"`
}

// SessionMetadata contains metadata about a session
type SessionMetadata struct {
	SessionID    string          `json:"sessionId"`
	StartTime    time.Time       `json:"startTime"`
	ModifiedTime time.Time       `json:"modifiedTime"`
	Summary      *string         `json:"summary,omitempty"`
	IsRemote     bool            `json:"isRemote"`
	Context      *SessionContext `json:"context,omitempty"`
}

// SessionLifecycleEventType represents the type of session lifecycle event
type SessionLifecycleEventType string

const (
	SessionLifecycleCreated    SessionLifecycleEventType = "session.created"
	SessionLifecycleDeleted    SessionLifecycleEventType = "session.deleted"
	SessionLifecycleUpdated    SessionLifecycleEventType = "session.updated"
	SessionLifecycleForeground SessionLifecycleEventType = "session.foreground"
	SessionLifecycleBackground SessionLifecycleEventType = "session.background"
)

// SessionLifecycleEvent represents a session lifecycle notification
type SessionLifecycleEvent struct {
	Type      SessionLifecycleEventType      `json:"type"`
	SessionID string                         `json:"sessionId"`
	Metadata  *SessionLifecycleEventMetadata `json:"metadata,omitempty"`
}

// SessionLifecycleEventMetadata contains optional metadata for lifecycle events
type SessionLifecycleEventMetadata struct {
	StartTime    time.Time `json:"startTime"`
	ModifiedTime time.Time `json:"modifiedTime"`
	Summary      *string   `json:"summary,omitempty"`
}

// SessionLifecycleHandler is a callback for session lifecycle events
type SessionLifecycleHandler func(event SessionLifecycleEvent)

// createSessionRequest is the request for session.create
type createSessionRequest struct {
	Model                              string                                 `json:"model,omitempty"`
	SessionID                          string                                 `json:"sessionId,omitempty"`
	ClientName                         string                                 `json:"clientName,omitempty"`
	ReasoningEffort                    string                                 `json:"reasoningEffort,omitempty"`
	ReasoningSummary                   ReasoningSummary                       `json:"reasoningSummary,omitempty"`
	ContextTier                        ContextTier                            `json:"contextTier,omitempty"`
	Tools                              []Tool                                 `json:"tools,omitempty"`
	SystemMessage                      *SystemMessageConfig                   `json:"systemMessage,omitempty"`
	AvailableTools                     []string                               `json:"availableTools"`
	ExcludedTools                      []string                               `json:"excludedTools,omitempty"`
	ToolFilterPrecedence               *rpc.OptionsUpdateToolFilterPrecedence `json:"toolFilterPrecedence,omitempty"`
	ExcludedBuiltInAgents              []string                               `json:"excludedBuiltinAgents,omitempty"`
	Provider                           *ProviderConfig                        `json:"provider,omitempty"`
	Capi                               *CapiSessionOptions                    `json:"capi,omitempty"`
	Providers                          []NamedProviderConfig                  `json:"providers,omitempty"`
	Models                             []ProviderModelConfig                  `json:"models,omitempty"`
	EnableSessionTelemetry             *bool                                  `json:"enableSessionTelemetry,omitempty"`
	EnableCitations                    *bool                                  `json:"enableCitations,omitempty"`
	SessionLimits                      *rpc.SessionLimitsConfig               `json:"sessionLimits,omitempty"`
	IsExperimentalMode                 *bool                                  `json:"isExperimentalMode,omitempty"`
	SkipCustomInstructions             *bool                                  `json:"skipCustomInstructions,omitempty"`
	CustomAgentsLocalOnly              *bool                                  `json:"customAgentsLocalOnly,omitempty"`
	CoauthorEnabled                    *bool                                  `json:"coauthorEnabled,omitempty"`
	ManageScheduleEnabled              *bool                                  `json:"manageScheduleEnabled,omitempty"`
	ModelCapabilities                  *rpc.ModelCapabilitiesOverride         `json:"modelCapabilities,omitempty"`
	RequestPermission                  *bool                                  `json:"requestPermission,omitempty"`
	RequestUserInput                   *bool                                  `json:"requestUserInput,omitempty"`
	RequestExitPlanMode                *bool                                  `json:"requestExitPlanMode,omitempty"`
	RequestAutoModeSwitch              *bool                                  `json:"requestAutoModeSwitch,omitempty"`
	Hooks                              *bool                                  `json:"hooks,omitempty"`
	WorkingDirectory                   string                                 `json:"workingDirectory,omitempty"`
	AdditionalDirectories              []string                               `json:"additionalDirectories,omitempty"`
	Streaming                          *bool                                  `json:"streaming,omitempty"`
	IncludeSubAgentStreamingEvents     *bool                                  `json:"includeSubAgentStreamingEvents,omitempty"`
	EnableGitHubTelemetryForwarding    *bool                                  `json:"enableGitHubTelemetryForwarding,omitempty"`
	MCPServers                         map[string]MCPServerConfig             `json:"mcpServers,omitempty"`
	MCPOAuthTokenStorage               string                                 `json:"mcpOAuthTokenStorage,omitempty"`
	EnvValueMode                       string                                 `json:"envValueMode,omitempty"`
	CustomAgents                       []CustomAgentConfig                    `json:"customAgents,omitempty"`
	DefaultAgent                       *DefaultAgentConfig                    `json:"defaultAgent,omitempty"`
	Agent                              string                                 `json:"agent,omitempty"`
	ConfigDir                          string                                 `json:"configDir,omitempty"`
	EnableConfigDiscovery              *bool                                  `json:"enableConfigDiscovery,omitempty"`
	SkipEmbeddingRetrieval             *bool                                  `json:"skipEmbeddingRetrieval,omitempty"`
	EmbeddingCacheStorage              *string                                `json:"embeddingCacheStorage,omitempty"`
	OrganizationCustomInstructions     *string                                `json:"organizationCustomInstructions,omitempty"`
	EnableOnDemandInstructionDiscovery *bool                                  `json:"enableOnDemandInstructionDiscovery,omitempty"`
	EnableFileHooks                    *bool                                  `json:"enableFileHooks,omitempty"`
	EnableHostGitOperations            *bool                                  `json:"enableHostGitOperations,omitempty"`
	EnableSessionStore                 *bool                                  `json:"enableSessionStore,omitempty"`
	EnableSkills                       *bool                                  `json:"enableSkills,omitempty"`
	SkillDirectories                   []string                               `json:"skillDirectories,omitempty"`
	PluginDirectories                  []string                               `json:"pluginDirectories,omitempty"`
	InstructionDirectories             []string                               `json:"instructionDirectories,omitempty"`
	DisabledSkills                     []string                               `json:"disabledSkills,omitempty"`
	DisabledMCPServers                 *[]string                              `json:"disabledMcpServers,omitempty"`
	InfiniteSessions                   *InfiniteSessionConfig                 `json:"infiniteSessions,omitempty"`
	LargeOutput                        *LargeToolOutputConfig                 `json:"largeOutput,omitempty"`
	ToolSearch                         *ToolSearchConfig                      `json:"toolSearch,omitempty"`
	Memory                             *MemoryConfiguration                   `json:"memory,omitempty"`
	Commands                           []wireCommand                          `json:"commands,omitempty"`
	RequestElicitation                 *bool                                  `json:"requestElicitation,omitempty"`
	RequestMCPApps                     *bool                                  `json:"requestMcpApps,omitempty"`
	GitHubMCPToolConfig                *GitHubMCPToolConfig                   `json:"githubMcpToolConfig,omitempty"`
	GitHubToken                        string                                 `json:"gitHubToken,omitempty"`
	RemoteSession                      rpc.RemoteSessionMode                  `json:"remoteSession,omitempty"`
	Cloud                              *CloudSessionOptions                   `json:"cloud,omitempty"`
	Canvases                           []CanvasDeclaration                    `json:"canvases,omitempty"`
	RequestCanvasRenderer              *bool                                  `json:"requestCanvasRenderer,omitempty"`
	RequestExtensions                  *bool                                  `json:"requestExtensions,omitempty"`
	ExtensionSDKPath                   *string                                `json:"extensionSdkPath,omitempty"`
	ExtensionInfo                      *ExtensionInfo                         `json:"extensionInfo,omitempty"`
	CanvasProvider                     *CanvasProviderIdentity                `json:"canvasProvider,omitempty"`
	ExpAssignments                     *CopilotExpAssignmentResponse          `json:"expAssignments,omitempty"`
	EnableManagedSettings              *bool                                  `json:"enableManagedSettings,omitempty"`
	ManagedSettings                    *ManagedSettings                       `json:"managedSettings,omitempty"`
	Traceparent                        string                                 `json:"traceparent,omitempty"`
	Tracestate                         string                                 `json:"tracestate,omitempty"`
}

// wireCommand is the wire representation of a command (name + description only, no handler).
type wireCommand struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// createSessionResponse is the response from session.create
type createSessionResponse struct {
	SessionID     string               `json:"sessionId"`
	WorkspacePath string               `json:"workspacePath"`
	Capabilities  *SessionCapabilities `json:"capabilities,omitempty"`
}

// resumeSessionRequest is the request for session.resume
type resumeSessionRequest struct {
	SessionID                          string                                 `json:"sessionId"`
	ClientName                         string                                 `json:"clientName,omitempty"`
	Model                              string                                 `json:"model,omitempty"`
	ReasoningEffort                    string                                 `json:"reasoningEffort,omitempty"`
	ReasoningSummary                   ReasoningSummary                       `json:"reasoningSummary,omitempty"`
	ContextTier                        ContextTier                            `json:"contextTier,omitempty"`
	Tools                              []Tool                                 `json:"tools,omitempty"`
	SystemMessage                      *SystemMessageConfig                   `json:"systemMessage,omitempty"`
	AvailableTools                     []string                               `json:"availableTools"`
	ExcludedTools                      []string                               `json:"excludedTools,omitempty"`
	ToolFilterPrecedence               *rpc.OptionsUpdateToolFilterPrecedence `json:"toolFilterPrecedence,omitempty"`
	ExcludedBuiltInAgents              []string                               `json:"excludedBuiltinAgents,omitempty"`
	Provider                           *ProviderConfig                        `json:"provider,omitempty"`
	Capi                               *CapiSessionOptions                    `json:"capi,omitempty"`
	Providers                          []NamedProviderConfig                  `json:"providers,omitempty"`
	Models                             []ProviderModelConfig                  `json:"models,omitempty"`
	EnableSessionTelemetry             *bool                                  `json:"enableSessionTelemetry,omitempty"`
	EnableCitations                    *bool                                  `json:"enableCitations,omitempty"`
	SessionLimits                      *rpc.SessionLimitsConfig               `json:"sessionLimits,omitempty"`
	IsExperimentalMode                 *bool                                  `json:"isExperimentalMode,omitempty"`
	SkipCustomInstructions             *bool                                  `json:"skipCustomInstructions,omitempty"`
	CustomAgentsLocalOnly              *bool                                  `json:"customAgentsLocalOnly,omitempty"`
	CoauthorEnabled                    *bool                                  `json:"coauthorEnabled,omitempty"`
	ManageScheduleEnabled              *bool                                  `json:"manageScheduleEnabled,omitempty"`
	ModelCapabilities                  *rpc.ModelCapabilitiesOverride         `json:"modelCapabilities,omitempty"`
	RequestPermission                  *bool                                  `json:"requestPermission,omitempty"`
	RequestUserInput                   *bool                                  `json:"requestUserInput,omitempty"`
	RequestExitPlanMode                *bool                                  `json:"requestExitPlanMode,omitempty"`
	RequestAutoModeSwitch              *bool                                  `json:"requestAutoModeSwitch,omitempty"`
	Hooks                              *bool                                  `json:"hooks,omitempty"`
	WorkingDirectory                   string                                 `json:"workingDirectory,omitempty"`
	AdditionalDirectories              []string                               `json:"additionalDirectories,omitempty"`
	ConfigDir                          string                                 `json:"configDir,omitempty"`
	EnableConfigDiscovery              *bool                                  `json:"enableConfigDiscovery,omitempty"`
	SkipEmbeddingRetrieval             *bool                                  `json:"skipEmbeddingRetrieval,omitempty"`
	EmbeddingCacheStorage              *string                                `json:"embeddingCacheStorage,omitempty"`
	OrganizationCustomInstructions     *string                                `json:"organizationCustomInstructions,omitempty"`
	EnableOnDemandInstructionDiscovery *bool                                  `json:"enableOnDemandInstructionDiscovery,omitempty"`
	EnableFileHooks                    *bool                                  `json:"enableFileHooks,omitempty"`
	EnableHostGitOperations            *bool                                  `json:"enableHostGitOperations,omitempty"`
	EnableSessionStore                 *bool                                  `json:"enableSessionStore,omitempty"`
	EnableSkills                       *bool                                  `json:"enableSkills,omitempty"`
	DisableResume                      *bool                                  `json:"disableResume,omitempty"`
	ContinuePendingWork                *bool                                  `json:"continuePendingWork,omitempty"`
	Streaming                          *bool                                  `json:"streaming,omitempty"`
	IncludeSubAgentStreamingEvents     *bool                                  `json:"includeSubAgentStreamingEvents,omitempty"`
	EnableGitHubTelemetryForwarding    *bool                                  `json:"enableGitHubTelemetryForwarding,omitempty"`
	MCPServers                         map[string]MCPServerConfig             `json:"mcpServers,omitempty"`
	MCPOAuthTokenStorage               string                                 `json:"mcpOAuthTokenStorage,omitempty"`
	EnvValueMode                       string                                 `json:"envValueMode,omitempty"`
	CustomAgents                       []CustomAgentConfig                    `json:"customAgents,omitempty"`
	DefaultAgent                       *DefaultAgentConfig                    `json:"defaultAgent,omitempty"`
	Agent                              string                                 `json:"agent,omitempty"`
	SkillDirectories                   []string                               `json:"skillDirectories,omitempty"`
	PluginDirectories                  []string                               `json:"pluginDirectories,omitempty"`
	InstructionDirectories             []string                               `json:"instructionDirectories,omitempty"`
	DisabledSkills                     []string                               `json:"disabledSkills,omitempty"`
	DisabledMCPServers                 *[]string                              `json:"disabledMcpServers,omitempty"`
	InfiniteSessions                   *InfiniteSessionConfig                 `json:"infiniteSessions,omitempty"`
	LargeOutput                        *LargeToolOutputConfig                 `json:"largeOutput,omitempty"`
	ToolSearch                         *ToolSearchConfig                      `json:"toolSearch,omitempty"`
	Memory                             *MemoryConfiguration                   `json:"memory,omitempty"`
	Commands                           []wireCommand                          `json:"commands,omitempty"`
	RequestElicitation                 *bool                                  `json:"requestElicitation,omitempty"`
	RequestMCPApps                     *bool                                  `json:"requestMcpApps,omitempty"`
	GitHubMCPToolConfig                *GitHubMCPToolConfig                   `json:"githubMcpToolConfig,omitempty"`
	GitHubToken                        string                                 `json:"gitHubToken,omitempty"`
	RemoteSession                      rpc.RemoteSessionMode                  `json:"remoteSession,omitempty"`
	Canvases                           []CanvasDeclaration                    `json:"canvases,omitempty"`
	OpenCanvases                       []rpc.OpenCanvasInstance               `json:"openCanvases,omitempty"`
	RequestCanvasRenderer              *bool                                  `json:"requestCanvasRenderer,omitempty"`
	RequestExtensions                  *bool                                  `json:"requestExtensions,omitempty"`
	ExtensionSDKPath                   *string                                `json:"extensionSdkPath,omitempty"`
	ExtensionInfo                      *ExtensionInfo                         `json:"extensionInfo,omitempty"`
	CanvasProvider                     *CanvasProviderIdentity                `json:"canvasProvider,omitempty"`
	ExpAssignments                     *CopilotExpAssignmentResponse          `json:"expAssignments,omitempty"`
	EnableManagedSettings              *bool                                  `json:"enableManagedSettings,omitempty"`
	ManagedSettings                    *ManagedSettings                       `json:"managedSettings,omitempty"`
	Traceparent                        string                                 `json:"traceparent,omitempty"`
	Tracestate                         string                                 `json:"tracestate,omitempty"`
}

// resumeSessionResponse is the response from session.resume
type resumeSessionResponse struct {
	SessionID     string                   `json:"sessionId"`
	WorkspacePath string                   `json:"workspacePath"`
	Capabilities  *SessionCapabilities     `json:"capabilities,omitempty"`
	OpenCanvases  []rpc.OpenCanvasInstance `json:"openCanvases,omitempty"`
}

type hooksInvokeRequest struct {
	SessionID string          `json:"sessionId"`
	Type      string          `json:"hookType"`
	Input     json.RawMessage `json:"input"`
}

// listSessionsRequest is the request for session.list
type listSessionsRequest struct {
	Filter *SessionListFilter `json:"filter,omitempty"`
}

// listSessionsResponse is the response from session.list
type listSessionsResponse struct {
	Sessions []SessionMetadata `json:"sessions"`
}

// getSessionMetadataRequest is the request for session.getMetadata
type getSessionMetadataRequest struct {
	SessionID string `json:"sessionId"`
}

// getSessionMetadataResponse is the response from session.getMetadata
type getSessionMetadataResponse struct {
	Session *SessionMetadata `json:"session,omitempty"`
}

// deleteSessionRequest is the request for session.delete
type deleteSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// deleteSessionResponse is the response from session.delete
type deleteSessionResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

// getLastSessionIDRequest is the request for session.getLastId
type getLastSessionIDRequest struct{}

// getLastSessionIDResponse is the response from session.getLastId
type getLastSessionIDResponse struct {
	SessionID *string `json:"sessionId,omitempty"`
}

// getForegroundSessionRequest is the request for session.getForeground
type getForegroundSessionRequest struct{}

// getForegroundSessionResponse is the response from session.getForeground
type getForegroundSessionResponse struct {
	SessionID     *string `json:"sessionId,omitempty"`
	WorkspacePath *string `json:"workspacePath,omitempty"`
}

// setForegroundSessionRequest is the request for session.setForeground
type setForegroundSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// setForegroundSessionResponse is the response from session.setForeground
type setForegroundSessionResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

type pingRequest struct {
	Message string `json:"message,omitempty"`
}

// PingResponse is the response from a ping request
type PingResponse struct {
	Message         string    `json:"message"`
	Timestamp       time.Time `json:"timestamp"`
	ProtocolVersion *int      `json:"protocolVersion,omitempty"`
}

// getStatusRequest is the request for status.get
type getStatusRequest struct{}

// GetStatusResponse is the response from status.get
type GetStatusResponse struct {
	Version         string `json:"version"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// getAuthStatusRequest is the request for auth.getStatus
type getAuthStatusRequest struct{}

// GetAuthStatusResponse is the response from auth.getStatus
type GetAuthStatusResponse struct {
	IsAuthenticated bool    `json:"isAuthenticated"`
	AuthType        *string `json:"authType,omitempty"`
	Host            *string `json:"host,omitempty"`
	Login           *string `json:"login,omitempty"`
	StatusMessage   *string `json:"statusMessage,omitempty"`
}

// listModelsRequest is the request for models.list
type listModelsRequest struct{}

// listModelsResponse is the response from models.list
type listModelsResponse struct {
	Models []ModelInfo `json:"models"`
}

// sessionGetMessagesRequest is the request for session.getMessages
type sessionGetMessagesRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionGetMessagesResponse is the response from session.getMessages
type sessionGetMessagesResponse struct {
	Events []SessionEvent `json:"events"`
}

// sessionDestroyRequest is the request for session.destroy
type sessionDestroyRequest struct {
	SessionID string `json:"sessionId"`
}

// sessionAbortRequest is the request for session.abort
type sessionAbortRequest struct {
	SessionID string `json:"sessionId"`
}

type sessionSendRequest struct {
	SessionID      string            `json:"sessionId"`
	Prompt         string            `json:"prompt"`
	DisplayPrompt  string            `json:"displayPrompt,omitempty"`
	Attachments    []Attachment      `json:"attachments,omitempty"`
	Mode           string            `json:"mode,omitempty"`
	AgentMode      AgentMode         `json:"agentMode,omitempty"`
	Traceparent    string            `json:"traceparent,omitempty"`
	Tracestate     string            `json:"tracestate,omitempty"`
	RequestHeaders map[string]string `json:"requestHeaders,omitempty"`
}

// sessionSendResponse is the response from session.send
type sessionSendResponse struct {
	MessageID string `json:"messageId"`
}

// sessionEventRequest is the request for session event notifications
type sessionEventRequest struct {
	SessionID string       `json:"sessionId"`
	Event     SessionEvent `json:"event"`
}

// userInputRequest represents a request for user input from the agent
type userInputRequest struct {
	SessionID     string   `json:"sessionId"`
	Question      string   `json:"question"`
	Choices       []string `json:"choices,omitempty"`
	AllowFreeform *bool    `json:"allowFreeform,omitempty"`
}

// userInputResponse represents the user's response to an input request
type userInputResponse struct {
	Answer      string `json:"answer"`
	WasFreeform bool   `json:"wasFreeform"`
}

type exitPlanModeRequest struct {
	SessionID         string   `json:"sessionId"`
	Summary           string   `json:"summary"`
	PlanContent       string   `json:"planContent,omitempty"`
	Actions           []string `json:"actions"`
	RecommendedAction string   `json:"recommendedAction"`
}

type autoModeSwitchRequest struct {
	SessionID         string   `json:"sessionId"`
	ErrorCode         *string  `json:"errorCode,omitempty"`
	RetryAfterSeconds *float64 `json:"retryAfterSeconds,omitempty"`
}

type autoModeSwitchResponse struct {
	Response AutoModeSwitchResponse `json:"response"`
}
