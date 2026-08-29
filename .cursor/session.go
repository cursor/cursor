// Package copilot provides a Go SDK for interacting with the GitHub Copilot CLI.
package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
	"github.com/github/copilot-sdk/go/rpc"
)

// toolSearchToolName is the fixed name of the runtime's built-in tool-search
// tool. A client can replace its behavior by registering a [Tool] with this
// exact name and OverridesBuiltInTool set to true.
const toolSearchToolName = "tool_search_tool"

type sessionHandler struct {
	id uint64
	fn SessionEventHandler
}

// Session represents a single conversation session with the Copilot CLI.
//
// A session maintains conversation state, handles events, and manages tool execution.
// Sessions are created via [Client.CreateSession] or resumed via [Client.ResumeSession].
//
// The session provides methods to send messages, subscribe to events, retrieve
// conversation history, and manage the session lifecycle. All methods are safe
// for concurrent use.
//
// Example usage:
//
//	session, err := client.CreateSession(copilot.SessionConfig{
//	    Model: "gpt-4",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer session.Disconnect()
//
//	// Subscribe to events
//	unsubscribe := session.On(func(event copilot.SessionEvent) {
//	    if d, ok := event.Data.(*copilot.AssistantMessageData); ok {
//	        fmt.Println("Assistant:", d.Content)
//	    }
//	})
//	defer unsubscribe()
//
//	// Send a message
//	messageID, err := session.Send(copilot.MessageOptions{
//	    Prompt: "Hello, world!",
//	})
type Session struct {
	// SessionID is the unique identifier for this session.
	SessionID             string
	workspacePath         string
	client                *jsonrpc2.Client
	clientSessionAPIs     *rpc.ClientSessionAPIHandlers
	handlers              []sessionHandler
	nextHandlerID         uint64
	handlerMutex          sync.RWMutex
	toolHandlers          map[string]ToolHandler
	toolHandlersM         sync.RWMutex
	permissionHandler     PermissionHandlerFunc
	permissionMux         sync.RWMutex
	managedSettings       bool
	mcpAuthHandler        MCPAuthHandler
	mcpAuthMu             sync.RWMutex
	userInputHandler      UserInputHandler
	userInputMux          sync.RWMutex
	exitPlanModeHandler   ExitPlanModeRequestHandler
	exitPlanModeMu        sync.RWMutex
	autoModeSwitchHandler AutoModeSwitchRequestHandler
	autoModeSwitchMu      sync.RWMutex
	hooks                 *SessionHooks
	hooksMux              sync.RWMutex
	transformCallbacks    map[string]SectionTransformFn
	transformMu           sync.Mutex
	commandHandlers       map[string]CommandHandler
	commandHandlersMu     sync.RWMutex
	elicitationHandler    ElicitationHandler
	elicitationMu         sync.RWMutex
	canvasHandler         CanvasHandler
	canvasMu              sync.RWMutex
	bearerTokenProviders  map[string]BearerTokenProvider
	bearerTokenMu         sync.RWMutex
	openCanvases          []rpc.OpenCanvasInstance
	openCanvasesMu        sync.RWMutex
	capabilities          SessionCapabilities
	capabilitiesMu        sync.RWMutex

	// eventCh serializes user event handler dispatch. dispatchEvent enqueues;
	// a single goroutine (processEvents) dequeues and invokes handlers in FIFO order.
	eventCh   chan SessionEvent
	closeOnce sync.Once // guards eventCh close so Disconnect is safe to call more than once

	// RPC provides typed session-scoped RPC methods.
	RPC *rpc.SessionRPC
}

// WorkspacePath returns the path to the session workspace directory when infinite
// sessions are enabled. Contains checkpoints/, plan.md, and files/ subdirectories.
// Returns empty string if infinite sessions are disabled.
func (s *Session) WorkspacePath() string {
	return s.workspacePath
}

// OpenCanvases returns the open-canvas snapshot last reported by the runtime.
// The snapshot is populated from session.resume and live session.canvas.opened
// and session.canvas.closed events. The returned slice is a copy and is safe to
// mutate by the caller.
func (s *Session) OpenCanvases() []rpc.OpenCanvasInstance {
	s.openCanvasesMu.RLock()
	defer s.openCanvasesMu.RUnlock()
	if len(s.openCanvases) == 0 {
		return nil
	}
	out := make([]rpc.OpenCanvasInstance, len(s.openCanvases))
	copy(out, s.openCanvases)
	return out
}

func (s *Session) setOpenCanvases(canvases []rpc.OpenCanvasInstance) {
	s.openCanvasesMu.Lock()
	defer s.openCanvasesMu.Unlock()
	s.openCanvases = canvases
}

func (s *Session) upsertOpenCanvas(canvas rpc.OpenCanvasInstance) {
	s.openCanvasesMu.Lock()
	defer s.openCanvasesMu.Unlock()
	for i := range s.openCanvases {
		if s.openCanvases[i].InstanceID == canvas.InstanceID {
			s.openCanvases[i] = canvas
			return
		}
	}
	s.openCanvases = append(s.openCanvases, canvas)
}

func (s *Session) removeOpenCanvas(instanceID string) {
	s.openCanvasesMu.Lock()
	defer s.openCanvasesMu.Unlock()
	filtered := make([]rpc.OpenCanvasInstance, 0, len(s.openCanvases))
	for _, canvas := range s.openCanvases {
		if canvas.InstanceID != instanceID {
			filtered = append(filtered, canvas)
		}
	}
	s.openCanvases = filtered
}

func (s *Session) updateOpenCanvasesFromEvent(event SessionEvent) {
	switch data := event.Data.(type) {
	case *SessionCanvasOpenedData:
		if data.InstanceID == "" || data.CanvasID == "" || data.ExtensionID == "" {
			fmt.Printf("failed to deserialize session.canvas.opened payload\n")
			return
		}
		s.upsertOpenCanvas(rpc.OpenCanvasInstance{
			CanvasID:      data.CanvasID,
			ExtensionID:   data.ExtensionID,
			ExtensionName: data.ExtensionName,
			Input:         data.Input,
			InstanceID:    data.InstanceID,
			Status:        data.Status,
			Title:         data.Title,
			Icon:          data.Icon,
			URL:           data.URL,
		})
	case *SessionCanvasClosedData:
		if data.InstanceID == "" {
			fmt.Printf("failed to deserialize session.canvas.closed payload\n")
			return
		}
		s.removeOpenCanvas(data.InstanceID)
	}
}

func (s *Session) registerCanvasHandler(handler CanvasHandler) {
	s.canvasMu.Lock()
	defer s.canvasMu.Unlock()
	s.canvasHandler = handler
}

func (s *Session) getCanvasHandler() CanvasHandler {
	s.canvasMu.RLock()
	defer s.canvasMu.RUnlock()
	return s.canvasHandler
}

// registerBearerTokenProviders installs per-provider [BearerTokenProvider] callbacks
// for BYOK providers configured with managed-identity / on-demand bearer-token
// auth, keyed by provider name.
//
// The runtime never receives the callback itself; the SDK strips it from the
// provider config and instead sends `hasBearerTokenProvider: true`. When the
// runtime needs a token it issues a session-scoped `providerToken.getToken`
// request, which the session's provider-token adapter routes to the matching
// per-provider callback.
func (s *Session) registerBearerTokenProviders(providers map[string]BearerTokenProvider) {
	s.bearerTokenMu.Lock()
	defer s.bearerTokenMu.Unlock()
	s.bearerTokenProviders = make(map[string]BearerTokenProvider, len(providers))
	for name, callback := range providers {
		if callback == nil {
			continue
		}
		s.bearerTokenProviders[name] = callback
	}
}

func (s *Session) getBearerTokenProvider(providerName string) BearerTokenProvider {
	s.bearerTokenMu.RLock()
	defer s.bearerTokenMu.RUnlock()
	return s.bearerTokenProviders[providerName]
}

type providerTokenClientSessionAdapter struct {
	session *Session
}

func newProviderTokenClientSessionAdapter(session *Session) rpc.ProviderTokenHandler {
	return &providerTokenClientSessionAdapter{session: session}
}

func (a *providerTokenClientSessionAdapter) GetToken(request *rpc.ProviderTokenAcquireRequest) (*rpc.ProviderTokenAcquireResult, error) {
	if request == nil {
		return nil, providerTokenJSONRPCError("missing provider token request")
	}
	if a.session == nil || a.session.SessionID != request.SessionID {
		return nil, providerTokenJSONRPCError(fmt.Sprintf("unknown session %s", request.SessionID))
	}
	callback := a.session.getBearerTokenProvider(request.ProviderName)
	if callback == nil {
		return nil, providerTokenJSONRPCError(fmt.Sprintf("No bearer-token provider registered for provider %q", request.ProviderName))
	}
	token, err := callback(ProviderTokenArgs{ProviderName: request.ProviderName, SessionID: request.SessionID})
	if err != nil {
		return nil, providerTokenJSONRPCError(err.Error())
	}
	return &rpc.ProviderTokenAcquireResult{Token: token}, nil
}

func providerTokenJSONRPCError(message string) *jsonrpc2.Error {
	return &jsonrpc2.Error{
		Code:    -32603,
		Message: message,
	}
}

type canvasClientSessionAdapter struct {
	session *Session
}

func newCanvasClientSessionAdapter(session *Session) rpc.CanvasHandler {
	return &canvasClientSessionAdapter{session: session}
}

func (a *canvasClientSessionAdapter) Close(request *rpc.CanvasProviderCloseRequest) (*rpc.CanvasCloseResult, error) {
	if request == nil {
		return nil, canvasJSONRPCError(NewCanvasError("canvas_handler_unset", "missing canvas close request"))
	}
	handler, err := a.resolveHandler(canvasProviderSessionID(request))
	if err != nil {
		return nil, err
	}
	if err := handler.OnClose(context.Background(), *request); err != nil {
		return nil, canvasResultError(err)
	}
	return nil, nil
}

func (a *canvasClientSessionAdapter) Invoke(request *rpc.CanvasProviderInvokeActionRequest) (any, error) {
	if request == nil {
		return nil, canvasJSONRPCError(NewCanvasError("canvas_handler_unset", "missing canvas action request"))
	}
	handler, err := a.resolveHandler(canvasProviderSessionID(request))
	if err != nil {
		return nil, err
	}
	result, actionErr := handler.OnAction(context.Background(), *request)
	if actionErr != nil {
		return nil, canvasResultError(actionErr)
	}
	return result, nil
}

func (a *canvasClientSessionAdapter) Open(request *rpc.CanvasProviderOpenRequest) (*rpc.CanvasProviderOpenResult, error) {
	if request == nil {
		return nil, canvasJSONRPCError(NewCanvasError("canvas_handler_unset", "missing canvas open request"))
	}
	handler, err := a.resolveHandler(canvasProviderSessionID(request))
	if err != nil {
		return nil, err
	}
	result, openErr := handler.OnOpen(context.Background(), *request)
	if openErr != nil {
		return nil, canvasResultError(openErr)
	}
	return &result, nil
}

func (a *canvasClientSessionAdapter) resolveHandler(sessionID string) (CanvasHandler, error) {
	if sessionID == "" {
		return nil, canvasJSONRPCError(NewCanvasError("canvas_handler_unset", "missing session ID"))
	}
	if a.session == nil || a.session.SessionID != sessionID {
		return nil, canvasJSONRPCError(NewCanvasError("canvas_handler_unset", fmt.Sprintf("unknown session %s", sessionID)))
	}
	handler := a.session.getCanvasHandler()
	if handler == nil {
		return nil, canvasJSONRPCError(NewCanvasError(
			"canvas_handler_unset",
			"No CanvasHandler installed on this session; install one via SessionConfig.CanvasHandler before creating the session.",
		))
	}
	return handler, nil
}

func canvasProviderSessionID(request any) string {
	switch req := request.(type) {
	case *rpc.CanvasProviderCloseRequest:
		if req != nil {
			return req.SessionID
		}
	case *rpc.CanvasProviderInvokeActionRequest:
		if req != nil {
			return req.SessionID
		}
	case *rpc.CanvasProviderOpenRequest:
		if req != nil {
			return req.SessionID
		}
	}
	return ""
}

func canvasJSONRPCError(cerr *CanvasError) *jsonrpc2.Error {
	data, _ := json.Marshal(map[string]string{
		"code":    cerr.Code,
		"message": cerr.Message,
	})
	return &jsonrpc2.Error{
		Code:    -32603,
		Message: cerr.Message,
		Data:    data,
	}
}

func canvasResultError(err error) error {
	if err == nil {
		return nil
	}
	if rpcErr, ok := err.(*jsonrpc2.Error); ok {
		return rpcErr
	}
	if cerr, ok := err.(*CanvasError); ok {
		return canvasJSONRPCError(cerr)
	}
	return canvasJSONRPCError(NewCanvasError("canvas_handler_error", err.Error()))
}

// newSession creates a new session wrapper with the given session ID and client.
func newSession(
	sessionID string,
	client *jsonrpc2.Client,
	workspacePath string,
	managedSettings bool,
) *Session {
	s := &Session{
		SessionID:         sessionID,
		workspacePath:     workspacePath,
		managedSettings:   managedSettings,
		client:            client,
		clientSessionAPIs: &rpc.ClientSessionAPIHandlers{},
		handlers:          make([]sessionHandler, 0),
		toolHandlers:      make(map[string]ToolHandler),
		commandHandlers:   make(map[string]CommandHandler),
		eventCh:           make(chan SessionEvent, 128),
		RPC:               rpc.NewSessionRPC(client, sessionID),
	}
	s.clientSessionAPIs.Canvas = newCanvasClientSessionAdapter(s)
	s.clientSessionAPIs.ProviderToken = newProviderTokenClientSessionAdapter(s)
	go s.processEvents()
	return s
}

// Send sends a message to this session and waits for the response.
//
// The message is processed asynchronously. Subscribe to events via [Session.On]
// to receive streaming responses and other session events.
//
// Parameters:
//   - options: The message options including the prompt and optional attachments.
//
// Returns the message ID of the response, which can be used to correlate events,
// or an error if the session has been disconnected or the connection fails.
//
// Example:
//
//	messageID, err := session.Send(context.Background(), copilot.MessageOptions{
//	    Prompt: "Explain this code",
//	    Attachments: []copilot.Attachment{
//	        &copilot.AttachmentFile{DisplayName: "main.go", Path: "./main.go"},
//	    },
//	})
//	if err != nil {
//	    log.Printf("Failed to send message: %v", err)
//	}
func (s *Session) Send(ctx context.Context, options MessageOptions) (string, error) {
	traceparent, tracestate := getTraceContext(ctx)
	req := sessionSendRequest{
		SessionID:      s.SessionID,
		Prompt:         options.Prompt,
		DisplayPrompt:  options.DisplayPrompt,
		Attachments:    options.Attachments,
		Mode:           options.Mode,
		AgentMode:      options.AgentMode,
		Traceparent:    traceparent,
		Tracestate:     tracestate,
		RequestHeaders: options.RequestHeaders,
	}

	result, err := s.client.Request(ctx, "session.send", req)
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	var response sessionSendResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return "", fmt.Errorf("failed to unmarshal send response: %w", err)
	}
	return response.MessageID, nil
}

// SendPrompt is a convenience wrapper for [Session.Send] that takes a plain
// prompt string instead of a [MessageOptions] struct. Equivalent to:
//
//	session.Send(ctx, copilot.MessageOptions{Prompt: prompt})
func (s *Session) SendPrompt(ctx context.Context, prompt string) (string, error) {
	return s.Send(ctx, MessageOptions{Prompt: prompt})
}

// SendAndWait sends a message to this session and waits until the session becomes idle.
//
// This is a convenience method that combines [Session.Send] with waiting for
// the session.idle event. Use this when you want to block until the assistant
// has finished processing the message.
//
// Events are still delivered to handlers registered via [Session.On] while waiting.
//
// Parameters:
//   - options: The message options including the prompt and optional attachments.
//   - timeout: How long to wait for completion. Defaults to 60 seconds if zero.
//     Controls how long to wait; does not abort in-flight agent work.
//
// Returns the final assistant message event, or nil if none was received.
// Returns an error if the timeout is reached or the connection fails.
//
// Example:
//
//	response, err := session.SendAndWait(context.Background(), copilot.MessageOptions{
//	    Prompt: "What is 2+2?",
//	}) // Use default 60s timeout
//	if err != nil {
//	    log.Printf("Failed: %v", err)
//	}
//	if response != nil {
//	    if d, ok := response.Data.(*AssistantMessageData); ok {
//	        fmt.Println(d.Content)
//	    }
//	}
func (s *Session) SendAndWait(ctx context.Context, options MessageOptions) (*SessionEvent, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}

	idleCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	var lastAssistantMessage *SessionEvent
	var mu sync.Mutex

	unsubscribe := s.On(func(event SessionEvent) {
		switch d := event.Data.(type) {
		case *AssistantMessageData:
			mu.Lock()
			eventCopy := event
			lastAssistantMessage = &eventCopy
			mu.Unlock()
		case *SessionIdleData:
			select {
			case idleCh <- struct{}{}:
			default:
			}
		case *SessionErrorData:
			select {
			case errCh <- fmt.Errorf("session error: %s", d.Message):
			default:
			}
		}
	})
	defer unsubscribe()

	_, err := s.Send(ctx, options)
	if err != nil {
		return nil, err
	}

	select {
	case <-idleCh:
		mu.Lock()
		result := lastAssistantMessage
		mu.Unlock()
		return result, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for session.idle: %w", ctx.Err())
	}
}

// SendPromptAndWait is a convenience wrapper for [Session.SendAndWait] that
// takes a plain prompt string instead of a [MessageOptions] struct. Equivalent
// to:
//
//	session.SendAndWait(ctx, copilot.MessageOptions{Prompt: prompt})
func (s *Session) SendPromptAndWait(ctx context.Context, prompt string) (*SessionEvent, error) {
	return s.SendAndWait(ctx, MessageOptions{Prompt: prompt})
}

// On subscribes to events from this session.
//
// Events include assistant messages, tool executions, errors, and session state
// changes. Multiple handlers can be registered and will all receive events.
// Handlers are called synchronously in the order they were registered.
//
// The returned function can be called to unsubscribe the handler. It is safe
// to call the unsubscribe function multiple times.
//
// Example:
//
//	unsubscribe := session.On(func(event copilot.SessionEvent) {
//	    switch d := event.Data.(type) {
//	    case *copilot.AssistantMessageData:
//	        fmt.Println("Assistant:", d.Content)
//	    case *copilot.SessionErrorData:
//	        fmt.Println("Error:", d.Message)
//	    }
//	})
//
//	// Later, to stop receiving events:
//	unsubscribe()
func (s *Session) On(handler SessionEventHandler) func() {
	s.handlerMutex.Lock()
	defer s.handlerMutex.Unlock()

	id := s.nextHandlerID
	s.nextHandlerID++
	s.handlers = append(s.handlers, sessionHandler{id: id, fn: handler})

	// Return unsubscribe function
	return func() {
		s.handlerMutex.Lock()
		defer s.handlerMutex.Unlock()

		for i, h := range s.handlers {
			if h.id == id {
				s.handlers = append(s.handlers[:i], s.handlers[i+1:]...)
				break
			}
		}
	}
}

// registerTools registers tool handlers for this session.
//
// Tools with handlers allow the assistant to execute custom functions automatically.
// Declaration-only tools are surfaced as events and left pending for the consumer.
//
// This method is internal and typically called when creating a session with tools.
func (s *Session) registerTools(tools []Tool) {
	s.toolHandlersM.Lock()
	defer s.toolHandlersM.Unlock()

	s.toolHandlers = make(map[string]ToolHandler)
	for _, tool := range tools {
		if tool.Name == "" || tool.Handler == nil {
			continue
		}
		s.toolHandlers[tool.Name] = tool.Handler
	}
}

// getToolHandler retrieves a registered tool handler by name.
// Returns the handler and true if found, or nil and false if not registered.
func (s *Session) getToolHandler(name string) (ToolHandler, bool) {
	s.toolHandlersM.RLock()
	handler, ok := s.toolHandlers[name]
	s.toolHandlersM.RUnlock()
	return handler, ok
}

// registerPermissionHandler registers a permission handler for this session.
//
// When the assistant needs permission to perform certain actions (e.g., file
// operations), this handler is called to approve or deny the request.
//
// This method is internal and typically called when creating a session.
func (s *Session) registerPermissionHandler(handler PermissionHandlerFunc) {
	s.permissionMux.Lock()
	defer s.permissionMux.Unlock()
	s.permissionHandler = handler
}

// getPermissionHandler returns the currently registered permission handler, or nil.
func (s *Session) getPermissionHandler() PermissionHandlerFunc {
	s.permissionMux.RLock()
	defer s.permissionMux.RUnlock()
	return s.permissionHandler
}

// registerUserInputHandler registers a user input handler for this session.
//
// When the assistant needs to ask the user a question (e.g., via ask_user tool),
// this handler is called to get the user's response.
//
// This method is internal and typically called when creating a session.
func (s *Session) registerUserInputHandler(handler UserInputHandler) {
	s.userInputMux.Lock()
	defer s.userInputMux.Unlock()
	s.userInputHandler = handler
}

// getUserInputHandler returns the currently registered user input handler, or nil.
func (s *Session) getUserInputHandler() UserInputHandler {
	s.userInputMux.RLock()
	defer s.userInputMux.RUnlock()
	return s.userInputHandler
}

// handleUserInputRequest handles a user input request from the Copilot CLI.
// This is an internal method called by the SDK when the CLI requests user input.
func (s *Session) handleUserInputRequest(request UserInputRequest) (UserInputResponse, error) {
	handler := s.getUserInputHandler()

	if handler == nil {
		return UserInputResponse{}, fmt.Errorf("no user input handler registered")
	}

	invocation := UserInputInvocation{
		SessionID: s.SessionID,
	}

	return handler(request, invocation)
}

func (s *Session) registerExitPlanModeHandler(handler ExitPlanModeRequestHandler) {
	s.exitPlanModeMu.Lock()
	defer s.exitPlanModeMu.Unlock()
	s.exitPlanModeHandler = handler
}

func (s *Session) getExitPlanModeHandler() ExitPlanModeRequestHandler {
	s.exitPlanModeMu.RLock()
	defer s.exitPlanModeMu.RUnlock()
	return s.exitPlanModeHandler
}

func (s *Session) handleExitPlanModeRequest(request ExitPlanModeRequest) (ExitPlanModeResult, error) {
	handler := s.getExitPlanModeHandler()
	if handler == nil {
		return ExitPlanModeResult{Approved: true}, nil
	}

	return handler(request, ExitPlanModeInvocation{SessionID: s.SessionID})
}

func (s *Session) registerAutoModeSwitchHandler(handler AutoModeSwitchRequestHandler) {
	s.autoModeSwitchMu.Lock()
	defer s.autoModeSwitchMu.Unlock()
	s.autoModeSwitchHandler = handler
}

func (s *Session) getAutoModeSwitchHandler() AutoModeSwitchRequestHandler {
	s.autoModeSwitchMu.RLock()
	defer s.autoModeSwitchMu.RUnlock()
	return s.autoModeSwitchHandler
}

func (s *Session) handleAutoModeSwitchRequest(request AutoModeSwitchRequest) (AutoModeSwitchResponse, error) {
	handler := s.getAutoModeSwitchHandler()
	if handler == nil {
		return AutoModeSwitchResponseNo, nil
	}

	return handler(request, AutoModeSwitchInvocation{SessionID: s.SessionID})
}

// registerHooks registers hook handlers for this session.
//
// Hooks are called at various points during session execution to allow
// customization and observation of the session lifecycle.
//
// This method is internal and typically called when creating a session.
func (s *Session) registerHooks(hooks *SessionHooks) {
	s.hooksMux.Lock()
	defer s.hooksMux.Unlock()
	s.hooks = hooks
}

// getHooks returns the currently registered hooks, or nil.
func (s *Session) getHooks() *SessionHooks {
	s.hooksMux.RLock()
	defer s.hooksMux.RUnlock()
	return s.hooks
}

// handleHooksInvoke handles a hook invocation from the Copilot CLI.
// This is an internal method called by the SDK when the CLI invokes a hook.
func (s *Session) handleHooksInvoke(hookType string, rawInput json.RawMessage) (any, error) {
	hooks := s.getHooks()

	if hooks == nil {
		return nil, nil
	}

	invocation := HookInvocation{
		SessionID: s.SessionID,
	}

	switch hookType {
	case "preToolUse":
		if hooks.OnPreToolUse == nil {
			return nil, nil
		}
		var input PreToolUseHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnPreToolUse(input, invocation)

	case "preMcpToolCall":
		if hooks.OnPreMCPToolCall == nil {
			return nil, nil
		}
		var input PreMCPToolCallHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnPreMCPToolCall(input, invocation)

	case "postToolUse":
		if hooks.OnPostToolUse == nil {
			return nil, nil
		}
		var input PostToolUseHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnPostToolUse(input, invocation)

	case "postToolUseFailure":
		if hooks.OnPostToolUseFailure == nil {
			return nil, nil
		}
		var input PostToolUseFailureHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnPostToolUseFailure(input, invocation)

	case "userPromptSubmitted":
		if hooks.OnUserPromptSubmitted == nil {
			return nil, nil
		}
		var input UserPromptSubmittedHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnUserPromptSubmitted(input, invocation)

	case "userPromptTransformed":
		if hooks.OnUserPromptTransformed == nil {
			return nil, nil
		}
		var input UserPromptTransformedHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnUserPromptTransformed(input, invocation)

	case "sessionStart":
		if hooks.OnSessionStart == nil {
			return nil, nil
		}
		var input SessionStartHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnSessionStart(input, invocation)

	case "sessionEnd":
		if hooks.OnSessionEnd == nil {
			return nil, nil
		}
		var input SessionEndHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnSessionEnd(input, invocation)

	case "errorOccurred":
		if hooks.OnErrorOccurred == nil {
			return nil, nil
		}
		var input ErrorOccurredHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnErrorOccurred(input, invocation)

	case "agentStop":
		if hooks.OnAgentStop == nil {
			return nil, nil
		}
		var input AgentStopHookInput
		if err := json.Unmarshal(rawInput, &input); err != nil {
			return nil, fmt.Errorf("invalid hook input: %w", err)
		}
		return hooks.OnAgentStop(input, invocation)

	default:
		return nil, nil
	}
}

// registerTransformCallbacks registers transform callbacks for this session.
//
// Transform callbacks are invoked when the CLI requests system message section
// transforms. This method is internal and typically called when creating a session.
func (s *Session) registerTransformCallbacks(callbacks map[string]SectionTransformFn) {
	s.transformMu.Lock()
	defer s.transformMu.Unlock()
	s.transformCallbacks = callbacks
}

type systemMessageTransformSection struct {
	Content string `json:"content"`
}

type systemMessageTransformRequest struct {
	SessionID string                                   `json:"sessionId"`
	Sections  map[string]systemMessageTransformSection `json:"sections"`
}

type systemMessageTransformResponse struct {
	Sections map[string]systemMessageTransformSection `json:"sections"`
}

// handleSystemMessageTransform handles a system message transform request from the Copilot CLI.
// This is an internal method called by the SDK when the CLI requests section transforms.
func (s *Session) handleSystemMessageTransform(sections map[string]systemMessageTransformSection) (systemMessageTransformResponse, error) {
	s.transformMu.Lock()
	callbacks := s.transformCallbacks
	s.transformMu.Unlock()

	result := make(map[string]systemMessageTransformSection)
	for sectionID, data := range sections {
		var callback SectionTransformFn
		if callbacks != nil {
			callback = callbacks[sectionID]
		}
		if callback != nil {
			transformed, err := callback(data.Content)
			if err != nil {
				result[sectionID] = systemMessageTransformSection{Content: data.Content}
			} else {
				result[sectionID] = systemMessageTransformSection{Content: transformed}
			}
		} else {
			result[sectionID] = systemMessageTransformSection{Content: data.Content}
		}
	}
	return systemMessageTransformResponse{Sections: result}, nil
}

// registerCommands registers command handlers for this session.
func (s *Session) registerCommands(commands []CommandDefinition) {
	s.commandHandlersMu.Lock()
	defer s.commandHandlersMu.Unlock()
	s.commandHandlers = make(map[string]CommandHandler)
	for _, cmd := range commands {
		if cmd.Name == "" || cmd.Handler == nil {
			continue
		}
		s.commandHandlers[cmd.Name] = cmd.Handler
	}
}

// getCommandHandler retrieves a registered command handler by name.
func (s *Session) getCommandHandler(name string) (CommandHandler, bool) {
	s.commandHandlersMu.RLock()
	handler, ok := s.commandHandlers[name]
	s.commandHandlersMu.RUnlock()
	return handler, ok
}

// executeCommandAndRespond dispatches a command.execute event to the registered handler
// and sends the result (or error) back via the RPC layer.
func (s *Session) executeCommandAndRespond(requestID, commandName, command, args string) {
	ctx := context.Background()
	handler, ok := s.getCommandHandler(commandName)
	if !ok {
		errMsg := fmt.Sprintf("Unknown command: %s", commandName)
		s.RPC.Commands.HandlePendingCommand(ctx, &rpc.CommandsHandlePendingCommandRequest{
			RequestID: requestID,
			Error:     &errMsg,
		})
		return
	}

	cmdCtx := CommandContext{
		SessionID:   s.SessionID,
		Command:     command,
		CommandName: commandName,
		Args:        args,
	}

	if err := handler(cmdCtx); err != nil {
		errMsg := err.Error()
		s.RPC.Commands.HandlePendingCommand(ctx, &rpc.CommandsHandlePendingCommandRequest{
			RequestID: requestID,
			Error:     &errMsg,
		})
		return
	}

	s.RPC.Commands.HandlePendingCommand(ctx, &rpc.CommandsHandlePendingCommandRequest{
		RequestID: requestID,
	})
}

// registerElicitationHandler registers an elicitation handler for this session.
func (s *Session) registerElicitationHandler(handler ElicitationHandler) {
	s.elicitationMu.Lock()
	defer s.elicitationMu.Unlock()
	s.elicitationHandler = handler
}

// getElicitationHandler returns the currently registered elicitation handler, or nil.
func (s *Session) getElicitationHandler() ElicitationHandler {
	s.elicitationMu.RLock()
	defer s.elicitationMu.RUnlock()
	return s.elicitationHandler
}

func (s *Session) registerMCPAuthHandler(handler MCPAuthHandler) {
	s.mcpAuthMu.Lock()
	defer s.mcpAuthMu.Unlock()
	s.mcpAuthHandler = handler
}

func (s *Session) getMCPAuthHandler() MCPAuthHandler {
	s.mcpAuthMu.RLock()
	defer s.mcpAuthMu.RUnlock()
	return s.mcpAuthHandler
}

func (s *Session) handleMCPAuthRequest(request MCPAuthRequest) {
	handler := s.getMCPAuthHandler()
	if handler == nil {
		return
	}

	ctx := context.Background()
	cancel := &rpc.MCPOauthPendingRequestResponseCancelled{}
	result, err := handler(request, MCPAuthInvocation{SessionID: s.SessionID})
	if err != nil {
		log.Printf(
			"MCP OAuth handler failed. SessionId=%s, RequestId=%s, Error=%v",
			s.SessionID,
			request.RequestID,
			err,
		)
	}
	if err != nil || result == nil || result.Kind == MCPAuthResultKindCancelled || result.Token == nil {
		s.RPC.MCP.Oauth().HandlePendingRequest(ctx, &rpc.MCPOauthHandlePendingRequest{
			RequestID: request.RequestID,
			Result:    cancel,
		})
		return
	}

	s.RPC.MCP.Oauth().HandlePendingRequest(ctx, &rpc.MCPOauthHandlePendingRequest{
		RequestID: request.RequestID,
		Result: &rpc.MCPOauthPendingRequestResponseToken{
			AccessToken: result.Token.AccessToken,
			TokenType:   result.Token.TokenType,
			ExpiresIn:   result.Token.ExpiresIn,
		},
	})
}

// handleElicitationRequest dispatches an elicitation.requested event to the registered handler
// and sends the result back via the RPC layer. Auto-cancels on error.
func (s *Session) handleElicitationRequest(elicitCtx ElicitationContext, requestID string) {
	handler := s.getElicitationHandler()
	if handler == nil {
		return
	}

	ctx := context.Background()

	result, err := handler(elicitCtx)
	if err != nil {
		// Handler failed — attempt to cancel so the request doesn't hang.
		s.RPC.UI.HandlePendingElicitation(ctx, &rpc.UIHandlePendingElicitationRequest{
			RequestID: requestID,
			Result: rpc.UIElicitationResponse{
				Action: rpc.UIElicitationResponseActionCancel,
			},
		})
		return
	}

	var rpcContent map[string]rpc.UIElicitationFieldValue
	if result.Content != nil {
		rpcContent = make(map[string]rpc.UIElicitationFieldValue, len(result.Content))
		for k, v := range result.Content {
			contentValue, err := toRPCContent(v)
			if err != nil {
				s.RPC.UI.HandlePendingElicitation(ctx, &rpc.UIHandlePendingElicitationRequest{
					RequestID: requestID,
					Result: rpc.UIElicitationResponse{
						Action: rpc.UIElicitationResponseActionCancel,
					},
				})
				return
			}
			rpcContent[k] = contentValue
		}
	}

	s.RPC.UI.HandlePendingElicitation(ctx, &rpc.UIHandlePendingElicitationRequest{
		RequestID: requestID,
		Result: rpc.UIElicitationResponse{
			Action:  result.Action,
			Content: rpcContent,
		},
	})
}

// toRPCContent converts an SDK content value to an RPC elicitation response value.
func toRPCContent(v any) (rpc.UIElicitationFieldValue, error) {
	if v == nil {
		return nil, nil
	}
	switch val := v.(type) {
	case bool:
		return rpc.UIElicitationBooleanValue(val), nil
	case float64:
		return rpc.UIElicitationNumberValue(val), nil
	case float32:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case int:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case int8:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case int16:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case int32:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case int64:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case uint:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case uint8:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case uint16:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case uint32:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case uint64:
		return rpc.UIElicitationNumberValue(float64(val)), nil
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return nil, err
		}
		return rpc.UIElicitationNumberValue(f), nil
	case string:
		return rpc.UIElicitationStringValue(val), nil
	case []string:
		return rpc.UIElicitationStringArrayValue(val), nil
	case []any:
		strs := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported elicitation string array item type %T", item)
			}
			strs[i] = s
		}
		return rpc.UIElicitationStringArrayValue(strs), nil
	default:
		return nil, fmt.Errorf("unsupported elicitation content value type %T", v)
	}
}

// Capabilities returns the session capabilities reported by the server.
func (s *Session) Capabilities() SessionCapabilities {
	s.capabilitiesMu.RLock()
	defer s.capabilitiesMu.RUnlock()
	return s.capabilities
}

// setCapabilities updates the session capabilities.
func (s *Session) setCapabilities(caps *SessionCapabilities) {
	s.capabilitiesMu.Lock()
	defer s.capabilitiesMu.Unlock()
	if caps != nil {
		s.capabilities = *caps
	} else {
		s.capabilities = SessionCapabilities{}
	}
}

// UI returns the interactive UI API for showing elicitation dialogs.
// Methods on the returned SessionUI will error if the host does not support
// elicitation (check Capabilities().UI.Elicitation first).
func (s *Session) UI() *SessionUI {
	return &SessionUI{session: s}
}

// assertElicitation checks that the host supports elicitation and returns an error if not.
func (s *Session) assertElicitation() error {
	caps := s.Capabilities()
	if caps.UI == nil || !caps.UI.Elicitation {
		return fmt.Errorf("elicitation is not supported by the host; check session.Capabilities().UI.Elicitation before calling UI methods")
	}
	return nil
}

// Elicitation shows a generic elicitation dialog with a custom schema.
func (ui *SessionUI) Elicitation(ctx context.Context, message string, requestedSchema ElicitationSchema) (*ElicitationResult, error) {
	if err := ui.session.assertElicitation(); err != nil {
		return nil, err
	}
	rpcSchema, err := toRPCUIElicitationSchema(requestedSchema)
	if err != nil {
		return nil, err
	}
	rpcResult, err := ui.session.RPC.UI.Elicitation(ctx, &rpc.UIElicitationRequest{
		Message:         message,
		RequestedSchema: rpcSchema,
	})
	if err != nil {
		return nil, err
	}
	return fromRPCElicitationResult(rpcResult), nil
}

func toRPCUIElicitationSchema(schema ElicitationSchema) (rpc.UIElicitationSchema, error) {
	var properties map[string]rpc.UIElicitationSchemaProperty
	if schema.Properties != nil {
		properties = make(map[string]rpc.UIElicitationSchemaProperty, len(schema.Properties))
		for name, property := range schema.Properties {
			rpcProperty, err := toRPCUIElicitationSchemaProperty(name, property)
			if err != nil {
				return rpc.UIElicitationSchema{}, err
			}
			properties[name] = rpcProperty
		}
	}

	return rpc.UIElicitationSchema{
		Properties: properties,
		Required:   append([]string(nil), schema.Required...),
		Type:       rpc.UIElicitationSchemaTypeObject,
	}, nil
}

func toRPCUIElicitationSchemaProperty(name string, property any) (rpc.UIElicitationSchemaProperty, error) {
	if property == nil {
		return nil, fmt.Errorf("elicitation schema property %q is nil", name)
	}
	if rpcProperty, ok := property.(rpc.UIElicitationSchemaProperty); ok {
		return rpcProperty, nil
	}

	data, err := json.Marshal(property)
	if err != nil {
		return nil, fmt.Errorf("marshal elicitation schema property %q: %w", name, err)
	}
	wrapperData, err := json.Marshal(struct {
		Properties map[string]json.RawMessage  `json:"properties"`
		Type       rpc.UIElicitationSchemaType `json:"type"`
	}{
		Properties: map[string]json.RawMessage{name: data},
		Type:       rpc.UIElicitationSchemaTypeObject,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal elicitation schema wrapper for property %q: %w", name, err)
	}

	var rpcSchema rpc.UIElicitationSchema
	if err := json.Unmarshal(wrapperData, &rpcSchema); err != nil {
		return nil, fmt.Errorf("decode elicitation schema property %q: %w", name, err)
	}
	rpcProperty, ok := rpcSchema.Properties[name]
	if !ok {
		return nil, fmt.Errorf("decode elicitation schema property %q: property missing after conversion", name)
	}
	return rpcProperty, nil
}

// Confirm shows a confirmation dialog and returns the user's boolean answer.
// Returns false if the user declines or cancels.
func (ui *SessionUI) Confirm(ctx context.Context, message string) (bool, error) {
	if err := ui.session.assertElicitation(); err != nil {
		return false, err
	}
	rpcResult, err := ui.session.RPC.UI.Elicitation(ctx, &rpc.UIElicitationRequest{
		Message: message,
		RequestedSchema: rpc.UIElicitationSchema{
			Type: rpc.UIElicitationSchemaTypeObject,
			Properties: map[string]rpc.UIElicitationSchemaProperty{
				"confirmed": &rpc.UIElicitationSchemaPropertyBoolean{
					Default: Bool(true),
				},
			},
			Required: []string{"confirmed"},
		},
	})
	if err != nil {
		return false, err
	}
	if rpcResult.Action == rpc.UIElicitationResponseActionAccept {
		if value, ok := rpcResult.Content["confirmed"].(rpc.UIElicitationBooleanValue); ok {
			return bool(value), nil
		}
	}
	return false, nil
}

// Select shows a selection dialog with the given options.
// Returns the selected string, or empty string and false if the user declines/cancels.
func (ui *SessionUI) Select(ctx context.Context, message string, options []string) (string, bool, error) {
	if err := ui.session.assertElicitation(); err != nil {
		return "", false, err
	}
	rpcResult, err := ui.session.RPC.UI.Elicitation(ctx, &rpc.UIElicitationRequest{
		Message: message,
		RequestedSchema: rpc.UIElicitationSchema{
			Type: rpc.UIElicitationSchemaTypeObject,
			Properties: map[string]rpc.UIElicitationSchemaProperty{
				"selection": &rpc.UIElicitationStringEnumField{
					Enum: options,
				},
			},
			Required: []string{"selection"},
		},
	})
	if err != nil {
		return "", false, err
	}
	if rpcResult.Action == rpc.UIElicitationResponseActionAccept {
		if value, ok := rpcResult.Content["selection"].(rpc.UIElicitationStringValue); ok {
			return string(value), true, nil
		}
	}
	return "", false, nil
}

// Input shows a text input dialog. Returns the entered text, or empty string and
// false if the user declines/cancels.
func (ui *SessionUI) Input(ctx context.Context, message string, opts *UIInputOptions) (string, bool, error) {
	if err := ui.session.assertElicitation(); err != nil {
		return "", false, err
	}
	prop := &rpc.UIElicitationSchemaPropertyString{}
	if opts != nil {
		if opts.Title != "" {
			prop.Title = &opts.Title
		}
		if opts.Description != "" {
			prop.Description = &opts.Description
		}
		if opts.MinLength != nil {
			f := int64(*opts.MinLength)
			prop.MinLength = &f
		}
		if opts.MaxLength != nil {
			f := int64(*opts.MaxLength)
			prop.MaxLength = &f
		}
		if opts.Format != "" {
			format := rpc.UIElicitationSchemaPropertyStringFormat(opts.Format)
			prop.Format = &format
		}
		if opts.Default != "" {
			prop.Default = String(opts.Default)
		}
	}
	rpcResult, err := ui.session.RPC.UI.Elicitation(ctx, &rpc.UIElicitationRequest{
		Message: message,
		RequestedSchema: rpc.UIElicitationSchema{
			Type: rpc.UIElicitationSchemaTypeObject,
			Properties: map[string]rpc.UIElicitationSchemaProperty{
				"value": prop,
			},
			Required: []string{"value"},
		},
	})
	if err != nil {
		return "", false, err
	}
	if rpcResult.Action == rpc.UIElicitationResponseActionAccept {
		if value, ok := rpcResult.Content["value"].(rpc.UIElicitationStringValue); ok {
			return string(value), true, nil
		}
	}
	return "", false, nil
}

// fromRPCElicitationResult converts the RPC result to the SDK ElicitationResult.
func fromRPCElicitationResult(r *rpc.UIElicitationResponse) *ElicitationResult {
	if r == nil {
		return nil
	}
	var content map[string]ElicitationFieldValue
	if r.Content != nil {
		content = make(map[string]ElicitationFieldValue, len(r.Content))
		for k, v := range r.Content {
			content[k] = fromRPCContent(v)
		}
	}
	return &ElicitationResult{
		Action:  r.Action,
		Content: content,
	}
}

func fromRPCContent(value rpc.UIElicitationFieldValue) ElicitationFieldValue {
	switch v := value.(type) {
	case nil:
		return nil
	case rpc.UIElicitationBooleanValue:
		return bool(v)
	case rpc.UIElicitationNumberValue:
		return float64(v)
	case rpc.UIElicitationStringValue:
		return string(v)
	case rpc.UIElicitationStringArrayValue:
		return []string(v)
	}
	return nil
}

func fromRPCElicitationRequestedSchema(schema *rpc.ElicitationRequestedSchema) *ElicitationSchema {
	if schema == nil {
		return nil
	}
	return &ElicitationSchema{
		Properties: schema.Properties,
		Required:   schema.Required,
	}
}

// dispatchEvent enqueues an event for delivery to user handlers and fires
// broadcast handlers concurrently.
//
// Broadcast work (tool calls, permission requests) is fired in a separate
// goroutine so it does not block the JSON-RPC read loop. User event handlers
// are delivered by a single consumer goroutine (processEvents), guaranteeing
// serial, FIFO dispatch without blocking the read loop.
func (s *Session) dispatchEvent(event SessionEvent) {
	s.updateOpenCanvasesFromEvent(event)
	go s.handleBroadcastEvent(event)

	// Send to the event channel in a closure with a recover guard.
	// Disconnect closes eventCh, and in Go sending on a closed channel
	// panics — there is no non-panicking send primitive. We only want
	// to suppress that specific panic; other panics are not expected here.
	func() {
		defer func() { recover() }()
		s.eventCh <- event
	}()
}

// processEvents is the single consumer goroutine for the event channel.
// It invokes user handlers serially, in arrival order. Panics in individual
// handlers are recovered so that one misbehaving handler does not prevent
// others from receiving the event.
func (s *Session) processEvents() {
	for event := range s.eventCh {
		s.handlerMutex.RLock()
		handlers := make([]SessionEventHandler, 0, len(s.handlers))
		for _, h := range s.handlers {
			handlers = append(handlers, h.fn)
		}
		s.handlerMutex.RUnlock()

		for _, handler := range handlers {
			func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("Error in session event handler: %v\n", r)
					}
				}()
				handler(event)
			}()
		}
	}
}

// handleBroadcastEvent handles broadcast request events by executing local handlers
// and responding via RPC. This implements the protocol v3 broadcast model where tool
// calls and permission requests are broadcast as session events to all clients.
//
// Handlers are executed in their own goroutine (not the JSON-RPC read loop or the
// event consumer loop) so that a stalled handler does not block event delivery or
// cause RPC deadlocks.
func (s *Session) handleBroadcastEvent(event SessionEvent) {
	switch d := event.Data.(type) {
	case *ExternalToolRequestedData:
		handler, ok := s.getToolHandler(d.ToolName)
		if !ok {
			return
		}
		var tp, ts string
		if d.Traceparent != nil {
			tp = *d.Traceparent
		}
		if d.Tracestate != nil {
			ts = *d.Tracestate
		}
		s.executeToolAndRespond(d.RequestID, d.ToolName, d.ToolCallID, d.Arguments, handler, tp, ts)

	case *PermissionRequestedData:
		if d.ResolvedByHook != nil && *d.ResolvedByHook {
			return // Already resolved by a permissionRequest hook; no client action needed.
		}
		handler := s.getPermissionHandler()
		if handler == nil {
			return
		}
		s.executePermissionAndRespond(d.RequestID, d.PermissionRequest, handler)

	case *MCPOauthRequiredData:
		handler := s.getMCPAuthHandler()
		if d.RequestID == "" {
			return
		}
		if handler == nil {
			log.Printf(
				"Received MCP OAuth request without a registered MCP auth handler. SessionId=%s, RequestId=%s",
				s.SessionID,
				d.RequestID,
			)
			return
		}
		var staticClientConfig *MCPAuthStaticClientConfig
		if d.StaticClientConfig != nil {
			var grantType *string
			if d.StaticClientConfig.GrantType != nil {
				value := string(*d.StaticClientConfig.GrantType)
				grantType = &value
			}
			staticClientConfig = &MCPAuthStaticClientConfig{
				ClientID:     d.StaticClientConfig.ClientID,
				ClientSecret: d.StaticClientConfig.ClientSecret,
				GrantType:    grantType,
				PublicClient: d.StaticClientConfig.PublicClient,
			}
		}
		request := MCPAuthRequest{
			RequestID:          d.RequestID,
			ServerName:         d.ServerName,
			ServerURL:          d.ServerURL,
			Reason:             d.Reason,
			StaticClientConfig: staticClientConfig,
		}
		if d.ResourceMetadata != nil {
			request.ResourceMetadata = d.ResourceMetadata
		}
		if d.WwwAuthenticateParams != nil {
			request.WwwAuthenticateParams = &MCPAuthWwwAuthenticateParams{
				ResourceMetadataURL: d.WwwAuthenticateParams.ResourceMetadataURL,
				Scope:               d.WwwAuthenticateParams.Scope,
				Error:               d.WwwAuthenticateParams.Error,
			}
		}
		s.handleMCPAuthRequest(request)

	case *CommandExecuteData:
		s.executeCommandAndRespond(d.RequestID, d.CommandName, d.Command, d.Args)

	case *ElicitationRequestedData:
		handler := s.getElicitationHandler()
		if handler == nil {
			return
		}
		s.handleElicitationRequest(ElicitationContext{
			SessionID:         s.SessionID,
			Message:           d.Message,
			RequestedSchema:   fromRPCElicitationRequestedSchema(d.RequestedSchema),
			Mode:              d.Mode,
			ElicitationSource: d.ElicitationSource,
			URL:               d.URL,
		}, d.RequestID)

	case *CapabilitiesChangedData:
		if d.UI != nil && d.UI.Elicitation != nil {
			s.setCapabilities(&SessionCapabilities{
				UI: &UICapabilities{Elicitation: *d.UI.Elicitation},
			})
		}
	}
}

// executeToolAndRespond executes a tool handler and sends the result back via RPC.
func (s *Session) executeToolAndRespond(requestID, toolName, toolCallID string, arguments any, handler ToolHandler, traceparent, tracestate string) {
	ctx := contextWithTraceParent(context.Background(), traceparent, tracestate)
	defer func() {
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("tool panic: %v", r)
			s.RPC.Tools.HandlePendingToolCall(ctx, &rpc.HandlePendingToolCallRequest{
				RequestID: requestID,
				Error:     &errMsg,
			})
		}
	}()

	invocation := ToolInvocation{
		SessionID:    s.SessionID,
		ToolCallID:   toolCallID,
		ToolName:     toolName,
		Arguments:    arguments,
		TraceContext: ctx,
	}

	// The built-in tool-search tool receives a snapshot of the session's
	// currently initialized tools so an override can filter the live catalog
	// without issuing its own RPC. Fetch it only for that tool to avoid a
	// round-trip on every tool call; a failed fetch leaves the snapshot nil
	// rather than failing the tool.
	if toolName == toolSearchToolName {
		if metadata, mErr := s.RPC.Tools.GetCurrentMetadata(ctx); mErr == nil && metadata != nil {
			invocation.AvailableTools = metadata.Tools
		}
	}

	result, err := handler(invocation)
	if err != nil {
		errMsg := err.Error()
		s.RPC.Tools.HandlePendingToolCall(ctx, &rpc.HandlePendingToolCallRequest{
			RequestID: requestID,
			Error:     &errMsg,
		})
		return
	}

	textResultForLLM := result.TextResultForLLM
	if textResultForLLM == "" {
		textResultForLLM = fmt.Sprintf("%v", result)
	}

	// Default ResultType to "success" when unset, or "failure" when there's an error.
	effectiveResultType := result.ResultType
	if effectiveResultType == "" {
		if result.Error != "" {
			effectiveResultType = "failure"
		} else {
			effectiveResultType = "success"
		}
	}

	rpcResult := &rpc.ExternalToolTextResultForLlm{
		TextResultForLlm: textResultForLLM,
		ToolTelemetry:    result.ToolTelemetry,
		ResultType:       &effectiveResultType,
		ToolReferences:   result.ToolReferences,
	}
	if result.Error != "" {
		rpcResult.Error = &result.Error
	}
	if result.SessionLog != "" {
		rpcResult.SessionLog = &result.SessionLog
	}
	for _, b := range result.BinaryResultsForLLM {
		entry := rpc.ExternalToolTextResultForLlmBinaryResultsForLlm{
			Data:     b.Data,
			MIMEType: b.MIMEType,
			Type:     rpc.ExternalToolTextResultForLlmBinaryResultsForLlmType(b.Type),
		}
		if b.Description != "" {
			entry.Description = &b.Description
		}
		rpcResult.BinaryResultsForLlm = append(rpcResult.BinaryResultsForLlm, entry)
	}
	s.RPC.Tools.HandlePendingToolCall(ctx, &rpc.HandlePendingToolCallRequest{
		RequestID: requestID,
		Result:    rpcResult,
	})
}

// executePermissionAndRespond executes a permission handler and sends the result back via RPC.
func (s *Session) executePermissionAndRespond(requestID string, permissionRequest PermissionRequest, handler PermissionHandlerFunc) {
	defer func() {
		if r := recover(); r != nil {
			s.RPC.Permissions.HandlePendingPermissionRequest(context.Background(), &rpc.PermissionDecisionRequest{
				RequestID: requestID,
				Result:    &rpc.PermissionDecisionUserNotAvailable{},
			})
		}
	}()

	invocation := PermissionInvocation{
		SessionID:              s.SessionID,
		ManagedSettingsEnabled: s.managedSettings,
	}

	decision, err := handler(permissionRequest, invocation)
	if err != nil {
		log.Printf("permission handler failed: session_id=%s request_id=%s error=%v", s.SessionID, requestID, err)
		s.RPC.Permissions.HandlePendingPermissionRequest(context.Background(), &rpc.PermissionDecisionRequest{
			RequestID: requestID,
			Result:    &rpc.PermissionDecisionUserNotAvailable{},
		})
		return
	}
	if decision == nil {
		// Handler returned (nil, nil); treat as user-not-available rather
		// than sending null on the wire.
		s.RPC.Permissions.HandlePendingPermissionRequest(context.Background(), &rpc.PermissionDecisionRequest{
			RequestID: requestID,
			Result:    &rpc.PermissionDecisionUserNotAvailable{},
		})
		return
	}
	if _, ok := decision.(*rpc.PermissionDecisionNoResult); ok {
		return
	}
	if _, ok := decision.(rpc.PermissionDecisionNoResult); ok {
		return
	}

	s.RPC.Permissions.HandlePendingPermissionRequest(context.Background(), &rpc.PermissionDecisionRequest{
		RequestID: requestID,
		Result:    decision,
	})
}

// GetEvents retrieves all events from this session's history.
//
// This returns the complete conversation history including user messages,
// assistant responses, tool executions, and other session events in
// chronological order.
//
// Returns an error if the session has been disconnected or the connection fails.
//
// Example:
//
//	events, err := session.GetEvents(context.Background())
//	if err != nil {
//	    log.Printf("Failed to get events: %v", err)
//	    return
//	}
//	for _, event := range events {
//	    if d, ok := event.Data.(*copilot.AssistantMessageData); ok {
//	        fmt.Println("Assistant:", d.Content)
//	    }
//	}
func (s *Session) GetEvents(ctx context.Context) ([]SessionEvent, error) {

	result, err := s.client.Request(ctx, "session.getMessages", sessionGetMessagesRequest{SessionID: s.SessionID})
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	var response sessionGetMessagesResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal get events response: %w", err)
	}
	return response.Events, nil
}

// Disconnect closes this session and releases all in-memory resources (event
// handlers, tool handlers, permission handlers).
//
// The caller should ensure the session is idle (e.g., [Session.SendAndWait] has
// returned) before disconnecting. If the session is not idle, in-flight event
// handlers or tool handlers may observe failures.
//
// Session state on disk (conversation history, planning state, artifacts) is
// preserved, so the conversation can be resumed later by calling
// [Client.ResumeSession] with the session ID. To permanently remove all
// session data including files on disk, use [Client.DeleteSession] instead.
//
// After calling this method, the session object can no longer be used.
//
// Returns an error if the connection fails.
//
// Example:
//
//	// Clean up when done — session can still be resumed later
//	if err := session.Disconnect(); err != nil {
//	    log.Printf("Failed to disconnect session: %v", err)
//	}
func (s *Session) Disconnect() error {
	_, err := s.client.Request(context.Background(), "session.destroy", sessionDestroyRequest{SessionID: s.SessionID})
	if err != nil {
		return fmt.Errorf("failed to disconnect session: %w", err)
	}

	s.closeOnce.Do(func() { close(s.eventCh) })

	// Clear handlers
	s.handlerMutex.Lock()
	s.handlers = nil
	s.handlerMutex.Unlock()

	s.toolHandlersM.Lock()
	s.toolHandlers = nil
	s.toolHandlersM.Unlock()

	s.permissionMux.Lock()
	s.permissionHandler = nil
	s.permissionMux.Unlock()

	s.commandHandlersMu.Lock()
	s.commandHandlers = nil
	s.commandHandlersMu.Unlock()

	s.elicitationMu.Lock()
	s.elicitationHandler = nil
	s.elicitationMu.Unlock()

	return nil
}

// Abort aborts the currently processing message in this session.
//
// Use this to cancel a long-running request. The session remains valid
// and can continue to be used for new messages.
//
// Returns an error if the session has been disconnected or the connection fails.
//
// Example:
//
//	// Start a long-running request in a goroutine
//	go func() {
//	    session.Send(context.Background(), copilot.MessageOptions{
//	        Prompt: "Write a very long story...",
//	    })
//	}()
//
//	// Abort after 5 seconds
//	time.Sleep(5 * time.Second)
//	if err := session.Abort(context.Background()); err != nil {
//	    log.Printf("Failed to abort: %v", err)
//	}
func (s *Session) Abort(ctx context.Context) error {
	_, err := s.client.Request(ctx, "session.abort", sessionAbortRequest{SessionID: s.SessionID})
	if err != nil {
		return fmt.Errorf("failed to abort session: %w", err)
	}

	return nil
}

// SetModelOptions configures optional parameters for SetModel.
type SetModelOptions struct {
	// ReasoningEffort sets the reasoning effort level for the new model (e.g., "low", "medium", "high", "xhigh", "max").
	ReasoningEffort *string
	// ReasoningSummary sets the reasoning summary mode for the new model.
	// Use ReasoningSummaryNone to suppress summary output regardless of whether reasoning is enabled.
	ReasoningSummary *ReasoningSummary
	// ContextTier explicitly selects a context window tier for models that support it.
	// Leave nil to use normal model behavior with no explicit tier.
	ContextTier *ContextTier
	// ModelCapabilities overrides individual model capabilities resolved by the runtime.
	// Only non-nil fields are applied over the runtime-resolved capabilities.
	ModelCapabilities *rpc.ModelCapabilitiesOverride
}

// SetModel changes the model for this session.
// The new model takes effect for the next message. Conversation history is preserved.
//
// Example:
//
//	if err := session.SetModel(context.Background(), "gpt-5.4", nil); err != nil {
//	    log.Printf("Failed to set model: %v", err)
//	}
//	if err := session.SetModel(context.Background(), "claude-sonnet-4.6", &SetModelOptions{ReasoningEffort: new("high")}); err != nil {
//	    log.Printf("Failed to set model: %v", err)
//	}
func (s *Session) SetModel(ctx context.Context, model string, opts *SetModelOptions) error {
	params := &rpc.ModelSwitchToRequest{ModelID: model}
	if opts != nil {
		params.ReasoningEffort = opts.ReasoningEffort
		params.ReasoningSummary = opts.ReasoningSummary
		params.ContextTier = opts.ContextTier
		params.ModelCapabilities = opts.ModelCapabilities
	}
	_, err := s.RPC.Model.SwitchTo(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to set model: %w", err)
	}

	return nil
}

type LogOptions struct {
	// Level sets the log severity. Valid values are [rpc.SessionLogLevelInfo] (default),
	// [rpc.SessionLogLevelWarning], and [rpc.SessionLogLevelError].
	Level rpc.SessionLogLevel
	// Ephemeral marks the message as transient so it is not persisted
	// to the session event log on disk. When nil the server decides the
	// default; set to a non-nil value to explicitly control persistence.
	Ephemeral *bool
}

// Log sends a log message to the session timeline.
// The message appears in the session event stream and is visible to SDK consumers
// and (for non-ephemeral messages) persisted to the session event log on disk.
//
// Pass nil for opts to use defaults (info level, non-ephemeral).
//
// Example:
//
//	// Simple info message
//	session.Log(ctx, "Processing started")
//
//	// Warning with options
//	session.Log(ctx, "Rate limit approaching", &copilot.LogOptions{Level: rpc.SessionLogLevelWarning})
//
//	// Ephemeral message (not persisted)
//	session.Log(ctx, "Working...", &copilot.LogOptions{Ephemeral: copilot.Bool(true)})
func (s *Session) Log(ctx context.Context, message string, opts *LogOptions) error {
	params := &rpc.LogRequest{Message: message}

	if opts != nil {
		if opts.Level != "" {
			params.Level = &opts.Level
		}
		if opts.Ephemeral != nil {
			params.Ephemeral = opts.Ephemeral
		}
	}

	_, err := s.RPC.Log(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to log message: %w", err)
	}

	return nil
}
