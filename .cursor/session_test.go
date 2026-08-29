package copilot

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
	"github.com/github/copilot-sdk/go/rpc"
)

// newTestSession creates a session with an event channel and starts the consumer goroutine.
// Returns a cleanup function that closes the channel (stopping the consumer).
func newTestSession() (*Session, func()) {
	s := &Session{
		handlers:        make([]sessionHandler, 0),
		commandHandlers: make(map[string]CommandHandler),
		eventCh:         make(chan SessionEvent, 128),
	}
	go s.processEvents()
	return s, func() { close(s.eventCh) }
}

func newTestEvent() SessionEvent {
	return SessionEvent{Data: &SessionIdleData{}}
}

func ptr[T any](value T) *T {
	return &value
}

func TestSession_SetModelForwardsContextTier(t *testing.T) {
	tier := ContextTierLongContext
	params := captureSetModelRequest(t, &SetModelOptions{ContextTier: &tier})

	if params["sessionId"] != "session-1" {
		t.Fatalf("expected sessionId session-1, got %v", params["sessionId"])
	}
	if params["modelId"] != "gpt-4.1" {
		t.Fatalf("expected modelId gpt-4.1, got %v", params["modelId"])
	}
	if params["contextTier"] != "long_context" {
		t.Fatalf("expected contextTier long_context, got %v", params["contextTier"])
	}
}

func TestSession_SetModelOmitsContextTierWhenUnset(t *testing.T) {
	params := captureSetModelRequest(t, nil)

	if _, ok := params["contextTier"]; ok {
		t.Fatalf("expected contextTier to be omitted, got %v", params["contextTier"])
	}
}

func TestSession_MCPAuthRequestSendsHostToken(t *testing.T) {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutR.Close()
	defer stdoutW.Close()

	client := jsonrpc2.NewClient(stdinW, stdoutR)
	client.Start()
	defer client.Stop()

	paramsCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)

	go func() {
		frame, err := readTestJSONRPCFrame(stdinR)
		if err != nil {
			errCh <- err
			return
		}

		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(frame, &request); err != nil {
			errCh <- err
			return
		}
		if request.Method != "session.mcp.oauth.handlePendingRequest" {
			errCh <- fmt.Errorf("expected session.mcp.oauth.handlePendingRequest, got %s", request.Method)
			return
		}

		paramsCh <- request.Params

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  map[string]any{"success": true},
		}
		data, err := json.Marshal(response)
		if err != nil {
			errCh <- err
			return
		}
		if _, err := fmt.Fprintf(stdoutW, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
			errCh <- err
		}
	}()

	session := &Session{
		SessionID: "session-1",
		client:    client,
		RPC:       rpc.NewSessionRPC(client, "session-1"),
	}
	var observedRequest MCPAuthRequest
	session.registerMCPAuthHandler(func(request MCPAuthRequest, invocation MCPAuthInvocation) (*MCPAuthResult, error) {
		observedRequest = request
		if invocation.SessionID != "session-1" {
			t.Fatalf("expected invocation session-1, got %s", invocation.SessionID)
		}
		if request.RequestID != "oauth-request" {
			t.Fatalf("expected oauth-request, got %s", request.RequestID)
		}
		tokenType := "Bearer"
		return MCPAuthResultToken(&MCPAuthToken{
			AccessToken: "host-token",
			TokenType:   &tokenType,
		}), nil
	})
	resourceMetadataURL := "https://example.com/.well-known/oauth-protected-resource"
	resourceMetadata := `{"resource":"https://example.com/mcp"}`
	clientSecret := "static-secret"
	grantType := rpc.MCPOauthRequiredStaticClientConfigGrantTypeClientCredentials
	publicClient := false
	session.handleBroadcastEvent(SessionEvent{
		Data: &MCPOauthRequiredData{
			RequestID:        "oauth-request",
			Reason:           rpc.MCPOauthRequestReasonInitial,
			ServerName:       "oauth-server",
			ServerURL:        "https://example.com/mcp",
			ResourceMetadata: &resourceMetadata,
			StaticClientConfig: &MCPOauthRequiredStaticClientConfig{
				ClientID:     "static-client",
				ClientSecret: &clientSecret,
				GrantType:    &grantType,
				PublicClient: &publicClient,
			},
			WwwAuthenticateParams: &MCPOauthWwwAuthenticateParams{
				ResourceMetadataURL: &resourceMetadataURL,
			},
		},
	})
	if observedRequest.ResourceMetadata == nil || *observedRequest.ResourceMetadata != `{"resource":"https://example.com/mcp"}` {
		t.Fatalf("expected resource metadata to be propagated, got %#v", observedRequest.ResourceMetadata)
	}
	if observedRequest.Reason != MCPOauthRequestReasonInitial {
		t.Fatalf("expected initial reason, got %q", observedRequest.Reason)
	}
	if observedRequest.WwwAuthenticateParams == nil {
		t.Fatal("expected WWW-Authenticate params to be propagated")
	}
	if observedRequest.StaticClientConfig == nil {
		t.Fatal("expected static client config to be propagated")
	}
	if observedRequest.StaticClientConfig.ClientSecret == nil || *observedRequest.StaticClientConfig.ClientSecret != "static-secret" {
		t.Fatalf("expected static client secret to be propagated, got %#v", observedRequest.StaticClientConfig.ClientSecret)
	}
	if observedRequest.StaticClientConfig.GrantType == nil || *observedRequest.StaticClientConfig.GrantType != "client_credentials" {
		t.Fatalf("expected static client grant type to be propagated, got %#v", observedRequest.StaticClientConfig.GrantType)
	}

	select {
	case params := <-paramsCh:
		if params["sessionId"] != "session-1" {
			t.Fatalf("expected sessionId session-1, got %v", params["sessionId"])
		}
		if params["requestId"] != "oauth-request" {
			t.Fatalf("expected requestId oauth-request, got %v", params["requestId"])
		}
		result, ok := params["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result object, got %T", params["result"])
		}
		if result["kind"] != "token" {
			t.Fatalf("expected token kind, got %v", result["kind"])
		}
		if result["accessToken"] != "host-token" {
			t.Fatalf("expected accessToken host-token, got %v", result["accessToken"])
		}
		if result["tokenType"] != "Bearer" {
			t.Fatalf("expected tokenType Bearer, got %v", result["tokenType"])
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for MCP OAuth request")
	}
}

func TestMCPAuthRequestAllowsMissingOptionalMetadata(t *testing.T) {
	request := MCPAuthRequest{RequestID: "oauth-request"}
	if request.ResourceMetadata != nil {
		t.Fatalf("expected no resource metadata, got %#v", request.ResourceMetadata)
	}
	if request.WwwAuthenticateParams != nil {
		t.Fatalf("expected no WWW-Authenticate params, got %#v", request.WwwAuthenticateParams)
	}
}

func TestMCPOauthRequiredDataAllowsOptionalMetadata(t *testing.T) {
	var withMetadata rpc.MCPOauthRequiredData
	if err := json.Unmarshal([]byte(`{
		"requestId": "oauth-request",
		"reason": "initial",
		"serverName": "oauth-server",
		"serverUrl": "https://example.com/mcp",
		"wwwAuthenticateParams": {
			"resourceMetadataUrl": "https://example.com/.well-known/oauth-protected-resource"
		},
		"resourceMetadata": "{\"resource\":\"https://example.com/mcp\"}",
		"staticClientConfig": {
		    "clientId": "static-client",
		    "clientSecret": "static-secret",
		    "publicClient": false
		}
	}`), &withMetadata); err != nil {
		t.Fatal(err)
	}
	if withMetadata.ResourceMetadata == nil || *withMetadata.ResourceMetadata != `{"resource":"https://example.com/mcp"}` {
		t.Fatalf("expected resource metadata, got %#v", withMetadata.ResourceMetadata)
	}
	if withMetadata.WwwAuthenticateParams == nil {
		t.Fatal("expected WWW-Authenticate params")
	}
	if withMetadata.StaticClientConfig == nil || withMetadata.StaticClientConfig.ClientSecret == nil || *withMetadata.StaticClientConfig.ClientSecret != "static-secret" {
		t.Fatalf("expected static client secret, got %#v", withMetadata.StaticClientConfig)
	}

	var withoutMetadata rpc.MCPOauthRequiredData
	if err := json.Unmarshal([]byte(`{
		"requestId": "oauth-request",
		"reason": "initial",
		"serverName": "oauth-server",
		"serverUrl": "https://example.com/mcp"
	}`), &withoutMetadata); err != nil {
		t.Fatal(err)
	}
	if withoutMetadata.ResourceMetadata != nil {
		t.Fatalf("expected no resource metadata, got %#v", withoutMetadata.ResourceMetadata)
	}
	if withoutMetadata.WwwAuthenticateParams != nil {
		t.Fatalf("expected no WWW-Authenticate params, got %#v", withoutMetadata.WwwAuthenticateParams)
	}
}

func captureSetModelRequest(t *testing.T, opts *SetModelOptions) map[string]any {
	t.Helper()

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	defer stdinR.Close()
	defer stdinW.Close()
	defer stdoutR.Close()
	defer stdoutW.Close()

	client := jsonrpc2.NewClient(stdinW, stdoutR)
	client.Start()
	defer client.Stop()

	paramsCh := make(chan map[string]any, 1)
	errCh := make(chan error, 1)

	go func() {
		frame, err := readTestJSONRPCFrame(stdinR)
		if err != nil {
			errCh <- err
			return
		}

		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(frame, &request); err != nil {
			errCh <- err
			return
		}
		if request.Method != "session.model.switchTo" {
			errCh <- fmt.Errorf("expected session.model.switchTo, got %s", request.Method)
			return
		}

		paramsCh <- request.Params

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  map[string]any{},
		}
		data, err := json.Marshal(response)
		if err != nil {
			errCh <- err
			return
		}
		if _, err := fmt.Fprintf(stdoutW, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
			errCh <- err
			return
		}
	}()

	session := &Session{
		SessionID: "session-1",
		client:    client,
		RPC:       rpc.NewSessionRPC(client, "session-1"),
	}
	if err := session.SetModel(context.Background(), "gpt-4.1", opts); err != nil {
		t.Fatalf("SetModel failed: %v", err)
	}

	select {
	case params := <-paramsCh:
		return params
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for session.model.switchTo request")
	}
	return nil
}

func readTestJSONRPCFrame(r io.Reader) ([]byte, error) {
	reader := bufio.NewReader(r)
	var contentLength int
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("invalid header line %q", line)
		}
		if name == "Content-Length" {
			contentLength, err = strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, err
			}
		}
	}
	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	data := make([]byte, contentLength)
	_, err := io.ReadFull(reader, data)
	return data, err
}

func TestSession_On(t *testing.T) {
	t.Run("multiple handlers all receive events", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var wg sync.WaitGroup
		wg.Add(3)
		var received1, received2, received3 bool
		session.On(func(event SessionEvent) { received1 = true; wg.Done() })
		session.On(func(event SessionEvent) { received2 = true; wg.Done() })
		session.On(func(event SessionEvent) { received3 = true; wg.Done() })

		session.dispatchEvent(newTestEvent())
		wg.Wait()

		if !received1 || !received2 || !received3 {
			t.Errorf("Expected all handlers to receive event, got received1=%v, received2=%v, received3=%v",
				received1, received2, received3)
		}
	})

	t.Run("unsubscribing one handler does not affect others", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var count1, count2, count3 atomic.Int32
		var wg sync.WaitGroup

		wg.Add(3)
		session.On(func(event SessionEvent) { count1.Add(1); wg.Done() })
		unsub2 := session.On(func(event SessionEvent) { count2.Add(1); wg.Done() })
		session.On(func(event SessionEvent) { count3.Add(1); wg.Done() })

		// First event - all handlers receive it
		session.dispatchEvent(newTestEvent())
		wg.Wait()

		// Unsubscribe handler 2
		unsub2()

		// Second event - only handlers 1 and 3 should receive it
		wg.Add(2)
		session.dispatchEvent(newTestEvent())
		wg.Wait()

		if count1.Load() != 2 {
			t.Errorf("Expected handler 1 to receive 2 events, got %d", count1.Load())
		}
		if count2.Load() != 1 {
			t.Errorf("Expected handler 2 to receive 1 event (before unsubscribe), got %d", count2.Load())
		}
		if count3.Load() != 2 {
			t.Errorf("Expected handler 3 to receive 2 events, got %d", count3.Load())
		}
	})

	t.Run("calling unsubscribe multiple times is safe", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var count atomic.Int32
		var wg sync.WaitGroup

		wg.Add(1)
		unsub := session.On(func(event SessionEvent) { count.Add(1); wg.Done() })

		session.dispatchEvent(newTestEvent())
		wg.Wait()

		unsub()
		unsub()
		unsub()

		// Dispatch again and wait for it to be processed via a sentinel handler
		wg.Add(1)
		session.On(func(event SessionEvent) { wg.Done() })
		session.dispatchEvent(newTestEvent())
		wg.Wait()

		if count.Load() != 1 {
			t.Errorf("Expected handler to receive 1 event, got %d", count.Load())
		}
	})

	t.Run("handlers are called in registration order", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var order []int
		var wg sync.WaitGroup
		wg.Add(3)
		session.On(func(event SessionEvent) { order = append(order, 1); wg.Done() })
		session.On(func(event SessionEvent) { order = append(order, 2); wg.Done() })
		session.On(func(event SessionEvent) { order = append(order, 3); wg.Done() })

		session.dispatchEvent(newTestEvent())
		wg.Wait()

		if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
			t.Errorf("Expected handlers to be called in order [1,2,3], got %v", order)
		}
	})

	t.Run("concurrent subscribe and unsubscribe is safe", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var wg sync.WaitGroup
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unsub := session.On(func(event SessionEvent) {})
				unsub()
			}()
		}
		wg.Wait()

		session.handlerMutex.RLock()
		count := len(session.handlers)
		session.handlerMutex.RUnlock()

		if count != 0 {
			t.Errorf("Expected 0 handlers after all unsubscribes, got %d", count)
		}
	})

	t.Run("events are dispatched serially", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var concurrentCount atomic.Int32
		var maxConcurrent atomic.Int32
		var done sync.WaitGroup
		const totalEvents = 5
		done.Add(totalEvents)

		session.On(func(event SessionEvent) {
			current := concurrentCount.Add(1)
			if current > maxConcurrent.Load() {
				maxConcurrent.Store(current)
			}

			time.Sleep(10 * time.Millisecond)

			concurrentCount.Add(-1)
			done.Done()
		})

		for i := 0; i < totalEvents; i++ {
			session.dispatchEvent(newTestEvent())
		}

		done.Wait()

		if max := maxConcurrent.Load(); max != 1 {
			t.Errorf("Expected max concurrent count of 1, got %d", max)
		}
	})

	t.Run("handler panic does not halt delivery", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var eventCount atomic.Int32
		var done sync.WaitGroup
		done.Add(2)

		session.On(func(event SessionEvent) {
			count := eventCount.Add(1)
			defer done.Done()
			if count == 1 {
				panic("boom")
			}
		})

		session.dispatchEvent(newTestEvent())
		session.dispatchEvent(newTestEvent())

		done.Wait()

		if eventCount.Load() != 2 {
			t.Errorf("Expected 2 events dispatched, got %d", eventCount.Load())
		}
	})
}

func TestSession_CommandRouting(t *testing.T) {
	t.Run("routes command.execute event to the correct handler", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var receivedCtx CommandContext
		session.registerCommands([]CommandDefinition{
			{
				Name:        "deploy",
				Description: "Deploy the app",
				Handler: func(ctx CommandContext) error {
					receivedCtx = ctx
					return nil
				},
			},
			{
				Name:        "rollback",
				Description: "Rollback",
				Handler: func(ctx CommandContext) error {
					return nil
				},
			},
		})

		// Simulate the dispatch — executeCommandAndRespond will fail on RPC (nil client)
		// but the handler will still be invoked. We test routing only.
		_, ok := session.getCommandHandler("deploy")
		if !ok {
			t.Fatal("Expected 'deploy' handler to be registered")
		}
		_, ok = session.getCommandHandler("rollback")
		if !ok {
			t.Fatal("Expected 'rollback' handler to be registered")
		}
		_, ok = session.getCommandHandler("nonexistent")
		if ok {
			t.Fatal("Expected 'nonexistent' handler to NOT be registered")
		}

		// Directly invoke handler to verify context is correct
		handler, _ := session.getCommandHandler("deploy")
		err := handler(CommandContext{
			SessionID:   "test-session",
			Command:     "/deploy production",
			CommandName: "deploy",
			Args:        "production",
		})
		if err != nil {
			t.Fatalf("Handler returned error: %v", err)
		}
		if receivedCtx.SessionID != "test-session" {
			t.Errorf("Expected sessionID 'test-session', got %q", receivedCtx.SessionID)
		}
		if receivedCtx.CommandName != "deploy" {
			t.Errorf("Expected commandName 'deploy', got %q", receivedCtx.CommandName)
		}
		if receivedCtx.Command != "/deploy production" {
			t.Errorf("Expected command '/deploy production', got %q", receivedCtx.Command)
		}
		if receivedCtx.Args != "production" {
			t.Errorf("Expected args 'production', got %q", receivedCtx.Args)
		}
	})

	t.Run("skips commands with empty name or nil handler", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.registerCommands([]CommandDefinition{
			{Name: "", Handler: func(ctx CommandContext) error { return nil }},
			{Name: "valid", Handler: nil},
			{Name: "good", Handler: func(ctx CommandContext) error { return nil }},
		})

		_, ok := session.getCommandHandler("")
		if ok {
			t.Error("Empty name should not be registered")
		}
		_, ok = session.getCommandHandler("valid")
		if ok {
			t.Error("Nil handler should not be registered")
		}
		_, ok = session.getCommandHandler("good")
		if !ok {
			t.Error("Expected 'good' handler to be registered")
		}
	})

	t.Run("handler error is propagated", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		handlerCalled := false
		session.registerCommands([]CommandDefinition{
			{
				Name: "fail",
				Handler: func(ctx CommandContext) error {
					handlerCalled = true
					return fmt.Errorf("deploy failed")
				},
			},
		})

		handler, ok := session.getCommandHandler("fail")
		if !ok {
			t.Fatal("Expected 'fail' handler to be registered")
		}

		err := handler(CommandContext{
			SessionID:   "test-session",
			CommandName: "fail",
			Command:     "/fail",
			Args:        "",
		})

		if !handlerCalled {
			t.Error("Expected handler to be called")
		}
		if err == nil {
			t.Fatal("Expected error from handler")
		}
		if !strings.Contains(err.Error(), "deploy failed") {
			t.Errorf("Expected error to contain 'deploy failed', got %q", err.Error())
		}
	})

	t.Run("unknown command returns no handler", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.registerCommands([]CommandDefinition{
			{Name: "deploy", Handler: func(ctx CommandContext) error { return nil }},
		})

		_, ok := session.getCommandHandler("unknown")
		if ok {
			t.Error("Expected no handler for unknown command")
		}
	})
}

func TestSession_Capabilities(t *testing.T) {
	t.Run("defaults capabilities when not injected", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		caps := session.Capabilities()
		if caps.UI != nil {
			t.Errorf("Expected UI to be nil by default, got %+v", caps.UI)
		}
	})

	t.Run("setCapabilities stores and retrieves capabilities", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.setCapabilities(&SessionCapabilities{
			UI: &UICapabilities{Elicitation: true},
		})
		caps := session.Capabilities()
		if caps.UI == nil || !caps.UI.Elicitation {
			t.Errorf("Expected UI.Elicitation to be true")
		}
	})

	t.Run("setCapabilities with nil resets to empty", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.setCapabilities(&SessionCapabilities{
			UI: &UICapabilities{Elicitation: true},
		})
		session.setCapabilities(nil)
		caps := session.Capabilities()
		if caps.UI != nil {
			t.Errorf("Expected UI to be nil after reset, got %+v", caps.UI)
		}
	})

	t.Run("capabilities.changed event updates session capabilities", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		// Initially no capabilities
		caps := session.Capabilities()
		if caps.UI != nil {
			t.Fatal("Expected UI to be nil initially")
		}

		// Dispatch a capabilities.changed event with elicitation=true
		elicitTrue := true
		session.dispatchEvent(SessionEvent{
			Data: &CapabilitiesChangedData{
				UI: &CapabilitiesChangedUI{Elicitation: &elicitTrue},
			},
		})

		// Capabilities are updated by handleBroadcastEvent which runs in a goroutine.
		// Poll instead of sleep so the test is bound by event processing, not arbitrary
		// timing — fast machines exit immediately, slow ones still get 2s.
		caps = waitForCapability(t, session, func(c SessionCapabilities) bool {
			return c.UI != nil && c.UI.Elicitation
		}, 2*time.Second)
		if caps.UI == nil || !caps.UI.Elicitation {
			t.Error("Expected UI.Elicitation to be true after capabilities.changed event")
		}

		// Dispatch with elicitation=false
		elicitFalse := false
		session.dispatchEvent(SessionEvent{
			Data: &CapabilitiesChangedData{
				UI: &CapabilitiesChangedUI{Elicitation: &elicitFalse},
			},
		})

		caps = waitForCapability(t, session, func(c SessionCapabilities) bool {
			return c.UI != nil && !c.UI.Elicitation
		}, 2*time.Second)
		if caps.UI == nil || caps.UI.Elicitation {
			t.Error("Expected UI.Elicitation to be false after second capabilities.changed event")
		}
	})

	t.Run("session.canvas.opened event updates open canvas snapshots", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				InstanceID:  "missing-canvas-id",
				ExtensionID: "project:counter",
			},
		})
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				ExtensionID:   "project:counter",
				ExtensionName: ptr("Counter Provider"),
				CanvasID:      "counter",
				InstanceID:    "counter-1",
				Title:         ptr("Counter"),
				Icon:          ptr("beaker"),
				Status:        ptr("ready"),
				URL:           ptr("https://example.test/counter"),
				Input:         map[string]any{"seed": float64(1)},
			},
		})
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				ExtensionID: "project:logs",
				CanvasID:    "logs",
				InstanceID:  "logs-1",
				Title:       ptr("Logs"),
			},
		})

		open := session.OpenCanvases()
		if len(open) != 2 {
			t.Fatalf("expected 2 open canvases, got %d", len(open))
		}
		if open[0].InstanceID != "counter-1" || open[1].InstanceID != "logs-1" {
			t.Fatalf("unexpected open canvas order: %+v", open)
		}

		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				ExtensionID:   "project:counter",
				ExtensionName: ptr("Counter Provider"),
				CanvasID:      "counter",
				InstanceID:    "counter-1",
				Title:         ptr("Counter Updated"),
				Icon:          ptr("beaker-filled"),
				Status:        ptr("reconnected"),
				URL:           ptr("https://example.test/counter-updated"),
				Input:         map[string]any{"seed": float64(2)},
			},
		})

		open = session.OpenCanvases()
		if len(open) != 2 {
			t.Fatalf("expected 2 open canvases after upsert, got %d", len(open))
		}
		if open[0].InstanceID != "counter-1" || open[1].InstanceID != "logs-1" {
			t.Fatalf("upsert should preserve order, got %+v", open)
		}
		if open[0].Title == nil || *open[0].Title != "Counter Updated" {
			t.Fatalf("expected updated title, got %+v", open[0].Title)
		}
		if open[0].Icon == nil || *open[0].Icon != "beaker-filled" {
			t.Fatalf("expected updated icon, got %+v", open[0].Icon)
		}
		if open[0].Status == nil || *open[0].Status != "reconnected" {
			t.Fatalf("expected updated status, got %+v", open[0].Status)
		}
		if open[0].URL == nil || *open[0].URL != "https://example.test/counter-updated" {
			t.Fatalf("expected updated URL, got %+v", open[0].URL)
		}
	})

	t.Run("session.canvas.closed event removes open canvas snapshots", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				ExtensionID: "project:counter",
				CanvasID:    "counter",
				InstanceID:  "counter-1",
				Title:       ptr("Counter"),
			},
		})
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasOpenedData{
				ExtensionID: "project:logs",
				CanvasID:    "logs",
				InstanceID:  "logs-1",
				Title:       ptr("Logs"),
			},
		})

		if open := session.OpenCanvases(); len(open) != 2 {
			t.Fatalf("expected 2 open canvases, got %d", len(open))
		}

		// Closing one instance removes it; the other remains.
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasClosedData{
				ExtensionID: "project:counter",
				CanvasID:    "counter",
				InstanceID:  "counter-1",
			},
		})
		open := session.OpenCanvases()
		if len(open) != 1 || open[0].InstanceID != "logs-1" {
			t.Fatalf("expected only logs-1 to remain, got %+v", open)
		}

		// Closing an absent instance is a no-op (idempotent).
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasClosedData{
				ExtensionID: "project:counter",
				CanvasID:    "counter",
				InstanceID:  "counter-1",
			},
		})
		open = session.OpenCanvases()
		if len(open) != 1 || open[0].InstanceID != "logs-1" {
			t.Fatalf("idempotent close should leave logs-1, got %+v", open)
		}

		// A closed event missing instanceID leaves the snapshot intact.
		session.dispatchEvent(SessionEvent{
			Data: &SessionCanvasClosedData{
				ExtensionID: "project:logs",
				CanvasID:    "logs",
			},
		})
		open = session.OpenCanvases()
		if len(open) != 1 || open[0].InstanceID != "logs-1" {
			t.Fatalf("invalid close should leave logs-1, got %+v", open)
		}
	})
}

// waitForCapability polls Session.Capabilities() until predicate matches or timeout.
// Returns the last observed capabilities. Avoids time.Sleep in tests.
func waitForCapability(t *testing.T, session *Session, predicate func(SessionCapabilities) bool, timeout time.Duration) SessionCapabilities {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last SessionCapabilities
	for {
		last = session.Capabilities()
		if predicate(last) {
			return last
		}
		if time.Now().After(deadline) {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSession_ElicitationCapabilityGating(t *testing.T) {
	t.Run("elicitation errors when capability is missing", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		err := session.assertElicitation()
		if err == nil {
			t.Fatal("Expected error when elicitation capability is missing")
		}
		expected := "elicitation is not supported"
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Expected error to contain %q, got %q", expected, err.Error())
		}
	})

	t.Run("elicitation succeeds when capability is present", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.setCapabilities(&SessionCapabilities{
			UI: &UICapabilities{Elicitation: true},
		})
		err := session.assertElicitation()
		if err != nil {
			t.Errorf("Expected no error when elicitation capability is present, got %v", err)
		}
	})
}

func TestSession_ElicitationHandler(t *testing.T) {
	t.Run("registerElicitationHandler stores handler", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		if session.getElicitationHandler() != nil {
			t.Error("Expected nil handler before registration")
		}

		session.registerElicitationHandler(func(ctx ElicitationContext) (ElicitationResult, error) {
			return ElicitationResult{Action: ElicitationActionAccept}, nil
		})

		if session.getElicitationHandler() == nil {
			t.Error("Expected non-nil handler after registration")
		}
	})

	t.Run("handler error is returned correctly", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.registerElicitationHandler(func(ctx ElicitationContext) (ElicitationResult, error) {
			return ElicitationResult{}, fmt.Errorf("handler exploded")
		})

		handler := session.getElicitationHandler()
		if handler == nil {
			t.Fatal("Expected non-nil handler")
		}

		_, err := handler(
			ElicitationContext{SessionID: "test-session", Message: "Pick a color"},
		)
		if err == nil {
			t.Fatal("Expected error from handler")
		}
		if !strings.Contains(err.Error(), "handler exploded") {
			t.Errorf("Expected error to contain 'handler exploded', got %q", err.Error())
		}
	})

	t.Run("handler success returns result", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		session.registerElicitationHandler(func(ctx ElicitationContext) (ElicitationResult, error) {
			return ElicitationResult{
				Action:  ElicitationActionAccept,
				Content: map[string]any{"color": "blue"},
			}, nil
		})

		handler := session.getElicitationHandler()
		result, err := handler(
			ElicitationContext{SessionID: "test-session", Message: "Pick a color"},
		)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if result.Action != ElicitationActionAccept {
			t.Errorf("Expected action 'accept', got %q", result.Action)
		}
		if result.Content["color"] != "blue" {
			t.Errorf("Expected content color 'blue', got %v", result.Content["color"])
		}
	})
}

func TestSession_PostToolUseFailureHook(t *testing.T) {
	t.Run("dispatches with parsed input and returns additional context", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		var captured PostToolUseFailureHookInput
		session.registerHooks(&SessionHooks{
			OnPostToolUseFailure: func(input PostToolUseFailureHookInput, _ HookInvocation) (*PostToolUseFailureHookOutput, error) {
				captured = input
				return &PostToolUseFailureHookOutput{
					AdditionalContext: "extra-context: " + input.Error,
				}, nil
			},
		})

		raw := json.RawMessage(`{
			"sessionId": "sess-1",
			"timestamp": 1700000000,
			"cwd": "/work",
			"toolName": "tool-x",
			"toolArgs": {"foo": "bar"},
			"error": "boom"
		}`)
		output, err := session.handleHooksInvoke("postToolUseFailure", raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if captured.SessionID != "sess-1" {
			t.Errorf("expected sessionId 'sess-1', got %q", captured.SessionID)
		}
		if captured.ToolName != "tool-x" {
			t.Errorf("expected toolName 'tool-x', got %q", captured.ToolName)
		}
		if captured.Error != "boom" {
			t.Errorf("expected error 'boom', got %q", captured.Error)
		}
		if !captured.Timestamp.Equal(time.UnixMilli(1700000000)) {
			t.Errorf("expected timestamp %v, got %v", time.UnixMilli(1700000000), captured.Timestamp)
		}
		if captured.WorkingDirectory != "/work" {
			t.Errorf("expected WorkingDirectory '/work', got %q", captured.WorkingDirectory)
		}
		out, ok := output.(*PostToolUseFailureHookOutput)
		if !ok {
			t.Fatalf("expected *PostToolUseFailureHookOutput, got %T", output)
		}
		if out.AdditionalContext != "extra-context: boom" {
			t.Errorf("unexpected AdditionalContext: %q", out.AdditionalContext)
		}
	})

	t.Run("no handler registered returns nil without error", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()
		session.registerHooks(&SessionHooks{})

		output, err := session.handleHooksInvoke("postToolUseFailure", json.RawMessage(`{"sessionId":"sess-1","timestamp":0,"cwd":"","toolName":"t","toolArgs":null,"error":"e"}`))
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if output != nil {
			t.Errorf("expected nil output, got %v", output)
		}
	})
}

func TestSession_AgentStopHook(t *testing.T) {
	session, cleanup := newTestSession()
	defer cleanup()

	var captured AgentStopHookInput
	session.registerHooks(&SessionHooks{
		OnAgentStop: func(input AgentStopHookInput, invocation HookInvocation) (*AgentStopHookOutput, error) {
			captured = input
			if invocation.SessionID != session.SessionID {
				t.Errorf("expected invocation session ID %q, got %q", session.SessionID, invocation.SessionID)
			}
			return &AgentStopHookOutput{
				Decision: "block",
				Reason:   "finish the remaining work",
			}, nil
		},
	})

	raw := json.RawMessage(`{
		"sessionId": "sess-1",
		"timestamp": 1700000000,
		"cwd": "/work",
		"stopReason": "end_turn",
		"transcriptPath": "/tmp/transcript.jsonl",
		"stop_hook_active": true
	}`)
	output, err := session.handleHooksInvoke("agentStop", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.SessionID != "sess-1" {
		t.Errorf("expected sessionId 'sess-1', got %q", captured.SessionID)
	}
	if captured.StopReason != "end_turn" {
		t.Errorf("expected stopReason 'end_turn', got %q", captured.StopReason)
	}
	if captured.TranscriptPath != "/tmp/transcript.jsonl" {
		t.Errorf("expected transcriptPath '/tmp/transcript.jsonl', got %q", captured.TranscriptPath)
	}
	if !captured.StopHookActive {
		t.Error("expected StopHookActive to be true")
	}
	if !captured.Timestamp.Equal(time.UnixMilli(1700000000)) {
		t.Errorf("expected timestamp %v, got %v", time.UnixMilli(1700000000), captured.Timestamp)
	}
	if captured.WorkingDirectory != "/work" {
		t.Errorf("expected WorkingDirectory '/work', got %q", captured.WorkingDirectory)
	}
	out, ok := output.(*AgentStopHookOutput)
	if !ok {
		t.Fatalf("expected *AgentStopHookOutput, got %T", output)
	}
	if out.Decision != "block" || out.Reason != "finish the remaining work" {
		t.Errorf("unexpected output: %#v", out)
	}
}

func TestSession_HookForwardCompatibility(t *testing.T) {
	t.Run("unknown hook type returns nil without error when known hooks are registered", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		// Register known hook handlers to simulate a real session configuration.
		// The handler itself does nothing; it only exists to confirm that even
		// when other hooks are active, an unknown hook type is still ignored.
		session.registerHooks(&SessionHooks{
			OnPostToolUse: func(input PostToolUseHookInput, invocation HookInvocation) (*PostToolUseHookOutput, error) {
				return nil, nil
			},
		})

		// "futureUnknownHookType" stands in for a hook type introduced by a
		// newer CLI version that the SDK does not yet know about.
		output, err := session.handleHooksInvoke("futureUnknownHookType", json.RawMessage(`{}`))
		if err != nil {
			t.Errorf("Expected no error for unknown hook type, got: %v", err)
		}
		if output != nil {
			t.Errorf("Expected nil output for unknown hook type, got: %v", output)
		}
	})

	t.Run("unknown hook type with no hooks registered returns nil without error", func(t *testing.T) {
		session, cleanup := newTestSession()
		defer cleanup()

		output, err := session.handleHooksInvoke("futureHookType", json.RawMessage(`{"someField":"value"}`))
		if err != nil {
			t.Errorf("Expected no error for unknown hook type with no hooks, got: %v", err)
		}
		if output != nil {
			t.Errorf("Expected nil output for unknown hook type with no hooks, got: %v", output)
		}
	})
}

func TestSession_ElicitationRequestSchema(t *testing.T) {
	t.Run("nil content values are allowed", func(t *testing.T) {
		value, err := toRPCContent(nil)
		if err != nil {
			t.Fatalf("Expected nil content to be accepted, got %v", err)
		}
		if value != nil {
			t.Fatalf("Expected nil RPC content, got %T", value)
		}
	})

	t.Run("elicitation.requested passes full schema to handler", func(t *testing.T) {
		// Verify the schema extraction logic from handleBroadcastEvent
		// preserves type, properties, and required.
		properties := map[string]any{
			"name": map[string]any{"type": "string"},
			"age":  map[string]any{"type": "number"},
		}
		required := []string{"name", "age"}

		requestedSchema := ElicitationSchema{
			Properties: properties,
			Required:   required,
		}

		props := requestedSchema.Properties
		if props == nil {
			t.Fatal("Expected schema properties map")
		}
		if len(props) != 2 {
			t.Errorf("Expected 2 properties, got %d", len(props))
		}
		if len(requestedSchema.Required) != 2 {
			t.Errorf("Expected required [name, age], got %v", requestedSchema.Required)
		}
	})

	t.Run("schema without required omits required key", func(t *testing.T) {
		properties := map[string]any{
			"optional_field": map[string]any{"type": "string"},
		}

		requestedSchema := ElicitationSchema{
			Properties: properties,
		}

		if requestedSchema.Required != nil {
			t.Error("Expected Required to be nil when omitted")
		}
	})

	t.Run("schema conversion adds object type", func(t *testing.T) {
		requestedSchema := ElicitationSchema{
			Properties: map[string]any{
				"name": map[string]any{"type": "string"},
			},
		}

		rpcSchema, err := toRPCUIElicitationSchema(requestedSchema)
		if err != nil {
			t.Fatalf("toRPCUIElicitationSchema failed: %v", err)
		}
		if rpcSchema.Type != rpc.UIElicitationSchemaTypeObject {
			t.Errorf("Expected RPC schema type object, got %q", rpcSchema.Type)
		}
		if _, ok := rpcSchema.Properties["name"].(*rpc.UIElicitationSchemaPropertyString); !ok {
			t.Fatalf("Expected name property to decode as string schema, got %T", rpcSchema.Properties["name"])
		}
	})

	t.Run("schema conversion preserves typed properties", func(t *testing.T) {
		property := &rpc.UIElicitationSchemaPropertyString{}
		rpcSchema, err := toRPCUIElicitationSchema(ElicitationSchema{
			Properties: map[string]any{"name": property},
		})
		if err != nil {
			t.Fatalf("toRPCUIElicitationSchema failed: %v", err)
		}
		if rpcSchema.Properties["name"] != property {
			t.Fatalf("Expected typed property to be preserved, got %T", rpcSchema.Properties["name"])
		}
	})
}
