// cursor-proxy exposes OpenAI- and Anthropic-compatible HTTP endpoints backed
// by Cursor.
//
// Usage:
//
//	cursor-proxy -addr 127.0.0.1:8317
//
// Endpoints:
//
//	GET  /v1/models
//	POST /v1/chat/completions    (OpenAI Chat Completion)
//	POST /v1/messages            (Anthropic Messages)
//
// The proxy reads Cursor auth from Cursor IDE's SQLite storage (macOS default).
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
	"github.com/router-for-me/cursor-proto/translator"
)

// ---------- OpenAI schemas ----------

type openaiChatRequest struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ---------- Anthropic schemas ----------

type anthropicMessagesRequest struct {
	Model    string             `json:"model"`
	System   any                `json:"system"`
	Messages []anthropicMessage `json:"messages"`
	Stream   bool               `json:"stream"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

// ---------- main ----------

func main() {
	addr := flag.String("addr", "127.0.0.1:8317", "listen address")
	apiKeysFlag := flag.String("api-keys", "", "comma-separated API keys required in Authorization: Bearer header; falls back to $"+apiKeysEnv+" when unset")
	tokenFile := flag.String("token-file", "", "path to account JSON (overrides IDE SQLite lookup); "+
		"env CURSOR_PROXY_ACCOUNT_FILE is used when this flag is empty")
	flag.Parse()

	tokenPath := *tokenFile
	if tokenPath == "" {
		tokenPath = os.Getenv("CURSOR_PROXY_ACCOUNT_FILE")
	}

	var acc *auth.Account
	if tokenPath != "" {
		a, err := auth.LoadAccount(tokenPath)
		if err != nil {
			log.Fatalf("load account from %s: %v", tokenPath, err)
		}
		acc = a
	} else {
		acc = loadAccountFromIDE()
	}

	c := executor.NewClient(acc)
	c.API3 = c.API2 // chat also lives on api2

	apiKeys := LoadAPIKeys(*apiKeysFlag)

	log.Printf("[proxy] cursor account loaded: email=%s", acc.Email)
	log.Printf("[proxy] listening on http://%s", *addr)
	if len(apiKeys) > 0 {
		log.Printf("[proxy] api-key auth enabled: %d key(s) configured", len(apiKeys))
	} else {
		log.Printf("[proxy] api-key auth disabled (set -api-keys or $%s to enable)", apiKeysEnv)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", modelsHandler(c))
	mux.HandleFunc("/v1/usage", usageHandler(c))
	mux.HandleFunc("/v1/usage/prometheus", usagePrometheusHandler(c))
	mux.HandleFunc("/v1/chat/completions", openaiChatHandler(c))
	mux.HandleFunc("/v1/messages", anthropicMessagesHandler(c))

	handler := RequireAPIKeys(apiKeys, mux)
	log.Fatal(http.ListenAndServe(*addr, handler))
}

// ---------- /v1/models ----------

func modelsHandler(c *executor.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resp, err := c.ListModels()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		list := []map[string]any{}
		for _, m := range resp.Models {
			list = append(list, map[string]any{
				"id":       m.GetName(),
				"object":   "model",
				"owned_by": "cursor",
			})
		}
		w.Header().Set("content-type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": list})
	}
}

// ---------- /v1/chat/completions (OpenAI) ----------

func openaiChatHandler(c *executor.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req openaiChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		systemPrompt := ""
		convTurns := make([]openaiMessage, 0, len(req.Messages))
		for _, m := range req.Messages {
			if m.Role == "system" {
				if systemPrompt != "" {
					systemPrompt += "\n"
				}
				systemPrompt += m.Content
				continue
			}
			convTurns = append(convTurns, m)
		}

		lastUserIdx := -1
		for i := len(convTurns) - 1; i >= 0; i-- {
			if convTurns[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx < 0 {
			http.Error(w, "no user message", 400)
			return
		}
		userText := convTurns[lastUserIdx].Content
		history := make([]executor.HistoryTurn, 0, lastUserIdx)
		for _, m := range convTurns[:lastUserIdx] {
			if m.Role != "user" && m.Role != "assistant" {
				continue
			}
			history = append(history, executor.HistoryTurn{Role: m.Role, Content: m.Content})
		}

		events, err := c.RunChat(r.Context(), &executor.ChatRequest{
			Model:             req.Model,
			UserMessage:       userText,
			SystemPrompt:      systemPrompt,
			History:           history,
			ConversationID:    r.Header.Get("x-conversation-id"),
			PureMode:          true,
			AutoStopOnTurnEnd: true,
		})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}

		if req.Stream {
			streamOpenAI(w, req.Model, events)
			return
		}
		nonStreamOpenAI(w, req.Model, events)
	}
}

func streamOpenAI(w http.ResponseWriter, model string, events <-chan executor.ChatEvent) {
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("x-accel-buffering", "no")
	flusher, _ := w.(http.Flusher)

	tr := translator.NewOpenAIStreamWriter(model)
	assistantSent := ""
	for ev := range events {
		if ev.Server == nil {
			continue
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			delta := diffSuffix(assistantSent, blob.AssistantText)
			if delta != "" {
				assistantSent = blob.AssistantText
				payload := tr.Encode(&translator.Event{Kind: translator.EventTextDelta, Text: delta})
				w.Write(payload)
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}
		trEv := translator.FromServerMessage(ev.Server)
		if trEv == nil {
			continue
		}
		if trEv.Kind == translator.EventTurnEnded {
			w.Write(tr.Encode(trEv))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	w.Write(tr.FinalDone())
	if flusher != nil {
		flusher.Flush()
	}
}

func nonStreamOpenAI(w http.ResponseWriter, model string, events <-chan executor.ChatEvent) {
	acc := translator.NonStreamingAccumulator{Model: model}
	for ev := range events {
		if ev.Server == nil {
			continue
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			acc.Text = blob.AssistantText
			continue
		}
		trEv := translator.FromServerMessage(ev.Server)
		if trEv != nil && trEv.Kind == translator.EventTurnEnded {
			acc.Usage = trEv.Usage
			acc.FinishStop = true
		}
	}
	w.Header().Set("content-type", "application/json")
	w.Write(acc.Response("chatcmpl-" + auth.GenerateSessionID()))
}

func diffSuffix(sent, full string) string {
	if sent == "" {
		return full
	}
	if len(full) > len(sent) && full[:len(sent)] == sent {
		return full[len(sent):]
	}
	return full
}

// ---------- /v1/messages (Anthropic) ----------

func anthropicMessagesHandler(c *executor.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req anthropicMessagesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		systemPrompt := flattenAnthropicSystem(req.System)
		lastUserIdx := -1
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == "user" {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx < 0 {
			http.Error(w, "no user message", 400)
			return
		}
		userText := flattenAnthropicContent(req.Messages[lastUserIdx].Content)
		history := make([]executor.HistoryTurn, 0, lastUserIdx)
		for _, m := range req.Messages[:lastUserIdx] {
			if m.Role != "user" && m.Role != "assistant" {
				continue
			}
			history = append(history, executor.HistoryTurn{
				Role:    m.Role,
				Content: flattenAnthropicContent(m.Content),
			})
		}

		events, err := c.RunChat(r.Context(), &executor.ChatRequest{
			Model:             req.Model,
			UserMessage:       userText,
			SystemPrompt:      systemPrompt,
			History:           history,
			ConversationID:    r.Header.Get("x-conversation-id"),
			PureMode:          true,
			AutoStopOnTurnEnd: true,
		})
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}

		if req.Stream {
			streamAnthropic(w, req.Model, events)
			return
		}
		nonStreamAnthropic(w, req.Model, events)
	}
}

func streamAnthropic(w http.ResponseWriter, model string, events <-chan executor.ChatEvent) {
	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("cache-control", "no-cache")
	w.Header().Set("x-accel-buffering", "no")
	flusher, _ := w.(http.Flusher)

	tr := translator.NewAnthropicStreamWriter(model)
	assistantSent := ""
	var lastUsage *translator.Usage
	for ev := range events {
		if ev.Server == nil {
			continue
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			delta := diffSuffix(assistantSent, blob.AssistantText)
			if delta != "" {
				assistantSent = blob.AssistantText
				w.Write(tr.Encode(&translator.Event{Kind: translator.EventTextDelta, Text: delta}))
				if flusher != nil {
					flusher.Flush()
				}
			}
			continue
		}
		trEv := translator.FromServerMessage(ev.Server)
		if trEv != nil && trEv.Kind == translator.EventTurnEnded {
			lastUsage = trEv.Usage
		}
	}
	end := &translator.Event{Kind: translator.EventTurnEnded, Usage: lastUsage}
	w.Write(tr.Encode(end))
	if flusher != nil {
		flusher.Flush()
	}
}

func nonStreamAnthropic(w http.ResponseWriter, model string, events <-chan executor.ChatEvent) {
	assistantText := ""
	var usage *translator.Usage
	for ev := range events {
		if ev.Server == nil {
			continue
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			assistantText = blob.AssistantText
			continue
		}
		trEv := translator.FromServerMessage(ev.Server)
		if trEv != nil && trEv.Kind == translator.EventTurnEnded {
			usage = trEv.Usage
		}
	}
	resp := map[string]any{
		"id":            "msg_" + auth.GenerateSessionID(),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []map[string]string{{"type": "text", "text": assistantText}},
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
	}
	if usage != nil {
		resp["usage"] = map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		}
	}
	w.Header().Set("content-type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func flattenAnthropicSystem(s any) string {
	switch v := s.(type) {
	case string:
		return v
	case []any:
		out := ""
		for _, block := range v {
			b, _ := block.(map[string]any)
			if b == nil {
				continue
			}
			if t, _ := b["text"].(string); t != "" {
				if out != "" {
					out += "\n"
				}
				out += t
			}
		}
		return out
	}
	return ""
}

func flattenAnthropicContent(c any) string {
	switch v := c.(type) {
	case string:
		return v
	case []any:
		out := ""
		for _, block := range v {
			b, _ := block.(map[string]any)
			if b == nil {
				continue
			}
			if t, _ := b["text"].(string); t != "" {
				if out != "" {
					out += "\n"
				}
				out += t
			}
		}
		return out
	}
	return ""
}

// ---------- auth loading ----------

func loadAccountFromIDE() *auth.Account {
	dbPath := os.Getenv("HOME") + "/Library/Application Support/Cursor/User/globalStorage/state.vscdb"
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?mode=ro")
	if err != nil {
		log.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	var access, email string
	if err := db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/accessToken'`).Scan(&access); err != nil {
		log.Fatalf("no accessToken: %v", err)
	}
	_ = db.QueryRow(`SELECT value FROM ItemTable WHERE key = 'cursorAuth/cachedEmail'`).Scan(&email)

	machineID, _ := auth.GetMachineID()
	macID, _ := auth.GetMacMachineID()
	return &auth.Account{
		Email:        email,
		AccessToken:  access,
		MachineID:    machineID,
		MacMachineID: macID,
	}
}
