# Phase 7-A: MCP tools

## What we shipped

Any OpenAI or Anthropic client can now pass a `tools[]` array through
`cursor-proxy` and receive `tool_calls` back from Cursor's Composer / Grok /
GPT-5.5 / Claude models — using Cursor's built-in MCP tool routing rather
than reinventing tool schemas.

### Wire mapping

- `executor.ChatRequest.Tools []executor.ToolDefinition` — new field.
- On the outbound side (`executor/chat_build.go`):
  - populates `AgentRunRequest.McpTools` with one `McpToolDefinition` per tool
  - mirrors the same list into `RequestContext.Tools` so the model can see them
  - `provider_identifier = "cursor-tools"`, `tool_name = <same as name>`
  - `input_schema` is wrapped as `google.protobuf.Value` (**not** `Struct` —
    Cursor's server stalls the SSE stream silently if you send a bare Struct)
- On the inbound side (`translator/events.go`):
  - `ExecServerMessage.McpArgs` → `EventToolCallStarted`
  - `translator/openai.go` serializes `delta.tool_calls[]`
  - `translator/anthropic.go` serializes `content_block_start`/`content_block_delta`
    with `input_json_delta`

### AutoStopOnToolCall

`ChatRequest.AutoStopOnToolCall = true` (set automatically by the proxy when
`tools[]` is non-empty) closes the SSE stream after the model emits the first
tool call. This mirrors the OpenAI/Anthropic non-streaming contract, where the
API returns immediately with `finish_reason: tool_calls` and expects the
caller to invoke the tool and re-submit.

## Verified

`phase-7a-verify.log` at the worktree root captures four scenarios:

1. Baseline chat (no tools) → normal text reply
2. `get_weather(location: string)` (non-streaming) → returns
   `tool_calls: [{ function: { name: "get_weather", arguments: "{\"location\":\"Paris\"}" } }]`
3. Same tool (streaming) → tool_calls chunk arrives in the SSE stream
4. Anthropic Messages equivalent → `content_block_start` with `type: "tool_use"`

## Files added / changed

- `executor/tools.go` — ToolDefinition + convertToMcpToolDefinition
- `executor/tools_test.go`
- `executor/chat_build.go` — wire tools into AgentRunRequest
- `executor/chat.go` — AutoStopOnToolCall handling
- `translator/events.go` — tool call event extraction
- `translator/tools_test.go`
- `translator/openai.go` + `translator/anthropic.go` — tool_calls encoding
- `cmd/cursor-proxy/main.go` — parse OpenAI/Anthropic tools[]
- `cmd/test-tools/main.go` — smoke test
- `cmd/test-tools-executor/main.go` — end-to-end tool execution demo
