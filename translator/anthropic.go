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
	Model      string
	ID         string
	blockOpen  bool
	blockIndex int
	sentStart  bool
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

	case EventTurnEnded:
		if w.blockOpen {
			buf = append(buf, w.frame("content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": w.blockIndex,
			})...)
			w.blockOpen = false
		}
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
		buf = append(buf, w.frame("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   "end_turn",
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
