package executor

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/router-for-me/cursor-proto/auth"
	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
)

// ChatRequest carries the minimum parameters needed to start an agent run.
type ChatRequest struct {
	Model          string // e.g. "composer-2.5"
	UserMessage    string // the human turn text
	ConversationID string // optional; auto-generated if empty
	WorkspacePath  string // optional; default os.Getwd
	Mode           uint32 // 1=ask, 3=agent  (default 3)

	// SystemPrompt overrides Cursor's default coding-assistant prompt with
	// your own. Populates AgentRunRequest.CustomSystemPrompt (field 8).
	// NOTE: Cursor's server rejects this field with `unknown option
	// '--system-prompt'` when the request presents as an IDE. Use Harness=""
	// (or a non-CLI value like "api") if you need a custom system prompt.
	SystemPrompt string

	// Harness overrides the harness label sent to the server. Empty means
	// omit the field entirely. Common values: "cursor-ide", "cursor-cli",
	// or leave empty for API-style access.
	Harness string

	// PureMode strips Cursor-specific env metadata from the request so the
	// server treats us as a bare API caller rather than an IDE. Toggling this
	// on removes workspace paths, shell env, and the "cursor-ide" harness tag.
	PureMode bool

	// AutoStopOnTurnEnd closes the event channel automatically as soon as a
	// turn_ended interaction arrives. Without this the server keeps the SSE
	// open with heartbeats indefinitely.
	AutoStopOnTurnEnd bool
}

// ChatEvent is one decoded server-side envelope from the SSE stream.
type ChatEvent struct {
	// Trailer, if this frame carried grpc-status metadata (end of stream).
	Trailer bool
	// Server → client message, if the frame was a data frame.
	// Exactly one of the oneof fields will be populated.
	Server *cursorpb.AgentV1_AgentServerMessage
	// Raw payload bytes (in case Server failed to unmarshal).
	Raw []byte
}

// RunChat starts an agent run and yields decoded server messages until the
// stream closes. The caller consumes events by iterating over the returned
// channel; the channel closes on stream end.
//
// Two HTTP calls happen in parallel:
//  1. POST api3/agent.v1.AgentService/RunSSE  — envelope-framed BidiRequestId
//     as body, SSE stream response.
//  2. POST api2/aiserver.v1.BidiService/BidiAppend  — envelope-framed
//     AgentClientMessage (wrapping the full AgentRunRequest).
//
// Cursor pairs the two via the shared request-id string.
func (c *Client) RunChat(ctx context.Context, req *ChatRequest) (<-chan ChatEvent, error) {
	if req.Mode == 0 {
		req.Mode = 3
	}
	if req.Model == "" {
		req.Model = "claude-4.5-sonnet"
	}
	requestID := auth.GenerateRequestID()
	if req.ConversationID == "" {
		req.ConversationID = auth.GenerateSessionID()
	}
	messageID := auth.GenerateRequestID()

	// If the caller supplied a SystemPrompt, prepend it to the user turn.
	// Cursor's backend rejects custom_system_prompt outright; splicing works
	// because the model treats the leading block as high-priority instruction.
	if req.SystemPrompt != "" {
		req.UserMessage = spliceSystemPrompt(req.SystemPrompt, req.UserMessage)
	}

	// Build the full AgentRunRequest. The BidiAppend payload wraps it as
	// field 1 of a manually-built "AgentClientMessage" (Cursor doesn't ship a
	// dedicated proto message for this outer envelope; per CursorGateway's
	// reverse-engineering the wrapper is just `{ field 1: AgentRunRequest }`).
	agentRun, err := c.buildAgentRunRequest(req, messageID)
	if err != nil {
		return nil, err
	}
	agentRunBytes, err := proto.Marshal(agentRun)
	if err != nil {
		return nil, fmt.Errorf("marshal AgentRunRequest: %w", err)
	}
	agentClientMsg := appendMessageField(nil, 1, agentRunBytes)

	// Build the RunSSE body: envelope + BidiRequestId proto.
	bidiRequestID := &cursorpb.AiserverV1_BidiRequestId{RequestId: requestID}
	bidiRequestIDBytes, err := proto.Marshal(bidiRequestID)
	if err != nil {
		return nil, fmt.Errorf("marshal BidiRequestId: %w", err)
	}
	sseBody := addConnectEnvelope(bidiRequestIDBytes, false)

	// Kick off the SSE request first (does not block on body; we'll read below).
	sseURL := fmt.Sprintf("%s/agent.v1.AgentService/RunSSE", c.API3)
	sseReq, err := http.NewRequestWithContext(ctx, "POST", sseURL, bytes.NewReader(sseBody))
	if err != nil {
		return nil, err
	}
	sseReq.Header.Set("content-type", "application/grpc-web+proto")
	ApplyCommonHeaders(sseReq, c.Account, requestID)

	// Use a client without a body timeout — the stream can be long.
	sseClient := &http.Client{Timeout: 0}
	sseResp, err := sseClient.Do(sseReq)
	if err != nil {
		return nil, fmt.Errorf("RunSSE dial: %w", err)
	}
	if sseResp.StatusCode != 200 {
		body, _ := io.ReadAll(sseResp.Body)
		sseResp.Body.Close()
		return nil, fmt.Errorf("RunSSE http %d: %s", sseResp.StatusCode, string(body))
	}

	// Send the first BidiAppend carrying the AgentClientMessage.
	if err := c.bidiAppend(ctx, requestID, 0, agentClientMsg); err != nil {
		sseResp.Body.Close()
		return nil, fmt.Errorf("BidiAppend seed: %w", err)
	}

	events := make(chan ChatEvent, 32)
	go readSSEStream(sseResp.Body, events, req.AutoStopOnTurnEnd)
	return events, nil
}

// postAssistantGrace is how long the reader keeps going after seeing the
// terminal assistant-role blob. If turn_ended also arrived we close after this.
const postAssistantGrace = 1 * time.Second

// heartbeatDeadline caps the total read time when only heartbeats have been
// arriving — protects against a server that never sends turn_ended.
const heartbeatDeadline = 60 * time.Second

func readSSEStream(body io.ReadCloser, out chan<- ChatEvent, autoStopOnTurnEnd bool) {
	defer close(out)
	defer body.Close()

	deadline := make(chan struct{})
	var deadlineOnce bool
	setDeadline := func(d time.Duration) {
		if deadlineOnce {
			return
		}
		deadlineOnce = true
		go func() {
			time.Sleep(d)
			close(deadline)
			body.Close()
		}()
	}

	turnEnded := false
	sawAssistant := false

	buf := make([]byte, 0, 8192)
	tmp := make([]byte, 4096)
	for {
		select {
		case <-deadline:
			return
		default:
		}
		n, err := body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				payload, isTrailer, rest, ok := splitConnectFrame(buf)
				if !ok {
					break
				}
				buf = append(buf[:0], rest...)

				ev := ChatEvent{Trailer: isTrailer, Raw: append([]byte(nil), payload...)}
				if !isTrailer {
					msg := &cursorpb.AgentV1_AgentServerMessage{}
					if e := proto.Unmarshal(payload, msg); e == nil {
						ev.Server = msg
						// Watch for terminal signals so we can close eagerly
						// without waiting for the server's idle heartbeats.
						if msg.GetInteractionUpdate().GetTurnEnded() != nil {
							turnEnded = true
						}
						if autoStopOnTurnEnd && sniffAssistantBlob(msg) {
							sawAssistant = true
						}
						if autoStopOnTurnEnd && turnEnded && sawAssistant {
							setDeadline(postAssistantGrace)
						} else if autoStopOnTurnEnd && turnEnded && !deadlineOnce {
							// turn_ended came first; wait for the assistant
							// blob up to a bounded window.
							setDeadline(heartbeatDeadline / 6) // 10s
						}
					}
				}
				out <- ev
				if isTrailer {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}

// sniffAssistantBlob returns true when the message is a KV SetBlobArgs whose
// payload contains an AI-SDK style `"role":"assistant"` (and/or
// `"role": "assistant"` with a space). This is the terminal marker Cursor
// emits at the end of a turn (see docs/phase-6).
func sniffAssistantBlob(m *cursorpb.AgentV1_AgentServerMessage) bool {
	kv := m.GetKvServerMessage()
	if kv == nil {
		return false
	}
	sb := kv.GetSetBlobArgs()
	if sb == nil {
		return false
	}
	data := sb.GetBlobData()
	if len(data) == 0 || len(data) > 200_000 {
		return false
	}
	// Cursor's assistant blob starts with `{"role":"assistant"` (no space) in
	// captured samples. Scan the whole payload to be safe.
	needle := []byte(`"role":"assistant"`)
	return bytesContains(data, needle)
}

// bytesContains reports whether haystack contains needle. Avoids importing
// `bytes` for one function to keep this file dependency-light.
func bytesContains(haystack, needle []byte) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	last := len(haystack) - len(needle)
	for i := 0; i <= last; i++ {
		if haystack[i] == needle[0] {
			match := true
			for j := 1; j < len(needle); j++ {
				if haystack[i+j] != needle[j] {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// bidiAppend sends one AgentClientMessage (or any data blob) to BidiAppend.
// The request-id + seq no. glues it to the concurrent RunSSE.
//
// BidiAppend is a Connect *unary* RPC (server_streaming is only the RunSSE half):
// the request body is the raw protobuf-serialised message (no 5-byte envelope
// framing) and the content-type is `application/proto`. This matches
// mitmproxy captures from 2026-07-09 which showed no envelope prefix and the
// content-type `application/proto` for CppAppend / FileSync / etc.
func (c *Client) bidiAppend(ctx context.Context, requestID string, seq int64, payload []byte) error {
	body, err := proto.Marshal(&cursorpb.AiserverV1_BidiAppendRequest{
		Data:        hexEncode(payload), // legacy field, CursorGateway still populates this
		DataBinary:  payload,             // 3.10 preferred wire form
		RequestId:   &cursorpb.AiserverV1_BidiRequestId{RequestId: requestID},
		AppendSeqno: seq,
	})
	if err != nil {
		return fmt.Errorf("marshal BidiAppendRequest: %w", err)
	}

	url := fmt.Sprintf("%s/aiserver.v1.BidiService/BidiAppend", c.API2)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/proto")
	ApplyCommonHeaders(req, c.Account, auth.GenerateRequestID())

	cli := &http.Client{Timeout: 30 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := readBody(resp)
	if resp.StatusCode != 200 {
		return fmt.Errorf("BidiAppend http %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// encodeBidiAppendRequest produces the wire form for a BidiAppendRequest.
// Field layout (matches CursorGateway):
//
//	1: string data      — hex-encoded payload
//	2: message request_id (BidiRequestId { 1: string request_id })
//	3: int64 seqno
func encodeBidiAppendRequest(payload []byte, requestID string, seq int64) []byte {
	dataHex := hexEncode(payload)
	var out []byte
	out = appendStringField(out, 1, dataHex)
	// nested request_id message
	inner := appendStringField(nil, 1, requestID)
	out = appendMessageField(out, 2, inner)
	out = appendInt64Field(out, 3, seq)
	return out
}

// ---- Minimal proto wire encoding helpers (varint + length-delimited) ----

func appendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

func appendTag(buf []byte, field int, wire int) []byte {
	return appendVarint(buf, uint64(field)<<3|uint64(wire))
}

func appendStringField(buf []byte, field int, s string) []byte {
	buf = appendTag(buf, field, 2) // wire type 2 = length-delimited
	buf = appendVarint(buf, uint64(len(s)))
	return append(buf, s...)
}

func appendMessageField(buf []byte, field int, msg []byte) []byte {
	buf = appendTag(buf, field, 2)
	buf = appendVarint(buf, uint64(len(msg)))
	return append(buf, msg...)
}

func appendInt64Field(buf []byte, field int, v int64) []byte {
	buf = appendTag(buf, field, 0) // wire type 0 = varint
	return appendVarint(buf, uint64(v))
}

func appendBytesField(buf []byte, field int, b []byte) []byte {
	buf = appendTag(buf, field, 2)
	buf = appendVarint(buf, uint64(len(b)))
	return append(buf, b...)
}

func hexEncode(b []byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

// Silence unused warnings if new callers appear; used by executor/chat_build.go.
var _ = appendBytesField
var _ = binary.BigEndian
