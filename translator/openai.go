package translator

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OpenAIStreamWriter serialises translator Events into OpenAI Chat Completion
// v1 SSE frames (`data: {...}` followed by a blank line, terminating with
// `data: [DONE]`).
type OpenAIStreamWriter struct {
	Model     string
	ID        string
	Created   int64
	SentStart bool
}

// NewOpenAIStreamWriter creates a writer with sensible defaults.
func NewOpenAIStreamWriter(model string) *OpenAIStreamWriter {
	return &OpenAIStreamWriter{
		Model:   model,
		ID:      "chatcmpl-" + uuid.NewString(),
		Created: time.Now().Unix(),
	}
}

// Encode returns the SSE frame(s) for one Event. Multi-frame results (e.g.
// initial role delta on the very first chunk) are concatenated.
func (w *OpenAIStreamWriter) Encode(ev *Event) []byte {
	if ev == nil {
		return nil
	}
	switch ev.Kind {
	case EventTextDelta:
		var buf []byte
		if !w.SentStart {
			w.SentStart = true
			buf = append(buf, w.frame(map[string]any{
				"index":         0,
				"delta":         map[string]string{"role": "assistant"},
				"finish_reason": nil,
			}, "")...)
		}
		buf = append(buf, w.frame(map[string]any{
			"index":         0,
			"delta":         map[string]string{"content": ev.Text},
			"finish_reason": nil,
		}, "")...)
		return buf

	case EventToolCallStarted:
		if !w.SentStart {
			w.SentStart = true
		}
		return w.frame(map[string]any{
			"index": 0,
			"delta": map[string]any{
				"role": "assistant",
				"tool_calls": []map[string]any{{
					"index": 0,
					"id":    ev.ToolCallID,
					"type":  "function",
					"function": map[string]any{
						"name":      ev.ToolName,
						"arguments": "",
					},
				}},
			},
			"finish_reason": nil,
		}, "")

	case EventToolCallDelta:
		return w.frame(map[string]any{
			"index": 0,
			"delta": map[string]any{
				"tool_calls": []map[string]any{{
					"index": 0,
					"function": map[string]any{
						"arguments": ev.ToolArgsDelta,
					},
				}},
			},
			"finish_reason": nil,
		}, "")

	case EventTurnEnded:
		return w.frame(map[string]any{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}, "usage")
	}
	return nil
}

// FinalDone returns the terminal `data: [DONE]\n\n` frame.
func (w *OpenAIStreamWriter) FinalDone() []byte {
	return []byte("data: [DONE]\n\n")
}

// frame renders one SSE frame with the standard OpenAI ChatCompletionChunk envelope.
func (w *OpenAIStreamWriter) frame(choice map[string]any, extraKey string) []byte {
	obj := map[string]any{
		"id":      w.ID,
		"object":  "chat.completion.chunk",
		"created": w.Created,
		"model":   w.Model,
		"choices": []any{choice},
	}
	b, _ := json.Marshal(obj)
	return []byte(fmt.Sprintf("data: %s\n\n", string(b)))
}

// EncodeNonStreaming builds a full OpenAI Chat Completion response (non-streaming
// mode). Call once after consuming the whole event stream, accumulating text
// and usage as you go.
type NonStreamingAccumulator struct {
	Model      string
	Text       string
	ToolCalls  []map[string]any
	Usage      *Usage
	FinishStop bool
}

func (a *NonStreamingAccumulator) Consume(ev *Event) {
	if ev == nil {
		return
	}
	switch ev.Kind {
	case EventTextDelta:
		a.Text += ev.Text
	case EventTurnEnded:
		a.FinishStop = true
		a.Usage = ev.Usage
	}
}

func (a *NonStreamingAccumulator) Response(id string) []byte {
	usage := map[string]any{}
	if a.Usage != nil {
		usage["prompt_tokens"] = a.Usage.InputTokens
		usage["completion_tokens"] = a.Usage.OutputTokens
		usage["total_tokens"] = a.Usage.InputTokens + a.Usage.OutputTokens
	}
	obj := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   a.Model,
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]string{
				"role":    "assistant",
				"content": a.Text,
			},
			"finish_reason": "stop",
		}},
		"usage": usage,
	}
	b, _ := json.Marshal(obj)
	return b
}
