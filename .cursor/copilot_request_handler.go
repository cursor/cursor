/*---------------------------------------------------------------------------------------------
 *  Copyright (c) Microsoft Corporation. All rights reserved.
 *--------------------------------------------------------------------------------------------*/

package copilot

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/github/copilot-sdk/go/rpc"
)

// Hop-by-hop and length headers the transport recomputes; forwarding them
// verbatim corrupts the request.
var forbiddenRequestHeaders = map[string]struct{}{
	"host":              {},
	"connection":        {},
	"content-length":    {},
	"transfer-encoding": {},
	"keep-alive":        {},
	"upgrade":           {},
	"proxy-connection":  {},
	"te":                {},
	"trailer":           {},
}

func isForbiddenRequestHeader(name string) bool {
	lower := strings.ToLower(name)
	if _, ok := forbiddenRequestHeaders[lower]; ok {
		return true
	}
	return strings.HasPrefix(lower, "sec-websocket-")
}

var sharedHTTPTransport = func() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return t
}()

// CopilotRequestContext is the per-request context handed to every
// [CopilotRequestHandler] seam.
type CopilotRequestContext struct {
	RequestID       string
	SessionID       string
	AgentID         string
	ParentAgentID   string
	InteractionType string
	// Transport is "http" (covering plain HTTP and SSE) or "websocket".
	Transport string
	Method    string
	URL       string
	Headers   http.Header
	// body yields request body frames as they arrive from the runtime. It is
	// unexported framework plumbing: the adapter drains it for HTTP requests
	// and pumps it to [CopilotWebSocketHandler.SendRequestMessage] for
	// WebSocket requests. Consumers read the HTTP body via the standard
	// [http.Request] Body in a custom RoundTripper, or receive WebSocket frames
	// via SendRequestMessage — never from this channel directly (doing so would
	// race the adapter's pump goroutine and lose frames). The channel is closed
	// when the body ends or the request is cancelled. For WebSocket requests
	// each frame's Binary flag distinguishes a binary frame from a UTF-8 text
	// frame; for HTTP it is always a body byte chunk.
	body <-chan CopilotWebSocketMessage
	// Context is cancelled when the runtime cancels this in-flight request.
	Context context.Context
}

// CopilotWebSocketCloseStatus is the terminal status for a callback-owned
// WebSocket connection.
type CopilotWebSocketCloseStatus struct {
	Description string
	ErrorCode   string
	Err         error
}

// CopilotWebSocketMessage is a single WebSocket frame exchanged through the
// handler seam. Binary distinguishes a binary frame from a UTF-8 text frame.
type CopilotWebSocketMessage struct {
	Data   []byte
	Binary bool
}

// Text decodes the frame payload as a UTF-8 string.
func (m CopilotWebSocketMessage) Text() string { return string(m.Data) }

// NewTextMessage creates a text-frame message from a UTF-8 string. Binary
// frames are constructed directly with CopilotWebSocketMessage{Data: ..., Binary: true}.
func NewTextMessage(text string) CopilotWebSocketMessage {
	return CopilotWebSocketMessage{Data: []byte(text), Binary: false}
}

// CopilotRequestHandler is the idiomatic handler for intercepting or replacing
// LLM inference requests. HTTP requests are forwarded through Transport (an
// [http.RoundTripper]); supply a custom RoundTripper to mutate the request,
// post-process the response, or replace the call entirely. WebSocket requests
// are serviced by OpenWebSocket; supply one to return a custom handler.
//
// The default behaviour (both fields nil) transparently forwards HTTP through a
// shared transport and opens a forwarding WebSocket connection to the runtime's
// original URL.
type CopilotRequestHandler struct {
	// Transport forwards HTTP requests. When nil a shared default transport is
	// used. RoundTrip is called directly, so redirects are not followed.
	Transport http.RoundTripper
	// OpenWebSocket returns a per-connection WebSocket handler. When nil a
	// transparent [CopilotWebSocketForwarder] to the request URL is opened.
	OpenWebSocket func(ctx *CopilotRequestContext) (CopilotWebSocketHandler, error)
}

// WebSocketResponseWriter forwards upstream→runtime WebSocket messages back
// into the runtime response. A [CopilotWebSocketHandler] receives one in
// [CopilotWebSocketHandler.Open].
type WebSocketResponseWriter interface {
	// SendText forwards an upstream text message to the runtime.
	SendText(data []byte) error
	// SendBinary forwards an upstream binary message to the runtime.
	SendBinary(data []byte) error
}

// CopilotWebSocketHandler is a per-connection WebSocket handler returned by
// [CopilotRequestHandler.OpenWebSocket]. The default implementation is
// [CopilotWebSocketForwarder]; a full transport replacement implements
// this interface directly.
type CopilotWebSocketHandler interface {
	// Open establishes the connection and starts forwarding upstream→runtime
	// messages into resp. It must not block. ctx is cancelled on teardown.
	Open(ctx context.Context, resp WebSocketResponseWriter) error
	// SendRequestMessage forwards one runtime→upstream message.
	SendRequestMessage(ctx context.Context, msg CopilotWebSocketMessage) error
	// Done is closed when the upstream connection completes (closed or errored).
	Done() <-chan struct{}
	// Err returns the terminal error after Done is closed, or nil on clean close.
	Err() error
	// Close tears down the connection.
	Close() error
}

// copilotContextKey is used to attach [CopilotRequestContext] to an
// [http.Request] so custom [http.RoundTripper] implementations can access
// metadata (e.g. SessionID and AgentID) without additional parameters.
type copilotContextKey struct{}

// RequestContextFrom returns the [CopilotRequestContext] attached to an
// http.Request by the adapter, or nil if not present. Call this from a custom
// [http.RoundTripper] to access metadata such as SessionID and AgentID.
func RequestContextFrom(r *http.Request) *CopilotRequestContext {
	v, _ := r.Context().Value(copilotContextKey{}).(*CopilotRequestContext)
	return v
}

func (h *CopilotRequestHandler) handle(rctx *CopilotRequestContext, sink *responseSink) error {
	if rctx.Transport == "websocket" {
		return h.handleWebSocket(rctx, sink)
	}
	return h.handleHTTP(rctx, sink)
}

func (h *CopilotRequestHandler) roundTripper() http.RoundTripper {
	if h.Transport != nil {
		return h.Transport
	}
	return sharedHTTPTransport
}

func (h *CopilotRequestHandler) handleHTTP(rctx *CopilotRequestContext, sink *responseSink) error {
	httpReq, err := buildHTTPRequest(rctx)
	if err != nil {
		return err
	}
	resp, err := h.roundTripper().RoundTrip(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return streamResponseToSink(resp, sink)
}

func buildHTTPRequest(rctx *CopilotRequestContext) (*http.Request, error) {
	body := drainBody(rctx.body)
	method := strings.ToUpper(rctx.Method)
	var bodyReader io.Reader
	if len(body) > 0 && method != http.MethodGet && method != http.MethodHead {
		bodyReader = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(rctx.Context, method, rctx.URL, bodyReader)
	if err != nil {
		return nil, err
	}
	// Attach rctx so custom RoundTripper implementations can read metadata
	// (e.g. SessionID and AgentID) via [RequestContextFrom].
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), copilotContextKey{}, rctx))
	for name, values := range rctx.Headers {
		if isForbiddenRequestHeader(name) {
			continue
		}
		for _, v := range values {
			httpReq.Header.Add(name, v)
		}
	}
	return httpReq, nil
}

func drainBody(ch <-chan CopilotWebSocketMessage) []byte {
	var buf bytes.Buffer
	for frame := range ch {
		buf.Write(frame.Data)
	}
	return buf.Bytes()
}

func streamResponseToSink(resp *http.Response, sink *responseSink) error {
	if err := sink.start(resp.StatusCode, statusText(resp), cloneHeader(resp.Header)); err != nil {
		return err
	}
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			// writeText copies eagerly via string(...), so the reused read
			// buffer can be passed directly without an extra per-chunk alloc.
			if err := sink.writeText(buf[:n]); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return sink.sinkError(readErr.Error(), "")
		}
	}
	return sink.end()
}

func statusText(resp *http.Response) string {
	return strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)))
}

func cloneHeader(h http.Header) http.Header {
	out := http.Header{}
	for k, vs := range h {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

func (h *CopilotRequestHandler) handleWebSocket(rctx *CopilotRequestContext, sink *responseSink) error {
	var handler CopilotWebSocketHandler
	var err error
	if h.OpenWebSocket != nil {
		handler, err = h.OpenWebSocket(rctx)
	} else {
		handler = NewCopilotWebSocketForwarder(rctx.URL, rctx.Headers)
	}
	if err != nil {
		return err
	}

	writer := &wsResponseWriter{sink: sink}
	// Emit the 101 upgrade head eagerly — the runtime gates connect_via_callback
	// on receiving httpResponseStart/101 before sending request chunks; a lazy
	// first-write start deadlocks until timeout.
	if err := writer.start(); err != nil {
		return err
	}
	if err := handler.Open(rctx.Context, writer); err != nil {
		return writer.fail(err.Error(), "")
	}
	defer func() { _ = handler.Close() }()

	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		for {
			select {
			case frame, ok := <-rctx.body:
				if !ok {
					return
				}
				if err := handler.SendRequestMessage(rctx.Context, frame); err != nil {
					return
				}
			case <-rctx.Context.Done():
				return
			}
		}
	}()

	select {
	case <-handler.Done():
		if e := handler.Err(); e != nil {
			return writer.fail(e.Error(), "")
		}
		return writer.end()
	case <-clientDone:
		_ = handler.Close()
		<-handler.Done()
		if e := handler.Err(); e != nil {
			return writer.fail(e.Error(), "")
		}
		return writer.end()
	case <-rctx.Context.Done():
		return writer.fail("Request cancelled by runtime", "cancelled")
	}
}

// wsResponseWriter serialises WebSocket response writes into the sink.
type wsResponseWriter struct {
	mu        sync.Mutex
	sink      *responseSink
	started   bool
	completed bool
}

func (w *wsResponseWriter) start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return nil
	}
	w.started = true
	return w.sink.start(101, "", http.Header{})
}

func (w *wsResponseWriter) SendText(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.completed {
		return nil
	}
	return w.sink.writeText(data)
}

func (w *wsResponseWriter) SendBinary(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.completed {
		return nil
	}
	return w.sink.writeBinary(data)
}

func (w *wsResponseWriter) end() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.completed {
		return nil
	}
	w.completed = true
	return w.sink.end()
}

func (w *wsResponseWriter) fail(message string, code string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.completed {
		return nil
	}
	w.completed = true
	return w.sink.sinkError(message, code)
}

// CopilotWebSocketForwarder is the default [CopilotWebSocketHandler]:
// it dials the real upstream and runs a receive loop forwarding upstream→runtime
// messages. Set OnSendRequestMessage / OnSendResponseMessage to observe,
// transform, or drop messages in either direction.
type CopilotWebSocketForwarder struct {
	URL     string
	Headers http.Header
	// OnSendRequestMessage observes or transforms each runtime→upstream frame.
	// The frame type (text vs binary) is available via the message's Binary
	// field and may be changed in the returned message. Return nil to drop the
	// frame.
	OnSendRequestMessage func(msg CopilotWebSocketMessage) *CopilotWebSocketMessage
	// OnSendResponseMessage observes or transforms each upstream→runtime frame.
	// The frame type (text vs binary) is available via the message's Binary
	// field and may be changed in the returned message. Return nil to drop the
	// frame.
	OnSendResponseMessage func(msg CopilotWebSocketMessage) *CopilotWebSocketMessage

	conn      *websocket.Conn
	resp      WebSocketResponseWriter
	done      chan struct{}
	err       error
	closeOnce sync.Once
}

// NewCopilotWebSocketForwarder creates a forwarding handler targeting
// url with the given handshake headers.
func NewCopilotWebSocketForwarder(url string, headers http.Header) *CopilotWebSocketForwarder {
	return &CopilotWebSocketForwarder{URL: url, Headers: headers, done: make(chan struct{})}
}

func (f *CopilotWebSocketForwarder) Open(ctx context.Context, resp WebSocketResponseWriter) error {
	f.resp = resp
	if f.done == nil {
		f.done = make(chan struct{})
	}
	opts := &websocket.DialOptions{HTTPHeader: f.dialHeaders()}
	conn, _, err := websocket.Dial(ctx, f.URL, opts)
	if err != nil {
		return err
	}
	conn.SetReadLimit(-1)
	f.conn = conn
	go f.receiveLoop(ctx)
	return nil
}

func (f *CopilotWebSocketForwarder) dialHeaders() http.Header {
	out := http.Header{}
	for name, values := range f.Headers {
		if isForbiddenRequestHeader(name) {
			continue
		}
		for _, v := range values {
			out.Add(name, v)
		}
	}
	return out
}

func (f *CopilotWebSocketForwarder) receiveLoop(ctx context.Context) {
	defer close(f.done)
	for {
		typ, data, err := f.conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
				f.err = nil
			} else if ctx.Err() != nil {
				f.err = nil
			} else {
				f.err = err
			}
			return
		}
		out := CopilotWebSocketMessage{Data: data, Binary: typ == websocket.MessageBinary}
		if f.OnSendResponseMessage != nil {
			transformed := f.OnSendResponseMessage(out)
			if transformed == nil {
				continue
			}
			out = *transformed
		}
		if out.Binary {
			_ = f.resp.SendBinary(out.Data)
		} else {
			_ = f.resp.SendText(out.Data)
		}
	}
}

func (f *CopilotWebSocketForwarder) SendRequestMessage(ctx context.Context, msg CopilotWebSocketMessage) error {
	out := msg
	if f.OnSendRequestMessage != nil {
		transformed := f.OnSendRequestMessage(msg)
		if transformed == nil {
			return nil
		}
		out = *transformed
	}
	if f.conn == nil {
		return nil
	}
	msgType := websocket.MessageText
	if out.Binary {
		msgType = websocket.MessageBinary
	}
	return f.conn.Write(ctx, msgType, out.Data)
}

func (f *CopilotWebSocketForwarder) Done() <-chan struct{} { return f.done }
func (f *CopilotWebSocketForwarder) Err() error            { return f.err }

func (f *CopilotWebSocketForwarder) Close() error {
	f.closeOnce.Do(func() {
		if f.conn != nil {
			_ = f.conn.Close(websocket.StatusNormalClosure, "")
		}
	})
	return nil
}

// --- Internal adapter ---

// frameQueue is an unbounded FIFO of body frames, decoupling the RPC dispatch
// goroutine (which only pushes) from the consumer goroutine (which pops).
type frameQueue struct {
	mu    sync.Mutex
	cond  *sync.Cond
	items []CopilotWebSocketMessage
	done  bool
}

func newFrameQueue() *frameQueue {
	q := &frameQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *frameQueue) push(m CopilotWebSocketMessage) {
	q.mu.Lock()
	if !q.done {
		q.items = append(q.items, m)
	}
	q.cond.Signal()
	q.mu.Unlock()
}

func (q *frameQueue) close() {
	q.mu.Lock()
	q.done = true
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *frameQueue) pop() (CopilotWebSocketMessage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.done {
		q.cond.Wait()
	}
	if len(q.items) > 0 {
		m := q.items[0]
		q.items = q.items[1:]
		return m, true
	}
	return CopilotWebSocketMessage{}, false
}

type pendingExchange struct {
	mu       sync.Mutex
	queue    *frameQueue
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	finished bool
}

type copilotRequestAdapter struct {
	handler *CopilotRequestHandler
	getRPC  func() *rpc.ServerLlmInferenceAPI

	mu      sync.Mutex
	pending map[string]*pendingExchange
}

func newCopilotRequestAdapter(handler *CopilotRequestHandler, getRPC func() *rpc.ServerLlmInferenceAPI) rpc.LlmInferenceHandler {
	return &copilotRequestAdapter{
		handler: handler,
		getRPC:  getRPC,
		pending: make(map[string]*pendingExchange),
	}
}

// getOrCreateExchange returns the exchange for requestID, allocating one if it
// does not yet exist. The runtime dispatches httpRequestStart and
// httpRequestChunk frames on separate goroutines (see jsonrpc2.handleRequest),
// so a body chunk — including the terminal end frame — can arrive before its
// start frame runs. Creating the exchange (and its buffering frameQueue) on
// first touch means those chunks are buffered rather than dropped, instead of
// hanging the body drain forever.
func (a *copilotRequestAdapter) getOrCreateExchange(requestID string) *pendingExchange {
	a.mu.Lock()
	defer a.mu.Unlock()
	if exchange, ok := a.pending[requestID]; ok {
		return exchange
	}
	ctx, cancel := context.WithCancel(context.Background())
	exchange := &pendingExchange{queue: newFrameQueue(), ctx: ctx, cancel: cancel}
	a.pending[requestID] = exchange
	return exchange
}

func (a *copilotRequestAdapter) HttpRequestStart(params *rpc.LlmInferenceHTTPRequestStartRequest) (*rpc.LlmInferenceHTTPRequestStartResult, error) {
	// Adopt any exchange a racing chunk already created — with its buffered
	// body — rather than dropping those frames.
	exchange := a.getOrCreateExchange(params.RequestID)
	ctx := exchange.ctx
	bodyCh := make(chan CopilotWebSocketMessage)

	go func() {
		defer close(bodyCh)
		for {
			m, ok := exchange.queue.pop()
			if !ok {
				return
			}
			select {
			case bodyCh <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	transport := "http"
	if params.Transport != nil {
		transport = string(*params.Transport)
	}
	sessionID := ""
	if params.SessionID != nil {
		sessionID = *params.SessionID
	}
	headers := http.Header{}
	for k, v := range params.Headers {
		headers[k] = append([]string(nil), v...)
	}

	rctx := &CopilotRequestContext{
		RequestID:       params.RequestID,
		SessionID:       sessionID,
		AgentID:         stringOrEmpty(params.AgentID),
		ParentAgentID:   stringOrEmpty(params.ParentAgentID),
		InteractionType: stringOrEmpty(params.InteractionType),
		Method:          params.Method,
		URL:             params.URL,
		Headers:         headers,
		Transport:       transport,
		body:            bodyCh,
		Context:         ctx,
	}
	sink := &responseSink{requestID: params.RequestID, adapter: a, exchange: exchange}
	go a.runHandler(rctx, sink, exchange)
	return &rpc.LlmInferenceHTTPRequestStartResult{}, nil
}

func (a *copilotRequestAdapter) HttpRequestChunk(params *rpc.LlmInferenceHTTPRequestChunkRequest) (*rpc.LlmInferenceHTTPRequestChunkResult, error) {
	// May arrive before the matching start frame (frames are dispatched on
	// separate goroutines); get-or-create so the body is buffered, never lost.
	exchange := a.getOrCreateExchange(params.RequestID)
	a.routeChunk(exchange, params)
	return &rpc.LlmInferenceHTTPRequestChunkResult{}, nil
}

func (a *copilotRequestAdapter) routeChunk(exchange *pendingExchange, params *rpc.LlmInferenceHTTPRequestChunkRequest) {
	if params.Cancel != nil && *params.Cancel {
		exchange.cancel()
		exchange.queue.close()
		return
	}
	if params.Data != "" {
		binary := params.Binary != nil && *params.Binary
		if data, err := decodeChunkData(params.Data, binary); err == nil {
			exchange.queue.push(CopilotWebSocketMessage{Data: data, Binary: binary})
		}
	}
	if params.End != nil && *params.End {
		exchange.queue.close()
	}
}

func (a *copilotRequestAdapter) runHandler(rctx *CopilotRequestContext, sink *responseSink, exchange *pendingExchange) {
	err := a.handler.handle(rctx, sink)
	if err != nil {
		if exchange.ctx.Err() != nil {
			a.finishCancelled(sink, exchange)
			return
		}
		a.failViaSink(sink, exchange, err.Error())
		return
	}
	exchange.mu.Lock()
	finished := exchange.finished
	exchange.mu.Unlock()
	if !finished {
		a.failViaSink(sink, exchange, "CopilotRequestHandler returned without finalising the response")
	}
}

func (a *copilotRequestAdapter) failViaSink(sink *responseSink, exchange *pendingExchange, message string) {
	exchange.mu.Lock()
	finished := exchange.finished
	started := exchange.started
	exchange.mu.Unlock()
	if finished {
		return
	}
	if !started {
		_ = sink.start(502, "", http.Header{})
	}
	_ = sink.sinkError(message, "")
}

func (a *copilotRequestAdapter) finishCancelled(sink *responseSink, exchange *pendingExchange) {
	exchange.mu.Lock()
	finished := exchange.finished
	started := exchange.started
	exchange.mu.Unlock()
	if finished {
		return
	}
	if !started {
		_ = sink.start(499, "", http.Header{})
	}
	_ = sink.sinkError("Request cancelled by runtime", "cancelled")
}

func (a *copilotRequestAdapter) removePending(requestID string) {
	a.mu.Lock()
	delete(a.pending, requestID)
	a.mu.Unlock()
}

func stringOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func decodeChunkData(data string, binary bool) ([]byte, error) {
	if binary {
		return base64.StdEncoding.DecodeString(data)
	}
	return []byte(data), nil
}

// responseSink writes response frames to the runtime via RPC.
type responseSink struct {
	requestID string
	adapter   *copilotRequestAdapter
	exchange  *pendingExchange
}

func (s *responseSink) rpcAPI() (*rpc.ServerLlmInferenceAPI, error) {
	r := s.adapter.getRPC()
	if r == nil {
		return nil, fmt.Errorf("CopilotRequestHandler response sink used after RPC connection closed")
	}
	return r, nil
}

func (s *responseSink) start(status int, statusTxt string, headers http.Header) error {
	s.exchange.mu.Lock()
	if s.exchange.started {
		s.exchange.mu.Unlock()
		return fmt.Errorf("CopilotRequestHandler response sink Start() called twice")
	}
	if s.exchange.finished {
		s.exchange.mu.Unlock()
		return fmt.Errorf("CopilotRequestHandler response sink already finished")
	}
	s.exchange.started = true
	s.exchange.mu.Unlock()

	api, err := s.rpcAPI()
	if err != nil {
		return err
	}
	var st *string
	if statusTxt != "" {
		st = &statusTxt
	}
	h := map[string][]string(headers)
	if h == nil {
		h = map[string][]string{}
	}
	_, err = api.HttpResponseStart(context.Background(), &rpc.LlmInferenceHTTPResponseStartRequest{
		RequestID:  s.requestID,
		Status:     int64(status),
		StatusText: st,
		Headers:    h,
	})
	return err
}

func (s *responseSink) writeText(data []byte) error {
	return s.writeRaw(string(data), false)
}

func (s *responseSink) writeBinary(data []byte) error {
	return s.writeRaw(base64.StdEncoding.EncodeToString(data), true)
}

func (s *responseSink) writeRaw(data string, binary bool) error {
	s.exchange.mu.Lock()
	started := s.exchange.started
	finished := s.exchange.finished
	s.exchange.mu.Unlock()
	if !started {
		return fmt.Errorf("CopilotRequestHandler response sink Write() called before Start()")
	}
	if finished {
		return fmt.Errorf("CopilotRequestHandler response sink Write() called after End()/Error()")
	}
	api, err := s.rpcAPI()
	if err != nil {
		return err
	}
	end := false
	chunk := &rpc.LlmInferenceHTTPResponseChunkRequest{
		RequestID: s.requestID,
		Data:      data,
		End:       &end,
	}
	if binary {
		b := true
		chunk.Binary = &b
	}
	_, err = api.HttpResponseChunk(context.Background(), chunk)
	return err
}

func (s *responseSink) end() error {
	s.exchange.mu.Lock()
	if s.exchange.finished {
		s.exchange.mu.Unlock()
		return nil
	}
	s.exchange.finished = true
	s.exchange.mu.Unlock()
	s.adapter.removePending(s.requestID)
	api, err := s.rpcAPI()
	if err != nil {
		return err
	}
	end := true
	_, err = api.HttpResponseChunk(context.Background(), &rpc.LlmInferenceHTTPResponseChunkRequest{
		RequestID: s.requestID,
		Data:      "",
		End:       &end,
	})
	return err
}

func (s *responseSink) sinkError(message string, code string) error {
	s.exchange.mu.Lock()
	if s.exchange.finished {
		s.exchange.mu.Unlock()
		return nil
	}
	s.exchange.finished = true
	s.exchange.mu.Unlock()
	s.adapter.removePending(s.requestID)
	api, err := s.rpcAPI()
	if err != nil {
		return err
	}
	end := true
	chunkErr := &rpc.LlmInferenceHTTPResponseChunkError{Message: message}
	if code != "" {
		c := code
		chunkErr.Code = &c
	}
	_, err = api.HttpResponseChunk(context.Background(), &rpc.LlmInferenceHTTPResponseChunkRequest{
		RequestID: s.requestID,
		Data:      "",
		End:       &end,
		Error:     chunkErr,
	})
	return err
}
