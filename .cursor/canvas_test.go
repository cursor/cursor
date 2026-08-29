package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/github/copilot-sdk/go/internal/jsonrpc2"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestCanvasDeclaration_JSONShape(t *testing.T) {
	desc := "bump"
	decl := CanvasDeclaration{
		ID:          "counter",
		DisplayName: "Counter",
		Description: "Count things",
		Actions: []rpc.CanvasAction{
			{Name: "increment", Description: &desc},
		},
	}

	data, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded["id"] != "counter" {
		t.Fatalf("expected id=counter, got %v", decoded["id"])
	}
	if decoded["displayName"] != "Counter" {
		t.Fatalf("expected displayName=Counter, got %v", decoded["displayName"])
	}
	if decoded["description"] != "Count things" {
		t.Fatalf("expected description, got %v", decoded["description"])
	}
	if _, present := decoded["inputSchema"]; present {
		t.Fatalf("inputSchema should be omitted when nil, got %v", decoded["inputSchema"])
	}
	actions, ok := decoded["actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("expected actions array of length 1, got %v", decoded["actions"])
	}
	first, _ := actions[0].(map[string]any)
	if first["name"] != "increment" {
		t.Fatalf("expected first action name=increment, got %v", first["name"])
	}
}

func TestCanvasDeclaration_OmitsEmptyActions(t *testing.T) {
	decl := CanvasDeclaration{ID: "x", DisplayName: "X", Description: "y"}
	data, err := json.Marshal(decl)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(data, &decoded)
	if _, present := decoded["actions"]; present {
		t.Fatalf("actions should be omitted when nil, got %v", decoded["actions"])
	}
}

func TestCanvasHandlerDefaults_OnAction_ReturnsNoHandler(t *testing.T) {
	d := CanvasHandlerDefaults{}
	_, err := d.OnAction(context.Background(), rpc.CanvasProviderInvokeActionRequest{})
	if err == nil {
		t.Fatalf("expected error from default OnAction")
	}
	cerr, ok := err.(*CanvasError)
	if !ok {
		t.Fatalf("expected *CanvasError, got %T", err)
	}
	if cerr.Code != "canvas_action_no_handler" {
		t.Fatalf("expected code=canvas_action_no_handler, got %q", cerr.Code)
	}
}

func TestCanvasHandlerDefaults_OnClose_ReturnsNil(t *testing.T) {
	d := CanvasHandlerDefaults{}
	if err := d.OnClose(context.Background(), rpc.CanvasProviderCloseRequest{}); err != nil {
		t.Fatalf("expected nil from default OnClose, got %v", err)
	}
}

func TestCanvasError_ErrorString(t *testing.T) {
	e := NewCanvasError("foo_code", "bar message")
	if got := e.Error(); got != "foo_code: bar message" {
		t.Fatalf("unexpected Error() output: %q", got)
	}
}

type recordingCanvasHandler struct {
	CanvasHandlerDefaults
	openCtx      *rpc.CanvasProviderOpenRequest
	closeCtx     *rpc.CanvasProviderCloseRequest
	actionCtx    *rpc.CanvasProviderInvokeActionRequest
	openResult   rpc.CanvasProviderOpenResult
	actionResult any
	openErr      error
	closeErr     error
	actionErr    error
}

func (h *recordingCanvasHandler) OnOpen(ctx context.Context, c rpc.CanvasProviderOpenRequest) (rpc.CanvasProviderOpenResult, error) {
	h.openCtx = &c
	return h.openResult, h.openErr
}

func (h *recordingCanvasHandler) OnClose(ctx context.Context, c rpc.CanvasProviderCloseRequest) error {
	h.closeCtx = &c
	return h.closeErr
}

func (h *recordingCanvasHandler) OnAction(ctx context.Context, c rpc.CanvasProviderInvokeActionRequest) (any, error) {
	h.actionCtx = &c
	return h.actionResult, h.actionErr
}

func TestCanvasAdapter_DispatchesToHandler(t *testing.T) {
	title := "Echo"
	url := "https://example.test/echo"
	handler := &recordingCanvasHandler{
		openResult: rpc.CanvasProviderOpenResult{URL: &url, Title: &title},
		actionResult: map[string]any{
			"count": float64(2),
		},
	}

	session := newTestCanvasSession("s1")
	session.registerCanvasHandler(handler)

	openResp, err := session.clientSessionAPIs.Canvas.Open(&rpc.CanvasProviderOpenRequest{
		SessionID:   "s1",
		ExtensionID: "project:echo",
		CanvasID:    "echo",
		InstanceID:  "echo-1",
		Input:       map[string]any{"x": float64(1)},
	})
	if err != nil {
		t.Fatalf("unexpected open error: %v", err)
	}
	if handler.openCtx == nil {
		t.Fatalf("handler.OnOpen was not called")
	}
	if handler.openCtx.CanvasID != "echo" || handler.openCtx.InstanceID != "echo-1" {
		t.Fatalf("unexpected open ctx: %+v", handler.openCtx)
	}
	if openResp.URL == nil || *openResp.URL != url {
		t.Fatalf("response URL not propagated: %+v", openResp)
	}

	actionResp, err := session.clientSessionAPIs.Canvas.Invoke(&rpc.CanvasProviderInvokeActionRequest{
		SessionID:   "s1",
		ExtensionID: "project:echo",
		CanvasID:    "echo",
		InstanceID:  "echo-1",
		ActionName:  "increment",
		Input:       map[string]any{"amount": float64(1)},
	})
	if err != nil {
		t.Fatalf("unexpected action error: %v", err)
	}
	if handler.actionCtx == nil {
		t.Fatalf("handler.OnAction was not called")
	}
	if handler.actionCtx.ActionName != "increment" {
		t.Fatalf("unexpected action ctx: %+v", handler.actionCtx)
	}
	result, ok := actionResp.(map[string]any)
	if !ok || result["count"] != float64(2) {
		t.Fatalf("unexpected action result: %#v", actionResp)
	}

	closeResp, err := session.clientSessionAPIs.Canvas.Close(&rpc.CanvasProviderCloseRequest{
		SessionID:   "s1",
		ExtensionID: "project:echo",
		CanvasID:    "echo",
		InstanceID:  "echo-1",
	})
	if err != nil {
		t.Fatalf("unexpected close error: %v", err)
	}
	if closeResp != nil {
		t.Fatal("expected nil close response")
	}
	if handler.closeCtx == nil || handler.closeCtx.CanvasID != "echo" {
		t.Fatalf("unexpected close ctx: %+v", handler.closeCtx)
	}
}

func TestCanvasAdapter_NoHandler_ReturnsUnsetError(t *testing.T) {
	session := newTestCanvasSession("s1")

	_, err := session.clientSessionAPIs.Canvas.Open(&rpc.CanvasProviderOpenRequest{SessionID: "s1"})
	assertCanvasJSONRPCError(t, err, "canvas_handler_unset", "")
}

func TestCanvasAdapter_HandlerCanvasError_Wired(t *testing.T) {
	session := newTestCanvasSession("s1")
	session.registerCanvasHandler(&recordingCanvasHandler{
		openErr: NewCanvasError("permission_denied", "nope"),
	})

	_, err := session.clientSessionAPIs.Canvas.Open(&rpc.CanvasProviderOpenRequest{SessionID: "s1"})
	assertCanvasJSONRPCError(t, err, "permission_denied", "nope")
}

func TestCanvasAdapter_HandlerGenericError_WrappedAsCanvasHandlerError(t *testing.T) {
	session := newTestCanvasSession("s1")
	session.registerCanvasHandler(&recordingCanvasHandler{
		openErr: errors.New("boom"),
	})

	_, err := session.clientSessionAPIs.Canvas.Open(&rpc.CanvasProviderOpenRequest{SessionID: "s1"})
	assertCanvasJSONRPCError(t, err, "canvas_handler_error", "boom")
}

func TestCanvasRegisterClientSessionAPIHandlers_RawJSONRoundTrip(t *testing.T) {
	clientToServerReader, clientToServerWriter := io.Pipe()
	serverToClientReader, serverToClientWriter := io.Pipe()

	requester := jsonrpc2.NewClient(clientToServerWriter, serverToClientReader)
	server := jsonrpc2.NewClient(serverToClientWriter, clientToServerReader)
	session := newTestCanvasSession("s1")
	session.registerCanvasHandler(&recordingCanvasHandler{
		openResult:   rpc.CanvasProviderOpenResult{Status: strPtr("ready")},
		actionResult: map[string]any{"count": float64(2)},
	})
	rpc.RegisterClientSessionAPIHandlers(server, func(sessionID string) *rpc.ClientSessionAPIHandlers {
		if sessionID == "s1" {
			return session.clientSessionAPIs
		}
		return nil
	})

	requester.Start()
	server.Start()
	t.Cleanup(func() {
		requester.Stop()
		server.Stop()
		_ = clientToServerWriter.Close()
		_ = clientToServerReader.Close()
		_ = serverToClientWriter.Close()
		_ = serverToClientReader.Close()
	})

	raw, err := requester.Request(t.Context(), "canvas.open", map[string]any{
		"sessionId":   "s1",
		"extensionId": "ext",
		"canvasId":    "echo",
		"instanceId":  "i1",
		"input":       map[string]any{"k": "v"},
		"host": map[string]any{
			"capabilities": map[string]any{
				"canvases": true,
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected rpc error: %v", err)
	}

	handler := session.getCanvasHandler().(*recordingCanvasHandler)
	if handler.openCtx == nil {
		t.Fatalf("handler not invoked")
	}
	if handler.openCtx.Host == nil || handler.openCtx.Host.Capabilities == nil ||
		handler.openCtx.Host.Capabilities.Canvases == nil || !*handler.openCtx.Host.Capabilities.Canvases {
		t.Fatalf("host capabilities not parsed: %+v", handler.openCtx.Host)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("bad output JSON: %v", err)
	}
	if decoded["status"] != "ready" {
		t.Fatalf("expected status=ready, got %v", decoded["status"])
	}

	actionRaw, err := requester.Request(t.Context(), "canvas.action.invoke", map[string]any{
		"sessionId":   "s1",
		"extensionId": "ext",
		"canvasId":    "echo",
		"instanceId":  "i1",
		"actionName":  "increment",
		"input":       map[string]any{"amount": float64(2)},
	})
	if err != nil {
		t.Fatalf("unexpected action rpc error: %v", err)
	}
	var actionDecoded map[string]any
	if err := json.Unmarshal(actionRaw, &actionDecoded); err != nil {
		t.Fatalf("bad action output JSON: %v", err)
	}
	if actionDecoded["count"] != float64(2) {
		t.Fatalf("expected raw provider result, got %v", actionDecoded)
	}
}

func TestCanvasResumeSessionResponse_OpenCanvasesParse(t *testing.T) {
	raw := []byte(`{
		"sessionId": "s1",
		"workspacePath": "/tmp/ws",
		"openCanvases": [
			{
				"canvasId": "echo",
				"extensionId": "project:echo",
				"instanceId": "echo-1"
			}
		]
	}`)

	var resp resumeSessionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(resp.OpenCanvases) != 1 {
		t.Fatalf("expected 1 open canvas, got %d", len(resp.OpenCanvases))
	}
	if resp.OpenCanvases[0].CanvasID != "echo" {
		t.Fatalf("unexpected canvasId: %q", resp.OpenCanvases[0].CanvasID)
	}

	session := &Session{SessionID: "s1"}
	session.setOpenCanvases(resp.OpenCanvases)
	got := session.OpenCanvases()
	if len(got) != 1 || got[0].InstanceID != "echo-1" {
		t.Fatalf("OpenCanvases did not surface snapshot: %+v", got)
	}
}

func TestCanvasResumeSessionRequest_OpenCanvasesWireShape(t *testing.T) {
	req := resumeSessionRequest{
		SessionID: "s1",
		OpenCanvases: []rpc.OpenCanvasInstance{
			{
				CanvasID:    "echo",
				ExtensionID: "project:echo",
				InstanceID:  "echo-1",
			},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	raw, ok := decoded["openCanvases"].([]any)
	if !ok || len(raw) != 1 {
		t.Fatalf("expected openCanvases array of length 1, got %v", decoded["openCanvases"])
	}
	first, _ := raw[0].(map[string]any)
	if first["canvasId"] != "echo" {
		t.Fatalf("expected canvasId=echo, got %v", first["canvasId"])
	}
	if first["instanceId"] != "echo-1" {
		t.Fatalf("expected instanceId=echo-1, got %v", first["instanceId"])
	}

	empty := resumeSessionRequest{SessionID: "s1"}
	emptyData, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal empty failed: %v", err)
	}
	var emptyDecoded map[string]any
	if err := json.Unmarshal(emptyData, &emptyDecoded); err != nil {
		t.Fatalf("unmarshal empty failed: %v", err)
	}
	if _, present := emptyDecoded["openCanvases"]; present {
		t.Fatalf("openCanvases should be omitted when nil")
	}
}

func assertCanvasJSONRPCError(t *testing.T, err error, wantCode, wantMessage string) {
	t.Helper()

	if err == nil {
		t.Fatal("expected error")
	}
	rpcErr, ok := err.(*jsonrpc2.Error)
	if !ok {
		t.Fatalf("expected *jsonrpc2.Error, got %T", err)
	}
	if rpcErr.Code != -32603 {
		t.Fatalf("expected internal-error code, got %d", rpcErr.Code)
	}

	var data map[string]string
	if err := json.Unmarshal(rpcErr.Data, &data); err != nil {
		t.Fatalf("invalid error data: %v", err)
	}
	if data["code"] != wantCode {
		t.Fatalf("expected code=%s, got %q", wantCode, data["code"])
	}
	if wantMessage != "" && data["message"] != wantMessage {
		t.Fatalf("expected message=%q, got %q", wantMessage, data["message"])
	}
}

func newTestCanvasSession(sessionID string) *Session {
	session := &Session{
		SessionID:         sessionID,
		clientSessionAPIs: &rpc.ClientSessionAPIHandlers{},
	}
	session.clientSessionAPIs.Canvas = newCanvasClientSessionAdapter(session)
	return session
}

func strPtr(s string) *string { return &s }
