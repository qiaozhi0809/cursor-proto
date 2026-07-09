# Phase 7d — Multi-turn Conversation Context

## Goal

Give the OpenAI/Anthropic-compatible proxy real multi-turn memory. Prior to
this phase the proxy dropped everything except the last user message; the
assistant answered every request as if it were the first turn.

## Change summary

- `executor.ChatRequest` gained a `History []HistoryTurn` field.
- `executor/chat_build.go` populates
  `UserMessageAction.ConversationHistory.Messages` from `req.History` and
  sets `replace_user_info=true`.
- `executor/chat.go` also splices the history in-band into the current user
  turn (a `<prior_conversation>...</prior_conversation>` block). See the
  "Why splice?" note below.
- `cmd/cursor-proxy/main.go` (both OpenAI and Anthropic handlers) now
  splits the incoming `messages` array into:
  - `SystemPrompt` — every `system` message joined with `\n`.
  - `History` — every user/assistant message before the last user turn.
  - `UserMessage` — the last user turn.
  Both handlers also read an optional `x-conversation-id` header so a client
  can pin one stable ConversationId across a session.
- `cmd/test-multi-turn/main.go` is a new smoke test.

Single-turn callers keep working exactly as before: when `History` is nil
neither the wire field nor the spliced transcript is added.

## Wire fields we populate (per turn)

```
AgentRunRequest
├── conversation_id     ← ChatRequest.ConversationID (auto-generated if empty)
├── conversation_state  ← existing (mode only)
├── action
│   └── user_message_action
│       ├── user_message           ← current turn (with spliced <prior_conversation> preamble if History != nil)
│       ├── request_context        ← existing
│       └── conversation_history   ← NEW
│           ├── replace_user_info = true
│           └── messages[]
│               ├── user      → { content: [ { text: "..." } ] }
│               └── assistant → { content: [ { text: "..." } ] }
├── model_details       ← existing
└── ...
```

Historical turns become either `AgentV1_ConversationHistoryUserMessage` with
a single `AgentV1_ConversationHistoryTextContent`, or
`AgentV1_ConversationHistoryAssistantMessage` with a single
`AgentV1_ConversationHistoryAssistantContent_Text`. Tool calls, images and
reasoning blocks are ignored (the proxy surface doesn't emit them yet).

## Why splice history into the user message?

Observed 2026-07-10 against `composer-2.5`: Cursor's backend accepts the
`UserMessageAction.ConversationHistory` field on the wire (no
`grpc-status 8`, no unknown-field error) but does not fold those messages
into the prompt fed to the model. Turn 2's model input still only contains
the current user turn plus workspace scaffolding — the assistant answers
"I don't have access to that number in this conversation."

Setting `ConversationHistory.replace_user_info = true` did not change the
behaviour.

The safe fallback is to also inject the transcript as text at the top of
the user turn. This mirrors what `spliceSystemPrompt` already does for
`SystemPrompt`. With the transcript spliced in, the same test passes:
turn 2 reply is `"42."`.

We keep populating the wire `ConversationHistory` too, so once Cursor
starts honouring it (or once we get a real capture that reveals additional
fields it needs, e.g. per-turn message ids) the codepath will be ready.

## Message flow diagram

```
Client (OpenAI / Anthropic JSON)                                       Cursor backend
──────────────────────────────                                        ──────────────────────

POST /v1/chat/completions
{
  "messages": [
    { "role":"system",    "content": "You are helpful." },
    { "role":"user",      "content": "Remember the number 42." },
    { "role":"assistant", "content": "Got it — 42." },
    { "role":"user",      "content": "What was the number?" }   ← current turn
  ]
}
        │
        ▼
cursor-proxy splits:
    SystemPrompt = "You are helpful."
    History      = [ {user, "Remember..."}, {assistant, "Got it — 42."} ]
    UserMessage  = "What was the number?"
        │
        ▼
executor.RunChat
    ├── spliceSystemPrompt  →  <system_instructions>…</system_instructions>
    ├── spliceHistory       →  <prior_conversation>
    │                            <user>Remember the number 42.</user>
    │                            <assistant>Got it — 42.</assistant>
    │                          </prior_conversation>
    │
    ▼
buildAgentRunRequest
    ├── UserMessageAction.UserMessage.Text  = "<system_instructions>...</system_instructions>\n\n<prior_conversation>...</prior_conversation>\n\nWhat was the number?"
    ├── UserMessageAction.ConversationHistory.Messages = [user, assistant]  (belt-and-braces)
    └── ConversationId = req.ConversationID or auto-generated
        │
        ▼                                                                   ┌────────────────┐
POST /agent.v1.AgentService/RunSSE      ────────────────────────────────▶  │  Cursor sees   │
POST /aiserver.v1.BidiService/BidiAppend ──────────────────────────────▶  │  full context, │
                                                                            │  replies "42." │
        ◀───────────── SSE stream (KV blobs + interaction updates) ──────  └────────────────┘
```

## ConversationId reuse

The proxy reads `x-conversation-id` from the incoming HTTP request. Callers
that want to pin a stable conversation across multiple `/v1/chat/completions`
calls can send that header; the executor forwards it verbatim into
`AgentRunRequest.ConversationId`. When absent (or empty) `RunChat`
auto-generates a fresh session id.

## Verification

See `phase-7d-verify.log` at the worktree root. Two-turn smoke test with
`composer-2.5` reports `PASS: turn 2 reply mentions "42"`.
