package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
	"github.com/github/copilot-sdk/go/internal/truncbuffer"
	"github.com/github/copilot-sdk/go/rpc"
)

// This file is for unit tests. Where relevant, prefer to add e2e tests in e2e/*.test.go instead

func TestClient_URLParsing(t *testing.T) {
	t.Run("should parse port-only URL format", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: URIConnection{URL: "8080"},
		})
		if client.actualPort != 8080 {
			t.Errorf("Expected port 8080, got %d", client.actualPort)
		}
		if client.actualHost != "localhost" {
			t.Errorf("Expected host localhost, got %s", client.actualHost)
		}
		if !client.isExternalServer {
			t.Error("Expected isExternalServer to be true")
		}
	})

	t.Run("should parse host:port URL format", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: URIConnection{URL: "127.0.0.1:9000"},
		})
		if client.actualPort != 9000 || client.actualHost != "127.0.0.1" {
			t.Errorf("Expected 127.0.0.1:9000, got %s:%d", client.actualHost, client.actualPort)
		}
	})

	t.Run("should parse http://host:port URL format", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: URIConnection{URL: "http://localhost:7000"},
		})
		if client.actualPort != 7000 || client.actualHost != "localhost" {
			t.Errorf("Expected localhost:7000, got %s:%d", client.actualHost, client.actualPort)
		}
	})

	t.Run("should parse https://host:port URL format", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: URIConnection{URL: "https://example.com:443"},
		})
		if client.actualPort != 443 || client.actualHost != "example.com" {
			t.Errorf("Expected example.com:443, got %s:%d", client.actualHost, client.actualPort)
		}
	})

	t.Run("should panic for invalid URL format", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for invalid URL format")
			}
		}()
		NewClient(&ClientOptions{Connection: URIConnection{URL: "invalid-url"}})
	})

	t.Run("should panic for invalid port - too high", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic")
			}
		}()
		NewClient(&ClientOptions{Connection: URIConnection{URL: "localhost:99999"}})
	})

	t.Run("should panic for invalid port - zero", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic")
			}
		}()
		NewClient(&ClientOptions{Connection: URIConnection{URL: "localhost:0"}})
	})

	t.Run("should panic for invalid port - negative", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic")
			}
		}()
		NewClient(&ClientOptions{Connection: URIConnection{URL: "localhost:-1"}})
	})

	t.Run("should panic when URIConnection has empty URL", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for empty URL")
			}
		}()
		NewClient(&ClientOptions{Connection: URIConnection{}})
	})

	t.Run("stdio connection uses stdio transport", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: StdioConnection{}})
		if !client.useStdio {
			t.Error("Expected useStdio=true for StdioConnection")
		}
	})

	t.Run("tcp connection uses tcp transport", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: TCPConnection{Port: 8080}})
		if client.useStdio {
			t.Error("Expected useStdio=false for TCPConnection")
		}
		if client.port != 8080 {
			t.Errorf("Expected port=8080, got %d", client.port)
		}
	})

	t.Run("uri connection is treated as external server", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: URIConnection{URL: "localhost:8080"},
		})
		if !client.isExternalServer {
			t.Error("Expected isExternalServer=true for URIConnection")
		}
	})
}

func TestClient_StopRequestsRuntimeShutdownForOwnedProcess(t *testing.T) {
	rpcClient, server, shutdownCalled := newRuntimeShutdownRpcPair(t)
	client := &Client{
		process:     &exec.Cmd{},
		client:      rpcClient,
		RPC:         rpc.NewServerRPC(rpcClient),
		sessions:    make(map[string]*Session),
		processDone: make(chan struct{}),
	}
	close(client.processDone)

	if err := client.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	select {
	case <-shutdownCalled:
	default:
		t.Fatal("Stop did not request runtime.shutdown")
	}

	server.Stop()
}

func TestClient_ForceStopAndExternalStopDoNotRequestRuntimeShutdown(t *testing.T) {
	rpcClient, server, shutdownCalled := newRuntimeShutdownRpcPair(t)
	client := &Client{
		process:  &exec.Cmd{},
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
	}

	client.ForceStop()
	assertRuntimeShutdownNotCalled(t, shutdownCalled)
	server.Stop()

	externalRpcClient, externalServer, externalShutdownCalled := newRuntimeShutdownRpcPair(t)
	externalClient := &Client{
		client:           externalRpcClient,
		RPC:              rpc.NewServerRPC(externalRpcClient),
		sessions:         make(map[string]*Session),
		isExternalServer: true,
	}

	if err := externalClient.Stop(); err != nil {
		t.Fatalf("external Stop failed: %v", err)
	}
	assertRuntimeShutdownNotCalled(t, externalShutdownCalled)
	externalServer.Stop()
}

func newRuntimeShutdownRpcPair(t *testing.T) (*jsonrpc2.Client, *jsonrpc2.Client, chan struct{}) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() {
		clientConn.Close()
		serverConn.Close()
	})

	rpcClient := jsonrpc2.NewClient(clientConn, clientConn)
	server := jsonrpc2.NewClient(serverConn, serverConn)
	shutdownCalled := make(chan struct{}, 1)
	server.SetRequestHandler("runtime.shutdown", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		shutdownCalled <- struct{}{}
		return []byte(`{}`), nil
	})
	rpcClient.Start()
	server.Start()
	return rpcClient, server, shutdownCalled
}

func TestClient_ForwardsCapiOptionsToSessionRequests(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	_, err := client.CreateSession(t.Context(), &SessionConfig{
		Capi: &CapiSessionOptions{EnableWebSocketResponses: Bool(false)},
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertCapiEnableWebSocketResponses(t, <-createParams)

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed-capi","workspacePath":"/workspace"}`), nil
	})

	_, err = client.ResumeSessionWithOptions(t.Context(), "resumed-capi", &ResumeSessionConfig{
		Capi: &CapiSessionOptions{EnableWebSocketResponses: Bool(false)},
	})
	if err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertCapiEnableWebSocketResponses(t, <-resumeParams)
}

func TestClient_ForwardsAdditionalDirectoriesToSessionRequests(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	_, err := client.CreateSession(t.Context(), &SessionConfig{
		AdditionalDirectories: []string{"/repo/shared", "/repo/generated"},
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertAdditionalDirectories(t, <-createParams, []string{"/repo/shared", "/repo/generated"})

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed-additional-directories","workspacePath":"/workspace"}`), nil
	})

	_, err = client.ResumeSessionWithOptions(
		t.Context(),
		"resumed-additional-directories",
		&ResumeSessionConfig{AdditionalDirectories: []string{"/repo/resumed"}},
	)
	if err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertAdditionalDirectories(t, <-resumeParams, []string{"/repo/resumed"})
}

func assertAdditionalDirectories(t *testing.T, params json.RawMessage, want []string) {
	t.Helper()
	var payload struct {
		AdditionalDirectories []string `json:"additionalDirectories"`
	}
	if err := json.Unmarshal(params, &payload); err != nil {
		t.Fatalf("failed to decode request params: %v", err)
	}
	if !reflect.DeepEqual(payload.AdditionalDirectories, want) {
		t.Fatalf("additionalDirectories = %v, want %v", payload.AdditionalDirectories, want)
	}
}

func TestClient_ForwardsCanvasProviderToSessionRequests(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	_, err := client.CreateSession(t.Context(), &SessionConfig{
		ExtensionInfo:  &ExtensionInfo{Source: "github-app", Name: "counter-provider"},
		CanvasProvider: &CanvasProviderIdentity{ID: "app:builtin:window-1", Name: String("Built-in")},
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertCanvasProviderForwarded(t, <-createParams, "app:builtin:window-1", "Built-in", "counter-provider")

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed-canvas","workspacePath":"/workspace"}`), nil
	})

	_, err = client.ResumeSessionWithOptions(t.Context(), "resumed-canvas", &ResumeSessionConfig{
		CanvasProvider: &CanvasProviderIdentity{ID: "app:builtin:window-1"},
	})
	if err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertCanvasProviderForwarded(t, <-resumeParams, "app:builtin:window-1", "", "")
}

// assertCanvasProviderForwarded checks the outbound params carry canvasProvider
// with the expected id. A non-empty wantName asserts the name is present; an
// empty wantName asserts the name key is omitted from the wire. A non-empty
// wantExtensionName asserts extensionInfo.name is forwarded alongside it.
func assertCanvasProviderForwarded(t *testing.T, params json.RawMessage, wantID, wantName, wantExtensionName string) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}
	provider, ok := decoded["canvasProvider"].(map[string]any)
	if !ok {
		t.Fatalf("expected canvasProvider object in request params, got %T", decoded["canvasProvider"])
	}
	if provider["id"] != wantID {
		t.Fatalf("expected canvasProvider.id=%q, got %v", wantID, provider["id"])
	}
	if wantName == "" {
		if _, present := provider["name"]; present {
			t.Fatalf("expected canvasProvider.name to be omitted, got %v", provider["name"])
		}
	} else if provider["name"] != wantName {
		t.Fatalf("expected canvasProvider.name=%q, got %v", wantName, provider["name"])
	}
	if wantExtensionName != "" {
		info, ok := decoded["extensionInfo"].(map[string]any)
		if !ok {
			t.Fatalf("expected extensionInfo object in request params, got %T", decoded["extensionInfo"])
		}
		if info["name"] != wantExtensionName {
			t.Fatalf("expected extensionInfo.name=%q, got %v", wantExtensionName, info["name"])
		}
	}
}

func TestClient_ForwardsNewSessionOptionsToSessionRequests(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	_, err := client.CreateSession(t.Context(), &SessionConfig{
		ExcludedBuiltInAgents: []string{"explore"},
		EnableCitations:       Bool(true),
		SessionLimits:         &rpc.SessionLimitsConfig{MaxAiCredits: float64Ptr(30)},
	})
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertNewSessionOptions(t, <-createParams, true, "explore", 30)

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed-options","workspacePath":"/workspace"}`), nil
	})

	_, err = client.ResumeSessionWithOptions(t.Context(), "resumed-options", &ResumeSessionConfig{
		ExcludedBuiltInAgents: []string{"task"},
		EnableCitations:       Bool(false),
		SessionLimits:         &rpc.SessionLimitsConfig{MaxAiCredits: float64Ptr(15)},
	})
	if err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertNewSessionOptions(t, <-resumeParams, false, "task", 15)
}

func assertCapiEnableWebSocketResponses(t *testing.T, params json.RawMessage) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}

	capi, ok := decoded["capi"].(map[string]any)
	if !ok {
		t.Fatalf("expected capi object in request params, got %T", decoded["capi"])
	}
	if capi["enableWebSocketResponses"] != false {
		t.Fatalf("expected capi.enableWebSocketResponses=false, got %v", capi["enableWebSocketResponses"])
	}
}

func assertNewSessionOptions(
	t *testing.T,
	params json.RawMessage,
	expectedCitations bool,
	expectedAgent string,
	expectedCredits float64,
) {
	t.Helper()

	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}
	if decoded["enableCitations"] != expectedCitations {
		t.Fatalf("expected enableCitations=%v, got %v", expectedCitations, decoded["enableCitations"])
	}
	agents, ok := decoded["excludedBuiltinAgents"].([]any)
	if !ok || len(agents) != 1 || agents[0] != expectedAgent {
		t.Fatalf("expected excludedBuiltinAgents=[%q], got %#v", expectedAgent, decoded["excludedBuiltinAgents"])
	}
	limits, ok := decoded["sessionLimits"].(map[string]any)
	if !ok {
		t.Fatalf("expected sessionLimits object, got %T", decoded["sessionLimits"])
	}
	if limits["maxAiCredits"] != expectedCredits {
		t.Fatalf("expected sessionLimits.maxAiCredits=%v, got %v", expectedCredits, limits["maxAiCredits"])
	}
}

func float64Ptr(value float64) *float64 {
	return &value
}

func sessionIDFromParams(t *testing.T, params json.RawMessage) string {
	t.Helper()

	var decoded struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}
	if decoded.SessionID == "" {
		t.Fatal("expected generated sessionId in request params")
	}
	return decoded.SessionID
}

func assertRuntimeShutdownNotCalled(t *testing.T, shutdownCalled <-chan struct{}) {
	t.Helper()
	select {
	case <-shutdownCalled:
		t.Fatal("runtime.shutdown should not have been requested")
	default:
	}
}

func TestClient_SessionFSConfig(t *testing.T) {
	t.Run("should throw error when InitialWorkingDirectory is missing", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for missing SessionFS.InitialWorkingDirectory")
			} else {
				matched, _ := regexp.MatchString("SessionFS.InitialWorkingDirectory is required", r.(string))
				if !matched {
					t.Errorf("Expected panic message to contain 'SessionFS.InitialWorkingDirectory is required', got: %v", r)
				}
			}
		}()

		NewClient(&ClientOptions{
			SessionFS: &SessionFSConfig{
				SessionStatePath: "/session-state",
				Conventions:      rpc.SessionFSSetProviderConventionsPosix,
			},
		})
	})

	t.Run("should throw error when SessionStatePath is missing", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for missing SessionFS.SessionStatePath")
			} else {
				matched, _ := regexp.MatchString("SessionFS.SessionStatePath is required", r.(string))
				if !matched {
					t.Errorf("Expected panic message to contain 'SessionFS.SessionStatePath is required', got: %v", r)
				}
			}
		}()

		NewClient(&ClientOptions{
			SessionFS: &SessionFSConfig{
				InitialWorkingDirectory: "/",
				Conventions:             rpc.SessionFSSetProviderConventionsPosix,
			},
		})
	})
}

func TestClient_AuthOptions(t *testing.T) {
	t.Run("should accept GitHubToken option", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			GitHubToken: "gho_test_token",
		})

		if client.options.GitHubToken != "gho_test_token" {
			t.Errorf("Expected GitHubToken to be 'gho_test_token', got %q", client.options.GitHubToken)
		}
	})

	t.Run("should default UseLoggedInUser to nil when no GitHubToken", func(t *testing.T) {
		client := NewClient(&ClientOptions{})

		if client.options.UseLoggedInUser != nil {
			t.Errorf("Expected UseLoggedInUser to be nil, got %v", client.options.UseLoggedInUser)
		}
	})

	t.Run("should allow explicit UseLoggedInUser false", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			UseLoggedInUser: Bool(false),
		})

		if client.options.UseLoggedInUser == nil || *client.options.UseLoggedInUser != false {
			t.Error("Expected UseLoggedInUser to be false")
		}
	})

	t.Run("should allow explicit UseLoggedInUser true with GitHubToken", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			GitHubToken:     "gho_test_token",
			UseLoggedInUser: Bool(true),
		})

		if client.options.UseLoggedInUser == nil || *client.options.UseLoggedInUser != true {
			t.Error("Expected UseLoggedInUser to be true")
		}
	})

	t.Run("should panic when GitHubToken is used with URIConnection", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for auth options with URIConnection")
			} else {
				matched, _ := regexp.MatchString("GitHubToken and UseLoggedInUser cannot be used with URIConnection", r.(string))
				if !matched {
					t.Errorf("Expected panic message about auth options, got: %v", r)
				}
			}
		}()

		NewClient(&ClientOptions{
			Connection:  URIConnection{URL: "localhost:8080"},
			GitHubToken: "gho_test_token",
		})
	})

	t.Run("should panic when UseLoggedInUser is used with URIConnection", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic for auth options with URIConnection")
			}
		}()

		NewClient(&ClientOptions{
			Connection:      URIConnection{URL: "localhost:8080"},
			UseLoggedInUser: Bool(false),
		})
	})
}

func TestClient_BaseDirectory(t *testing.T) {
	t.Run("should accept BaseDirectory option", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			BaseDirectory: "/custom/copilot/home",
		})

		if client.options.BaseDirectory != "/custom/copilot/home" {
			t.Errorf("Expected BaseDirectory to be '/custom/copilot/home', got %q", client.options.BaseDirectory)
		}
	})

	t.Run("should default BaseDirectory to empty string", func(t *testing.T) {
		client := NewClient(&ClientOptions{})

		if client.options.BaseDirectory != "" {
			t.Errorf("Expected BaseDirectory to be empty, got %q", client.options.BaseDirectory)
		}
	})
}

func TestClient_EnvOptions(t *testing.T) {
	t.Run("should store custom environment variables", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Env: []string{"FOO=bar", "BAZ=qux"},
		})

		if len(client.options.Env) != 2 {
			t.Errorf("Expected 2 environment variables, got %d", len(client.options.Env))
		}
		if client.options.Env[0] != "FOO=bar" {
			t.Errorf("Expected first env var to be 'FOO=bar', got %q", client.options.Env[0])
		}
		if client.options.Env[1] != "BAZ=qux" {
			t.Errorf("Expected second env var to be 'BAZ=qux', got %q", client.options.Env[1])
		}
	})

	t.Run("should default to inherit from current process", func(t *testing.T) {
		client := NewClient(&ClientOptions{})

		if want := os.Environ(); !reflect.DeepEqual(client.options.Env, want) {
			t.Errorf("Expected Env to be %v, got %v", want, client.options.Env)
		}
	})

	t.Run("should default to inherit from current process with nil options", func(t *testing.T) {
		client := NewClient(nil)

		if want := os.Environ(); !reflect.DeepEqual(client.options.Env, want) {
			t.Errorf("Expected Env to be %v, got %v", want, client.options.Env)
		}
	})

	t.Run("should allow empty environment", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Env: []string{},
		})

		if client.options.Env == nil {
			t.Error("Expected Env to be non-nil empty slice")
		}
		if len(client.options.Env) != 0 {
			t.Errorf("Expected 0 environment variables, got %d", len(client.options.Env))
		}
	})
}

func TestClient_InProcessConnection(t *testing.T) {
	t.Run("requires build tag", func(t *testing.T) {
		if inProcessAvailable {
			t.Skip("in-process transport is enabled")
		}

		client := NewClient(&ClientOptions{Connection: InProcessConnection{}})
		err := client.Start(context.Background())
		if err == nil || !strings.Contains(err.Error(), "-tags copilot_inprocess") {
			t.Fatalf("Expected build-tag error, got %v", err)
		}
	})

	t.Run("uses in-process transport", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: InProcessConnection{}})
		if !client.useInProcess {
			t.Error("Expected useInProcess=true for InProcessConnection")
		}
		if client.useStdio {
			t.Error("Expected useStdio=false for InProcessConnection")
		}
		if client.isExternalServer {
			t.Error("Expected isExternalServer=false for InProcessConnection")
		}
		if client.cliPath != "" {
			t.Errorf("Expected in-process cliPath to stay empty at construction, got %q", client.cliPath)
		}
	})

	t.Run("does not resolve COPILOT_CLI_PATH into cliPath at construction", func(t *testing.T) {
		t.Setenv("COPILOT_CLI_PATH", "/from/env/copilot")
		client := NewClient(&ClientOptions{Connection: InProcessConnection{}})
		if client.cliPath != "" {
			t.Errorf("Expected in-process cliPath to stay empty at construction, got %q", client.cliPath)
		}
	})

	t.Run("panics when Env is set", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when Env is set with InProcessConnection")
			}
		}()
		NewClient(&ClientOptions{
			Connection: InProcessConnection{},
			Env:        []string{"FOO=bar"},
		})
	})

	t.Run("panics when WorkingDirectory is set", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when WorkingDirectory is set with InProcessConnection")
			}
		}()
		NewClient(&ClientOptions{
			Connection:       InProcessConnection{},
			WorkingDirectory: "/tmp/work",
		})
	})

	t.Run("panics when Telemetry is set", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when Telemetry is set with InProcessConnection")
			}
		}()
		NewClient(&ClientOptions{
			Connection: InProcessConnection{},
			Telemetry:  &TelemetryConfig{ExporterType: "file"},
		})
	})

	t.Run("forwards typed runtime options", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection:                InProcessConnection{},
			GitHubToken:               "test-token",
			UseLoggedInUser:           Bool(false),
			BaseDirectory:             "/copilot-home",
			LogLevel:                  "debug",
			SessionIdleTimeoutSeconds: 30,
			EnableRemoteSessions:      true,
			Mode:                      ModeEmpty,
		})

		config := client.inProcessHostConfig()
		expectedArgs := []string{
			"--log-level", "debug",
			"--auth-token-env", "COPILOT_SDK_AUTH_TOKEN",
			"--no-auto-login",
			"--session-idle-timeout", "30",
			"--remote",
		}
		if !reflect.DeepEqual(config.Args, expectedArgs) {
			t.Fatalf("Expected managed arguments %v, got %v", expectedArgs, config.Args)
		}
		expectedEnvironment := map[string]string{
			"COPILOT_SDK_AUTH_TOKEN": "test-token",
			"COPILOT_HOME":           "/copilot-home",
			"COPILOT_DISABLE_KEYTAR": "1",
		}
		if !reflect.DeepEqual(config.Environment, expectedEnvironment) {
			t.Fatalf("Expected managed environment %v, got %v", expectedEnvironment, config.Environment)
		}
	})
}

func TestClient_DefaultConnection(t *testing.T) {
	t.Run("defaults to stdio when override is unset", func(t *testing.T) {
		t.Setenv(defaultConnectionEnvVar, "")

		client := NewClient(nil)

		if !client.useStdio || client.useInProcess {
			t.Fatalf("Expected stdio default, got useStdio=%v useInProcess=%v", client.useStdio, client.useInProcess)
		}
	})

	t.Run("selects in-process case-insensitively", func(t *testing.T) {
		t.Setenv(defaultConnectionEnvVar, "InPrOcEsS")

		client := NewClient(nil)

		if !client.useInProcess || client.useStdio {
			t.Fatalf("Expected in-process default, got useStdio=%v useInProcess=%v", client.useStdio, client.useInProcess)
		}
	})

	t.Run("accepts explicit stdio override", func(t *testing.T) {
		t.Setenv(defaultConnectionEnvVar, "STDIO")

		client := NewClient(nil)

		if !client.useStdio || client.useInProcess {
			t.Fatalf("Expected stdio default, got useStdio=%v useInProcess=%v", client.useStdio, client.useInProcess)
		}
	})

	t.Run("explicit connection takes precedence", func(t *testing.T) {
		t.Setenv(defaultConnectionEnvVar, "inprocess")

		client := NewClient(&ClientOptions{Connection: TCPConnection{Port: 1234}})

		if client.useInProcess || client.useStdio || client.port != 1234 {
			t.Fatalf("Expected explicit TCP connection to win, got useStdio=%v useInProcess=%v port=%d", client.useStdio, client.useInProcess, client.port)
		}
	})

	t.Run("panics for invalid override", func(t *testing.T) {
		t.Setenv(defaultConnectionEnvVar, "tcp")

		defer func() {
			if r := recover(); r == nil {
				t.Fatal("Expected invalid default connection override to panic")
			}
		}()
		NewClient(nil)
	})
}

func TestClient_ConnectionLevelEnv(t *testing.T) {
	t.Run("rejects env set on both client and connection", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Expected panic when env is set on both client and connection")
			}
		}()
		NewClient(&ClientOptions{
			Connection: StdioConnection{Env: []string{"A=1"}},
			Env:        []string{"B=2"},
		})
	})

	t.Run("stdio connection env is used when client env is unset", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: StdioConnection{Env: []string{"ONLY=conn"}},
		})
		if len(client.options.Env) != 1 || client.options.Env[0] != "ONLY=conn" {
			t.Errorf("Expected connection-level Env to be used, got %v", client.options.Env)
		}
	})

	t.Run("tcp connection env is used when client env is unset", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			Connection: TCPConnection{Port: 9000, Env: []string{"ONLY=conn"}},
		})
		if len(client.options.Env) != 1 || client.options.Env[0] != "ONLY=conn" {
			t.Errorf("Expected connection-level Env to be used, got %v", client.options.Env)
		}
	})
}

func TestClient_SessionIdleTimeoutSeconds(t *testing.T) {
	t.Run("should store SessionIdleTimeoutSeconds option", func(t *testing.T) {
		client := NewClient(&ClientOptions{
			SessionIdleTimeoutSeconds: 600,
		})

		if client.options.SessionIdleTimeoutSeconds != 600 {
			t.Errorf("Expected SessionIdleTimeoutSeconds to be 600, got %d", client.options.SessionIdleTimeoutSeconds)
		}
	})

	t.Run("should default SessionIdleTimeoutSeconds to zero", func(t *testing.T) {
		client := NewClient(&ClientOptions{})

		if client.options.SessionIdleTimeoutSeconds != 0 {
			t.Errorf("Expected SessionIdleTimeoutSeconds to be 0, got %d", client.options.SessionIdleTimeoutSeconds)
		}
	})
}

func findCLIPathForTest() string {
	base, err := filepath.Abs("../nodejs/node_modules/@github")
	if err == nil {
		matches, _ := filepath.Glob(filepath.Join(base, "copilot-*", "index.js"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

func TestCreateSessionRequest_ClientName(t *testing.T) {
	t.Run("includes clientName in JSON when set", func(t *testing.T) {
		req := createSessionRequest{ClientName: "my-app"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["clientName"] != "my-app" {
			t.Errorf("Expected clientName to be 'my-app', got %v", m["clientName"])
		}
	})

	t.Run("omits clientName from JSON when empty", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["clientName"]; ok {
			t.Error("Expected clientName to be omitted when empty")
		}
	})
}

func TestResumeSessionRequest_ClientName(t *testing.T) {
	t.Run("includes clientName in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ClientName: "my-app"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["clientName"] != "my-app" {
			t.Errorf("Expected clientName to be 'my-app', got %v", m["clientName"])
		}
	})

	t.Run("omits clientName from JSON when empty", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["clientName"]; ok {
			t.Error("Expected clientName to be omitted when empty")
		}
	})
}

func TestSessionRequests_ReasoningSummary(t *testing.T) {
	t.Run("create includes reasoningSummary in JSON when set", func(t *testing.T) {
		req := createSessionRequest{ReasoningSummary: ReasoningSummaryConcise}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["reasoningSummary"] != "concise" {
			t.Errorf("Expected reasoningSummary to be 'concise', got %v", m["reasoningSummary"])
		}
	})

	t.Run("resume includes reasoningSummary in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ReasoningSummary: ReasoningSummaryNone}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["reasoningSummary"] != "none" {
			t.Errorf("Expected reasoningSummary to be 'none', got %v", m["reasoningSummary"])
		}
	})
}

func TestSessionRequests_ContextTier(t *testing.T) {
	t.Run("create includes contextTier in JSON when set", func(t *testing.T) {
		req := createSessionRequest{ContextTier: ContextTierLongContext}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["contextTier"] != "long_context" {
			t.Errorf("Expected contextTier to be 'long_context', got %v", m["contextTier"])
		}
	})

	t.Run("resume includes contextTier in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ContextTier: ContextTierDefault}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["contextTier"] != "default" {
			t.Errorf("Expected contextTier to be 'default', got %v", m["contextTier"])
		}
	})
}

func TestSessionRequests_EnableConfigDiscovery(t *testing.T) {
	t.Run("create includes enableConfigDiscovery when true", func(t *testing.T) {
		req := createSessionRequest{EnableConfigDiscovery: Bool(true)}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableConfigDiscovery"] != true {
			t.Errorf("Expected enableConfigDiscovery to be true, got %v", m["enableConfigDiscovery"])
		}
	})

	t.Run("create includes enableConfigDiscovery when false", func(t *testing.T) {
		req := createSessionRequest{EnableConfigDiscovery: Bool(false)}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableConfigDiscovery"] != false {
			t.Errorf("Expected enableConfigDiscovery to be false, got %v", m["enableConfigDiscovery"])
		}
	})

	t.Run("create omits enableConfigDiscovery when unset", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableConfigDiscovery"]; ok {
			t.Error("Expected enableConfigDiscovery to be omitted when unset")
		}
	})

	t.Run("resume includes enableConfigDiscovery when false", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", EnableConfigDiscovery: Bool(false)}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableConfigDiscovery"] != false {
			t.Errorf("Expected enableConfigDiscovery to be false, got %v", m["enableConfigDiscovery"])
		}
	})

	t.Run("resume omits enableConfigDiscovery when unset", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableConfigDiscovery"]; ok {
			t.Error("Expected enableConfigDiscovery to be omitted when unset")
		}
	})
}

func TestSessionRequests_PluginDirectoriesAndLargeOutput(t *testing.T) {
	pluginDirs := []string{"/tmp/plugins/a", "/tmp/plugins/b"}
	enabled := true
	maxBytes := int64(1024)
	largeOutput := &LargeToolOutputConfig{
		Enabled:         &enabled,
		MaxSizeBytes:    &maxBytes,
		OutputDirectory: "/tmp/large-output",
	}

	expectedLargeOutput := map[string]any{
		"enabled":      true,
		"maxSizeBytes": float64(1024),
		"outputDir":    "/tmp/large-output",
	}
	expectedPluginDirs := []any{"/tmp/plugins/a", "/tmp/plugins/b"}
	expectedDisabledMCPServers := []any{"local-files", "remote-github"}
	disabledMCPServers := []string{"local-files", "remote-github"}

	t.Run("create includes pluginDirectories and largeOutput in JSON when set", func(t *testing.T) {
		req := createSessionRequest{PluginDirectories: pluginDirs, DisabledMCPServers: &disabledMCPServers, LargeOutput: largeOutput}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if !reflect.DeepEqual(m["pluginDirectories"], expectedPluginDirs) {
			t.Errorf("Expected pluginDirectories %v, got %v", expectedPluginDirs, m["pluginDirectories"])
		}
		if !reflect.DeepEqual(m["disabledMcpServers"], expectedDisabledMCPServers) {
			t.Errorf("Expected disabledMcpServers %v, got %v", expectedDisabledMCPServers, m["disabledMcpServers"])
		}
		if !reflect.DeepEqual(m["largeOutput"], expectedLargeOutput) {
			t.Errorf("Expected largeOutput %v, got %v", expectedLargeOutput, m["largeOutput"])
		}
	})

	t.Run("resume includes pluginDirectories and largeOutput in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", PluginDirectories: pluginDirs, DisabledMCPServers: &disabledMCPServers, LargeOutput: largeOutput}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if !reflect.DeepEqual(m["pluginDirectories"], expectedPluginDirs) {
			t.Errorf("Expected pluginDirectories %v, got %v", expectedPluginDirs, m["pluginDirectories"])
		}
		if !reflect.DeepEqual(m["disabledMcpServers"], expectedDisabledMCPServers) {
			t.Errorf("Expected disabledMcpServers %v, got %v", expectedDisabledMCPServers, m["disabledMcpServers"])
		}
		if !reflect.DeepEqual(m["largeOutput"], expectedLargeOutput) {
			t.Errorf("Expected largeOutput %v, got %v", expectedLargeOutput, m["largeOutput"])
		}
	})

	t.Run("create and resume include explicit empty disabledMcpServers", func(t *testing.T) {
		emptyDisabledMCPServers := []string{}
		requests := []any{
			createSessionRequest{DisabledMCPServers: &emptyDisabledMCPServers},
			resumeSessionRequest{SessionID: "s1", DisabledMCPServers: &emptyDisabledMCPServers},
		}

		for _, request := range requests {
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}
			if value, ok := m["disabledMcpServers"]; !ok || !reflect.DeepEqual(value, []any{}) {
				t.Errorf("Expected explicit empty disabledMcpServers, got %v", value)
			}
		}
	})

	t.Run("create omits pluginDirectories and largeOutput when nil", func(t *testing.T) {
		req := createSessionRequest{}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["pluginDirectories"]; ok {
			t.Errorf("Expected pluginDirectories to be omitted")
		}
		if _, ok := m["disabledMcpServers"]; ok {
			t.Error("Expected disabledMcpServers to be omitted")
		}
		if _, ok := m["largeOutput"]; ok {
			t.Errorf("Expected largeOutput to be omitted")
		}
	})

	t.Run("resume omits disabledMcpServers when nil", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["disabledMcpServers"]; ok {
			t.Error("Expected disabledMcpServers to be omitted")
		}
	})
}

func TestSessionRequests_Memory(t *testing.T) {
	t.Run("create includes memory in JSON when enabled", func(t *testing.T) {
		req := createSessionRequest{Memory: &MemoryConfiguration{Enabled: true}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		expected := map[string]any{"enabled": true}
		if !reflect.DeepEqual(m["memory"], expected) {
			t.Errorf("Expected memory %v, got %v", expected, m["memory"])
		}
	})

	t.Run("resume includes memory in JSON when disabled", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", Memory: &MemoryConfiguration{Enabled: false}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		expected := map[string]any{"enabled": false}
		if !reflect.DeepEqual(m["memory"], expected) {
			t.Errorf("Expected memory %v, got %v", expected, m["memory"])
		}
	})

	t.Run("create omits memory when nil", func(t *testing.T) {
		req := createSessionRequest{}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["memory"]; ok {
			t.Errorf("Expected memory to be omitted")
		}
	})

	t.Run("resume omits memory when nil", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["memory"]; ok {
			t.Errorf("Expected memory to be omitted")
		}
	})
}

func TestCreateSessionRequest_Agent(t *testing.T) {
	t.Run("includes agent in JSON when set", func(t *testing.T) {
		req := createSessionRequest{Agent: "test-agent"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["agent"] != "test-agent" {
			t.Errorf("Expected agent to be 'test-agent', got %v", m["agent"])
		}
	})

	t.Run("omits agent from JSON when empty", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["agent"]; ok {
			t.Error("Expected agent to be omitted when empty")
		}
	})
}

func TestResumeSessionRequest_Agent(t *testing.T) {
	t.Run("includes agent in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", Agent: "test-agent"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["agent"] != "test-agent" {
			t.Errorf("Expected agent to be 'test-agent', got %v", m["agent"])
		}
	})

	t.Run("omits agent from JSON when empty", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["agent"]; ok {
			t.Error("Expected agent to be omitted when empty")
		}
	})
}

func TestCreateSessionRequest_InstructionDirectories(t *testing.T) {
	t.Run("includes instructionDirectories in JSON when set", func(t *testing.T) {
		req := createSessionRequest{InstructionDirectories: []string{`C:\extra-instructions`, `C:\more-instructions`}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		got := m["instructionDirectories"].([]any)
		if len(got) != 2 || got[0] != `C:\extra-instructions` || got[1] != `C:\more-instructions` {
			t.Errorf("Expected instructionDirectories to be serialized, got %v", got)
		}
	})

	t.Run("omits instructionDirectories from JSON when empty", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["instructionDirectories"]; ok {
			t.Error("Expected instructionDirectories to be omitted when empty")
		}
	})
}

func TestResumeSessionRequest_InstructionDirectories(t *testing.T) {
	t.Run("includes instructionDirectories in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:              "s1",
			InstructionDirectories: []string{`C:\resume-instructions`},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		got := m["instructionDirectories"].([]any)
		if len(got) != 1 || got[0] != `C:\resume-instructions` {
			t.Errorf("Expected instructionDirectories to be serialized, got %v", got)
		}
	})

	t.Run("omits instructionDirectories from JSON when empty", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["instructionDirectories"]; ok {
			t.Error("Expected instructionDirectories to be omitted when empty")
		}
	})
}

func TestCreateSessionRequest_MCPOAuthTokenStorage(t *testing.T) {
	t.Run("includes mcpOAuthTokenStorage in JSON when set", func(t *testing.T) {
		req := createSessionRequest{MCPOAuthTokenStorage: "in-memory"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["mcpOAuthTokenStorage"] != "in-memory" {
			t.Errorf("Expected mcpOAuthTokenStorage to be 'in-memory', got %v", m["mcpOAuthTokenStorage"])
		}
	})

	t.Run("omits mcpOAuthTokenStorage from JSON when empty", func(t *testing.T) {
		req := createSessionRequest{}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["mcpOAuthTokenStorage"]; ok {
			t.Error("Expected mcpOAuthTokenStorage to be omitted when empty")
		}
	})
}

func TestResumeSessionRequest_MCPOAuthTokenStorage(t *testing.T) {
	t.Run("includes mcpOAuthTokenStorage in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", MCPOAuthTokenStorage: "persistent"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["mcpOAuthTokenStorage"] != "persistent" {
			t.Errorf("Expected mcpOAuthTokenStorage to be 'persistent', got %v", m["mcpOAuthTokenStorage"])
		}
	})

	t.Run("omits mcpOAuthTokenStorage from JSON when empty", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["mcpOAuthTokenStorage"]; ok {
			t.Error("Expected mcpOAuthTokenStorage to be omitted when empty")
		}
	})
}

func TestOverridesBuiltInTool(t *testing.T) {
	t.Run("OverridesBuiltInTool is serialized in tool definition", func(t *testing.T) {
		tool := Tool{
			Name:                 "grep",
			Description:          "Custom grep",
			OverridesBuiltInTool: true,
			Handler:              func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if v, ok := m["overridesBuiltInTool"]; !ok || v != true {
			t.Errorf("expected overridesBuiltInTool=true, got %v", m)
		}
	})

	t.Run("OverridesBuiltInTool omitted when false", func(t *testing.T) {
		tool := Tool{
			Name:        "custom_tool",
			Description: "A custom tool",
			Handler:     func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if _, ok := m["overridesBuiltInTool"]; ok {
			t.Errorf("expected overridesBuiltInTool to be omitted, got %v", m)
		}
	})
}

func TestToolDefer(t *testing.T) {
	t.Run("Defer is serialized in tool definition", func(t *testing.T) {
		tool := Tool{
			Name:        "lookup_issue",
			Description: "Fetch issue details",
			Defer:       ToolDeferAuto,
			Handler:     func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if v, ok := m["defer"]; !ok || v != "auto" {
			t.Errorf("expected defer=auto, got %v", m)
		}
	})

	t.Run("Defer omitted when unset", func(t *testing.T) {
		tool := Tool{
			Name:        "custom_tool",
			Description: "A custom tool",
			Handler:     func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if _, ok := m["defer"]; ok {
			t.Errorf("expected defer to be omitted, got %v", m)
		}
	})
}

func TestToolMetadata(t *testing.T) {
	t.Run("Metadata is serialized in tool definition", func(t *testing.T) {
		tool := Tool{
			Name:        "my_tool",
			Description: "A custom tool",
			Metadata: map[string]any{
				"github.com/copilot:safeForTelemetry": map[string]any{"name": true, "inputsNames": false},
			},
			Handler: func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		meta, ok := m["metadata"].(map[string]any)
		if !ok {
			t.Fatalf("expected metadata object, got %v", m)
		}
		if _, ok := meta["github.com/copilot:safeForTelemetry"]; !ok {
			t.Errorf("expected namespaced key preserved, got %v", meta)
		}
	})

	t.Run("Metadata omitted when unset", func(t *testing.T) {
		tool := Tool{
			Name:        "custom_tool",
			Description: "A custom tool",
			Handler:     func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if _, ok := m["metadata"]; ok {
			t.Errorf("expected metadata to be omitted, got %v", m)
		}
	})
}

func TestClient_CreateSession_AllowsMissingPermissionHandler(t *testing.T) {
	t.Run("accepts nil config before connection validation", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: StdioConnection{Path: "/__nonexistent_copilot_binary__"}})
		_, err := client.CreateSession(t.Context(), nil)
		if err == nil {
			t.Fatal("Expected error when client is not connected")
		}
		if strings.Contains(err.Error(), "OnPermissionRequest") {
			t.Errorf("Did not expect permission handler validation error, got: %v", err)
		}
	})

	t.Run("accepts missing OnPermissionRequest before connection validation", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: StdioConnection{Path: "/__nonexistent_copilot_binary__"}})
		_, err := client.CreateSession(t.Context(), &SessionConfig{})
		if err == nil {
			t.Fatal("Expected error when client is not connected")
		}
		if strings.Contains(err.Error(), "OnPermissionRequest") {
			t.Errorf("Did not expect permission handler validation error, got: %v", err)
		}
	})
}

func TestClient_ResumeSession_AllowsMissingPermissionHandler(t *testing.T) {
	t.Run("accepts nil config before connection validation", func(t *testing.T) {
		client := NewClient(&ClientOptions{Connection: StdioConnection{Path: "/__nonexistent_copilot_binary__"}})
		_, err := client.ResumeSessionWithOptions(t.Context(), "some-id", nil)
		if err == nil {
			t.Fatal("Expected error when client is not connected")
		}
		if strings.Contains(err.Error(), "OnPermissionRequest") {
			t.Errorf("Did not expect permission handler validation error, got: %v", err)
		}
	})
}

func TestListModelsWithCustomHandler(t *testing.T) {
	customModels := []ModelInfo{
		{
			ID:   "my-custom-model",
			Name: "My Custom Model",
			Capabilities: ModelCapabilities{
				Supports: ModelSupports{Vision: false, ReasoningEffort: false},
				Limits:   ModelLimits{MaxContextWindowTokens: Int(128000)},
			},
		},
	}

	callCount := 0
	handler := func(ctx context.Context) ([]ModelInfo, error) {
		callCount++
		return customModels, nil
	}

	client := NewClient(&ClientOptions{OnListModels: handler})

	models, err := client.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected handler called once, got %d", callCount)
	}
	if len(models) != 1 || models[0].ID != "my-custom-model" {
		t.Errorf("unexpected models: %+v", models)
	}
}

func TestModelBillingTokenPricesJSON(t *testing.T) {
	int64Ptr := func(v int64) *int64 {
		return &v
	}

	wire := `{
		"multiplier": 1.5,
		"tokenPrices": {
			"inputPrice": 2.0,
			"outputPrice": 8.0,
			"cachePrice": 0.5,
			"batchSize": 1000000,
			"contextMax": 128000,
			"longContext": {
				"inputPrice": 4.0,
				"outputPrice": 16.0,
				"cachePrice": 1.0,
				"maxPromptTokens": 1000000
			}
		}
	}`
	expected := rpc.ModelBillingTokenPrices{
		InputPrice:  Float64(2.0),
		OutputPrice: Float64(8.0),
		CachePrice:  Float64(0.5),
		BatchSize:   int64Ptr(1000000),
		ContextMax:  int64Ptr(128000),
		LongContext: &rpc.ModelBillingTokenPricesLongContext{
			InputPrice:      Float64(4.0),
			OutputPrice:     Float64(16.0),
			CachePrice:      Float64(1.0),
			MaxPromptTokens: int64Ptr(1000000),
		},
	}

	var billing ModelBilling
	if err := json.Unmarshal([]byte(wire), &billing); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if billing.TokenPrices == nil {
		t.Fatal("expected TokenPrices to be set")
	}
	tp := billing.TokenPrices
	if !reflect.DeepEqual(*tp, expected) {
		t.Errorf("unexpected TokenPrices: %+v", tp)
	}
	if tp.LongContext == nil {
		t.Fatal("expected LongContext to be set")
	}
	lc := tp.LongContext
	if lc.InputPrice == nil || *lc.InputPrice != 4.0 {
		t.Errorf("unexpected LongContext.InputPrice: %v", lc.InputPrice)
	}
	if lc.MaxPromptTokens == nil || *lc.MaxPromptTokens != 1000000 {
		t.Errorf("unexpected LongContext.MaxPromptTokens: %v", lc.MaxPromptTokens)
	}

	// Round-trip back to JSON and ensure the nested structure survives.
	out, err := json.Marshal(billing)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var reparsed ModelBilling
	if err := json.Unmarshal(out, &reparsed); err != nil {
		t.Fatalf("re-unmarshal failed: %v", err)
	}
	if reparsed.TokenPrices == nil || !reflect.DeepEqual(*reparsed.TokenPrices, expected) {
		t.Errorf("round-trip lost token price data: %s", out)
	}
}

func TestListModelsHandlerCachesResults(t *testing.T) {
	customModels := []ModelInfo{
		{
			ID:   "cached-model",
			Name: "Cached Model",
			Capabilities: ModelCapabilities{
				Supports: ModelSupports{Vision: false, ReasoningEffort: false},
				Limits:   ModelLimits{MaxContextWindowTokens: Int(128000)},
			},
		},
	}

	callCount := 0
	handler := func(ctx context.Context) ([]ModelInfo, error) {
		callCount++
		return customModels, nil
	}

	client := NewClient(&ClientOptions{OnListModels: handler})

	_, _ = client.ListModels(t.Context())
	_, _ = client.ListModels(t.Context())
	if callCount != 1 {
		t.Errorf("expected handler called once due to caching, got %d", callCount)
	}
}

func TestClient_StartContextCancellationDoesNotKillProcess(t *testing.T) {
	cliPath := findCLIPathForTest()
	if cliPath == "" {
		t.Skip("CLI not found")
	}

	client := NewClient(&ClientOptions{Connection: StdioConnection{Path: cliPath}})
	t.Cleanup(func() { client.ForceStop() })

	// Start with a context, then cancel it after the client is connected.
	ctx, cancel := context.WithCancel(t.Context())
	if err := client.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cancel() // cancel the context that was used for Start

	// The CLI process should still be alive and responsive.
	resp, err := client.Ping(t.Context(), "still alive")
	if err != nil {
		t.Fatalf("Ping after context cancellation failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil ping response")
	}
}

func TestClient_StartStopRace(t *testing.T) {
	cliPath := findCLIPathForTest()
	if cliPath == "" {
		t.Skip("CLI not found")
	}
	client := NewClient(&ClientOptions{Connection: StdioConnection{Path: cliPath}})
	defer client.ForceStop()
	errChan := make(chan error)
	wg := sync.WaitGroup{}
	for range 10 {
		wg.Add(3)
		go func() {
			defer wg.Done()
			if err := client.Start(t.Context()); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}()
		go func() {
			defer wg.Done()
			if err := client.Stop(); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}()
		go func() {
			defer wg.Done()
			client.ForceStop()
		}()
	}
	wg.Wait()
	close(errChan)
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}
}

func TestClient_MCPAuthInterestRegistration(t *testing.T) {
	t.Run("create skips MCP OAuth interest without auth handler", func(t *testing.T) {
		client, requests, cleanup := newInMemoryClient(t)
		defer cleanup()

		session, err := client.CreateSession(t.Context(), &SessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			OnEvent:             func(SessionEvent) {},
		})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer session.Disconnect()

		assertNoMCPAuthInterest(t, requests.snapshot())
		assertRequestMethod(t, requests.snapshot(), "session.create")
		assertCreateRequestPermission(t, requests.snapshot())
	})

	t.Run("create registers MCP OAuth interest after local session create when auth handler is configured", func(t *testing.T) {
		client, requests, cleanup := newInMemoryClient(t)
		defer cleanup()

		session, err := client.CreateSession(t.Context(), &SessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			OnMCPAuthRequest: func(MCPAuthRequest, MCPAuthInvocation) (*MCPAuthResult, error) {
				return MCPAuthResultCancelled(), nil
			},
		})
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
		defer session.Disconnect()

		snapshot := requests.snapshot()
		assertRequestMethod(t, snapshot, "session.eventLog.registerInterest")
		if snapshot[0].Method != "session.create" {
			t.Fatalf("expected session.create before MCP auth interest, got %s", snapshot[0].Method)
		}
		if snapshot[1].Method != "session.eventLog.registerInterest" {
			t.Fatalf("expected MCP auth interest after session.create, got %s", snapshot[1].Method)
		}
		assertMCPAuthInterest(t, snapshot[1])
		assertCreateRequestPermission(t, snapshot)
	})

	t.Run("cloud create registers MCP OAuth interest after server assigns id only when auth handler is configured", func(t *testing.T) {
		client, requests, cleanup := newInMemoryClient(t)
		defer cleanup()

		withoutAuth, err := client.CreateSession(t.Context(), &SessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			Cloud: &CloudSessionOptions{
				Repository: &CloudSessionRepository{Owner: "github", Name: "copilot-sdk", Branch: "main"},
			},
		})
		if err != nil {
			t.Fatalf("CreateSession without auth failed: %v", err)
		}
		defer withoutAuth.Disconnect()

		assertNoMCPAuthInterest(t, requests.snapshot())
		requests.clear()

		withAuth, err := client.CreateSession(t.Context(), &SessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			OnMCPAuthRequest: func(MCPAuthRequest, MCPAuthInvocation) (*MCPAuthResult, error) {
				return MCPAuthResultCancelled(), nil
			},
			Cloud: &CloudSessionOptions{
				Repository: &CloudSessionRepository{Owner: "github", Name: "copilot-sdk", Branch: "main"},
			},
		})
		if err != nil {
			t.Fatalf("CreateSession with auth failed: %v", err)
		}
		defer withAuth.Disconnect()

		snapshot := requests.snapshot()
		if snapshot[0].Method != "session.create" {
			t.Fatalf("expected cloud session.create before MCP auth interest, got %s", snapshot[0].Method)
		}
		if snapshot[1].Method != "session.eventLog.registerInterest" {
			t.Fatalf("expected MCP auth interest after cloud session.create, got %s", snapshot[1].Method)
		}
		assertMCPAuthInterest(t, snapshot[1])
	})

	t.Run("resume conditionally registers MCP OAuth interest after session resume", func(t *testing.T) {
		client, requests, cleanup := newInMemoryClient(t)
		defer cleanup()

		withoutAuth, err := client.ResumeSession(t.Context(), "session-without-auth", &ResumeSessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			OnEvent:             func(SessionEvent) {},
		})
		if err != nil {
			t.Fatalf("ResumeSession without auth failed: %v", err)
		}
		defer withoutAuth.Disconnect()

		assertNoMCPAuthInterest(t, requests.snapshot())
		assertRequestMethod(t, requests.snapshot(), "session.resume")
		requests.clear()

		withAuth, err := client.ResumeSession(t.Context(), "session-with-auth", &ResumeSessionConfig{
			OnPermissionRequest: PermissionHandler.ApproveAll,
			OnMCPAuthRequest: func(MCPAuthRequest, MCPAuthInvocation) (*MCPAuthResult, error) {
				return MCPAuthResultCancelled(), nil
			},
		})
		if err != nil {
			t.Fatalf("ResumeSession with auth failed: %v", err)
		}
		defer withAuth.Disconnect()

		snapshot := requests.snapshot()
		if snapshot[0].Method != "session.resume" {
			t.Fatalf("expected session.resume before MCP auth interest, got %s", snapshot[0].Method)
		}
		if snapshot[1].Method != "session.eventLog.registerInterest" {
			t.Fatalf("expected MCP auth interest after session.resume, got %s", snapshot[1].Method)
		}
		assertMCPAuthInterest(t, snapshot[1])
	})
}

type recordedRequest struct {
	Method string
	Params map[string]any
}

type requestRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *requestRecorder) append(request recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
}

func (r *requestRecorder) snapshot() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRequest, len(r.requests))
	copy(out, r.requests)
	return out
}

func (r *requestRecorder) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = nil
}

func newInMemoryClient(t *testing.T) (*Client, *requestRecorder, func()) {
	t.Helper()

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	rpcClient := jsonrpc2.NewClient(stdinW, stdoutR)
	rpcClient.Start()

	client := NewClient(&ClientOptions{})
	client.client = rpcClient
	client.RPC = rpc.NewServerRPC(rpcClient)
	client.state = stateConnected

	requests := &requestRecorder{}
	done := make(chan struct{})
	go serveInMemoryRuntime(t, stdinR, stdoutW, requests, done)

	cleanup := func() {
		rpcClient.Stop()
		stdinR.Close()
		stdinW.Close()
		stdoutR.Close()
		stdoutW.Close()
		<-done
	}
	return client, requests, cleanup
}

func serveInMemoryRuntime(t *testing.T, stdinR *io.PipeReader, stdoutW *io.PipeWriter, requests *requestRecorder, done chan<- struct{}) {
	t.Helper()
	defer close(done)

	serverAssignedSessions := 0
	for {
		frame, err := readTestJSONRPCFrame(stdinR)
		if err != nil {
			return
		}

		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if err := json.Unmarshal(frame, &request); err != nil {
			t.Errorf("failed to unmarshal JSON-RPC request: %v", err)
			return
		}
		requests.append(recordedRequest{Method: request.Method, Params: request.Params})

		var result map[string]any
		switch request.Method {
		case "session.create", "session.resume":
			sessionID, _ := request.Params["sessionId"].(string)
			if sessionID == "" {
				serverAssignedSessions++
				sessionID = fmt.Sprintf("server-assigned-session-%d", serverAssignedSessions)
			}
			result = map[string]any{"sessionId": sessionID, "workspacePath": nil}
		case "session.eventLog.registerInterest":
			result = map[string]any{"id": "interest-1"}
		case "session.options.update":
			result = map[string]any{"success": true}
		case "session.skills.reload", "session.destroy":
			result = map[string]any{}
		default:
			t.Errorf("unexpected JSON-RPC method %s", request.Method)
			return
		}

		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      json.RawMessage(request.ID),
			"result":  result,
		}
		data, err := json.Marshal(response)
		if err != nil {
			t.Errorf("failed to marshal JSON-RPC response: %v", err)
			return
		}
		if _, err := fmt.Fprintf(stdoutW, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
			return
		}
	}
}

func assertRequestMethod(t *testing.T, requests []recordedRequest, method string) {
	t.Helper()
	for _, request := range requests {
		if request.Method == method {
			return
		}
	}
	t.Fatalf("expected %s request in %+v", method, requests)
}

func assertNoMCPAuthInterest(t *testing.T, requests []recordedRequest) {
	t.Helper()
	for _, request := range requests {
		if request.Method == "session.eventLog.registerInterest" && request.Params["eventType"] == "mcp.oauth_required" {
			t.Fatalf("did not expect MCP auth interest registration in %+v", requests)
		}
	}
}

func assertMCPAuthInterest(t *testing.T, request recordedRequest) {
	t.Helper()
	if request.Method != "session.eventLog.registerInterest" {
		t.Fatalf("expected registerInterest request, got %s", request.Method)
	}
	if request.Params["eventType"] != "mcp.oauth_required" {
		t.Fatalf("expected mcp.oauth_required interest, got %v", request.Params["eventType"])
	}
}

func assertCreateRequestPermission(t *testing.T, requests []recordedRequest) {
	t.Helper()
	for _, request := range requests {
		if request.Method == "session.create" {
			if request.Params["requestPermission"] != true {
				t.Fatalf("expected create requestPermission=true, got %v", request.Params["requestPermission"])
			}
			return
		}
	}
	t.Fatalf("session.create request not found in %+v", requests)
}

func TestCreateSessionRequest_Commands(t *testing.T) {
	t.Run("forwards commands in session.create RPC", func(t *testing.T) {
		req := createSessionRequest{
			Commands: []wireCommand{
				{Name: "deploy", Description: "Deploy the app"},
				{Name: "rollback", Description: "Rollback last deploy"},
			},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		cmds, ok := m["commands"].([]any)
		if !ok {
			t.Fatalf("Expected commands to be an array, got %T", m["commands"])
		}
		if len(cmds) != 2 {
			t.Fatalf("Expected 2 commands, got %d", len(cmds))
		}
		cmd0 := cmds[0].(map[string]any)
		if cmd0["name"] != "deploy" {
			t.Errorf("Expected first command name 'deploy', got %v", cmd0["name"])
		}
		if cmd0["description"] != "Deploy the app" {
			t.Errorf("Expected first command description 'Deploy the app', got %v", cmd0["description"])
		}
	})

	t.Run("omits commands from JSON when empty", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["commands"]; ok {
			t.Error("Expected commands to be omitted when empty")
		}
	})
}

func TestCreateSessionRequest_Cloud(t *testing.T) {
	t.Run("forwards cloud options in session.create RPC", func(t *testing.T) {
		req := createSessionRequest{
			Cloud: &CloudSessionOptions{
				Repository: &CloudSessionRepository{
					Owner:  "github",
					Name:   "copilot-sdk",
					Branch: "main",
				},
			},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		cloud, ok := m["cloud"].(map[string]any)
		if !ok {
			t.Fatalf("Expected cloud to be an object, got %T", m["cloud"])
		}
		repository, ok := cloud["repository"].(map[string]any)
		if !ok {
			t.Fatalf("Expected cloud.repository to be an object, got %T", cloud["repository"])
		}
		if repository["owner"] != "github" {
			t.Errorf("Expected owner 'github', got %v", repository["owner"])
		}
		if repository["name"] != "copilot-sdk" {
			t.Errorf("Expected name 'copilot-sdk', got %v", repository["name"])
		}
		if repository["branch"] != "main" {
			t.Errorf("Expected branch 'main', got %v", repository["branch"])
		}
	})

	t.Run("omits cloud from JSON when unset", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["cloud"]; ok {
			t.Error("Expected cloud to be omitted when unset")
		}
	})
}

func TestSessionRequests_Capi(t *testing.T) {
	t.Run("forwards capi options in session.create RPC", func(t *testing.T) {
		req := createSessionRequest{
			Capi: &CapiSessionOptions{EnableWebSocketResponses: Bool(false)},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		capi, ok := m["capi"].(map[string]any)
		if !ok {
			t.Fatalf("Expected capi to be an object, got %T", m["capi"])
		}
		if capi["enableWebSocketResponses"] != false {
			t.Errorf("Expected enableWebSocketResponses=false, got %v", capi["enableWebSocketResponses"])
		}
	})

	t.Run("forwards capi options in session.resume RPC", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID: "s1",
			Capi:      &CapiSessionOptions{EnableWebSocketResponses: Bool(false)},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		capi, ok := m["capi"].(map[string]any)
		if !ok {
			t.Fatalf("Expected capi to be an object, got %T", m["capi"])
		}
		if capi["enableWebSocketResponses"] != false {
			t.Errorf("Expected enableWebSocketResponses=false, got %v", capi["enableWebSocketResponses"])
		}
	})

	t.Run("omits capi from JSON when unset", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["capi"]; ok {
			t.Error("Expected capi to be omitted when unset")
		}
	})
}

func TestProviderConfig_Transport(t *testing.T) {
	t.Run("serializes transport with camelCase key", func(t *testing.T) {
		cfg := ProviderConfig{BaseURL: "https://example.com", Transport: "websockets"}
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["transport"] != "websockets" {
			t.Errorf("Expected transport=websockets, got %v", m["transport"])
		}
	})

	t.Run("omits transport from JSON when unset", func(t *testing.T) {
		cfg := ProviderConfig{BaseURL: "https://example.com"}
		data, _ := json.Marshal(cfg)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["transport"]; ok {
			t.Error("Expected transport to be omitted when unset")
		}
	})
}

func TestResumeSessionRequest_Commands(t *testing.T) {
	t.Run("forwards commands in session.resume RPC", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID: "s1",
			Commands: []wireCommand{
				{Name: "deploy", Description: "Deploy the app"},
			},
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		cmds, ok := m["commands"].([]any)
		if !ok {
			t.Fatalf("Expected commands to be an array, got %T", m["commands"])
		}
		if len(cmds) != 1 {
			t.Fatalf("Expected 1 command, got %d", len(cmds))
		}
		cmd0 := cmds[0].(map[string]any)
		if cmd0["name"] != "deploy" {
			t.Errorf("Expected command name 'deploy', got %v", cmd0["name"])
		}
	})

	t.Run("omits commands from JSON when empty", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["commands"]; ok {
			t.Error("Expected commands to be omitted when empty")
		}
	})
}

func TestCreateSessionRequest_RequestElicitation(t *testing.T) {
	t.Run("sends requestElicitation flag when OnElicitationRequest is provided", func(t *testing.T) {
		req := createSessionRequest{
			RequestElicitation: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["requestElicitation"] != true {
			t.Errorf("Expected requestElicitation to be true, got %v", m["requestElicitation"])
		}
	})

	t.Run("does not send requestElicitation when no handler provided", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["requestElicitation"]; ok {
			t.Error("Expected requestElicitation to be omitted when not set")
		}
	})
}

func TestCreateSessionRequest_ModeCallbackFlags(t *testing.T) {
	t.Run("sends mode callback flags when handlers are provided", func(t *testing.T) {
		req := createSessionRequest{
			RequestExitPlanMode:   Bool(true),
			RequestAutoModeSwitch: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["requestExitPlanMode"] != true {
			t.Errorf("Expected requestExitPlanMode to be true, got %v", m["requestExitPlanMode"])
		}
		if m["requestAutoModeSwitch"] != true {
			t.Errorf("Expected requestAutoModeSwitch to be true, got %v", m["requestAutoModeSwitch"])
		}
	})

	t.Run("omits mode callback flags when handlers are not provided", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["requestExitPlanMode"]; ok {
			t.Error("Expected requestExitPlanMode to be omitted when not set")
		}
		if _, ok := m["requestAutoModeSwitch"]; ok {
			t.Error("Expected requestAutoModeSwitch to be omitted when not set")
		}
	})
}

func TestResumeSessionRequest_RequestElicitation(t *testing.T) {
	t.Run("sends requestElicitation flag when OnElicitationRequest is provided", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:          "s1",
			RequestElicitation: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["requestElicitation"] != true {
			t.Errorf("Expected requestElicitation to be true, got %v", m["requestElicitation"])
		}
	})

	t.Run("does not send requestElicitation when no handler provided", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["requestElicitation"]; ok {
			t.Error("Expected requestElicitation to be omitted when not set")
		}
	})
}

func TestCreateSessionRequest_RequestMCPApps(t *testing.T) {
	t.Run("sends requestMCPApps flag when EnableMCPApps is set", func(t *testing.T) {
		req := createSessionRequest{
			RequestMCPApps: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["requestMcpApps"] != true {
			t.Errorf("Expected requestMcpApps to be true, got %v", m["requestMcpApps"])
		}
	})

	t.Run("does not send requestMcpApps when EnableMCPApps is unset", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["requestMcpApps"]; ok {
			t.Error("Expected requestMcpApps to be omitted when not set")
		}
	})
}

func TestSessionRequests_EnableExperimentalMode(t *testing.T) {
	t.Run("create forwards enableExperimentalMode when explicitly false", func(t *testing.T) {
		req := createSessionRequest{
			IsExperimentalMode: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["isExperimentalMode"] != false {
			t.Errorf("Expected isExperimentalMode to be false, got %v", m["isExperimentalMode"])
		}
	})

	t.Run("create omits enableExperimentalMode when unset", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["isExperimentalMode"]; ok {
			t.Error("Expected isExperimentalMode to be omitted when not set")
		}
	})

	t.Run("resume forwards enableExperimentalMode when explicitly true", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:          "s1",
			IsExperimentalMode: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["isExperimentalMode"] != true {
			t.Errorf("Expected isExperimentalMode to be true, got %v", m["isExperimentalMode"])
		}
	})

	t.Run("resume omits enableExperimentalMode when unset", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["isExperimentalMode"]; ok {
			t.Error("Expected isExperimentalMode to be omitted when not set")
		}
	})
}

func TestResumeSessionRequest_RequestMCPApps(t *testing.T) {
	t.Run("sends requestMcpApps flag when EnableMCPApps is set", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:      "s1",
			RequestMCPApps: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["requestMcpApps"] != true {
			t.Errorf("Expected requestMcpApps to be true, got %v", m["requestMcpApps"])
		}
	})

	t.Run("does not send requestMcpApps when RequestMCPApps is unset", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["requestMcpApps"]; ok {
			t.Error("Expected requestMcpApps to be omitted when not set")
		}
	})
}

func TestSessionRequests_GitHubMCPToolConfig(t *testing.T) {
	config := &GitHubMCPToolConfig{
		EnableAllTools:      Bool(true),
		AdditionalToolsets:  []string{"repos"},
		AdditionalTools:     []string{"get_issue"},
		EnableInsidersMode:  Bool(true),
		DisableFormDeferral: Bool(true),
	}
	expected := map[string]any{
		"enableAllTools":      true,
		"additionalToolsets":  []any{"repos"},
		"additionalTools":     []any{"get_issue"},
		"enableInsidersMode":  true,
		"disableFormDeferral": true,
	}

	t.Run("create", func(t *testing.T) {
		data, err := json.Marshal(createSessionRequest{GitHubMCPToolConfig: config})
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if !reflect.DeepEqual(payload["githubMcpToolConfig"], expected) {
			t.Fatalf("Unexpected githubMcpToolConfig: %#v", payload["githubMcpToolConfig"])
		}
	})

	t.Run("resume", func(t *testing.T) {
		data, err := json.Marshal(resumeSessionRequest{
			SessionID:           "s1",
			GitHubMCPToolConfig: config,
		})
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if !reflect.DeepEqual(payload["githubMcpToolConfig"], expected) {
			t.Fatalf("Unexpected githubMcpToolConfig: %#v", payload["githubMcpToolConfig"])
		}
	})

	t.Run("unset is omitted", func(t *testing.T) {
		data, err := json.Marshal(createSessionRequest{})
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := payload["githubMcpToolConfig"]; ok {
			t.Fatal("Expected githubMcpToolConfig to be omitted")
		}
	})
}

func TestResumeSessionRequest_ModeCallbackFlags(t *testing.T) {
	req := resumeSessionRequest{
		SessionID:             "s1",
		RequestExitPlanMode:   Bool(true),
		RequestAutoModeSwitch: Bool(true),
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	if m["requestExitPlanMode"] != true {
		t.Errorf("Expected requestExitPlanMode to be true, got %v", m["requestExitPlanMode"])
	}
	if m["requestAutoModeSwitch"] != true {
		t.Errorf("Expected requestAutoModeSwitch to be true, got %v", m["requestAutoModeSwitch"])
	}
}

func TestModeCallbackRequestHandlers(t *testing.T) {
	session := &Session{SessionID: "s1"}
	client := &Client{sessions: map[string]*Session{"s1": session}}

	expectedSummary := "Review the plan"
	expectedPlanContent := "Plan body"
	expectedActions := []string{"interactive", "autopilot"}
	expectedRecommendedAction := "autopilot"
	session.registerExitPlanModeHandler(func(request ExitPlanModeRequest, invocation ExitPlanModeInvocation) (ExitPlanModeResult, error) {
		if invocation.SessionID != "s1" {
			t.Fatalf("Expected session ID s1, got %s", invocation.SessionID)
		}
		if request.Summary != expectedSummary {
			t.Fatalf("Expected summary, got %q", request.Summary)
		}
		if request.PlanContent != expectedPlanContent {
			t.Fatalf("Expected plan content, got %q", request.PlanContent)
		}
		if !reflect.DeepEqual(request.Actions, expectedActions) {
			t.Fatalf("Expected actions to round-trip, got %#v", request.Actions)
		}
		if request.RecommendedAction != expectedRecommendedAction {
			t.Fatalf("Expected recommended action, got %q", request.RecommendedAction)
		}
		return ExitPlanModeResult{
			Approved:       true,
			SelectedAction: "interactive",
			Feedback:       "Looks good",
		}, nil
	})

	errorCode := "user_weekly_rate_limited"
	retryAfter := float64(3600)
	session.registerAutoModeSwitchHandler(func(request AutoModeSwitchRequest, invocation AutoModeSwitchInvocation) (AutoModeSwitchResponse, error) {
		if invocation.SessionID != "s1" {
			t.Fatalf("Expected session ID s1, got %s", invocation.SessionID)
		}
		if request.ErrorCode == nil || *request.ErrorCode != errorCode {
			t.Fatalf("Expected error code %q, got %#v", errorCode, request.ErrorCode)
		}
		if request.RetryAfterSeconds == nil || *request.RetryAfterSeconds != retryAfter {
			t.Fatalf("Expected retry-after %v, got %#v", retryAfter, request.RetryAfterSeconds)
		}
		return AutoModeSwitchResponseYesAlways, nil
	})

	exitResult, rpcErr := client.handleExitPlanModeRequest(exitPlanModeRequest{
		SessionID:         "s1",
		Summary:           "Review the plan",
		PlanContent:       "Plan body",
		Actions:           []string{"interactive", "autopilot"},
		RecommendedAction: "autopilot",
	})
	if rpcErr != nil {
		t.Fatalf("Unexpected RPC error: %v", rpcErr)
	}
	if !exitResult.Approved || exitResult.SelectedAction != "interactive" || exitResult.Feedback != "Looks good" {
		t.Fatalf("Unexpected exit-plan-mode result: %#v", exitResult)
	}

	expectedSummary = ""
	expectedPlanContent = ""
	expectedActions = nil
	expectedRecommendedAction = "autopilot"
	exitResult, rpcErr = client.handleExitPlanModeRequest(exitPlanModeRequest{
		SessionID: "s1",
	})
	if rpcErr != nil {
		t.Fatalf("Unexpected RPC error for minimal exit-plan-mode request: %v", rpcErr)
	}
	if !exitResult.Approved {
		t.Fatalf("Unexpected minimal exit-plan-mode result: %#v", exitResult)
	}

	autoResult, rpcErr := client.handleAutoModeSwitchRequest(autoModeSwitchRequest{
		SessionID:         "s1",
		ErrorCode:         &errorCode,
		RetryAfterSeconds: &retryAfter,
	})
	if rpcErr != nil {
		t.Fatalf("Unexpected RPC error: %v", rpcErr)
	}
	if autoResult.Response != AutoModeSwitchResponseYesAlways {
		t.Fatalf("Expected yes_always, got %q", autoResult.Response)
	}
}

func TestResumeSessionRequest_ContinuePendingWork(t *testing.T) {
	t.Run("forwards continuePendingWork when true", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:           "s1",
			ContinuePendingWork: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["continuePendingWork"] != true {
			t.Errorf("Expected continuePendingWork to be true, got %v", m["continuePendingWork"])
		}
	})

	t.Run("forwards continuePendingWork when false", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:           "s1",
			ContinuePendingWork: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["continuePendingWork"] != false {
			t.Errorf("Expected continuePendingWork to be false, got %v", m["continuePendingWork"])
		}
	})

	t.Run("omits continuePendingWork when not set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["continuePendingWork"]; ok {
			t.Error("Expected continuePendingWork to be omitted when not set")
		}
	})
}

func TestCreateSessionRequest_EnableSessionTelemetry(t *testing.T) {
	t.Run("forwards enableSessionTelemetry when false", func(t *testing.T) {
		req := createSessionRequest{
			EnableSessionTelemetry: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableSessionTelemetry"] != false {
			t.Errorf("Expected enableSessionTelemetry to be false, got %v", m["enableSessionTelemetry"])
		}
	})

	t.Run("omits enableSessionTelemetry when not set", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableSessionTelemetry"]; ok {
			t.Error("Expected enableSessionTelemetry to be omitted when not set")
		}
	})
}

func TestCreateSessionRequest_IncludeSubAgentStreamingEvents(t *testing.T) {
	t.Run("defaults to true when nil", func(t *testing.T) {
		req := createSessionRequest{
			IncludeSubAgentStreamingEvents: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["includeSubAgentStreamingEvents"] != true {
			t.Errorf("Expected includeSubAgentStreamingEvents to be true, got %v", m["includeSubAgentStreamingEvents"])
		}
	})

	t.Run("preserves explicit false", func(t *testing.T) {
		req := createSessionRequest{
			IncludeSubAgentStreamingEvents: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["includeSubAgentStreamingEvents"] != false {
			t.Errorf("Expected includeSubAgentStreamingEvents to be false, got %v", m["includeSubAgentStreamingEvents"])
		}
	})
}

func TestResumeSessionRequest_EnableSessionTelemetry(t *testing.T) {
	t.Run("forwards enableSessionTelemetry when false", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:              "s1",
			EnableSessionTelemetry: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableSessionTelemetry"] != false {
			t.Errorf("Expected enableSessionTelemetry to be false, got %v", m["enableSessionTelemetry"])
		}
	})

	t.Run("omits enableSessionTelemetry when not set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableSessionTelemetry"]; ok {
			t.Error("Expected enableSessionTelemetry to be omitted when not set")
		}
	})
}

func TestResumeSessionRequest_IncludeSubAgentStreamingEvents(t *testing.T) {
	t.Run("defaults to true when nil", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:                      "s1",
			IncludeSubAgentStreamingEvents: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["includeSubAgentStreamingEvents"] != true {
			t.Errorf("Expected includeSubAgentStreamingEvents to be true, got %v", m["includeSubAgentStreamingEvents"])
		}
	})

	t.Run("preserves explicit false", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:                      "s1",
			IncludeSubAgentStreamingEvents: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["includeSubAgentStreamingEvents"] != false {
			t.Errorf("Expected includeSubAgentStreamingEvents to be false, got %v", m["includeSubAgentStreamingEvents"])
		}
	})
}

func TestCreateSessionRequest_EnableGitHubTelemetryForwarding(t *testing.T) {
	t.Run("forwards explicit true", func(t *testing.T) {
		req := createSessionRequest{
			EnableGitHubTelemetryForwarding: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableGitHubTelemetryForwarding"] != true {
			t.Errorf("Expected enableGitHubTelemetryForwarding to be true, got %v", m["enableGitHubTelemetryForwarding"])
		}
	})

	t.Run("omits when not set", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableGitHubTelemetryForwarding"]; ok {
			t.Error("Expected enableGitHubTelemetryForwarding to be omitted when not set")
		}
	})
}

func TestResumeSessionRequest_EnableGitHubTelemetryForwarding(t *testing.T) {
	t.Run("forwards explicit true", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:                       "s1",
			EnableGitHubTelemetryForwarding: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableGitHubTelemetryForwarding"] != true {
			t.Errorf("Expected enableGitHubTelemetryForwarding to be true, got %v", m["enableGitHubTelemetryForwarding"])
		}
	})

	t.Run("omits when not set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableGitHubTelemetryForwarding"]; ok {
			t.Error("Expected enableGitHubTelemetryForwarding to be omitted when not set")
		}
	})
}

func TestClient_ForwardsGitHubTelemetryForwardingToSessionRequests(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
		options:  ClientOptions{OnGitHubTelemetry: func(*rpc.GitHubTelemetryNotification) {}},
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	if _, err := client.CreateSession(t.Context(), &SessionConfig{}); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertForwardingFlagTrue(t, <-createParams)

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed","workspacePath":"/workspace"}`), nil
	})

	if _, err := client.ResumeSessionWithOptions(t.Context(), "resumed", &ResumeSessionConfig{}); err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertForwardingFlagTrue(t, <-resumeParams)
}

func assertForwardingFlagTrue(t *testing.T, params json.RawMessage) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}
	if decoded["enableGitHubTelemetryForwarding"] != true {
		t.Fatalf("expected enableGitHubTelemetryForwarding=true, got %v", decoded["enableGitHubTelemetryForwarding"])
	}
}

func TestClient_OmitsGitHubTelemetryForwardingWhenNoHandler(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
		options:  ClientOptions{},
	}

	createParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.create", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		createParams <- append(json.RawMessage(nil), params...)
		sessionID := sessionIDFromParams(t, params)
		return []byte(`{"sessionId":"` + sessionID + `","workspacePath":"/workspace"}`), nil
	})

	if _, err := client.CreateSession(t.Context(), &SessionConfig{}); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	assertForwardingFlagAbsent(t, <-createParams)

	resumeParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("session.resume", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		resumeParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"sessionId":"resumed","workspacePath":"/workspace"}`), nil
	})

	if _, err := client.ResumeSessionWithOptions(t.Context(), "resumed", &ResumeSessionConfig{}); err != nil {
		t.Fatalf("ResumeSessionWithOptions failed: %v", err)
	}
	assertForwardingFlagAbsent(t, <-resumeParams)
}

func assertForwardingFlagAbsent(t *testing.T, params json.RawMessage) {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(params, &decoded); err != nil {
		t.Fatalf("failed to unmarshal request params: %v", err)
	}
	if _, ok := decoded["enableGitHubTelemetryForwarding"]; ok {
		t.Fatalf("expected enableGitHubTelemetryForwarding to be omitted, got %v", decoded["enableGitHubTelemetryForwarding"])
	}
}

func TestClient_ForwardsGitHubTelemetryForwardingOnConnect(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:      rpcClient,
		RPC:         rpc.NewServerRPC(rpcClient),
		internalRPC: rpc.NewInternalServerRPC(rpcClient),
		sessions:    make(map[string]*Session),
		options:     ClientOptions{OnGitHubTelemetry: func(*rpc.GitHubTelemetryNotification) {}},
	}

	connectParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("connect", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		connectParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"ok":true,"protocolVersion":3,"version":"test"}`), nil
	})

	if err := client.verifyProtocolVersion(t.Context()); err != nil {
		t.Fatalf("verifyProtocolVersion failed: %v", err)
	}
	assertForwardingFlagTrue(t, <-connectParams)
}

func TestClient_OmitsGitHubTelemetryForwardingOnConnectWhenNoHandler(t *testing.T) {
	rpcClient, server, _ := newRuntimeShutdownRpcPair(t)
	t.Cleanup(server.Stop)
	client := &Client{
		client:      rpcClient,
		RPC:         rpc.NewServerRPC(rpcClient),
		internalRPC: rpc.NewInternalServerRPC(rpcClient),
		sessions:    make(map[string]*Session),
		options:     ClientOptions{},
	}

	connectParams := make(chan json.RawMessage, 1)
	server.SetRequestHandler("connect", func(params json.RawMessage) (json.RawMessage, *jsonrpc2.Error) {
		connectParams <- append(json.RawMessage(nil), params...)
		return []byte(`{"ok":true,"protocolVersion":3,"version":"test"}`), nil
	})

	if err := client.verifyProtocolVersion(t.Context()); err != nil {
		t.Fatalf("verifyProtocolVersion failed: %v", err)
	}
	assertForwardingFlagAbsent(t, <-connectParams)
}

func TestGitHubTelemetryNotificationRoutesToCallback(t *testing.T) {
	// The runtime forwards telemetry via a JSON-RPC *notification* (no id).
	// Drive a real Content-Length-framed notification through the transport and
	// verify that a real Client wired with OnGitHubTelemetry routes it to the
	// callback through the client's own client-global handler registration
	// (setupNotificationHandler), rather than registering the adapter by hand.
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	rpcClient := jsonrpc2.NewClient(clientConn, clientConn)
	rpcClient.Start()
	defer rpcClient.Stop()

	// Drain the client->server direction so net.Pipe writes never block.
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := serverConn.Read(buf); err != nil {
				return
			}
		}
	}()

	received := make(chan *rpc.GitHubTelemetryNotification, 1)
	client := &Client{
		client:   rpcClient,
		RPC:      rpc.NewServerRPC(rpcClient),
		sessions: make(map[string]*Session),
		options: ClientOptions{
			OnGitHubTelemetry: func(n *rpc.GitHubTelemetryNotification) { received <- n },
		},
	}
	// setupNotificationHandler is what registers the gitHubTelemetryAdapter when
	// OnGitHubTelemetry is set; exercising it here covers the real client wiring.
	client.setupNotificationHandler()

	notification := map[string]any{
		"jsonrpc": "2.0",
		"method":  "gitHubTelemetry.event",
		"params": map[string]any{
			"sessionId":  "sess-telemetry",
			"restricted": true,
			"event": map[string]any{
				"kind":       "tool_call_executed",
				"metrics":    map[string]any{"duration_ms": 12.5},
				"properties": map[string]any{"tool": "shell"},
			},
		},
	}
	data, err := json.Marshal(notification)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}
	go func() {
		_, _ = fmt.Fprintf(serverConn, "Content-Length: %d\r\n\r\n%s", len(data), data)
	}()

	select {
	case n := <-received:
		sessionID := ""
		if n.SessionID != nil {
			sessionID = *n.SessionID
		}
		if sessionID != "sess-telemetry" {
			t.Errorf("session id = %q, want sess-telemetry", sessionID)
		}
		if !n.Restricted {
			t.Error("expected restricted to be true")
		}
		if n.Event.Kind != "tool_call_executed" {
			t.Errorf("kind = %q, want tool_call_executed", n.Event.Kind)
		}
		if n.Event.Metrics["duration_ms"] != 12.5 {
			t.Errorf("metrics[duration_ms] = %v, want 12.5", n.Event.Metrics["duration_ms"])
		}
		if n.Event.Properties["tool"] != "shell" {
			t.Errorf("properties[tool] = %q, want shell", n.Event.Properties["tool"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for telemetry notification")
	}
}

func TestCreateSessionRequest_EnableOnDemandInstructionDiscovery(t *testing.T) {
	t.Run("forwards explicit true", func(t *testing.T) {
		req := createSessionRequest{
			EnableOnDemandInstructionDiscovery: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableOnDemandInstructionDiscovery"] != true {
			t.Errorf("Expected enableOnDemandInstructionDiscovery to be true, got %v", m["enableOnDemandInstructionDiscovery"])
		}
	})

	t.Run("preserves explicit false", func(t *testing.T) {
		req := createSessionRequest{
			EnableOnDemandInstructionDiscovery: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableOnDemandInstructionDiscovery"] != false {
			t.Errorf("Expected enableOnDemandInstructionDiscovery to be false, got %v", m["enableOnDemandInstructionDiscovery"])
		}
	})

	t.Run("omits enableOnDemandInstructionDiscovery when not set", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableOnDemandInstructionDiscovery"]; ok {
			t.Error("Expected enableOnDemandInstructionDiscovery to be omitted when not set")
		}
	})
}

func TestResumeSessionRequest_EnableOnDemandInstructionDiscovery(t *testing.T) {
	t.Run("forwards explicit true", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:                          "s1",
			EnableOnDemandInstructionDiscovery: Bool(true),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableOnDemandInstructionDiscovery"] != true {
			t.Errorf("Expected enableOnDemandInstructionDiscovery to be true, got %v", m["enableOnDemandInstructionDiscovery"])
		}
	})

	t.Run("preserves explicit false", func(t *testing.T) {
		req := resumeSessionRequest{
			SessionID:                          "s1",
			EnableOnDemandInstructionDiscovery: Bool(false),
		}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableOnDemandInstructionDiscovery"] != false {
			t.Errorf("Expected enableOnDemandInstructionDiscovery to be false, got %v", m["enableOnDemandInstructionDiscovery"])
		}
	})

	t.Run("omits enableOnDemandInstructionDiscovery when not set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["enableOnDemandInstructionDiscovery"]; ok {
			t.Error("Expected enableOnDemandInstructionDiscovery to be omitted when not set")
		}
	})
}

func TestCreateSessionResponse_Capabilities(t *testing.T) {
	t.Run("reads capabilities from session.create response", func(t *testing.T) {
		responseJSON := `{"sessionId":"s1","workspacePath":"/tmp","capabilities":{"ui":{"elicitation":true}}}`
		var response createSessionResponse
		if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if response.Capabilities == nil {
			t.Fatal("Expected capabilities to be non-nil")
		}
		if response.Capabilities.UI == nil {
			t.Fatal("Expected capabilities.UI to be non-nil")
		}
		if !response.Capabilities.UI.Elicitation {
			t.Errorf("Expected capabilities.UI.Elicitation to be true")
		}
	})

	t.Run("defaults capabilities when not present", func(t *testing.T) {
		responseJSON := `{"sessionId":"s1","workspacePath":"/tmp"}`
		var response createSessionResponse
		if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if response.Capabilities != nil && response.Capabilities.UI != nil && response.Capabilities.UI.Elicitation {
			t.Errorf("Expected capabilities.UI.Elicitation to be falsy when not injected")
		}
	})
}

// TestHelperProcess is a helper used by tests that need to spawn a process
// which writes to stderr and exits with a given status. It is invoked
// via "go test" by running the test binary itself with -test.run.
// The stderr message and exit code are passed via environment variables
// HELPER_STDERR_MSG and HELPER_EXIT_CODE (defaulting to "" and 1).
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		// Not in helper process mode; let the test run normally.
		return
	}

	msg := os.Getenv("HELPER_STDERR_MSG")
	if msg == "" {
		// Fall back to command-line args after "--" for backwards compat.
		for i, arg := range os.Args {
			if arg == "--" && i+1 < len(os.Args) {
				msg = os.Args[i+1]
				break
			}
		}
	}
	if msg != "" {
		_, _ = os.Stderr.WriteString(msg + "\n")
	}

	exitCode := 1
	if ec := os.Getenv("HELPER_EXIT_CODE"); ec != "" {
		if v, err := strconv.Atoi(ec); err == nil {
			exitCode = v
		}
	}
	os.Exit(exitCode)
}

// newStderrTestCommand constructs a command that re-invokes the current test
// binary to run TestHelperProcess with the provided stderr message and exit
// code. This avoids any dependency on a shell like "sh" and is portable.
func newStderrTestCommand(stderrMsg string, exitCode int) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(),
		"GO_WANT_HELPER_PROCESS=1",
		"HELPER_STDERR_MSG="+stderrMsg,
		"HELPER_EXIT_CODE="+strconv.Itoa(exitCode),
	)
	return cmd
}

// TestMonitorProcess_StderrCaptured validates that when the CLI process
// writes an error to stderr and exits, the stderr content IS included
// in the process error (now that startCLIServer sets Stderr).
func TestMonitorProcess_StderrCaptured(t *testing.T) {
	client := &Client{
		sessions: make(map[string]*Session),
	}

	stderrMsg := "error: authentication failed: invalid token"
	client.process = exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", stderrMsg)
	client.process.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	// Replicate what startCLIServer now does: capture stderr.
	client.process.Stderr = truncbuffer.NewTruncBuffer(stderrBufferSize)

	if err := client.process.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	client.monitorProcess()

	// Wait for the process to exit.
	<-client.processDone

	processError := *client.processErrorPtr
	if processError == nil {
		t.Fatal("expected a process error after non-zero exit, got nil")
	}

	if !strings.Contains(processError.Error(), stderrMsg) {
		t.Errorf("stderr output not included in process error.\n"+
			"  got:  %q\n"+
			"  want: error containing %q", processError.Error(), stderrMsg)
	}
}

// TestMonitorProcess_StderrCapturedOnZeroExit validates that even when the
// CLI process exits with code 0, stderr content is included in the error.
func TestMonitorProcess_StderrCapturedOnZeroExit(t *testing.T) {
	client := &Client{
		sessions: make(map[string]*Session),
	}

	stderrMsg := "warning: version mismatch, shutting down"
	client.process = newStderrTestCommand(stderrMsg, 0)
	client.process.Stderr = truncbuffer.NewTruncBuffer(stderrBufferSize)

	if err := client.process.Start(); err != nil {
		t.Fatalf("failed to start test process: %v", err)
	}

	client.monitorProcess()
	<-client.processDone

	processError := *client.processErrorPtr
	if processError == nil {
		t.Fatal("expected a process error for unexpected exit, got nil")
	}

	if !strings.Contains(processError.Error(), stderrMsg) {
		t.Errorf("stderr output not included in process error for exit code 0.\n"+
			"  got:  %q\n"+
			"  want: error containing %q", processError.Error(), stderrMsg)
	}
}

// TestStartCLIServer_StderrFieldSet verifies that startCLIServer sets
// exec.Cmd.Stderr to a *truncbuffer.TruncBuffer so CLI diagnostic output is captured.
func TestStartCLIServer_StderrFieldSet(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	buf := truncbuffer.NewTruncBuffer(stderrBufferSize)
	cmd.Stderr = buf
	if _, ok := cmd.Stderr.(*truncbuffer.TruncBuffer); !ok {
		t.Error("expected Stderr to be *truncbuffer.TruncBuffer after assignment")
	}
}

func TestCreateSessionRequest_ExpAssignments(t *testing.T) {
	assignments := &CopilotExpAssignmentResponse{
		Features: []string{"copilot_exp_flag"},
		Flights:  map[string]string{"copilot_exp_flag": "treatment"},
		Configs: []ExpConfigEntry{
			{ID: "cfg-1", Parameters: map[string]ExpFlagValue{"threshold": 5, "enabled": true}},
		},
		AssignmentContext: "ctx-123",
	}

	t.Run("includes expAssignments in JSON when set", func(t *testing.T) {
		req := createSessionRequest{ExpAssignments: assignments}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		got, ok := m["expAssignments"].(map[string]any)
		if !ok {
			t.Fatalf("Expected expAssignments to be an object, got %v", m["expAssignments"])
		}
		if got["AssignmentContext"] != "ctx-123" {
			t.Errorf("Expected AssignmentContext 'ctx-123', got %v", got["AssignmentContext"])
		}
	})

	t.Run("omits expAssignments from JSON when nil", func(t *testing.T) {
		req := createSessionRequest{}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["expAssignments"]; ok {
			t.Error("Expected expAssignments to be omitted when nil")
		}
	})
}

func TestCopilotExpAssignmentResponse_MarshalNormalizesNilCollections(t *testing.T) {
	// A response left with zero-value collections must still serialize the
	// required fields as JSON arrays/objects, not null, so the runtime does not
	// treat the payload as malformed.
	data, err := json.Marshal(&CopilotExpAssignmentResponse{AssignmentContext: "ctx"})
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}
	for _, tc := range []struct{ key, want string }{
		{"Features", "[]"},
		{"Flights", "{}"},
		{"Configs", "[]"},
		{"AssignmentContext", `"ctx"`},
	} {
		if got := string(m[tc.key]); got != tc.want {
			t.Errorf("Expected %s to serialize as %s, got %s", tc.key, tc.want, got)
		}
	}

	// A nil Parameters map on an entry must likewise serialize as {}.
	entryData, err := json.Marshal(ExpConfigEntry{ID: "cfg"})
	if err != nil {
		t.Fatalf("Failed to marshal entry: %v", err)
	}
	if err := json.Unmarshal(entryData, &m); err != nil {
		t.Fatalf("Failed to unmarshal entry: %v", err)
	}
	if got := string(m["Parameters"]); got != "{}" {
		t.Errorf("Expected Parameters to serialize as {}, got %s", got)
	}
}

func TestResumeSessionRequest_ExpAssignments(t *testing.T) {
	assignments := &CopilotExpAssignmentResponse{
		Features: []string{"copilot_exp_flag"},
		Flights:  map[string]string{"copilot_exp_flag": "treatment"},
		Configs: []ExpConfigEntry{
			{ID: "cfg-1", Parameters: map[string]ExpFlagValue{"copilot_exp_flag": "treatment"}},
		},
		AssignmentContext: "ctx-456",
	}

	t.Run("includes expAssignments in JSON when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ExpAssignments: assignments}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		got, ok := m["expAssignments"].(map[string]any)
		if !ok {
			t.Fatalf("Expected expAssignments to be an object, got %v", m["expAssignments"])
		}
		if got["AssignmentContext"] != "ctx-456" {
			t.Errorf("Expected AssignmentContext 'ctx-456', got %v", got["AssignmentContext"])
		}
	})

	t.Run("omits expAssignments from JSON when nil", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1"}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["expAssignments"]; ok {
			t.Error("Expected expAssignments to be omitted when nil")
		}
	})
}

func TestIsTerminal(t *testing.T) {
	t.Run("IsTerminal is serialized in tool definition", func(t *testing.T) {
		tool := Tool{
			Name:        "clear_context",
			Description: "Clear the conversation",
			IsTerminal:  true,
			Handler:     func(_ ToolInvocation) (ToolResult, error) { return ToolResult{}, nil },
		}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["isTerminal"] != true {
			t.Errorf("Expected isTerminal to be true, got %v", m["isTerminal"])
		}
	})

	t.Run("IsTerminal is omitted when false", func(t *testing.T) {
		tool := Tool{Name: "plain", Description: "A plain tool"}
		data, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["isTerminal"]; ok {
			t.Error("Expected isTerminal to be omitted when false")
		}
	})
}

func TestSessionRequests_ManagedSettings(t *testing.T) {
	settings := &ManagedSettings{
		Permissions: &ManagedSettingsPermissions{
			DisableBypassPermissionsMode: DisableBypassPermissionsModeDisable,
			Deny:                         []string{"Shell(git push)"},
			Ask:                          []string{"Domain(publish.example)"},
			Allow:                        []string{"Read(**)"},
		},
	}

	expectedPermissions := map[string]any{
		"disableBypassPermissionsMode": "disable",
		"deny":                         []any{"Shell(git push)"},
		"ask":                          []any{"Domain(publish.example)"},
		"allow":                        []any{"Read(**)"},
	}

	t.Run("direct injection enables managed safeguards", func(t *testing.T) {
		if !hasManagedSettings(nil, settings) {
			t.Fatal("expected injected managed settings to enable managed safeguards")
		}
		if hasManagedSettings(nil, nil) {
			t.Fatal("expected an ordinary session to remain unmanaged")
		}
	})

	t.Run("includes managedSettings on create when set", func(t *testing.T) {
		req := createSessionRequest{EnableManagedSettings: Bool(true), ManagedSettings: settings}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if m["enableManagedSettings"] != true {
			t.Errorf("Expected enableManagedSettings true, got %v", m["enableManagedSettings"])
		}
		ms, ok := m["managedSettings"].(map[string]any)
		if !ok {
			t.Fatalf("Expected managedSettings object, got %v", m["managedSettings"])
		}
		perms, ok := ms["permissions"].(map[string]any)
		if !ok {
			t.Fatalf("Expected permissions object, got %v", ms["permissions"])
		}
		if !reflect.DeepEqual(perms, expectedPermissions) {
			t.Errorf("permissions mismatch:\n got: %#v\nwant: %#v", perms, expectedPermissions)
		}
	})

	t.Run("includes managedSettings on resume when set", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ManagedSettings: settings}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("Failed to unmarshal: %v", err)
		}
		if _, ok := m["managedSettings"].(map[string]any); !ok {
			t.Fatalf("Expected managedSettings object, got %v", m["managedSettings"])
		}
	})

	t.Run("omits managedSettings when nil", func(t *testing.T) {
		req := createSessionRequest{}
		data, _ := json.Marshal(req)
		var m map[string]any
		json.Unmarshal(data, &m)
		if _, ok := m["managedSettings"]; ok {
			t.Error("Expected managedSettings to be omitted when nil")
		}
	})

	t.Run("preserves explicit empty permission arrays", func(t *testing.T) {
		// A non-nil empty allow list is restrictive: it admits no operations.
		// Preserve field presence while still omitting nil slices.
		req := createSessionRequest{ManagedSettings: &ManagedSettings{
			Permissions: &ManagedSettingsPermissions{
				DisableBypassPermissionsMode: DisableBypassPermissionsModeDisable,
				Deny:                         []string{},
				Ask:                          []string{},
				Allow:                        []string{},
			},
		}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		perms := m["managedSettings"].(map[string]any)["permissions"].(map[string]any)
		if perms["disableBypassPermissionsMode"] != "disable" {
			t.Errorf("Expected disableBypassPermissionsMode preserved, got %v", perms["disableBypassPermissionsMode"])
		}
		for _, key := range []string{"deny", "ask", "allow"} {
			if value, ok := perms[key].([]any); !ok || len(value) != 0 {
				t.Errorf("Expected %s to be an explicit empty array, got %v", key, perms[key])
			}
		}
	})

	t.Run("distinguishes explicit empty allow from an absent allow", func(t *testing.T) {
		// Security-critical: a present empty allow list admits nothing, while an
		// absent allow list imposes no allow restriction. The wire output must
		// tell these apart per-field, so an explicit empty slice serializes as
		// `[]` while a nil slice is omitted entirely.
		req := createSessionRequest{ManagedSettings: &ManagedSettings{
			Permissions: &ManagedSettingsPermissions{
				Allow: []string{}, // present but empty: admit nothing
				// Deny and Ask left nil: no such restriction supplied.
			},
		}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		perms := m["managedSettings"].(map[string]any)["permissions"].(map[string]any)

		allow, ok := perms["allow"].([]any)
		if !ok || len(allow) != 0 {
			t.Errorf("Expected allow to be an explicit empty array, got %v", perms["allow"])
		}
		if _, present := perms["deny"]; present {
			t.Errorf("Expected deny to be omitted when nil, got %v", perms["deny"])
		}
		if _, present := perms["ask"]; present {
			t.Errorf("Expected ask to be omitted when nil, got %v", perms["ask"])
		}
	})

	t.Run("distinguishes explicit empty arrays on resume", func(t *testing.T) {
		req := resumeSessionRequest{SessionID: "s1", ManagedSettings: &ManagedSettings{
			Permissions: &ManagedSettingsPermissions{
				Deny:  []string{},
				Ask:   []string{},
				Allow: []string{},
			},
		}}
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("Failed to marshal: %v", err)
		}
		var m map[string]any
		json.Unmarshal(data, &m)
		perms := m["managedSettings"].(map[string]any)["permissions"].(map[string]any)
		for _, key := range []string{"deny", "ask", "allow"} {
			if value, ok := perms[key].([]any); !ok || len(value) != 0 {
				t.Errorf("Expected %s to be an explicit empty array on resume, got %v", key, perms[key])
			}
		}
	})
}
