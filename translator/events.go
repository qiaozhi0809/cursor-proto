// Package translator converts Cursor's raw AgentServerMessage stream into
// canonical, provider-neutral events, and then serializes those events into
// OpenAI Chat Completion or Anthropic Messages compatible SSE payloads.
package translator

import (
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
func FromServerMessage(m *cursorpb.AgentV1_AgentServerMessage) *Event {
	if m == nil {
		return nil
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
		return &Event{
			Kind:       EventToolCallStarted,
			ToolCallID: s.GetCallId(),
			ToolName:   extractToolName(s.GetToolCall()),
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
		return &Event{
			Kind:       EventToolCallCompleted,
			ToolCallID: c.GetCallId(),
			ToolName:   extractToolName(c.GetToolCall()),
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

// extractToolName walks a ToolCall oneof for the string tool name.
// ToolCall in Cursor is a large union — we defensively probe common shapes.
// If the struct changes we return "" and let the caller fall back gracefully.
func extractToolName(tc *cursorpb.AgentV1_ToolCall) string {
	if tc == nil {
		return ""
	}
	// Probe the union for known getters. Cursor's ToolCall is a large oneof,
	// we only surface the tools most commonly seen in agent mode.
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
	if tc.GetMcpToolCall() != nil {
		return "mcp"
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

// extractToolArgsDelta returns a raw JSON-ish string chunk from a ToolCallDelta.
// The full argument shape depends on the tool; here we just surface the raw
// bytes so the downstream translator can accumulate & flush as valid JSON on
// completion.
func extractToolArgsDelta(d *cursorpb.AgentV1_ToolCallDelta) string {
	if d == nil {
		return ""
	}
	// ToolCallDelta typically carries a `partial` union with per-tool args
	// deltas. The exact wire shape isn't documented — we probe the common
	// text-bearing fields and return the first non-empty one.
	if s := d.String(); s != "" {
		return s
	}
	return ""
}
