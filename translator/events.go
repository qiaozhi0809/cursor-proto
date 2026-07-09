// Package translator converts Cursor's raw AgentServerMessage stream into
// canonical, provider-neutral events, and then serializes those events into
// OpenAI Chat Completion or Anthropic Messages compatible SSE payloads.
package translator

import (
	"encoding/json"
	"sort"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/router-for-me/cursor-proto/executor"
	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
)

// EventKind names the discriminator on Event.
type EventKind int

const (
	EventUnknown EventKind = iota
	EventTextDelta
	EventThinkingDelta
	EventToolCallStarted
	EventToolCallDelta
	EventToolCallCompleted
	EventTurnEnded
	EventStepStarted
	EventStepCompleted
	EventHeartbeat
)

// Event is a translator-neutral representation of one meaningful thing that
// happened in a Cursor stream. Fields not relevant to the kind are zero.
type Event struct {
	Kind EventKind

	// Text carries the delta text for EventTextDelta / EventThinkingDelta.
	Text string

	// ToolCallID + ToolName + ToolArgsDelta carry incremental tool-use info
	// for EventToolCall*. ArgsDelta is the JSON fragment; the accumulated
	// arguments assembled by the caller.
	ToolCallID    string
	ToolName      string
	ToolArgsDelta string

	// Usage is populated on EventTurnEnded.
	Usage *Usage
}

// Usage aggregates token counters.
type Usage struct {
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
}

// FromServerMessage extracts an Event from one raw AgentServerMessage. Returns
// nil if the message doesn't carry an event we care about (e.g. bare KV blob).
//
// Note: user-supplied MCP tool calls arrive via `ExecServerMessage.mcp_args`,
// not `InteractionUpdate.tool_call_started`. We surface those here so the
// downstream writers can emit a single tool_call event.
func FromServerMessage(m *cursorpb.AgentV1_AgentServerMessage) *Event {
	if m == nil {
		return nil
	}
	if exec := m.GetExecServerMessage(); exec != nil {
		if mcp := exec.GetMcpArgs(); mcp != nil {
			return &Event{
				Kind:          EventToolCallStarted,
				ToolCallID:    mcp.GetToolCallId(),
				ToolName:      executor.RestoreMcpToolName(pickFirstNonEmpty(mcp.GetToolName(), mcp.GetName())),
				ToolArgsDelta: encodeMcpArgs(mcp.GetArgs()),
			}
		}
	}
	iu := m.GetInteractionUpdate()
	if iu == nil {
		return nil
	}
	if td := iu.GetTextDelta(); td != nil {
		return &Event{Kind: EventTextDelta, Text: td.GetText()}
	}
	if td := iu.GetThinkingDelta(); td != nil {
		return &Event{Kind: EventThinkingDelta, Text: td.GetText()}
	}
	if s := iu.GetToolCallStarted(); s != nil {
		tc := s.GetToolCall()
		callID := s.GetCallId()
		if callID == "" {
			callID = extractToolCallID(tc)
		}
		return &Event{
			Kind:          EventToolCallStarted,
			ToolCallID:    callID,
			ToolName:      extractToolName(tc),
			ToolArgsDelta: extractToolArgsFromStart(tc),
		}
	}
	if d := iu.GetToolCallDelta(); d != nil {
		return &Event{
			Kind:          EventToolCallDelta,
			ToolCallID:    d.GetCallId(),
			ToolArgsDelta: extractToolArgsDelta(d.GetToolCallDelta()),
		}
	}
	if c := iu.GetToolCallCompleted(); c != nil {
		tc := c.GetToolCall()
		callID := c.GetCallId()
		if callID == "" {
			callID = extractToolCallID(tc)
		}
		return &Event{
			Kind:       EventToolCallCompleted,
			ToolCallID: callID,
			ToolName:   extractToolName(tc),
		}
	}
	if te := iu.GetTurnEnded(); te != nil {
		u := &Usage{
			InputTokens:      te.GetInputTokens(),
			OutputTokens:     te.GetOutputTokens(),
			CacheReadTokens:  te.GetCacheReadTokens(),
			CacheWriteTokens: te.GetCacheWriteTokens(),
			ReasoningTokens:  te.GetReasoningTokens(),
		}
		return &Event{Kind: EventTurnEnded, Usage: u}
	}
	if iu.GetStepStarted() != nil {
		return &Event{Kind: EventStepStarted}
	}
	if iu.GetStepCompleted() != nil {
		return &Event{Kind: EventStepCompleted}
	}
	if iu.GetHeartbeat() != nil {
		return &Event{Kind: EventHeartbeat}
	}
	return nil
}

// pickFirstNonEmpty returns the first non-empty string in the list, or "".
func pickFirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// encodeMcpArgs converts McpArgs.args (map<string, bytes>) into a JSON object
// string. Each value is a marshaled google.protobuf.Value on the wire; we
// decode it and re-emit the interior value as JSON.
func encodeMcpArgs(args map[string][]byte) string {
	if len(args) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteString(":")
		vb := decodeMcpArgValue(args[k])
		b.Write(vb)
	}
	b.WriteString("}")
	return b.String()
}

// decodeMcpArgValue turns one map value (a marshaled google.protobuf.Value)
// into a JSON fragment. Falls back to raw JSON or a quoted string if the
// bytes don't parse as a Value.
func decodeMcpArgValue(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	var v structpb.Value
	if err := proto.Unmarshal(raw, &v); err == nil {
		if b, err := protojson.Marshal(&v); err == nil {
			return b
		}
	}
	if json.Valid(raw) {
		return raw
	}
	b, _ := json.Marshal(string(raw))
	return b
}

// extractToolName walks a ToolCall oneof for the string tool name.
// ToolCall in Cursor is a large union — we defensively probe common shapes.
// If the struct changes we return "" and let the caller fall back gracefully.
//
// For McpToolCall (the branch user-supplied MCP tools travel through), we
// return the caller's original tool name (undoing the `mcp_` prefix we may
// have added on the way out).
func extractToolName(tc *cursorpb.AgentV1_ToolCall) string {
	if tc == nil {
		return ""
	}
	if mcp := tc.GetMcpToolCall(); mcp != nil {
		if a := mcp.GetArgs(); a != nil {
			// The wire carries the sanitized name in Name; ToolName mirrors
			// it in newer builds. Prefer ToolName when set — it stays stable
			// even if the server rewrites Name.
			name := a.GetToolName()
			if name == "" {
				name = a.GetName()
			}
			return executor.RestoreMcpToolName(name)
		}
		return ""
	}
	// Probe the union for known native tool getters. Cursor's ToolCall is a
	// large oneof; we only surface the tools most commonly seen in agent mode.
	if tc.GetShellToolCall() != nil {
		return "shell"
	}
	if tc.GetReadToolCall() != nil {
		return "read"
	}
	if tc.GetDeleteToolCall() != nil {
		return "delete"
	}
	if tc.GetGrepToolCall() != nil {
		return "grep"
	}
	if tc.GetLsToolCall() != nil {
		return "ls"
	}
	if tc.GetGlobToolCall() != nil {
		return "glob"
	}
	if tc.GetFetchToolCall() != nil {
		return "fetch"
	}
	if tc.GetEditToolCall() != nil {
		return "edit"
	}
	if tc.GetAskQuestionToolCall() != nil {
		return "ask_question"
	}
	return ""
}

// extractToolCallID returns the tool_call_id embedded in a ToolCall envelope
// when present. For MCP tool calls this echoes back what the server assigned;
// for other tool types the top-level ToolCall.tool_call_id is the source.
func extractToolCallID(tc *cursorpb.AgentV1_ToolCall) string {
	if tc == nil {
		return ""
	}
	if id := tc.GetToolCallId(); id != "" {
		return id
	}
	if mcp := tc.GetMcpToolCall(); mcp != nil {
		if a := mcp.GetArgs(); a != nil {
			return a.GetToolCallId()
		}
	}
	return ""
}

// extractToolArgsFromStart returns the full JSON-encoded arguments for a
// tool_call_started envelope. MCP tool calls arrive with the complete args
// map on start (Cursor doesn't stream MCP args incrementally), so we serialize
// them once and let downstream writers emit a single argument delta.
func extractToolArgsFromStart(tc *cursorpb.AgentV1_ToolCall) string {
	if tc == nil {
		return ""
	}
	mcp := tc.GetMcpToolCall()
	if mcp == nil {
		return ""
	}
	a := mcp.GetArgs()
	if a == nil {
		return ""
	}
	args := a.GetArgs()
	if len(args) == 0 {
		return ""
	}
	// McpArgs.args is a map<string, bytes> where each value is the JSON
	// serialization of the argument value. Reassemble a single JSON object,
	// keeping keys sorted for stable output.
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("{")
	for i, k := range keys {
		if i > 0 {
			b.WriteString(",")
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteString(":")
		v := args[k]
		if len(v) == 0 || !json.Valid(v) {
			// Fall back to a JSON string if the raw bytes aren't valid JSON.
			sb, _ := json.Marshal(string(v))
			b.Write(sb)
			continue
		}
		b.Write(v)
	}
	b.WriteString("}")
	return b.String()
}

// extractToolArgsDelta returns a raw JSON-ish string chunk from a ToolCallDelta.
// The full argument shape depends on the tool; here we just surface the raw
// bytes so the downstream translator can accumulate & flush as valid JSON on
// completion.
//
// For MCP tool calls Cursor delivers the full arguments on tool_call_started
// (see extractToolArgsFromStart) — this helper stays for native tools whose
// wire shape isn't yet documented.
func extractToolArgsDelta(d *cursorpb.AgentV1_ToolCallDelta) string {
	if d == nil {
		return ""
	}
	if s := d.String(); s != "" {
		return s
	}
	return ""
}
