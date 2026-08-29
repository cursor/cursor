// Canvas declarations, provider callbacks, and host-side canvas RPC types.
//
// This file mirrors rust/src/canvas.rs. The SDK does not maintain a per-canvas
// registry; multiplexing across declared canvases is the CanvasHandler
// implementor's responsibility (typically by switching on CanvasProviderOpenRequest.CanvasID).

package copilot

import (
	"context"

	"github.com/github/copilot-sdk/go/rpc"
)

// CanvasDeclaration is the declarative metadata for a single canvas, sent over
// the wire on `session.create` / `session.resume`.
//
// Experimental: CanvasDeclaration is part of an experimental wire-protocol
// surface and may change or be removed in future SDK or CLI releases.
type CanvasDeclaration struct {
	// ID is the canvas identifier, unique within the declaring connection.
	ID string `json:"id"`
	// DisplayName is the human-readable name shown in host UI and canvas pickers.
	DisplayName string `json:"displayName"`
	// Description is a short, single-sentence description shown to the agent in canvas catalogs.
	Description string `json:"description"`
	// InputSchema is the JSON Schema for the `input` payload accepted by `canvas.open`.
	InputSchema map[string]any `json:"inputSchema,omitzero"`
	// Actions are the agent-callable actions this canvas exposes.
	Actions []rpc.CanvasAction `json:"actions,omitempty"`
}

// ExtensionInfo carries stable extension identity for session participants
// that provide canvases.
//
// Experimental: ExtensionInfo is part of an experimental wire-protocol
// surface and may change or be removed in future SDK or CLI releases.
type ExtensionInfo struct {
	// Source is the extension namespace/source, e.g. "github-app".
	Source string `json:"source"`
	// Name is the extension identifier within that source, e.g. "my-app".
	Name string `json:"name"`
}

// CanvasProviderIdentity is the stable identity for a host/SDK connection
// that supplies built-in canvases.
//
// When set on session create or resume, the runtime uses ID verbatim as the
// agent-facing canvas extension id, so host-provided canvases survive
// reconnect and CLI restart.
//
// Experimental: CanvasProviderIdentity is part of an experimental
// wire-protocol surface and may change or be removed in future SDK or CLI
// releases.
type CanvasProviderIdentity struct {
	// ID is an opaque, stable provider id used verbatim as the canvas
	// extension id.
	ID string `json:"id"`
	// Name is an optional display name surfaced as the canvas extension name.
	Name *string `json:"name,omitempty"`
}

// CanvasError is a structured error returned from canvas handlers.
//
// Wire envelope:
//
//	{ "code": "<code>", "message": "<message>" }
//
// Experimental: CanvasError is part of an experimental wire-protocol
// surface and may change or be removed in future SDK or CLI releases.
type CanvasError struct {
	// Code is the machine-readable error code.
	Code string `json:"code"`
	// Message is the human-readable message.
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *CanvasError) Error() string {
	return e.Code + ": " + e.Message
}

// NewCanvasError constructs a new error envelope with the given code and message.
func NewCanvasError(code, message string) *CanvasError {
	return &CanvasError{Code: code, Message: message}
}

// CanvasErrorNoHandler is the default error returned when a custom action has no handler.
func CanvasErrorNoHandler() *CanvasError {
	return NewCanvasError(
		"canvas_action_no_handler",
		"No handler implemented for this canvas action",
	)
}

// CanvasHandler is the provider-side canvas lifecycle handler.
//
// A session installs a single CanvasHandler (via SessionConfig.CanvasHandler).
// The handler receives every inbound `canvas.open` / `canvas.close` /
// `canvas.action.invoke` JSON-RPC request the runtime issues for this session
// and decides — typically by inspecting CanvasProviderOpenRequest.CanvasID — which
// application-side canvas should handle the call.
//
// The SDK does not maintain a per-canvas registry; multiplexing across declared
// canvases is the implementor's responsibility.
//
// Embed CanvasHandlerDefaults to inherit no-op defaults for OnClose and a
// "no handler" error for OnAction.
//
// Experimental: CanvasHandler is part of an experimental wire-protocol
// surface and may change or be removed in future SDK or CLI releases.
type CanvasHandler interface {
	OnOpen(ctx context.Context, c rpc.CanvasProviderOpenRequest) (rpc.CanvasProviderOpenResult, error)
	OnClose(ctx context.Context, c rpc.CanvasProviderCloseRequest) error
	OnAction(ctx context.Context, c rpc.CanvasProviderInvokeActionRequest) (any, error)
}

// CanvasHandlerDefaults supplies default OnClose / OnAction implementations
// that consumers can inherit by embedding it in their CanvasHandler.
//
// Example:
//
//	type myHandler struct {
//	    copilot.CanvasHandlerDefaults
//	}
//	func (h *myHandler) OnOpen(ctx context.Context, c rpc.CanvasProviderOpenRequest) (rpc.CanvasProviderOpenResult, error) { ... }
//
// Experimental: CanvasHandlerDefaults is part of an experimental wire-protocol
// surface and may change or be removed in future SDK or CLI releases.
type CanvasHandlerDefaults struct{}

// OnClose returns nil by default.
func (CanvasHandlerDefaults) OnClose(ctx context.Context, c rpc.CanvasProviderCloseRequest) error {
	return nil
}

// OnAction returns CanvasErrorNoHandler() by default.
func (CanvasHandlerDefaults) OnAction(ctx context.Context, c rpc.CanvasProviderInvokeActionRequest) (any, error) {
	return nil, CanvasErrorNoHandler()
}
