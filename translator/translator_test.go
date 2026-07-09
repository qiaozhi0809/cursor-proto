package translator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOpenAIStreamShape(t *testing.T) {
	w := NewOpenAIStreamWriter("composer-2.5")

	// First text delta should emit two frames: role delta, then content delta.
	first := string(w.Encode(&Event{Kind: EventTextDelta, Text: "Hello"}))
	if strings.Count(first, "data: ") != 2 {
		t.Errorf("first delta should emit 2 frames, got:\n%s", first)
	}
	if !strings.Contains(first, `"role":"assistant"`) {
		t.Errorf("missing role delta:\n%s", first)
	}
	if !strings.Contains(first, `"content":"Hello"`) {
		t.Errorf("missing content delta:\n%s", first)
	}

	// Second delta = one frame with content only.
	second := string(w.Encode(&Event{Kind: EventTextDelta, Text: " World"}))
	if strings.Count(second, "data: ") != 1 {
		t.Errorf("second delta should emit 1 frame, got:\n%s", second)
	}
	if strings.Contains(second, `"role"`) {
		t.Errorf("second delta must not repeat role:\n%s", second)
	}

	// Turn ended = final frame with finish_reason=stop.
	end := string(w.Encode(&Event{Kind: EventTurnEnded, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}))
	if !strings.Contains(end, `"finish_reason":"stop"`) {
		t.Errorf("missing finish_reason on turn end:\n%s", end)
	}

	// FinalDone marker
	if string(w.FinalDone()) != "data: [DONE]\n\n" {
		t.Error("FinalDone shape wrong")
	}
}

func TestAnthropicStreamShape(t *testing.T) {
	w := NewAnthropicStreamWriter("claude-opus-4-8")

	first := string(w.Encode(&Event{Kind: EventTextDelta, Text: "Hello"}))
	// Should include message_start + content_block_start + content_block_delta
	for _, kw := range []string{"event: message_start", "event: content_block_start", "event: content_block_delta"} {
		if !strings.Contains(first, kw) {
			t.Errorf("missing %q in first delta:\n%s", kw, first)
		}
	}

	end := string(w.Encode(&Event{Kind: EventTurnEnded, Usage: &Usage{InputTokens: 10, OutputTokens: 5}}))
	for _, kw := range []string{"event: content_block_stop", "event: message_delta", "event: message_stop", `"stop_reason":"end_turn"`} {
		if !strings.Contains(end, kw) {
			t.Errorf("missing %q in end frames:\n%s", kw, end)
		}
	}
}

func TestNonStreamingAccumulator(t *testing.T) {
	a := NonStreamingAccumulator{Model: "composer-2.5"}
	a.Consume(&Event{Kind: EventTextDelta, Text: "Hello, "})
	a.Consume(&Event{Kind: EventTextDelta, Text: "world."})
	a.Consume(&Event{Kind: EventTurnEnded, Usage: &Usage{InputTokens: 3, OutputTokens: 2}})

	if a.Text != "Hello, world." {
		t.Errorf("text = %q, want %q", a.Text, "Hello, world.")
	}

	body := a.Response("chatcmpl-test")
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("json: %v", err)
	}
	choices := got["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello, world." {
		t.Errorf("content field mismatch: %v", msg["content"])
	}
	usage := got["usage"].(map[string]any)
	if usage["total_tokens"] != float64(5) {
		t.Errorf("total_tokens = %v, want 5", usage["total_tokens"])
	}
}

func TestFromServerMessageNil(t *testing.T) {
	if FromServerMessage(nil) != nil {
		t.Error("nil input should map to nil event")
	}
}
