# 阶段 5 报告：Cursor executor（chat + agent）

## 端到端 Chat 打通 ✅

用真实 IDE token 调用 Cursor 3.10.20 的 `agent.v1.AgentService/RunSSE`，成功收到 AI 的流式回复。

抓到的事件序列（32 个）：
- `heartbeat:{}` — 服务器 keepalive
- `kv_server_message` — 模型 blob（推理链、上下文快照）
- `token_delta:{tokens:N}` — token 计数
- `text_delta:{text:"LO"}` — 实时文本增量
- `turn_ended:{input_tokens:13126, output_tokens:296, ...}` — turn 结束

最终 AI 完整回复："HELLO"（正如提示词要求）。

## 关键发现（对 CursorGateway 的修正）

| 项目 | CursorGateway | 真值 |
|---|---|---|
| Chat 域名 | api2 | **api2**（不是 api3；api3 上的 TLS 失败其实是别的服务如 CodebaseSnapshot 用的） |
| RunSSE content-type | application/grpc-web+proto | 同 |
| BidiAppend content-type | application/connect+proto | **application/proto**（unary） |
| BidiAppend envelope | Connect 5-byte frame | **无 envelope**（裸 proto） |
| ConversationState | 空 buffer | **必填**（至少 `mode` 字段） |
| harness | 不填 / cli-unknown | **`cursor-ide`** |
| Client 支持声明 | 不填 | **`client_supports_inline_images: true`** + `client_supports_send_to_user: true` |
| ModelDetails 字段名 | `model_name` | **`model_id`** |
| RequestContextEnv 字段名 | `cwd` / `workspace_path` | **`project_folder`** / `workspace_paths[]`（repeated） |
| ModelDetails ModelID | `claude-4.5-sonnet` (老名) | **`composer-2.5`** / `gpt-5.5-*` / `grok-4.5-*` / `claude-opus-4-8-*`  |

## 产出

```
executor/
├── client.go        — 通用 Connect unary + 帧解析
├── headers.go       — 25 个 x-cursor-* header 统一构造
├── platform.go      — os/arch/timezone 派生
├── models.go        — AvailableModels / GetDefaultModel
├── chat.go          — RunSSE + BidiAppend
└── chat_build.go    — AgentRunRequest 组装
cmd/test-chat/main.go — 端到端验证工具
```

Client API：
```go
c := executor.NewClient(account)

// Unary
models, _ := c.ListModels()
def, _ := c.GetDefaultModel()

// Streaming chat
events, _ := c.RunChat(ctx, &executor.ChatRequest{
    Model:       "composer-2.5",
    UserMessage: "Hello",
})
for ev := range events {
    if ev.Server != nil && ev.Server.GetInteractionUpdate() != nil {
        // handle text_delta, token_delta, turn_ended, ...
    }
}
```

## 关于区域限制

Anthropic 模型（`claude-opus-4-8-*` 等）在国内 IP 会返回 **"Model not available: This model provider is not supported in your region"**。可用模型：`composer-2.5`, `composer-2.5-fast`, `grok-4.5-*`, `gpt-5.5-*`, `default`。

## 下一步

阶段 6：写 translator —— 把 Cursor 的 InteractionUpdate/kv_server_message 事件流翻译成 OpenAI Chat Completion / Anthropic Messages 兼容的 SSE 格式。这样 CPA 只要挂个 executor 就能把 Cursor 暴露给任意 OpenAI 客户端。
