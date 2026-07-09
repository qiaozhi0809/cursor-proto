// test-conv-memory probes whether Cursor's backend remembers prior turns
// keyed by ConversationId when the client stops resending history.
//
// Each variant runs turn 1 (which seeds a memorable value under a fresh
// ConversationId) followed by turn 2 (which asks the model to recall it).
// The four variants only differ in how turn 2 is transmitted:
//
//	A: same conversation_id, NO wire history, NO in-band splice
//	B: same conversation_id, WITH wire history, NO in-band splice
//	C: same conversation_id, NO wire history, WITH in-band splice
//	D: new conversation_id, NO wire history, NO in-band splice   (control)
//
// Each variant runs N=3 times to control for flakiness. Turn 2 InputTokens
// is captured for each run so we can quantify the savings when the server
// remembers on its own.
//
// A fifth optional probe (--probe-prepend) populates
// UserMessageAction.prepend_user_messages instead of ConversationHistory
// to see if that field feeds the model.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/router-for-me/cursor-proto/auth"
	"github.com/router-for-me/cursor-proto/executor"
	cursorpb "github.com/router-for-me/cursor-proto/gen/cursor"
	"github.com/router-for-me/cursor-proto/translator"
)

var debugMode bool

type turnResult struct {
	Assistant    string
	InputTokens  int64
	OutputTokens int64
	Events       int
	// Tool-call surface: true when the model tried to invoke a workspace
	// tool (shell/read/grep/etc.) instead of answering the prompt directly.
	// A common failure mode when server-side memory is missing: the model
	// tries to grep the codebase looking for "42".
	ToolCalled bool
	ToolKinds  []string
	// TurnEnded is true when we observed an `interaction_update.turn_ended`
	// frame — i.e. the SSE closed naturally instead of being reaped by the
	// AutoStop heuristics.
	TurnEnded bool
}

type variantOutcome struct {
	Name        string
	Description string
	Runs        []runOutcome
}

type runOutcome struct {
	ConversationID string
	Turn1          turnResult
	Turn2          turnResult
	Mentions42     bool
	Error          string
}

func main() {
	model := flag.String("model", "composer-2.5", "model to use")
	timeout := flag.Duration("timeout", 120*time.Second, "per-turn timeout")
	runs := flag.Int("runs", 3, "runs per variant")
	probePrepend := flag.Bool("probe-prepend", true, "also run the PrependUserMessages probe (variant E)")
	outJSON := flag.String("out-json", "", "optional path to dump raw results as JSON")
	debug := flag.Bool("debug", false, "print per-run event-kind breakdown")
	flag.Parse()
	debugMode = *debug

	acc := loadAccountFromIDE()
	c := executor.NewClient(acc)
	c.API3 = c.API2

	fmt.Printf("[test-conv-memory] account=%s model=%s runs_per_variant=%d\n", acc.Email, *model, *runs)

	variants := []struct {
		Name        string
		Description string
		Build       func() *executor.ChatRequest
		FreshID     bool
	}{
		{
			Name:        "A",
			Description: "same conv_id, NO wire history, NO splice",
			Build: func() *executor.ChatRequest {
				return &executor.ChatRequest{
					OmitSplicedHistory:          true,
					OmitConversationHistoryWire: true,
				}
			},
		},
		{
			Name:        "B",
			Description: "same conv_id, WITH wire history, NO splice",
			Build: func() *executor.ChatRequest {
				return &executor.ChatRequest{
					OmitSplicedHistory: true,
					// wire history is default (not omitted).
				}
			},
		},
		{
			Name:        "C",
			Description: "same conv_id, NO wire history, WITH splice (baseline)",
			Build: func() *executor.ChatRequest {
				return &executor.ChatRequest{
					OmitConversationHistoryWire: true,
					// splice is default (not omitted).
				}
			},
		},
		{
			Name:        "D",
			Description: "NEW conv_id, NO wire history, NO splice (control)",
			Build: func() *executor.ChatRequest {
				return &executor.ChatRequest{
					OmitSplicedHistory:          true,
					OmitConversationHistoryWire: true,
				}
			},
			FreshID: true,
		},
	}

	if *probePrepend {
		variants = append(variants, struct {
			Name        string
			Description string
			Build       func() *executor.ChatRequest
			FreshID     bool
		}{
			Name:        "E",
			Description: "same conv_id, PrependUserMessages populated (probe)",
			Build: func() *executor.ChatRequest {
				return &executor.ChatRequest{
					OmitSplicedHistory:          true,
					OmitConversationHistoryWire: true,
				}
			},
		})
	}

	outcomes := make([]variantOutcome, 0, len(variants))
	for _, v := range variants {
		fmt.Printf("\n=== Variant %s: %s ===\n", v.Name, v.Description)
		var runs2 []runOutcome
		for i := 0; i < *runs; i++ {
			fmt.Printf("\n[variant %s] run %d/%d\n", v.Name, i+1, *runs)
			out := runVariant(c, *model, *timeout, v.Name, v.Build, v.FreshID)
			runs2 = append(runs2, out)
			if out.Error != "" {
				fmt.Printf("  ERROR: %s\n", out.Error)
				continue
			}
			fmt.Printf("  conv_id_turn2=%s\n", out.ConversationID)
			fmt.Printf("  turn1: input=%d output=%d tool_called=%t assistant=%q\n", out.Turn1.InputTokens, out.Turn1.OutputTokens, out.Turn1.ToolCalled, truncate(out.Turn1.Assistant, 120))
			fmt.Printf("  turn2: input=%d output=%d turn_ended=%t tool_called=%t tools=%v mentions42=%t assistant=%q\n",
				out.Turn2.InputTokens, out.Turn2.OutputTokens, out.Turn2.TurnEnded, out.Turn2.ToolCalled, out.Turn2.ToolKinds,
				out.Mentions42, truncate(out.Turn2.Assistant, 200))
		}
		outcomes = append(outcomes, variantOutcome{Name: v.Name, Description: v.Description, Runs: runs2})
	}

	fmt.Println("\n============ SUMMARY ============")
	for _, o := range outcomes {
		var passCount int
		var t2Sum int64
		var t2Count int
		for _, r := range o.Runs {
			if r.Error != "" {
				continue
			}
			if r.Mentions42 {
				passCount++
			}
			t2Sum += r.Turn2.InputTokens
			t2Count++
		}
		avg := int64(0)
		if t2Count > 0 {
			avg = t2Sum / int64(t2Count)
		}
		fmt.Printf("variant %s: pass=%d/%d turn2_input_avg=%d  (%s)\n", o.Name, passCount, len(o.Runs), avg, o.Description)
	}

	if *outJSON != "" {
		b, _ := json.MarshalIndent(outcomes, "", "  ")
		_ = os.WriteFile(*outJSON, b, 0o644)
		fmt.Printf("\nRaw results written to %s\n", *outJSON)
	}
}

func runVariant(c *executor.Client, model string, timeout time.Duration, name string, build func() *executor.ChatRequest, freshTurn2ID bool) runOutcome {
	turn1User := "Remember the number 42. Reply with a very short acknowledgement, only a few words."
	turn2User := "What was the number I asked you to remember?"

	convID := auth.GenerateSessionID()
	out := runOutcome{ConversationID: convID}

	// Turn 1 is the same for every variant: no history, plain user message.
	ctx1, cancel1 := context.WithTimeout(context.Background(), timeout)
	defer cancel1()
	req1 := &executor.ChatRequest{
		Model:             model,
		UserMessage:       turn1User,
		ConversationID:    convID,
		Mode:              1, // ask
		AutoStopOnTurnEnd: true,
		// PureMode strips workspace-context scaffolding. Without it the
		// model in ask mode sometimes decides to grep the codebase looking
		// for the answer. That's a valid signal, but it makes the test
		// noisier — with PureMode turned on, the model has to answer from
		// context alone, which is exactly what we're probing.
		PureMode:           true,
		AutoStopOnToolCall: true,
	}
	t1, err := runOneTurn(ctx1, c, req1)
	if err != nil {
		out.Error = fmt.Sprintf("turn1: %v", err)
		return out
	}
	if t1.Assistant == "" {
		out.Error = fmt.Sprintf("turn1: no assistant text (events=%d tools=%v)", t1.Events, t1.ToolKinds)
		return out
	}
	out.Turn1 = t1

	// Turn 2: pick conversation id and per-variant flags.
	turn2ConvID := convID
	if freshTurn2ID {
		turn2ConvID = auth.GenerateSessionID()
		out.ConversationID = turn2ConvID
	}
	req2 := build()
	req2.Model = model
	req2.UserMessage = turn2User
	req2.ConversationID = turn2ConvID
	req2.Mode = 1
	req2.AutoStopOnTurnEnd = true
	req2.PureMode = true
	// Stop on a tool call so we can distinguish "model needed to fetch
	// context" from "model produced text". Without this, variants where the
	// model tries to call a workspace tool (because it lacks context) will
	// hang until the SSE heartbeat deadline.
	req2.AutoStopOnToolCall = true
	history := []executor.HistoryTurn{
		{Role: "user", Content: turn1User},
		{Role: "assistant", Content: t1.Assistant},
	}
	req2.History = history
	// Variant E: instead of the ConversationHistory field, populate
	// PrependUserMessages so we can probe whether that channel is the one
	// the model actually reads.
	if name == "E" {
		req2.PrependUserMessages = history
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), timeout)
	defer cancel2()
	t2, err := runOneTurn(ctx2, c, req2)
	if err != nil {
		out.Error = fmt.Sprintf("turn2: %v", err)
		return out
	}
	out.Turn2 = t2
	out.Mentions42 = strings.Contains(t2.Assistant, "42")
	return out
}

func runOneTurn(ctx context.Context, c *executor.Client, req *executor.ChatRequest) (turnResult, error) {
	var res turnResult
	events, err := c.RunChat(ctx, req)
	if err != nil {
		return res, err
	}
	var deltaBuf strings.Builder
	var thinkingBuf strings.Builder
	var trailerText string
	kinds := map[string]int{}
	for ev := range events {
		res.Events++
		if ev.Trailer {
			if len(ev.Raw) > 0 {
				trailerText = string(ev.Raw)
			}
			continue
		}
		if ev.Server == nil {
			continue
		}
		if blob := translator.FromKvBlob(ev.Server); blob != nil && blob.AssistantText != "" {
			res.Assistant = blob.AssistantText
		}
		if debugMode {
			kinds[classifyServerMessage(ev.Server)]++
		}
		if trEv := translator.FromServerMessage(ev.Server); trEv != nil {
			switch trEv.Kind {
			case translator.EventTextDelta:
				deltaBuf.WriteString(trEv.Text)
			case translator.EventThinkingDelta:
				thinkingBuf.WriteString(trEv.Text)
			case translator.EventToolCallStarted:
				res.ToolCalled = true
				if trEv.ToolName != "" {
					res.ToolKinds = append(res.ToolKinds, trEv.ToolName)
				}
			case translator.EventTurnEnded:
				res.TurnEnded = true
				if trEv.Usage != nil {
					res.InputTokens = trEv.Usage.InputTokens
					res.OutputTokens = trEv.Usage.OutputTokens
				}
			}
		}
		// Native (non-MCP) tool calls arrive as ExecServerMessage frames.
		// Classify by the wire discriminator so we notice cases where the
		// model tried to shell out or grep the workspace.
		if exec := ev.Server.GetExecServerMessage(); exec != nil {
			kind := classifyExec(exec)
			if kind != "unknown" {
				res.ToolCalled = true
				res.ToolKinds = append(res.ToolKinds, kind)
			}
		}
	}
	if debugMode {
		fmt.Printf("    [debug] server_message_kinds=%v thinking_len=%d delta_len=%d trailer=%q\n",
			kinds, thinkingBuf.Len(), deltaBuf.Len(), truncate(trailerText, 200))
	}
	// Fall back to accumulated text deltas if the KV blob channel is silent
	// (some server paths stream text without emitting the terminal blob).
	if res.Assistant == "" && deltaBuf.Len() > 0 {
		res.Assistant = deltaBuf.String()
	}
	return res, nil
}

func classifyServerMessage(m *cursorpb.AgentV1_AgentServerMessage) string {
	if iu := m.GetInteractionUpdate(); iu != nil {
		switch {
		case iu.GetTextDelta() != nil:
			return "iu.text_delta"
		case iu.GetThinkingDelta() != nil:
			return "iu.thinking_delta"
		case iu.GetThinkingCompleted() != nil:
			return "iu.thinking_completed"
		case iu.GetToolCallStarted() != nil:
			return "iu.tool_call_started"
		case iu.GetToolCallDelta() != nil:
			return "iu.tool_call_delta"
		case iu.GetToolCallCompleted() != nil:
			return "iu.tool_call_completed"
		case iu.GetPartialToolCall() != nil:
			return "iu.partial_tool_call"
		case iu.GetTokenDelta() != nil:
			return "iu.token_delta"
		case iu.GetUserMessageAppended() != nil:
			return "iu.user_message_appended"
		case iu.GetSummary() != nil:
			return "iu.summary"
		case iu.GetTurnEnded() != nil:
			return "iu.turn_ended"
		case iu.GetStepStarted() != nil:
			return "iu.step_started"
		case iu.GetStepCompleted() != nil:
			return "iu.step_completed"
		case iu.GetHeartbeat() != nil:
			return "iu.heartbeat"
		}
		return "iu.other"
	}
	if kv := m.GetKvServerMessage(); kv != nil {
		if sb := kv.GetSetBlobArgs(); sb != nil {
			return fmt.Sprintf("kv.set_blob(len=%d)", len(sb.GetBlobData()))
		}
		return "kv.other"
	}
	if exec := m.GetExecServerMessage(); exec != nil {
		return "exec." + classifyExec(exec)
	}
	if m.GetConversationCheckpointUpdate() != nil {
		return "conv_checkpoint"
	}
	return "other"
}

func classifyExec(exec *cursorpb.AgentV1_ExecServerMessage) string {
	switch {
	case exec.GetShellArgs() != nil:
		return "shell"
	case exec.GetWriteArgs() != nil:
		return "write"
	case exec.GetReadArgs() != nil:
		return "read"
	case exec.GetGrepArgs() != nil:
		return "grep"
	case exec.GetLsArgs() != nil:
		return "ls"
	case exec.GetDiagnosticsArgs() != nil:
		return "diagnostics"
	case exec.GetRequestContextArgs() != nil:
		return "request_context"
	case exec.GetMcpArgs() != nil:
		return "mcp"
	case exec.GetFetchArgs() != nil:
		return "fetch"
	case exec.GetShellStreamArgs() != nil:
		return "shell_stream"
	}
	return "unknown"
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

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
