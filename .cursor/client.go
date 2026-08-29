// Package copilot provides a Go SDK for interacting with the GitHub Copilot CLI.
//
// The copilot package enables Go applications to communicate with the Copilot CLI
// server, create and manage conversation sessions, and integrate custom tools.
//
// Basic usage:
//
//	client := copilot.NewClient(nil)
//	if err := client.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Stop()
//
//	session, err := client.CreateSession(&copilot.SessionConfig{
//	    OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
//	    Model: "gpt-4",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	session.On(func(event copilot.SessionEvent) {
//	    if d, ok := event.Data.(*copilot.AssistantMessageData); ok {
//	        fmt.Println(d.Content)
//	    }
//	})
//
//	session.Send(copilot.MessageOptions{Prompt: "Hello!"})
package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/github/copilot-sdk/go/internal/embeddedcli"
	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
	"github.com/github/copilot-sdk/go/internal/truncbuffer"
	"github.com/github/copilot-sdk/go/rpc"
)

// defaultBearerTokenProviderName is the implicit provider name for the singular,
// whole-session [ProviderConfig]. Named providers are keyed by their own Name.
const defaultBearerTokenProviderName = "default"

// collectBearerTokenProviders gathers the per-provider [BearerTokenProvider] callbacks
// from the singular provider and any named providers, keyed by provider name. The
// singular provider uses the implicit name "default"; named providers use their
// own Name. Returns nil when no callbacks are configured.
func collectBearerTokenProviders(provider *ProviderConfig, providers []NamedProviderConfig) map[string]BearerTokenProvider {
	callbacks := make(map[string]BearerTokenProvider)
	if provider != nil && provider.BearerTokenProvider != nil {
		callbacks[defaultBearerTokenProviderName] = provider.BearerTokenProvider
	}
	for i := range providers {
		if providers[i].BearerTokenProvider != nil {
			callbacks[providers[i].Name] = providers[i].BearerTokenProvider
		}
	}
	if len(callbacks) == 0 {
		return nil
	}
	return callbacks
}

func validateSessionFSConfig(config *SessionFSConfig) error {
	if config == nil {
		return nil
	}
	if config.InitialWorkingDirectory == "" {
		return errors.New("SessionFS.InitialWorkingDirectory is required")
	}
	if config.SessionStatePath == "" {
		return errors.New("SessionFS.SessionStatePath is required")
	}
	if config.Conventions != rpc.SessionFSSetProviderConventionsPosix && config.Conventions != rpc.SessionFSSetProviderConventionsWindows {
		return errors.New("SessionFS.Conventions must be either 'posix' or 'windows'")
	}
	return nil
}

// validateEnvironmentOptions enforces the transport-specific rules for
// per-client environment, working directory, and telemetry. It panics (fails
// loud) on a misconfiguration, matching the other SDKs.
//
// The in-process transport loads the native runtime into this process, whose
// single environment block and process-global working directory cannot carry
// per-client values, and whose telemetry lowers to shared process-global env
// vars — so options that depend on them are rejected there. Child-process
// transports each own their OS process, so per-connection env is allowed, but
// setting it in both the client-level option and the connection is rejected.
func validateEnvironmentOptions(connection RuntimeConnection, opts *ClientOptions) {
	if _, ok := connection.(InProcessConnection); ok {
		if opts.Env != nil {
			panic("Env is not supported with InProcessConnection: the in-process transport loads the native runtime into the shared host process, whose single environment block cannot carry per-client values. Set the variables on the host process environment instead.")
		}
		if opts.WorkingDirectory != "" {
			panic("WorkingDirectory is not supported with InProcessConnection: the native runtime shares the host process working directory. Use a child-process transport, or set the process working directory before creating the client.")
		}
		if opts.Telemetry != nil {
			panic("Telemetry is not supported with InProcessConnection: telemetry configuration is lowered to environment variables read by native runtime code running in the shared host process, so per-client telemetry cannot be honored in-process. Configure telemetry via the host process environment, or use a child-process transport.")
		}
		return
	}

	if cp, ok := connection.(childProcessConnection); ok {
		if cp.connEnv() != nil && opts.Env != nil {
			panic("Set environment variables via either the client-level Env option or the connection's Env, not both. Prefer the connection-level Env for child-process transports.")
		}
	}
}

// Client manages the connection to the Copilot CLI server and provides session management.
//
// The Client can either spawn a CLI server process or connect to an existing server.
// It handles JSON-RPC communication, session lifecycle, tool execution, and permission requests.
//
// Example:
//
//	// Create a client with default options (spawns CLI server using stdio)
//	client := copilot.NewClient(nil)
//
//	// Or connect to an existing server
//	client := copilot.NewClient(&copilot.ClientOptions{
//	    Connection: copilot.URIConnection{URL: "localhost:3000"},
//	})
//
//	if err := client.Start(); err != nil {
//	    log.Fatal(err)
//	}
//	defer client.Stop()
type Client struct {
	options          ClientOptions
	process          *exec.Cmd
	client           *jsonrpc2.Client
	actualPort       int
	actualHost       string
	state            connectionState
	sessions         map[string]*Session
	sessionsMux      sync.Mutex
	isExternalServer bool
	conn             net.Conn // stores net.Conn for external TCP connections
	useStdio         bool     // resolved value from options
	useInProcess     bool     // true for InProcessConnection (FFI transport)
	ffiHost          inProcessHost
	// resolved process options for the spawned runtime (zero values for URIConnection)
	cliPath            string
	cliArgs            []string
	port               int
	tcpConnectionToken string

	modelsCache               []ModelInfo
	modelsCacheMux            sync.Mutex
	lifecycleHandlers         map[uint64]SessionLifecycleHandler
	typedLifecycleHandlers    map[SessionLifecycleEventType]map[uint64]SessionLifecycleHandler
	nextLifecycleHandlerID    uint64
	lifecycleHandlersMux      sync.Mutex
	startStopMux              sync.RWMutex // protects process and state during start/[force]stop
	processDone               chan struct{}
	processErrorPtr           *error
	osProcess                 atomic.Pointer[os.Process]
	negotiatedProtocolVersion int
	// effectiveConnectionToken is the token sent in `connect`; auto-generated when
	// the SDK spawns its own CLI in TCP mode.
	effectiveConnectionToken string
	onListModels             func(ctx context.Context) ([]ModelInfo, error)

	// RPC provides typed server-scoped RPC methods.
	// This field is nil until the client is connected via Start().
	RPC *rpc.ServerRPC

	// internalRPC provides SDK-internal RPC methods (handshake helpers etc.).
	// Lowercase = not exported; external callers cannot reach it.
	internalRPC *rpc.InternalServerRPC
}

// NewClient creates a new Copilot runtime client with the given options.
//
// If options is nil, default options are used (spawns the bundled runtime over
// stdio). The client is not connected after creation; call [Client.Start] to
// connect, or simply call [Client.CreateSession]/[Client.ResumeSession], which
// auto-start the runtime on first use.
//
// Example:
//
//	// Default options: bundled runtime over stdio
//	client := copilot.NewClient(nil)
//
//	// Custom CLI path over stdio
//	client := copilot.NewClient(&copilot.ClientOptions{
//	    Connection: copilot.StdioConnection{Path: "/usr/local/bin/copilot"},
//	    LogLevel:   "debug",
//	})
//
//	// Connect to an already-running runtime
//	client := copilot.NewClient(&copilot.ClientOptions{
//	    Connection: copilot.URIConnection{URL: "localhost:8080"},
//	})
func NewClient(options *ClientOptions) *Client {
	opts := ClientOptions{}

	client := &Client{
		options:          opts,
		state:            stateDisconnected,
		sessions:         make(map[string]*Session),
		actualHost:       "localhost",
		isExternalServer: false,
		useStdio:         true,
	}

	if options != nil {
		opts = *options
	}

	// Resolve the connection. An explicit connection always wins; otherwise
	// honor the same process/environment override as the other SDKs.
	connection := opts.Connection
	if connection == nil {
		env := opts.Env
		if env == nil {
			env = os.Environ()
		}
		connection = resolveDefaultConnection(env)
	}
	switch conn := connection.(type) {
	case StdioConnection:
		client.useStdio = true
		client.cliPath = conn.Path
		if len(conn.Args) > 0 {
			client.cliArgs = append([]string{}, conn.Args...)
		}
	case TCPConnection:
		client.useStdio = false
		client.cliPath = conn.Path
		if len(conn.Args) > 0 {
			client.cliArgs = append([]string{}, conn.Args...)
		}
		client.port = conn.Port
		client.tcpConnectionToken = conn.ConnectionToken
	case URIConnection:
		if conn.URL == "" {
			panic("URIConnection requires a non-empty URL")
		}
		host, port := parseCLIURL(conn.URL)
		client.actualHost = host
		client.actualPort = port
		client.isExternalServer = true
		client.useStdio = false
		client.tcpConnectionToken = conn.ConnectionToken
	case InProcessConnection:
		client.useStdio = false
		client.useInProcess = true
	default:
		panic(fmt.Sprintf("unknown RuntimeConnection type: %T", connection))
	}

	// Validate transport-specific option constraints (fail loud). The in-process
	// transport loads the runtime into this process, whose single environment
	// block, process-global working directory, and shared telemetry state cannot
	// carry per-client values. Child-process transports may set env via either
	// the client-level option or the connection, but not both.
	validateEnvironmentOptions(connection, &opts)

	// Validate auth options when connecting to an external runtime.
	if client.isExternalServer && (opts.GitHubToken != "" || opts.UseLoggedInUser != nil) {
		panic("GitHubToken and UseLoggedInUser cannot be used with URIConnection (external runtime manages its own auth)")
	}

	// For child-process transports, a connection-level env takes precedence over
	// the client-level env (setting both was rejected above). Resolve it before
	// defaulting so an explicit empty connection env stays authoritative.
	if cp, ok := connection.(childProcessConnection); ok {
		if env := cp.connEnv(); env != nil {
			opts.Env = env
		}
	}

	// Default Env to current environment if not set
	if opts.Env == nil {
		opts.Env = os.Environ()
	}

	// Check the effective environment for a child-process runtime override.
	if client.cliPath == "" && !client.useInProcess {
		if cliPath := getEnvValue(opts.Env, "COPILOT_CLI_PATH"); cliPath != "" {
			client.cliPath = cliPath
		}
	}

	// Resolve the effective connection token: explicit value if set; else if the SDK
	// spawns its own runtime in TCP mode, generate a UUID; otherwise empty. The
	// in-process transport uses no socket, so it needs no connection token.
	if client.tcpConnectionToken != "" {
		client.effectiveConnectionToken = client.tcpConnectionToken
	} else if !client.useStdio && !client.isExternalServer && !client.useInProcess {
		client.effectiveConnectionToken = uuid.NewString()
	}

	if opts.OnListModels != nil {
		client.onListModels = opts.OnListModels
	}
	if opts.SessionFS != nil {
		if err := validateSessionFSConfig(opts.SessionFS); err != nil {
			panic(err.Error())
		}
	}

	client.options = opts
	validateNewClientForMode(&client.options)
	return client
}

const defaultConnectionEnvVar = "COPILOT_SDK_DEFAULT_CONNECTION"

// resolveDefaultConnection selects the transport when no explicit connection
// was supplied. The override is primarily used by hosts and the E2E transport
// matrix; explicit connection options always take precedence.
func resolveDefaultConnection(env []string) RuntimeConnection {
	value := getEnvValue(env, defaultConnectionEnvVar)
	switch {
	case value == "", strings.EqualFold(value, "stdio"):
		return StdioConnection{}
	case strings.EqualFold(value, "inprocess"):
		return InProcessConnection{}
	default:
		panic(fmt.Sprintf(
			"invalid %s value %q: expected \"inprocess\", \"stdio\", or unset",
			defaultConnectionEnvVar,
			value,
		))
	}
}

// getEnvValue looks up a key in an environment slice ([]string of "KEY=VALUE").
// Returns the value if found, or empty string otherwise.
func getEnvValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return env[i][len(prefix):]
		}
	}
	return ""
}

// setEnvValue returns a copy of env with all existing entries for key removed and
// a single trailing KEY=VALUE entry added so SDK-managed values win deterministically.
func setEnvValue(env []string, key string, value string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return append(filtered, key+"="+value)
}

// parseCLIURL parses a CLI URL into host and port components.
//
// Supports formats: "host:port", "http://host:port", "https://host:port", or just "port".
// Panics if the URL format is invalid or the port is out of range.
func parseCLIURL(url string) (string, int) {
	// Remove protocol if present
	cleanURL, _ := strings.CutPrefix(url, "https://")
	cleanURL, _ = strings.CutPrefix(cleanURL, "http://")

	// Parse host:port or port format
	var host string
	var portStr string
	if before, after, found := strings.Cut(cleanURL, ":"); found {
		host = before
		portStr = after
	} else {
		// Only port provided
		portStr = before
	}

	if host == "" {
		host = "localhost"
	}

	// Validate port
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		panic(fmt.Sprintf("Invalid port in URIConnection: %s", url))
	}

	return host, port
}

// Start starts the CLI server (if not using an external server) and establishes
// a connection.
//
// If connecting to an external server (via URIConnection), only establishes the connection.
// Otherwise, spawns the CLI server process and then connects.
//
// This method is called automatically when creating a session if AutoStart is true (default).
//
// Returns an error if the server fails to start or the connection fails.
//
// Example:
//
//	client := copilot.NewClient(&copilot.ClientOptions{AutoStart: boolPtr(false)})
//	if err := client.Start(context.Background()); err != nil {
//	    log.Fatal("Failed to start:", err)
//	}
//	// Now ready to create sessions
func (c *Client) Start(ctx context.Context) error {
	c.startStopMux.Lock()
	defer c.startStopMux.Unlock()

	if c.state == stateConnected {
		return nil
	}

	c.state = stateConnecting

	// Only start CLI server process if not connecting to external server
	if !c.isExternalServer {
		if err := c.startCLIServer(ctx); err != nil {
			c.process = nil
			c.state = stateError
			return err
		}
	}

	// Connect to the server
	if err := c.connectToServer(ctx); err != nil {
		killErr := c.killProcess()
		c.state = stateError
		return errors.Join(err, killErr)
	}

	// Verify protocol version compatibility
	if err := c.verifyProtocolVersion(ctx); err != nil {
		killErr := c.killProcess()
		c.state = stateError
		return errors.Join(err, killErr)
	}

	// If a session filesystem provider was configured, register it.
	if c.options.SessionFS != nil {
		req := &rpc.SessionFSSetProviderRequest{
			InitialCwd:       c.options.SessionFS.InitialWorkingDirectory,
			SessionStatePath: c.options.SessionFS.SessionStatePath,
			Conventions:      c.options.SessionFS.Conventions,
		}
		if c.options.SessionFS.Capabilities != nil {
			sqlite := c.options.SessionFS.Capabilities.Sqlite
			req.Capabilities = &rpc.SessionFSSetProviderCapabilities{
				Sqlite: &sqlite,
			}
		}
		_, err := c.RPC.SessionFS.SetProvider(ctx, req)
		if err != nil {
			killErr := c.killProcess()
			c.state = stateError
			return errors.Join(err, killErr)
		}
	}

	// If a request handler was configured, register as the inference provider.
	if c.options.RequestHandler != nil {
		if _, err := c.RPC.LlmInference.SetProvider(ctx); err != nil {
			killErr := c.killProcess()
			c.state = stateError
			return errors.Join(err, killErr)
		}
	}

	c.state = stateConnected
	return nil
}

// Stop stops the CLI server and closes all active sessions.
//
// This method performs graceful cleanup:
//  1. Closes all active sessions (releases in-memory resources)
//  2. Requests runtime shutdown for SDK-owned CLI processes
//  3. Closes the JSON-RPC connection
//  4. Terminates the CLI server process (if spawned by this client)
//
// Note: session data on disk is preserved, so sessions can be resumed later.
// To permanently remove session data before stopping, call [Client.DeleteSession]
// for each session first.
//
// Returns an error that aggregates all errors encountered during cleanup.
//
// Example:
//
//	if err := client.Stop(); err != nil {
//	    log.Printf("Cleanup error: %v", err)
//	}
func (c *Client) Stop() error {
	var errs []error

	// Disconnect all active sessions
	c.sessionsMux.Lock()
	sessions := make([]*Session, 0, len(c.sessions))
	for _, session := range c.sessions {
		sessions = append(sessions, session)
	}
	c.sessionsMux.Unlock()

	for _, session := range sessions {
		if err := session.Disconnect(); err != nil {
			errs = append(errs, fmt.Errorf("failed to disconnect session %s: %w", session.SessionID, err))
		}
	}

	c.sessionsMux.Lock()
	c.sessions = make(map[string]*Session)
	c.sessionsMux.Unlock()

	c.startStopMux.Lock()
	defer c.startStopMux.Unlock()

	if (c.process != nil || c.ffiHost != nil) && !c.isExternalServer && c.RPC != nil {
		rpcClient := c.RPC
		runtimeShutdownStart := time.Now()
		shutdownDone := make(chan error, 1)
		go func() {
			_, err := rpcClient.Runtime.Shutdown(context.Background())
			shutdownDone <- err
		}()

		select {
		case err := <-shutdownDone:
			if err != nil {
				c.logDebugTiming(runtimeShutdownStart, "CopilotClient.Stop runtime shutdown failed")
				errs = append(errs, fmt.Errorf("failed to gracefully shut down runtime: %w", err))
			} else {
				c.logDebugTiming(runtimeShutdownStart, "CopilotClient.Stop runtime shutdown complete")
			}
		case <-time.After(runtimeShutdownTimeout):
			c.logDebugTiming(runtimeShutdownStart, "CopilotClient.Stop runtime shutdown timed out")
			errs = append(errs, fmt.Errorf("timed out gracefully shutting down runtime after %s", runtimeShutdownTimeout))
		}
	}

	// The runtime completes all cleanup before responding to runtime.shutdown
	// and then leaves termination to us; it deliberately keeps its JSON-RPC
	// server alive to send the response and never self-exits. Waiting for a
	// self-exit that will never come just wastes time, so terminate the child
	// immediately and only wait to reap it.
	if c.process != nil && !c.isExternalServer {
		if err := c.killProcessAndWait(); err != nil {
			errs = append(errs, err)
		}
	}
	c.process = nil

	// Tear down the in-process FFI host (closes the connection and shuts down the
	// native runtime). No child process to reap in this mode.
	if c.ffiHost != nil {
		c.ffiHost.Dispose()
		c.ffiHost = nil
	}

	// Close external TCP connection if exists
	if c.isExternalServer && c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close socket: %w", err))
		}
		c.conn = nil
	}

	// Then close JSON-RPC client (readLoop can now exit)
	if c.client != nil {
		c.client.Stop()
		c.client = nil
	}

	// Clear models cache
	c.modelsCacheMux.Lock()
	c.modelsCache = nil
	c.modelsCacheMux.Unlock()

	c.state = stateDisconnected
	if !c.isExternalServer {
		c.actualPort = 0
	}

	c.RPC = nil
	c.internalRPC = nil
	return errors.Join(errs...)
}

func (c *Client) logDebugTiming(start time.Time, message string) {
	switch strings.ToLower(c.options.LogLevel) {
	case "debug", "all":
		log.Printf("%s elapsed=%s", message, time.Since(start))
	}
}

// ForceStop forcefully stops the CLI server without graceful cleanup.
//
// Use this when [Client.Stop] fails or takes too long. This method:
//   - Clears all sessions immediately without destroying them
//   - Force closes the connection
//   - Kills the CLI process (if spawned by this client)
//
// Example:
//
//	// If normal stop hangs, force stop
//	done := make(chan struct{})
//	go func() {
//	    client.Stop()
//	    close(done)
//	}()
//
//	select {
//	case <-done:
//	    // Stopped successfully
//	case <-time.After(5 * time.Second):
//	    client.ForceStop()
//	}
func (c *Client) ForceStop() {
	// Kill the process without waiting for startStopMux, which Start may hold.
	// This unblocks any I/O Start is doing (connect, version check).
	if p := c.osProcess.Swap(nil); p != nil {
		p.Kill()
	}

	// Clear sessions immediately without trying to destroy them
	c.sessionsMux.Lock()
	c.sessions = make(map[string]*Session)
	c.sessionsMux.Unlock()

	c.startStopMux.Lock()
	defer c.startStopMux.Unlock()

	// Kill CLI process (only if we spawned it)
	// This is a fallback in case the process wasn't killed above (e.g. if Start hadn't set
	// osProcess yet), or if the process was restarted and osProcess now points to a new process.
	if c.process != nil && !c.isExternalServer {
		_ = c.killProcess() // Ignore errors since we're force stopping
	}
	c.process = nil

	// Dispose the in-process FFI host (if any) without waiting on graceful shutdown.
	if c.ffiHost != nil {
		c.ffiHost.Dispose()
		c.ffiHost = nil
	}

	// Close external TCP connection if exists
	if c.isExternalServer && c.conn != nil {
		_ = c.conn.Close() // Ignore errors
		c.conn = nil
	}

	// Close JSON-RPC client
	if c.client != nil {
		c.client.Stop()
		c.client = nil
	}

	// Clear models cache
	c.modelsCacheMux.Lock()
	c.modelsCache = nil
	c.modelsCacheMux.Unlock()

	c.state = stateDisconnected
	if !c.isExternalServer {
		c.actualPort = 0
	}

	c.RPC = nil
	c.internalRPC = nil
}

func (c *Client) ensureConnected(ctx context.Context) error {
	if c.client != nil {
		return nil
	}
	return c.Start(ctx)
}

// CreateSession creates a new conversation session with the Copilot CLI.
//
// Sessions maintain conversation state, handle events, and manage tool execution.
// If the client is not connected, this will automatically start the runtime.
//
// The config parameter is optional. If no OnPermissionRequest handler is provided,
// permission requests are surfaced as events for the caller to resolve manually.
//
// Returns the created session or an error if session creation fails.
//
// Example:
//
//	// Basic session
//	session, err := client.CreateSession(context.Background(), nil)
//
//	// Session with model and tools
//	session, err := client.CreateSession(context.Background(), &copilot.SessionConfig{
//	    OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
//	    Model: "gpt-4",
//	    Tools: []copilot.Tool{
//	        {
//	            Name:        "get_weather",
//	            Description: "Get weather for a location",
//	            Handler:     weatherHandler,
//	        },
//	    },
//	})
//
// extractTransformCallbacks separates transform callbacks from a SystemMessageConfig,
// returning a wire-safe config and a map of callbacks (nil if none).
func extractTransformCallbacks(config *SystemMessageConfig) (*SystemMessageConfig, map[string]SectionTransformFn) {
	if config == nil || config.Mode != "customize" || len(config.Sections) == 0 {
		return config, nil
	}

	callbacks := make(map[string]SectionTransformFn)
	wireSections := make(map[string]SectionOverride)
	for id, override := range config.Sections {
		if override.Transform != nil {
			callbacks[id] = override.Transform
			wireSections[id] = SectionOverride{Action: "transform"}
		} else {
			wireSections[id] = override
		}
	}

	if len(callbacks) == 0 {
		return config, nil
	}

	wireConfig := &SystemMessageConfig{
		Mode:     config.Mode,
		Content:  config.Content,
		Sections: wireSections,
	}
	return wireConfig, callbacks
}

func hasManagedSettings(enableManagedSettings *bool, managedSettings *ManagedSettings) bool {
	return (enableManagedSettings != nil && *enableManagedSettings) || managedSettings != nil
}

func (c *Client) CreateSession(ctx context.Context, config *SessionConfig) (*Session, error) {
	if config == nil {
		config = &SessionConfig{}
	}

	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	c.applyConfigDefaultsForMode(config)

	req := createSessionRequest{}
	req.Model = config.Model
	req.ClientName = config.ClientName
	req.ReasoningEffort = config.ReasoningEffort
	req.ReasoningSummary = config.ReasoningSummary
	req.ContextTier = config.ContextTier
	req.ConfigDir = config.ConfigDirectory
	req.EnableConfigDiscovery = config.EnableConfigDiscovery
	req.SkipEmbeddingRetrieval = config.SkipEmbeddingRetrieval
	req.EmbeddingCacheStorage = config.EmbeddingCacheStorage
	req.OrganizationCustomInstructions = config.OrganizationCustomInstructions
	req.EnableOnDemandInstructionDiscovery = config.EnableOnDemandInstructionDiscovery
	req.EnableFileHooks = config.EnableFileHooks
	req.EnableHostGitOperations = config.EnableHostGitOperations
	req.EnableSessionStore = config.EnableSessionStore
	req.EnableSkills = config.EnableSkills
	req.Tools = config.Tools
	systemMessage := c.systemMessageForMode(config.SystemMessage)
	wireSystemMessage, transformCallbacks := extractTransformCallbacks(systemMessage)
	req.SystemMessage = wireSystemMessage
	availableTools, excludedTools, precedence, ferr := c.resolveToolFilterOptions(config.AvailableTools, config.ExcludedTools)
	if ferr != nil {
		return nil, ferr
	}
	req.AvailableTools = availableTools
	req.ExcludedTools = excludedTools
	req.ToolFilterPrecedence = precedence
	req.ExcludedBuiltInAgents = config.ExcludedBuiltInAgents
	req.Provider = config.Provider
	req.Capi = config.Capi
	req.Providers = config.Providers
	req.Models = config.Models
	req.EnableSessionTelemetry = config.EnableSessionTelemetry
	req.EnableCitations = config.EnableCitations
	req.SessionLimits = config.SessionLimits
	req.IsExperimentalMode = config.EnableExperimentalMode
	req.SkipCustomInstructions = config.SkipCustomInstructions
	req.CustomAgentsLocalOnly = config.CustomAgentsLocalOnly
	req.CoauthorEnabled = config.CoauthorEnabled
	req.ManageScheduleEnabled = config.ManageScheduleEnabled
	req.ModelCapabilities = config.ModelCapabilities
	req.WorkingDirectory = config.WorkingDirectory
	req.AdditionalDirectories = config.AdditionalDirectories
	req.MCPServers = config.MCPServers
	req.MCPOAuthTokenStorage = config.MCPOAuthTokenStorage
	req.EnvValueMode = "direct"
	req.CustomAgents = config.CustomAgents
	req.DefaultAgent = config.DefaultAgent
	req.Agent = config.Agent
	req.SkillDirectories = config.SkillDirectories
	req.PluginDirectories = config.PluginDirectories
	req.InstructionDirectories = config.InstructionDirectories
	req.DisabledSkills = config.DisabledSkills
	if config.DisabledMCPServers != nil {
		req.DisabledMCPServers = &config.DisabledMCPServers
	}
	req.InfiniteSessions = config.InfiniteSessions
	req.LargeOutput = config.LargeOutput
	req.ToolSearch = config.ToolSearch
	req.Memory = config.Memory
	req.GitHubToken = config.GitHubToken
	req.RemoteSession = config.RemoteSession
	req.Cloud = config.Cloud
	req.Canvases = config.Canvases
	req.ExtensionInfo = config.ExtensionInfo
	req.CanvasProvider = config.CanvasProvider
	req.RequestCanvasRenderer = config.RequestCanvasRenderer
	req.RequestExtensions = config.RequestExtensions
	req.ExtensionSDKPath = config.ExtensionSDKPath
	req.ExtensionInfo = config.ExtensionInfo
	req.ExpAssignments = config.ExpAssignments
	req.EnableManagedSettings = config.EnableManagedSettings
	req.ManagedSettings = config.ManagedSettings

	if len(config.Commands) > 0 {
		cmds := make([]wireCommand, 0, len(config.Commands))
		for _, cmd := range config.Commands {
			cmds = append(cmds, wireCommand{Name: cmd.Name, Description: cmd.Description})
		}
		req.Commands = cmds
	}
	if config.OnElicitationRequest != nil {
		req.RequestElicitation = Bool(true)
	}
	if config.OnExitPlanModeRequest != nil {
		req.RequestExitPlanMode = Bool(true)
	}
	if config.OnAutoModeSwitchRequest != nil {
		req.RequestAutoModeSwitch = Bool(true)
	}
	if config.EnableMCPApps {
		req.RequestMCPApps = Bool(true)
	}
	req.GitHubMCPToolConfig = config.GitHubMCPToolConfig

	if config.Streaming != nil {
		req.Streaming = config.Streaming
	}
	if config.IncludeSubAgentStreamingEvents != nil {
		req.IncludeSubAgentStreamingEvents = config.IncludeSubAgentStreamingEvents
	} else {
		req.IncludeSubAgentStreamingEvents = Bool(true)
	}
	if c.options.OnGitHubTelemetry != nil {
		req.EnableGitHubTelemetryForwarding = Bool(true)
	}
	if config.OnUserInputRequest != nil {
		req.RequestUserInput = Bool(true)
	}
	if config.Hooks != nil && (config.Hooks.OnPreToolUse != nil ||
		config.Hooks.OnPreMCPToolCall != nil ||
		config.Hooks.OnPostToolUse != nil ||
		config.Hooks.OnPostToolUseFailure != nil ||
		config.Hooks.OnUserPromptSubmitted != nil ||
		config.Hooks.OnUserPromptTransformed != nil ||
		config.Hooks.OnSessionStart != nil ||
		config.Hooks.OnSessionEnd != nil ||
		config.Hooks.OnErrorOccurred != nil ||
		config.Hooks.OnAgentStop != nil) {
		req.Hooks = Bool(true)
	}
	if config.OnPermissionRequest != nil {
		req.RequestPermission = Bool(true)
	}

	traceparent, tracestate := getTraceContext(ctx)
	req.Traceparent = traceparent
	req.Tracestate = tracestate

	// For cloud sessions, let the CLI/server assign the session id and
	// register the session lazily once the response arrives. For non-cloud
	// sessions we generate the id client-side (when the caller didn't
	// supply one) so the session can be registered BEFORE the RPC — the
	// CLI may issue session-scoped requests (e.g. sessionFs.writeFile for
	// workspace metadata) during session.create processing, before it has
	// sent the response.
	useServerGeneratedID := config.Cloud != nil && config.SessionID == ""
	var localSessionID string
	if useServerGeneratedID {
		localSessionID = ""
	} else if config.SessionID != "" {
		localSessionID = config.SessionID
	} else {
		localSessionID = uuid.NewString()
	}
	req.SessionID = localSessionID

	// initializeSession creates the session, wires up handlers, and registers
	// it in the sessions map. Invoked from the read loop the instant the
	// session.create response arrives (synchronously, before the next
	// message is dispatched) so notifications for the new session id are
	// routed to a registered session.
	initializeSession := func(sessionID string) (*Session, error) {
		s := newSession(
			sessionID,
			c.client,
			"",
			hasManagedSettings(config.EnableManagedSettings, config.ManagedSettings),
		)

		s.registerTools(config.Tools)
		s.registerPermissionHandler(config.OnPermissionRequest)
		s.registerMCPAuthHandler(config.OnMCPAuthRequest)
		if config.OnUserInputRequest != nil {
			s.registerUserInputHandler(config.OnUserInputRequest)
		}
		if config.Hooks != nil {
			s.registerHooks(config.Hooks)
		}
		if transformCallbacks != nil {
			s.registerTransformCallbacks(transformCallbacks)
		}
		if config.OnEvent != nil {
			s.On(config.OnEvent)
		}
		if len(config.Commands) > 0 {
			s.registerCommands(config.Commands)
		}
		if config.OnElicitationRequest != nil {
			s.registerElicitationHandler(config.OnElicitationRequest)
		}
		if config.OnExitPlanModeRequest != nil {
			s.registerExitPlanModeHandler(config.OnExitPlanModeRequest)
		}
		if config.OnAutoModeSwitchRequest != nil {
			s.registerAutoModeSwitchHandler(config.OnAutoModeSwitchRequest)
		}
		if config.CanvasHandler != nil {
			s.registerCanvasHandler(config.CanvasHandler)
		}
		if bearerTokenProviders := collectBearerTokenProviders(config.Provider, config.Providers); bearerTokenProviders != nil {
			s.registerBearerTokenProviders(bearerTokenProviders)
		}

		c.sessionsMux.Lock()
		c.sessions[sessionID] = s
		c.sessionsMux.Unlock()

		if c.options.SessionFS != nil {
			if config.CreateSessionFSProvider == nil {
				c.sessionsMux.Lock()
				delete(c.sessions, sessionID)
				c.sessionsMux.Unlock()
				return nil, fmt.Errorf("CreateSessionFSProvider is required in session config when SessionFS is enabled in client options")
			}
			provider := config.CreateSessionFSProvider(s)
			if c.options.SessionFS.Capabilities != nil && c.options.SessionFS.Capabilities.Sqlite {
				if _, ok := provider.(SessionFSSqliteProvider); !ok {
					c.sessionsMux.Lock()
					delete(c.sessions, sessionID)
					c.sessionsMux.Unlock()
					return nil, fmt.Errorf("SessionFS capabilities declare SQLite support but the provider does not implement SessionFSSqliteProvider")
				}
			}
			s.clientSessionAPIs.SessionFS = newSessionFSAdapter(provider)
		}
		return s, nil
	}

	var session *Session
	var registeredSessionID string

	// Pre-register non-cloud sessions BEFORE issuing the RPC so any
	// session-scoped requests the CLI emits during session.create processing
	// (e.g. sessionFs.writeFile for workspace metadata) can be routed to the
	// correct handlers.
	if localSessionID != "" {
		s, err := initializeSession(localSessionID)
		if err != nil {
			return nil, err
		}
		session = s
		registeredSessionID = localSessionID
	}

	// For the server-assigned (cloud) path, register the session
	// synchronously from the read loop the instant the response arrives,
	// before the read loop dispatches the next message. Without this hook
	// the awaiter goroutine may not run until after the read loop has
	// dispatched the first session.event notification, which would be
	// silently dropped because the session id isn't yet in the lookup
	// table. Non-cloud sessions are already registered above.
	var inlineCb func(raw json.RawMessage) error
	if session == nil {
		inlineCb = func(raw json.RawMessage) error {
			var early struct {
				SessionID string `json:"sessionId"`
			}
			if err := json.Unmarshal(raw, &early); err != nil {
				return fmt.Errorf("failed to parse sessionId from response: %w", err)
			}
			if early.SessionID == "" {
				return fmt.Errorf("session.create response did not include a sessionId")
			}
			s, err := initializeSession(early.SessionID)
			if err != nil {
				return err
			}
			session = s
			registeredSessionID = early.SessionID
			return nil
		}
	}

	result, err := c.client.RequestWithInlineResponse(ctx, "session.create", req, inlineCb)
	if err != nil {
		if registeredSessionID != "" {
			c.sessionsMux.Lock()
			delete(c.sessions, registeredSessionID)
			c.sessionsMux.Unlock()
		}
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	var response createSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		if registeredSessionID != "" {
			c.sessionsMux.Lock()
			delete(c.sessions, registeredSessionID)
			c.sessionsMux.Unlock()
		}
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if session == nil {
		return nil, fmt.Errorf("session.create response did not include a sessionId")
	}

	if localSessionID != "" && response.SessionID != "" && response.SessionID != localSessionID {
		c.sessionsMux.Lock()
		delete(c.sessions, registeredSessionID)
		c.sessionsMux.Unlock()
		return nil, fmt.Errorf("session.create returned sessionId %s but the caller requested %s", response.SessionID, localSessionID)
	}
	if config.OnMCPAuthRequest != nil {
		if _, err := c.client.Request(ctx, "session.eventLog.registerInterest", map[string]any{
			"sessionId": session.SessionID,
			"eventType": "mcp.oauth_required",
		}); err != nil {
			return nil, err
		}
	}

	session.workspacePath = response.WorkspacePath
	session.setCapabilities(response.Capabilities)

	if err := c.updateSessionOptionsForMode(ctx, session, optBackInFields{
		SkipCustomInstructions: config.SkipCustomInstructions,
		CustomAgentsLocalOnly:  config.CustomAgentsLocalOnly,
		CoauthorEnabled:        config.CoauthorEnabled,
		ManageScheduleEnabled:  config.ManageScheduleEnabled,
	}); err != nil {
		return nil, err
	}

	return session, nil
}

// ResumeSession resumes an existing conversation session by its ID.
//
// This is a convenience method that calls [Client.ResumeSessionWithOptions].
// Example:
//
//	session, err := client.ResumeSession(context.Background(), "session-123", &copilot.ResumeSessionConfig{
//	    OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
//	})
func (c *Client) ResumeSession(ctx context.Context, sessionID string, config *ResumeSessionConfig) (*Session, error) {
	return c.ResumeSessionWithOptions(ctx, sessionID, config)
}

// ResumeSessionWithOptions resumes an existing conversation session with additional configuration.
//
// This allows you to continue a previous conversation, maintaining all conversation history.
// The session must have been previously created and not deleted.
//
// Example:
//
//	session, err := client.ResumeSessionWithOptions(context.Background(), "session-123", &copilot.ResumeSessionConfig{
//	    OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
//	    Tools: []copilot.Tool{myNewTool},
//	})
func (c *Client) ResumeSessionWithOptions(ctx context.Context, sessionID string, config *ResumeSessionConfig) (*Session, error) {
	if config == nil {
		config = &ResumeSessionConfig{}
	}

	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	c.applyResumeDefaultsForMode(config)

	var req resumeSessionRequest
	req.SessionID = sessionID
	req.ClientName = config.ClientName
	req.Model = config.Model
	req.ReasoningEffort = config.ReasoningEffort
	req.ReasoningSummary = config.ReasoningSummary
	req.ContextTier = config.ContextTier
	systemMessage := c.systemMessageForMode(config.SystemMessage)
	wireSystemMessage, transformCallbacks := extractTransformCallbacks(systemMessage)
	req.SystemMessage = wireSystemMessage
	req.Tools = config.Tools
	req.Provider = config.Provider
	req.Capi = config.Capi
	req.Providers = config.Providers
	req.Models = config.Models
	req.EnableSessionTelemetry = config.EnableSessionTelemetry
	req.IsExperimentalMode = config.EnableExperimentalMode
	req.SkipCustomInstructions = config.SkipCustomInstructions
	req.CustomAgentsLocalOnly = config.CustomAgentsLocalOnly
	req.CoauthorEnabled = config.CoauthorEnabled
	req.ManageScheduleEnabled = config.ManageScheduleEnabled
	req.ModelCapabilities = config.ModelCapabilities
	availableTools, excludedTools, precedence, ferr := c.resolveToolFilterOptions(config.AvailableTools, config.ExcludedTools)
	if ferr != nil {
		return nil, ferr
	}
	req.AvailableTools = availableTools
	req.ExcludedTools = excludedTools
	req.ToolFilterPrecedence = precedence
	req.ExcludedBuiltInAgents = config.ExcludedBuiltInAgents
	req.EnableCitations = config.EnableCitations
	req.SessionLimits = config.SessionLimits
	if config.Streaming != nil {
		req.Streaming = config.Streaming
	}
	if config.IncludeSubAgentStreamingEvents != nil {
		req.IncludeSubAgentStreamingEvents = config.IncludeSubAgentStreamingEvents
	} else {
		req.IncludeSubAgentStreamingEvents = Bool(true)
	}
	if c.options.OnGitHubTelemetry != nil {
		req.EnableGitHubTelemetryForwarding = Bool(true)
	}
	if config.OnUserInputRequest != nil {
		req.RequestUserInput = Bool(true)
	}
	if config.Hooks != nil && (config.Hooks.OnPreToolUse != nil ||
		config.Hooks.OnPreMCPToolCall != nil ||
		config.Hooks.OnPostToolUse != nil ||
		config.Hooks.OnPostToolUseFailure != nil ||
		config.Hooks.OnUserPromptSubmitted != nil ||
		config.Hooks.OnUserPromptTransformed != nil ||
		config.Hooks.OnSessionStart != nil ||
		config.Hooks.OnSessionEnd != nil ||
		config.Hooks.OnErrorOccurred != nil ||
		config.Hooks.OnAgentStop != nil) {
		req.Hooks = Bool(true)
	}
	req.WorkingDirectory = config.WorkingDirectory
	req.AdditionalDirectories = config.AdditionalDirectories
	req.ConfigDir = config.ConfigDirectory
	req.EnableConfigDiscovery = config.EnableConfigDiscovery
	req.SkipEmbeddingRetrieval = config.SkipEmbeddingRetrieval
	req.EmbeddingCacheStorage = config.EmbeddingCacheStorage
	req.OrganizationCustomInstructions = config.OrganizationCustomInstructions
	req.EnableOnDemandInstructionDiscovery = config.EnableOnDemandInstructionDiscovery
	req.EnableFileHooks = config.EnableFileHooks
	req.EnableHostGitOperations = config.EnableHostGitOperations
	req.EnableSessionStore = config.EnableSessionStore
	req.EnableSkills = config.EnableSkills
	if config.SuppressResumeEvent {
		req.DisableResume = Bool(true)
	}
	req.ContinuePendingWork = config.ContinuePendingWork
	req.MCPServers = config.MCPServers
	req.MCPOAuthTokenStorage = config.MCPOAuthTokenStorage
	req.EnvValueMode = "direct"
	req.CustomAgents = config.CustomAgents
	req.DefaultAgent = config.DefaultAgent
	req.Agent = config.Agent
	req.SkillDirectories = config.SkillDirectories
	req.PluginDirectories = config.PluginDirectories
	req.InstructionDirectories = config.InstructionDirectories
	req.DisabledSkills = config.DisabledSkills
	if config.DisabledMCPServers != nil {
		req.DisabledMCPServers = &config.DisabledMCPServers
	}
	req.InfiniteSessions = config.InfiniteSessions
	req.LargeOutput = config.LargeOutput
	req.ToolSearch = config.ToolSearch
	req.Memory = config.Memory
	req.GitHubToken = config.GitHubToken
	req.RemoteSession = config.RemoteSession
	req.Canvases = config.Canvases
	req.OpenCanvases = config.OpenCanvases
	req.ExtensionInfo = config.ExtensionInfo
	req.CanvasProvider = config.CanvasProvider
	req.RequestCanvasRenderer = config.RequestCanvasRenderer
	req.RequestExtensions = config.RequestExtensions
	req.ExtensionSDKPath = config.ExtensionSDKPath
	req.ExtensionInfo = config.ExtensionInfo
	req.ExpAssignments = config.ExpAssignments
	req.EnableManagedSettings = config.EnableManagedSettings
	req.ManagedSettings = config.ManagedSettings
	if config.OnPermissionRequest != nil {
		req.RequestPermission = Bool(true)
	}

	if len(config.Commands) > 0 {
		cmds := make([]wireCommand, 0, len(config.Commands))
		for _, cmd := range config.Commands {
			cmds = append(cmds, wireCommand{Name: cmd.Name, Description: cmd.Description})
		}
		req.Commands = cmds
	}
	if config.OnElicitationRequest != nil {
		req.RequestElicitation = Bool(true)
	}
	if config.OnExitPlanModeRequest != nil {
		req.RequestExitPlanMode = Bool(true)
	}
	if config.OnAutoModeSwitchRequest != nil {
		req.RequestAutoModeSwitch = Bool(true)
	}
	if config.EnableMCPApps {
		req.RequestMCPApps = Bool(true)
	}
	req.GitHubMCPToolConfig = config.GitHubMCPToolConfig

	traceparent, tracestate := getTraceContext(ctx)
	req.Traceparent = traceparent
	req.Tracestate = tracestate

	// Create and register the session before issuing the RPC so that
	// events emitted by the CLI (e.g. session.start) are not dropped.
	session := newSession(
		sessionID,
		c.client,
		"",
		hasManagedSettings(config.EnableManagedSettings, config.ManagedSettings),
	)

	session.registerTools(config.Tools)
	session.registerPermissionHandler(config.OnPermissionRequest)
	session.registerMCPAuthHandler(config.OnMCPAuthRequest)
	if config.OnUserInputRequest != nil {
		session.registerUserInputHandler(config.OnUserInputRequest)
	}
	if config.Hooks != nil {
		session.registerHooks(config.Hooks)
	}
	if transformCallbacks != nil {
		session.registerTransformCallbacks(transformCallbacks)
	}
	if config.OnEvent != nil {
		session.On(config.OnEvent)
	}
	if len(config.Commands) > 0 {
		session.registerCommands(config.Commands)
	}
	if config.OnElicitationRequest != nil {
		session.registerElicitationHandler(config.OnElicitationRequest)
	}
	if config.OnExitPlanModeRequest != nil {
		session.registerExitPlanModeHandler(config.OnExitPlanModeRequest)
	}
	if config.OnAutoModeSwitchRequest != nil {
		session.registerAutoModeSwitchHandler(config.OnAutoModeSwitchRequest)
	}
	if config.CanvasHandler != nil {
		session.registerCanvasHandler(config.CanvasHandler)
	}
	if bearerTokenProviders := collectBearerTokenProviders(config.Provider, config.Providers); bearerTokenProviders != nil {
		session.registerBearerTokenProviders(bearerTokenProviders)
	}

	c.sessionsMux.Lock()
	c.sessions[sessionID] = session
	c.sessionsMux.Unlock()

	if c.options.SessionFS != nil {
		if config.CreateSessionFSProvider == nil {
			c.sessionsMux.Lock()
			delete(c.sessions, sessionID)
			c.sessionsMux.Unlock()
			return nil, fmt.Errorf("CreateSessionFSProvider is required in session config when SessionFS is enabled in client options")
		}
		provider := config.CreateSessionFSProvider(session)
		if c.options.SessionFS.Capabilities != nil && c.options.SessionFS.Capabilities.Sqlite {
			if _, ok := provider.(SessionFSSqliteProvider); !ok {
				c.sessionsMux.Lock()
				delete(c.sessions, sessionID)
				c.sessionsMux.Unlock()
				return nil, fmt.Errorf("SessionFS capabilities declare SQLite support but the provider does not implement SessionFSSqliteProvider")
			}
		}
		session.clientSessionAPIs.SessionFS = newSessionFSAdapter(provider)
	}

	result, err := c.client.Request(ctx, "session.resume", req)
	if err != nil {
		c.sessionsMux.Lock()
		delete(c.sessions, sessionID)
		c.sessionsMux.Unlock()
		return nil, fmt.Errorf("failed to resume session: %w", err)
	}

	var response resumeSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		c.sessionsMux.Lock()
		delete(c.sessions, sessionID)
		c.sessionsMux.Unlock()
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if config.OnMCPAuthRequest != nil {
		if _, err := c.client.Request(ctx, "session.eventLog.registerInterest", map[string]any{
			"sessionId": sessionID,
			"eventType": "mcp.oauth_required",
		}); err != nil {
			c.sessionsMux.Lock()
			delete(c.sessions, sessionID)
			c.sessionsMux.Unlock()
			return nil, err
		}
	}

	session.workspacePath = response.WorkspacePath
	session.setCapabilities(response.Capabilities)
	session.setOpenCanvases(response.OpenCanvases)

	if err := c.updateSessionOptionsForMode(ctx, session, optBackInFields{
		SkipCustomInstructions: config.SkipCustomInstructions,
		CustomAgentsLocalOnly:  config.CustomAgentsLocalOnly,
		CoauthorEnabled:        config.CoauthorEnabled,
		ManageScheduleEnabled:  config.ManageScheduleEnabled,
	}); err != nil {
		return nil, err
	}

	return session, nil
}

// ListSessions returns metadata about all sessions known to the server.
//
// Returns a list of SessionMetadata for all available sessions, including their IDs,
// timestamps, optional summaries, and context information.
//
// An optional filter can be provided to filter sessions by working directory, git root, repository, or branch.
//
// Example:
//
//	sessions, err := client.ListSessions(context.Background(), nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	for _, session := range sessions {
//	    fmt.Printf("Session: %s\n", session.SessionID)
//	}
//
// Example with filter:
//
//	sessions, err := client.ListSessions(context.Background(), &SessionListFilter{Repository: "owner/repo"})
func (c *Client) ListSessions(ctx context.Context, filter *SessionListFilter) ([]SessionMetadata, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	params := listSessionsRequest{}
	if filter != nil {
		params.Filter = filter
	}
	result, err := c.client.Request(ctx, "session.list", params)
	if err != nil {
		return nil, err
	}

	var response listSessionsResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal sessions response: %w", err)
	}

	return response.Sessions, nil
}

// GetSessionMetadata returns metadata for a specific session by ID.
//
// This provides an efficient O(1) lookup of a single session's metadata
// instead of listing all sessions. Returns nil if the session is not found.
//
// Example:
//
//	metadata, err := client.GetSessionMetadata(context.Background(), "session-123")
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if metadata != nil {
//	    fmt.Printf("Session started at: %s\n", metadata.StartTime)
//	}
func (c *Client) GetSessionMetadata(ctx context.Context, sessionID string) (*SessionMetadata, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	result, err := c.client.Request(ctx, "session.getMetadata", getSessionMetadataRequest{SessionID: sessionID})
	if err != nil {
		return nil, err
	}

	var response getSessionMetadataResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal session metadata response: %w", err)
	}

	return response.Session, nil
}

// DeleteSession permanently deletes a session and all its data from disk,
// including conversation history, planning state, and artifacts.
//
// Unlike [Session.Disconnect], which only releases in-memory resources and
// preserves session data for later resumption, DeleteSession is irreversible.
// The session cannot be resumed after deletion. If the session is in the local
// sessions map, it will be removed.
//
// Example:
//
//	if err := client.DeleteSession(context.Background(), "session-123"); err != nil {
//	    log.Fatal(err)
//	}
func (c *Client) DeleteSession(ctx context.Context, sessionID string) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}

	result, err := c.client.Request(ctx, "session.delete", deleteSessionRequest{SessionID: sessionID})
	if err != nil {
		return err
	}

	var response deleteSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return fmt.Errorf("failed to unmarshal delete response: %w", err)
	}

	if !response.Success {
		errorMsg := "unknown error"
		if response.Error != nil {
			errorMsg = *response.Error
		}
		return fmt.Errorf("failed to delete session %s: %s", sessionID, errorMsg)
	}

	// Remove from local sessions map if present
	c.sessionsMux.Lock()
	delete(c.sessions, sessionID)
	c.sessionsMux.Unlock()

	return nil
}

// GetLastSessionID returns the ID of the most recently updated session.
//
// This is useful for resuming the last conversation when the session ID
// was not stored. Returns nil if no sessions exist.
//
// Example:
//
//	lastID, err := client.GetLastSessionID(context.Background())
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if lastID != nil {
//	    session, err := client.ResumeSession(context.Background(), *lastID, &copilot.ResumeSessionConfig{
//	        OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
//	    })
//	}
func (c *Client) GetLastSessionID(ctx context.Context) (*string, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	result, err := c.client.Request(ctx, "session.getLastId", getLastSessionIDRequest{})
	if err != nil {
		return nil, err
	}

	var response getLastSessionIDResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal getLastId response: %w", err)
	}

	return response.SessionID, nil
}

// GetForegroundSessionID returns the ID of the session currently displayed in the TUI.
//
// This is only available when connecting to a server running in TUI+server mode
// (--ui-server). Returns nil if no foreground session is set.
//
// Example:
//
//	sessionID, err := client.GetForegroundSessionID()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if sessionID != nil {
//	    fmt.Printf("TUI is displaying session: %s\n", *sessionID)
//	}
func (c *Client) GetForegroundSessionID(ctx context.Context) (*string, error) {
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}

	result, err := c.client.Request(ctx, "session.getForeground", getForegroundSessionRequest{})
	if err != nil {
		return nil, err
	}

	var response getForegroundSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal getForeground response: %w", err)
	}

	return response.SessionID, nil
}

// SetForegroundSessionID requests the TUI to switch to displaying the specified session.
//
// This is only available when connecting to a server running in TUI+server mode
// (--ui-server).
//
// Example:
//
//	if err := client.SetForegroundSessionID("session-123"); err != nil {
//	    log.Fatal(err)
//	}
func (c *Client) SetForegroundSessionID(ctx context.Context, sessionID string) error {
	if err := c.ensureConnected(ctx); err != nil {
		return err
	}

	result, err := c.client.Request(ctx, "session.setForeground", setForegroundSessionRequest{SessionID: sessionID})
	if err != nil {
		return err
	}

	var response setForegroundSessionResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return fmt.Errorf("failed to unmarshal setForeground response: %w", err)
	}

	if !response.Success {
		errorMsg := "unknown error"
		if response.Error != nil {
			errorMsg = *response.Error
		}
		return fmt.Errorf("failed to set foreground session: %s", errorMsg)
	}

	return nil
}

// On subscribes to all session lifecycle events.
//
// Lifecycle events are emitted when sessions are created, deleted, updated,
// or change foreground/background state (in TUI+server mode).
//
// Returns a function that, when called, unsubscribes the handler.
//
// Example:
//
//	unsubscribe := client.On(func(event copilot.SessionLifecycleEvent) {
//	    fmt.Printf("Session %s: %s\n", event.SessionID, event.Type)
//	})
//	defer unsubscribe()
func (c *Client) On(handler SessionLifecycleHandler) func() {
	c.lifecycleHandlersMux.Lock()
	if c.lifecycleHandlers == nil {
		c.lifecycleHandlers = make(map[uint64]SessionLifecycleHandler)
	}
	c.nextLifecycleHandlerID++
	id := c.nextLifecycleHandlerID
	c.lifecycleHandlers[id] = handler
	c.lifecycleHandlersMux.Unlock()

	return func() {
		c.lifecycleHandlersMux.Lock()
		defer c.lifecycleHandlersMux.Unlock()
		delete(c.lifecycleHandlers, id)
	}
}

// OnEventType subscribes to a specific session lifecycle event type.
//
// Returns a function that, when called, unsubscribes the handler.
//
// Example:
//
//	unsubscribe := client.OnEventType(copilot.SessionLifecycleForeground, func(event copilot.SessionLifecycleEvent) {
//	    fmt.Printf("Session %s is now in foreground\n", event.SessionID)
//	})
//	defer unsubscribe()
func (c *Client) OnEventType(eventType SessionLifecycleEventType, handler SessionLifecycleHandler) func() {
	c.lifecycleHandlersMux.Lock()
	if c.typedLifecycleHandlers == nil {
		c.typedLifecycleHandlers = make(map[SessionLifecycleEventType]map[uint64]SessionLifecycleHandler)
	}
	if c.typedLifecycleHandlers[eventType] == nil {
		c.typedLifecycleHandlers[eventType] = make(map[uint64]SessionLifecycleHandler)
	}
	c.nextLifecycleHandlerID++
	id := c.nextLifecycleHandlerID
	c.typedLifecycleHandlers[eventType][id] = handler
	c.lifecycleHandlersMux.Unlock()

	return func() {
		c.lifecycleHandlersMux.Lock()
		defer c.lifecycleHandlersMux.Unlock()
		if handlers, ok := c.typedLifecycleHandlers[eventType]; ok {
			delete(handlers, id)
		}
	}
}

// handleLifecycleEvent dispatches a lifecycle event to all registered handlers
func (c *Client) handleLifecycleEvent(event SessionLifecycleEvent) {
	c.lifecycleHandlersMux.Lock()
	// Copy handlers to avoid holding lock during callbacks
	typedHandlers := make([]SessionLifecycleHandler, 0)
	if handlers, ok := c.typedLifecycleHandlers[event.Type]; ok {
		for _, handler := range handlers {
			typedHandlers = append(typedHandlers, handler)
		}
	}
	wildcardHandlers := make([]SessionLifecycleHandler, 0, len(c.lifecycleHandlers))
	for _, handler := range c.lifecycleHandlers {
		wildcardHandlers = append(wildcardHandlers, handler)
	}
	c.lifecycleHandlersMux.Unlock()

	// Dispatch to typed handlers
	for _, handler := range typedHandlers {
		func() {
			defer func() { recover() }() // Ignore handler panics
			handler(event)
		}()
	}

	// Dispatch to wildcard handlers
	for _, handler := range wildcardHandlers {
		func() {
			defer func() { recover() }() // Ignore handler panics
			handler(event)
		}()
	}
}

// RuntimePort returns the TCP port the runtime is listening on.
// Returns 0 if the client is not connected or using stdio transport.
func (c *Client) RuntimePort() int {
	return c.actualPort
}

// Ping sends a ping request to the server to verify connectivity.
//
// The message parameter is optional and will be echoed back in the response.
// Returns a PingResponse containing the message and server timestamp, or an error.
//
// Example:
//
//	resp, err := client.Ping(context.Background(), "health check")
//	if err != nil {
//	    log.Printf("Server unreachable: %v", err)
//	} else {
//	    log.Printf("Server responded at %s", resp.Timestamp)
//	}
func (c *Client) Ping(ctx context.Context, message string) (*PingResponse, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.Request(ctx, "ping", pingRequest{Message: message})
	if err != nil {
		return nil, err
	}

	var response PingResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetStatus returns CLI status including version and protocol information
func (c *Client) GetStatus(ctx context.Context) (*GetStatusResponse, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.Request(ctx, "status.get", getStatusRequest{})
	if err != nil {
		return nil, err
	}

	var response GetStatusResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// GetAuthStatus returns current authentication status
func (c *Client) GetAuthStatus(ctx context.Context) (*GetAuthStatusResponse, error) {
	if c.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	result, err := c.client.Request(ctx, "auth.getStatus", getAuthStatusRequest{})
	if err != nil {
		return nil, err
	}

	var response GetAuthStatusResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// ListModels returns available models with their metadata.
//
// Results are cached after the first successful call to avoid rate limiting.
// The cache is cleared when the client disconnects.
func (c *Client) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Use mutex for locking to prevent race condition with concurrent calls
	c.modelsCacheMux.Lock()
	defer c.modelsCacheMux.Unlock()

	// Check cache (already inside lock)
	if c.modelsCache != nil {
		result := make([]ModelInfo, len(c.modelsCache))
		copy(result, c.modelsCache)
		return result, nil
	}

	var models []ModelInfo
	if c.onListModels != nil {
		// Use custom handler instead of CLI RPC
		var err error
		models, err = c.onListModels(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		if c.client == nil {
			return nil, fmt.Errorf("client not connected")
		}
		// Cache miss - fetch from backend while holding lock
		result, err := c.client.Request(ctx, "models.list", listModelsRequest{})
		if err != nil {
			return nil, err
		}

		var response listModelsResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return nil, fmt.Errorf("failed to unmarshal models response: %w", err)
		}
		models = response.Models
	}

	// Update cache before releasing lock (copy to prevent external mutation)
	cache := make([]ModelInfo, len(models))
	copy(cache, models)
	c.modelsCache = cache

	// Return a copy to prevent cache mutation
	result := make([]ModelInfo, len(models))
	copy(result, models)
	return result, nil
}

// minProtocolVersion is the minimum protocol version this SDK can communicate with.
const minProtocolVersion = 3
const runtimeShutdownTimeout = 10 * time.Second
const processExitTimeout = 10 * time.Second

// verifyProtocolVersion sends the `connect` handshake (carrying the optional token) and
// verifies the server's protocol version. Falls back to `ping` against legacy servers
// that don't implement `connect`.
func (c *Client) verifyProtocolVersion(ctx context.Context) error {
	if c.client == nil {
		return fmt.Errorf("client not connected")
	}
	maxVersion := GetSDKProtocolVersion()

	var serverVersion *int
	tokenPtr := (*string)(nil)
	if c.effectiveConnectionToken != "" {
		t := c.effectiveConnectionToken
		tokenPtr = &t
	}
	connectReq := &connectHandshakeRequest{Token: tokenPtr}
	// Opt in to GitHub telemetry forwarding at the connection level when a handler is
	// registered (mirrors the runtime, which reads this flag on the `connect` handshake
	// so the first session's un-replayable `session.start` event is forwarded). Also
	// sent on session.create/resume for older CLIs.
	if c.options.OnGitHubTelemetry != nil {
		connectReq.EnableGitHubTelemetryForwarding = Bool(true)
	}
	rawConnectResult, err := c.client.Request(ctx, "connect", connectReq)
	if err != nil {
		var rpcErr *jsonrpc2.Error
		if errors.As(err, &rpcErr) && (rpcErr.Code == jsonrpc2.ErrMethodNotFound.Code || rpcErr.Message == "Unhandled method connect") {
			// Legacy server without `connect`; fall back to `ping`. A token, if any,
			// is silently dropped — the legacy server can't enforce one.
			pingResult, perr := c.Ping(ctx, "")
			if perr != nil {
				return perr
			}
			serverVersion = pingResult.ProtocolVersion
		} else {
			return err
		}
	} else {
		var connectResult rpc.ConnectResult
		if err := json.Unmarshal(rawConnectResult, &connectResult); err != nil {
			return err
		}
		v := int(connectResult.ProtocolVersion)
		serverVersion = &v
	}

	if serverVersion == nil {
		return fmt.Errorf("SDK protocol version mismatch: SDK supports versions %d-%d, but server does not report a protocol version. Please update your server to ensure compatibility", minProtocolVersion, maxVersion)
	}

	if *serverVersion < minProtocolVersion || *serverVersion > maxVersion {
		return fmt.Errorf("SDK protocol version mismatch: SDK supports versions %d-%d, but server reports version %d. Please update your SDK or server to ensure compatibility", minProtocolVersion, maxVersion, *serverVersion)
	}

	c.negotiatedProtocolVersion = *serverVersion
	return nil
}

type connectHandshakeRequest struct {
	Token                           *string `json:"token,omitempty"`
	EnableGitHubTelemetryForwarding *bool   `json:"enableGitHubTelemetryForwarding,omitempty"`
}

// stderrBufferSize is the maximum number of bytes kept from the CLI process's
// stderr. Only the tail is retained so that memory stays bounded even when the
// process produces a large amount of diagnostic output.
const stderrBufferSize = 64 * 1024

// startCLIServer starts the CLI server process.
//
// This spawns the CLI server as a subprocess using the configured transport
// mode (stdio or TCP).
func (c *Client) startCLIServer(ctx context.Context) error {
	if c.useInProcess {
		return c.startInProcess(ctx)
	}

	cliPath := c.cliPath
	if cliPath == "" {
		// If no CLI path is provided, attempt to use the embedded CLI if available
		cliPath = embeddedcli.Path()
	}
	if cliPath == "" {
		// Default to "copilot" in PATH if no embedded CLI is available and no custom path is set
		cliPath = "copilot"
	}

	// Start with user-provided CLIArgs, then add SDK-managed args
	args := append([]string{}, c.cliArgs...)
	args = append(args, "--headless", "--no-auto-update")
	// Only pass --log-level when explicitly configured; otherwise let the
	// runtime use its own default.
	if c.options.LogLevel != "" {
		args = append(args, "--log-level", c.options.LogLevel)
	}

	// Choose transport mode
	if c.useStdio {
		args = append(args, "--stdio")
	} else if c.port > 0 {
		args = append(args, "--port", strconv.Itoa(c.port))
	}

	// Add auth-related flags
	if c.options.GitHubToken != "" {
		args = append(args, "--auth-token-env", "COPILOT_SDK_AUTH_TOKEN")
	}
	// Default useLoggedInUser to false when GitHubToken is provided
	useLoggedInUser := true
	if c.options.UseLoggedInUser != nil {
		useLoggedInUser = *c.options.UseLoggedInUser
	} else if c.options.GitHubToken != "" {
		useLoggedInUser = false
	}
	if !useLoggedInUser {
		args = append(args, "--no-auto-login")
	}

	if c.options.SessionIdleTimeoutSeconds > 0 {
		args = append(args, "--session-idle-timeout", strconv.Itoa(c.options.SessionIdleTimeoutSeconds))
	}

	if c.options.EnableRemoteSessions {
		args = append(args, "--remote")
	}

	// If CLIPath is a .js file, run it with node
	// Note we can't rely on the shebang as Windows doesn't support it
	command := cliPath
	if strings.HasSuffix(cliPath, ".js") {
		command = "node"
		args = append([]string{cliPath}, args...)
	}

	c.process = exec.Command(command, args...)

	// Configure platform-specific process attributes (e.g., hide window on Windows)
	configureProcAttr(c.process)

	// Set working directory if specified
	if c.options.WorkingDirectory != "" {
		c.process.Dir = c.options.WorkingDirectory
	}

	c.process.Env = append([]string{}, c.options.Env...)
	if c.options.GitHubToken != "" {
		c.process.Env = setEnvValue(c.process.Env, "COPILOT_SDK_AUTH_TOKEN", c.options.GitHubToken)
	}

	if c.effectiveConnectionToken != "" {
		c.process.Env = setEnvValue(c.process.Env, "COPILOT_CONNECTION_TOKEN", c.effectiveConnectionToken)
	}

	if c.options.BaseDirectory != "" {
		c.process.Env = setEnvValue(c.process.Env, "COPILOT_HOME", c.options.BaseDirectory)
	}

	if c.options.Mode == ModeEmpty {
		c.process.Env = setEnvValue(c.process.Env, "COPILOT_DISABLE_KEYTAR", "1")
	}

	if c.options.Telemetry != nil {
		t := c.options.Telemetry
		c.process.Env = setEnvValue(c.process.Env, "COPILOT_OTEL_ENABLED", "true")
		if t.OTLPEndpoint != "" {
			c.process.Env = setEnvValue(c.process.Env, "OTEL_EXPORTER_OTLP_ENDPOINT", t.OTLPEndpoint)
		}
		if t.OTLPProtocol != "" {
			c.process.Env = setEnvValue(c.process.Env, "OTEL_EXPORTER_OTLP_PROTOCOL", t.OTLPProtocol)
		}
		if t.FilePath != "" {
			c.process.Env = setEnvValue(c.process.Env, "COPILOT_OTEL_FILE_EXPORTER_PATH", t.FilePath)
		}
		if t.ExporterType != "" {
			c.process.Env = setEnvValue(c.process.Env, "COPILOT_OTEL_EXPORTER_TYPE", t.ExporterType)
		}
		if t.SourceName != "" {
			c.process.Env = setEnvValue(c.process.Env, "COPILOT_OTEL_SOURCE_NAME", t.SourceName)
		}
		if t.CaptureContent != nil {
			val := "false"
			if *t.CaptureContent {
				val = "true"
			}
			c.process.Env = setEnvValue(c.process.Env, "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT", val)
		}
	}

	if c.useStdio {
		// For stdio mode, we need stdin/stdout pipes
		stdin, err := c.process.StdinPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdin pipe: %w", err)
		}

		stdout, err := c.process.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		c.process.Stderr = truncbuffer.NewTruncBuffer(stderrBufferSize)

		if err := c.process.Start(); err != nil {
			return fmt.Errorf("failed to start CLI server: %w", err)
		}

		c.monitorProcess()

		// Create JSON-RPC client immediately
		c.client = jsonrpc2.NewClient(stdin, stdout)
		c.client.SetProcessDone(c.processDone, c.processErrorPtr)
		c.client.SetOnClose(func() {
			// Run in a goroutine to avoid deadlocking with Stop/ForceStop,
			// which hold startStopMux while waiting for readLoop to finish.
			go func() {
				c.startStopMux.Lock()
				defer c.startStopMux.Unlock()
				c.state = stateDisconnected
			}()
		})
		c.RPC = rpc.NewServerRPC(c.client)
		c.internalRPC = rpc.NewInternalServerRPC(c.client)
		c.setupNotificationHandler()
		c.client.Start()

		return nil
	} else {
		// For TCP mode, capture stdout to get port number
		stdout, err := c.process.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		c.process.Stderr = truncbuffer.NewTruncBuffer(stderrBufferSize)

		if err := c.process.Start(); err != nil {
			return fmt.Errorf("failed to start CLI server: %w", err)
		}

		c.monitorProcess()

		proc := c.process
		scanner := bufio.NewScanner(stdout)
		portRegex := regexp.MustCompile(`listening on port (\d+)`)

		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				killErr := c.killProcess()
				baseErr := fmt.Errorf("failed waiting for CLI server to start: %w", ctx.Err())
				if buf, ok := proc.Stderr.(*truncbuffer.TruncBuffer); ok {
					if stderr := strings.TrimSpace(buf.String()); stderr != "" {
						baseErr = fmt.Errorf("%w; stderr: %s", baseErr, stderr)
					}
				}
				return errors.Join(baseErr, killErr)
			case <-c.processDone:
				killErr := c.killProcess()
				baseErr := errors.New("CLI server process exited before reporting port")
				if buf, ok := proc.Stderr.(*truncbuffer.TruncBuffer); ok {
					if stderr := strings.TrimSpace(buf.String()); stderr != "" {
						baseErr = fmt.Errorf("%w; stderr: %s", baseErr, stderr)
					}
				}
				return errors.Join(baseErr, killErr)
			default:
				if scanner.Scan() {
					line := scanner.Text()
					if matches := portRegex.FindStringSubmatch(line); len(matches) > 1 {
						port, err := strconv.Atoi(matches[1])
						if err != nil {
							killErr := c.killProcess()
							return errors.Join(fmt.Errorf("failed to parse port: %w", err), killErr)
						}
						c.actualPort = port
						return nil
					}
				}
			}
		}
	}
}

// startInProcess loads the native runtime library and wires the JSON-RPC client
// to its FFI byte streams.
func (c *Client) startInProcess(ctx context.Context) error {
	if !inProcessAvailable {
		return errors.New("in-process transport unavailable: rebuild with -tags copilot_inprocess on a supported platform")
	}

	runtimePath := c.cliPath
	if runtimePath == "" {
		// The in-process transport does not resolve a bare command name from PATH
		// (unlike the child-process transport).
		if p := getEnvValue(c.options.Env, "COPILOT_CLI_PATH"); p != "" {
			runtimePath = p
		}
	}
	if runtimePath == "" {
		runtimePath = embeddedcli.Path()
	}
	if runtimePath == "" {
		return errors.New("in-process runtime unavailable: set COPILOT_CLI_PATH to a compatible runtime package or build with the bundled embedded runtime")
	}

	config := c.inProcessHostConfig()

	host, err := createInProcessHost(runtimePath, config)
	if err != nil {
		return err
	}
	// Own the host before the blocking handshake so a cancelled or failed start
	// leaves it disposable by Stop/ForceStop rather than leaking (host.Start runs
	// on its own goroutine and cannot be interrupted once the native call begins).
	c.ffiHost = host

	errCh := make(chan error, 1)
	go func() { errCh <- host.Start() }()
	select {
	case err := <-errCh:
		if err != nil {
			host.Dispose()
			c.ffiHost = nil
			return err
		}
	case <-ctx.Done():
		c.ffiHost = nil
		go func() {
			<-errCh
			host.Dispose()
		}()
		return ctx.Err()
	}

	c.client = jsonrpc2.NewClient(host.Writer(), host.Reader())
	c.client.SetOnClose(func() {
		// Run in a goroutine to avoid deadlocking with Stop/ForceStop, which hold
		// startStopMux while waiting for readLoop to finish.
		go func() {
			c.startStopMux.Lock()
			defer c.startStopMux.Unlock()
			c.state = stateDisconnected
		}()
	})
	c.RPC = rpc.NewServerRPC(c.client)
	c.internalRPC = rpc.NewInternalServerRPC(c.client)
	c.setupNotificationHandler()
	c.client.Start()
	return nil
}

func (c *Client) inProcessHostConfig() inProcessHostConfig {
	args := make([]string, 0, 8)
	if c.options.LogLevel != "" {
		args = append(args, "--log-level", c.options.LogLevel)
	}
	if c.options.GitHubToken != "" {
		args = append(args, "--auth-token-env", "COPILOT_SDK_AUTH_TOKEN")
	}
	useLoggedInUser := true
	if c.options.UseLoggedInUser != nil {
		useLoggedInUser = *c.options.UseLoggedInUser
	} else if c.options.GitHubToken != "" {
		useLoggedInUser = false
	}
	if !useLoggedInUser {
		args = append(args, "--no-auto-login")
	}
	if c.options.SessionIdleTimeoutSeconds > 0 {
		args = append(args, "--session-idle-timeout", strconv.Itoa(c.options.SessionIdleTimeoutSeconds))
	}
	if c.options.EnableRemoteSessions {
		args = append(args, "--remote")
	}

	environment := make(map[string]string)
	if c.options.GitHubToken != "" {
		environment["COPILOT_SDK_AUTH_TOKEN"] = c.options.GitHubToken
	}
	if c.options.BaseDirectory != "" {
		environment["COPILOT_HOME"] = c.options.BaseDirectory
	}
	if c.options.Mode == ModeEmpty {
		environment["COPILOT_DISABLE_KEYTAR"] = "1"
	}

	return inProcessHostConfig{
		Environment: environment,
		Args:        args,
	}
}

func (c *Client) killProcess() error {
	// Tear down the in-process FFI host on error paths that reuse killProcess to
	// abort a start (there is no OS process to kill in that mode).
	if c.ffiHost != nil {
		c.ffiHost.Dispose()
		c.ffiHost = nil
	}
	if p := c.osProcess.Swap(nil); p != nil {
		if err := p.Kill(); err != nil {
			return fmt.Errorf("failed to kill CLI process: %w", err)
		}
	}
	c.process = nil
	return nil
}

func (c *Client) killProcessAndWait() error {
	done := c.processDone
	killErr := c.killProcess()
	if done == nil {
		return killErr
	}

	select {
	case <-done:
		return killErr
	case <-time.After(processExitTimeout):
		return errors.Join(killErr, fmt.Errorf("timed out waiting for CLI process to exit after kill"))
	}
}

// monitorProcess signals when the CLI process exits and captures any exit error.
// processError is intentionally a local: each process lifecycle gets its own
// error value, so goroutines from previous processes can't overwrite the
// current one. Closing the channel synchronizes with readers, guaranteeing
// they see the final processError value.
func (c *Client) monitorProcess() {
	done := make(chan struct{})
	c.processDone = done
	proc := c.process
	c.osProcess.Store(proc.Process)
	var processError error
	c.processErrorPtr = &processError
	go func() {
		waitErr := proc.Wait()
		var stderrOutput string
		if buf, ok := proc.Stderr.(*truncbuffer.TruncBuffer); ok {
			stderrOutput = strings.TrimSpace(buf.String())
		}
		if waitErr != nil {
			if stderrOutput != "" {
				processError = fmt.Errorf("CLI process exited: %w\nstderr: %s", waitErr, stderrOutput)
			} else {
				processError = fmt.Errorf("CLI process exited: %w", waitErr)
			}
		} else {
			if stderrOutput != "" {
				processError = fmt.Errorf("CLI process exited unexpectedly\nstderr: %s", stderrOutput)
			} else {
				processError = errors.New("CLI process exited unexpectedly")
			}
		}
		close(done)
	}()
}

// connectToServer establishes a connection to the server.
func (c *Client) connectToServer(ctx context.Context) error {
	if c.useStdio || c.useInProcess {
		// Already connected: stdio in startCLIServer, FFI streams in startInProcess.
		return nil
	}

	// Connect via TCP
	return c.connectViaTCP(ctx)
}

// connectViaTCP connects to the CLI server via TCP socket.
func (c *Client) connectViaTCP(ctx context.Context) error {
	if c.actualPort == 0 {
		return fmt.Errorf("server port not available")
	}

	// Merge a 10-second timeout with the caller's context so whichever
	// deadline comes first wins.
	address := net.JoinHostPort(c.actualHost, fmt.Sprintf("%d", c.actualPort))
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(dialCtx, "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to connect to CLI server at %s: %w", address, err)
	}

	c.conn = conn

	// Create JSON-RPC client with the connection
	c.client = jsonrpc2.NewClient(conn, conn)
	if c.processDone != nil {
		c.client.SetProcessDone(c.processDone, c.processErrorPtr)
	}
	c.client.SetOnClose(func() {
		go func() {
			c.startStopMux.Lock()
			defer c.startStopMux.Unlock()
			c.state = stateDisconnected
		}()
	})
	c.RPC = rpc.NewServerRPC(c.client)
	c.internalRPC = rpc.NewInternalServerRPC(c.client)
	c.setupNotificationHandler()
	c.client.Start()

	return nil
}

// setupNotificationHandler configures handlers for session events and RPC requests.
func (c *Client) setupNotificationHandler() {
	c.client.SetRequestHandler("session.event", jsonrpc2.NotificationHandlerFor(c.handleSessionEvent))
	c.client.SetRequestHandler("session.lifecycle", jsonrpc2.NotificationHandlerFor(c.handleLifecycleEvent))
	c.client.SetRequestHandler("userInput.request", jsonrpc2.RequestHandlerFor(c.handleUserInputRequest))
	c.client.SetRequestHandler("exitPlanMode.request", jsonrpc2.RequestHandlerFor(c.handleExitPlanModeRequest))
	c.client.SetRequestHandler("autoModeSwitch.request", jsonrpc2.RequestHandlerFor(c.handleAutoModeSwitchRequest))
	c.client.SetRequestHandler("systemMessage.transform", jsonrpc2.RequestHandlerFor(c.handleSystemMessageTransform))
	rpc.RegisterClientSessionAPIHandlers(c.client, func(sessionID string) *rpc.ClientSessionAPIHandlers {
		c.sessionsMux.Lock()
		defer c.sessionsMux.Unlock()
		session := c.sessions[sessionID]
		if session == nil {
			return nil
		}
		return session.clientSessionAPIs
	})
	// hooks.invoke is a client-global RPC method: one connection-level handler
	// receives every hook callback and routes to the owning session via the
	// payload's sessionId. Always register the global handlers so the generated
	// hooks.invoke handler is wired to our dispatcher.
	handlers := &rpc.ClientGlobalAPIHandlers{
		Hooks: &hooksAdapter{client: c},
	}
	if c.options.RequestHandler != nil {
		handlers.LlmInference = newCopilotRequestAdapter(c.options.RequestHandler, func() *rpc.ServerLlmInferenceAPI {
			if c.RPC == nil {
				return nil
			}
			return c.RPC.LlmInference
		})
	}
	if c.options.OnGitHubTelemetry != nil {
		handlers.GitHubTelemetry = &gitHubTelemetryAdapter{callback: c.options.OnGitHubTelemetry}
	}
	rpc.RegisterClientGlobalAPIHandlers(c.client, handlers)
}

// gitHubTelemetryAdapter adapts the OnGitHubTelemetry option to the generated
// rpc.GitHubTelemetryHandler interface.
type gitHubTelemetryAdapter struct {
	callback func(notification *rpc.GitHubTelemetryNotification)
}

func (a *gitHubTelemetryAdapter) Event(request *rpc.GitHubTelemetryNotification) error {
	defer func() { recover() }() // Ignore handler panics
	a.callback(request)
	return nil
}

func (c *Client) handleSessionEvent(req sessionEventRequest) {
	if req.SessionID == "" {
		return
	}
	// Dispatch to session
	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()

	if ok {
		session.dispatchEvent(req.Event)
	}
}

// handleUserInputRequest handles a user input request from the CLI server.
func (c *Client) handleUserInputRequest(req userInputRequest) (*userInputResponse, *jsonrpc2.Error) {
	if req.SessionID == "" || req.Question == "" {
		return nil, &jsonrpc2.Error{Code: -32602, Message: "invalid user input request payload"}
	}

	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()
	if !ok {
		return nil, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("unknown session %s", req.SessionID)}
	}

	response, err := session.handleUserInputRequest(UserInputRequest{
		Question:      req.Question,
		Choices:       req.Choices,
		AllowFreeform: req.AllowFreeform,
	})
	if err != nil {
		return nil, &jsonrpc2.Error{Code: -32603, Message: err.Error()}
	}

	return &userInputResponse{Answer: response.Answer, WasFreeform: response.WasFreeform}, nil
}

// handleExitPlanModeRequest handles an exitPlanMode.request callback from the CLI server.
func (c *Client) handleExitPlanModeRequest(req exitPlanModeRequest) (*ExitPlanModeResult, *jsonrpc2.Error) {
	if req.SessionID == "" {
		return nil, &jsonrpc2.Error{Code: -32602, Message: "invalid exit plan mode request payload"}
	}
	recommendedAction := req.RecommendedAction
	if recommendedAction == "" {
		recommendedAction = "autopilot"
	}

	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()
	if !ok {
		return nil, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("unknown session %s", req.SessionID)}
	}

	response, err := session.handleExitPlanModeRequest(ExitPlanModeRequest{
		Summary:           req.Summary,
		PlanContent:       req.PlanContent,
		Actions:           req.Actions,
		RecommendedAction: recommendedAction,
	})
	if err != nil {
		return nil, &jsonrpc2.Error{Code: -32603, Message: err.Error()}
	}

	return &response, nil
}

// handleAutoModeSwitchRequest handles an autoModeSwitch.request callback from the CLI server.
func (c *Client) handleAutoModeSwitchRequest(req autoModeSwitchRequest) (*autoModeSwitchResponse, *jsonrpc2.Error) {
	if req.SessionID == "" {
		return nil, &jsonrpc2.Error{Code: -32602, Message: "invalid auto mode switch request payload"}
	}

	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()
	if !ok {
		return nil, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("unknown session %s", req.SessionID)}
	}

	response, err := session.handleAutoModeSwitchRequest(AutoModeSwitchRequest{
		ErrorCode:         req.ErrorCode,
		RetryAfterSeconds: req.RetryAfterSeconds,
	})
	if err != nil {
		return nil, &jsonrpc2.Error{Code: -32603, Message: err.Error()}
	}

	return &autoModeSwitchResponse{Response: response}, nil
}

// handleHooksInvoke routes a hook callback to its owning session, keyed by the
// payload's sessionId.
func (c *Client) handleHooksInvoke(req hooksInvokeRequest) (map[string]any, *jsonrpc2.Error) {
	if req.SessionID == "" || req.Type == "" {
		return nil, &jsonrpc2.Error{Code: -32602, Message: "invalid hooks invoke payload"}
	}

	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()
	if !ok {
		return nil, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("unknown session %s", req.SessionID)}
	}

	output, err := session.handleHooksInvoke(req.Type, req.Input)
	if err != nil {
		return nil, &jsonrpc2.Error{Code: -32603, Message: err.Error()}
	}

	result := make(map[string]any)
	if output != nil {
		result["output"] = output
	}
	return result, nil
}

// hooksAdapter implements the generated rpc.HooksHandler, delegating to the
// client's per-session hook dispatcher.
type hooksAdapter struct {
	client *Client
}

func (a *hooksAdapter) Invoke(request *rpc.HookInvokeRequest) (*rpc.HookInvokeResponse, error) {
	rawInput, err := json.Marshal(request.Input)
	if err != nil {
		return nil, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("invalid hooks invoke payload: %v", err)}
	}

	result, rpcErr := a.client.handleHooksInvoke(hooksInvokeRequest{
		SessionID: request.SessionID,
		Type:      string(request.HookType),
		Input:     rawInput,
	})
	if rpcErr != nil {
		return nil, rpcErr
	}

	response := &rpc.HookInvokeResponse{}
	if result != nil {
		response.Output = result["output"]
	}
	return response, nil
}

// handleSystemMessageTransform handles a system message transform request from the CLI server.
func (c *Client) handleSystemMessageTransform(req systemMessageTransformRequest) (systemMessageTransformResponse, *jsonrpc2.Error) {
	if req.SessionID == "" {
		return systemMessageTransformResponse{}, &jsonrpc2.Error{Code: -32602, Message: "invalid system message transform payload"}
	}

	c.sessionsMux.Lock()
	session, ok := c.sessions[req.SessionID]
	c.sessionsMux.Unlock()
	if !ok {
		return systemMessageTransformResponse{}, &jsonrpc2.Error{Code: -32602, Message: fmt.Sprintf("unknown session %s", req.SessionID)}
	}

	resp, err := session.handleSystemMessageTransform(req.Sections)
	if err != nil {
		return systemMessageTransformResponse{}, &jsonrpc2.Error{Code: -32603, Message: err.Error()}
	}
	return resp, nil
}
