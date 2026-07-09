# 阶段 6 报告：Translator（OpenAI / Anthropic 兼容）

## 端到端 HTTP 代理跑通 ✅

`cursor-proxy` 起来后，任何 OpenAI 或 Anthropic 客户端都能直接对着 Cursor 后端聊天。

```bash
# 流式
curl -N http://127.0.0.1:8317/v1/chat/completions \
  -H "content-type: application/json" \
  -d '{"model":"composer-2.5","messages":[
      {"role":"system","content":"Reply exactly 3 words."},
      {"role":"user","content":"greet the user"}
  ],"stream":true}'
# → data: {"choices":[{"delta":{"content":"Hello there friend."}, ...}]}
# → data: [DONE]

# Anthropic 兼容
curl -N http://127.0.0.1:8317/v1/messages \
  -H "content-type: application/json" \
  -d '{"model":"composer-2.5","system":"Reply exactly 3 words.","messages":[...],"stream":true}'
# → event: message_start ... event: message_stop
```

## 关键突破 & 教训

### 1. Cursor 拒绝 `custom_system_prompt`

```
grpc-message: unknown%20option%20'--system-prompt'
grpc-status: 3
```

**无论 harness 值是什么**（cursor-ide/cursor-cli/api/extension/空）都被拒。

**对策**：把 system prompt **拼进 user message 前面**：
```
<system_instructions>
You are a pirate. Every reply must start with 'Arrr!'
</system_instructions>

Say hi
```

实测：模型 100% 遵守 —— `"Arrr! Ahoy there, matey!"` 完美角色扮演。

### 2. Assistant text 在 KV blob 里，不在 `text_delta`

服务端会发多个 blob，只有 **`"role":"assistant"`** 的 JSON blob 是终态答复。前面所有 `text_delta` 只是 partial fragments。

Blob 格式（AI SDK 风格）：
```json
{
  "role": "assistant",
  "content": [
    { "type": "redacted-reasoning", "data": "..." },
    { "type": "text", "text": "Arrr! Ahoy there..." }
  ],
  "id": "1"
}
```

`translator/kv.go` 从 blob 里解出 `content[].type=="text"` 的文本。

### 3. Auto-stop 需要"看到 assistant blob"作为终结信号

Cursor 后端在 turn_ended 之后可能还继续发心跳 30-60 秒。以前 `AutoStopOnTurnEnd` 一见 turn_ended 就关，结果**丢掉了 assistant blob**（有时候 blob 在 turn_ended 之后 5-10 秒才到）。

**新策略**：
- **看到 assistant blob + turn_ended** → 1 秒 grace 后关
- **只看到 turn_ended（没 blob）** → 10 秒等 blob
- **一直只有 heartbeat** → 60 秒硬顶

实测 10/10 成功抓到 assistant text。

### 4. PureMode（去 IDE 化）

用 `PureMode=true` 时，请求里去掉：
- `harness: "cursor-ide"`
- `workspace_paths` / `project_folder` / `shell`
- 只留 `os_version` + `time_zone` 作为最小 env

服务端接受，且不再触发 IDE 专属的 workspace-aware 行为。

## 产出

```
translator/
├── events.go         — AgentServerMessage → 中性 Event
├── kv.go             — KV blob → assistant text
├── openai.go         — Event → OpenAI Chat Completion SSE
├── anthropic.go      — Event → Anthropic Messages SSE
└── translator_test.go — 单测 4/4 通过

cmd/cursor-proxy/main.go — 完整 HTTP 代理
    GET  /v1/models
    POST /v1/chat/completions   (OpenAI)
    POST /v1/messages           (Anthropic)
```

## 下一步

阶段 7 集成 —— 打包 + docker + Ready-to-CLIProxyAPI-Plugin 化。也可能要补：
- MCP 工具支持（`AgentRunRequest.McpTools`）—— 用户明确要求
- 增量 `text_delta` 转 real streaming（现在是拿到完整 blob 一次性 flush）
- 多轮对话（`ConversationHistory`）
