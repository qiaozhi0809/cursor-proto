package translator

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// AnthropicStreamWriter serialises translator Events into Anthropic Messages
// v1 SSE frames.
//
// Anthropic's stream shape:
//
//	event: message_start
//	data: {...}
//
//	event: content_block_start
//	data: {"index":0,"content_block":{"type":"text","text":""}}
//
//	event: content_block_delta
//	data: {"index":0,"delta":{"type":"text_delta","text":"Hello"}}
//
//	event: content_block_stop
//	data: {"index":0}
//
//	event: message_delta
//	data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":N}}
//
//	event: message_stop
//	data: {}
type AnthropicStreamWriter struct {
	Model       string
	ID          string
	blockOpen   bool
	blockIndex  int
	sentStart   bool
	// toolBlocks maps tool_call_id -> block index for its content_block.
	toolBlocks  map[string]int
	sawToolCall bool
}

func NewAnthropicStreamWriter(model string) *AnthropicStreamWriter {
	return &AnthropicStreamWriter{
		Model: model,
		ID:    "msg_" + uuid.NewString(),
	}
}

// Encode returns the SSE frame(s) for one Event, potentially emitting several
// concatenated blocks (start-of-message + start-of-block + delta on first
// chunk).
func (w *AnthropicStreamWriter) Encode(ev *Event) []byte {
	if ev == nil {
		return nil
	}
	var buf []byte
	switch ev.Kind {
	case EventTextDelta:
		if !w.sentStart {
			w.sentStart = true
			buf = append(buf, w.frame("message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            w.ID,
					"type":          "message",
					"role":          "assistant",
					"model":         w.Model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
				},
			})...)
		}
		if !w.blockOpen {
			w.blockOpen = true
			buf = append(buf, w.frame("content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": w.blockIndex,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			})...)
		}
		buf = append(buf, w.frame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": w.blockIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": ev.Text,
			},
		})...)
		return buf

	case EventToolCallStarted:
		if !w.sentStart {
			w.sentStart = true
			buf = append(buf, w.frame("message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":            w.ID,
					"type":          "message",
					"role":          "assistant",
					"model":         w.Model,
					"content":       []any{},
					"stop_reason":   nil,
					"stop_sequence": nil,
					"usage":         map[string]int{"input_tokens": 0, "output_tokens": 0},
				},
			})...)
		}
		// Close any open text block before opening the tool_use block —
		// Anthropic streams have one content block open at a time.
		if w.blockOpen {
			buf = append(buf, w.frame("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": w.blockIndex,
			})...)
			w.blockOpen = false
			w.blockIndex++
		}
		if w.toolBlocks == nil {
			w.toolBlocks = map[string]int{}
		}
		toolIdx, seen := w.toolBlocks[ev.ToolCallID]
		if !seen {
			toolIdx = w.blockIndex
			w.toolBlocks[ev.ToolCallID] = toolIdx
			w.blockIndex++
		}
		w.sawToolCall = true
		buf = append(buf, w.frame("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": toolIdx,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    ev.ToolCallID,
				"name":  ev.ToolName,
				"input": map[string]any{},
			},
		})...)
		if ev.ToolArgsDelta != "" {
			buf = append(buf, w.frame("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": toolIdx,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": ev.ToolArgsDelta,
				},
			})...)
		}
		return buf

	case EventToolCallDelta:
		if ev.ToolArgsDelta == "" || w.toolBlocks == nil {
			return nil
		}
		toolIdx, ok := w.toolBlocks[ev.ToolCallID]
		if !ok {
			return nil
		}
		return w.frame("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": toolIdx,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": ev.ToolArgsDelta,
			},
		})

	case EventToolCallCompleted:
		if w.toolBlocks == nil {
			return nil
		}
		toolIdx, ok := w.toolBlocks[ev.ToolCallID]
		if !ok {
			return nil
		}
		return w.frame("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": toolIdx,
		})

	case EventTurnEnded:
		if w.blockOpen {
			buf = append(buf, w.frame("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": w.blockIndex,
			})...)
			w.blockOpen = false
		}
		// Close any tool_use blocks that were opened but never received an
		// explicit tool_call_completed event. Cursor doesn't send one when
		// the SSE stalls waiting for a tool result, so we synthesize the
		// content_block_stop frames here.
		for _, idx := range w.toolBlocks {
			buf = append(buf, w.frame("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": idx,
			})...)
		}
		w.toolBlocks = nil
		usage := map[string]any{"output_tokens": 0}
		if ev.Usage != nil {
			usage["input_tokens"] = ev.Usage.InputTokens
			usage["output_tokens"] = ev.Usage.OutputTokens
			if ev.Usage.CacheReadTokens > 0 {
				usage["cache_read_input_tokens"] = ev.Usage.CacheReadTokens
			}
			if ev.Usage.CacheWriteTokens > 0 {
				usage["cache_creation_input_tokens"] = ev.Usage.CacheWriteTokens
			}
		}
		stopReason := "end_turn"
		if w.sawToolCall {
			stopReason = "tool_use"
		}
		buf = append(buf, w.frame("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": usage,
		})...)
		buf = append(buf, w.frame("message_stop", map[string]any{
			"type": "message_stop",
		})...)
		return buf
	}
	return nil
}

func (w *AnthropicStreamWriter) frame(event string, data map[string]any) []byte {
	b, _ := json.Marshal(data)
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, string(b)))
}
