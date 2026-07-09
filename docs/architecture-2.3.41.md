# CursorGateway 架构文档

## 项目概述

CursorGateway 是一个 **Cursor API 协议网关**，将 Cursor 编辑器的 AI 能力（Claude、GPT-4 等）通过标准的 OpenAI / Anthropic 兼容 API 暴露出来，支持完整的 **Agent 模式**（工具调用）和**多客户端适配**。

```text
客户端 (Claude Code / opencode / openclaw / SDK)
    │
    ▼
CursorGateway（协议转换 + 工具路由 + 多客户端适配）
    │
    ▼
Cursor API (api2.cursor.sh)
    │
    ▼
AI 模型 (Claude / GPT-4 / ...)
```

---

## 核心架构

### 两种工作模式

#### 普通模式（Simple Mode）

简单的聊天问答，无工具调用。

```text
客户端 → OpenAI 格式请求 → 代理 → Protobuf 编码 → Cursor API
                                                        │
客户端 ← OpenAI 格式响应 ← 代理 ← Protobuf 解码 ← ─────┘
```

#### Agent 模式（Agent Mode）

AI 可以主动调用工具（执行命令、读写文件、搜索等），需要双向通信。

```text
客户端 → 请求（含 tools）→ 代理 → RunSSE + MCP 注册 → Cursor API
                                                            │
                           ┌── ExecServerMessage (工具调用) ←┘
                           │
                           ├─ 原生 exec? → execRequestToToolUse → 客户端执行
                           ├─ MCP 调用?  → restoreMcpToolName   → 客户端执行
                           │                                        │
                           │         tool_result ←──────────────────┘
                           │              │
                           ├─ sendToolResult → BidiAppend → Cursor API
                           │                                    │
                           ├── continueStream ←─────────────────┘
                           │
                           └─ 循环直到无更多工具调用 → 返回最终结果
```

---

## 项目目录结构

```text
src/
├── app.js                         服务器入口
├── config/
│   └── config.js                  配置（端口、代理、超时等）
├── middleware/
│   └── auth.js                    认证中间件（客户端检测 + Token 验证）
├── routes/
│   ├── index.js                   路由总管
│   ├── v1.js                      OpenAI Chat Completions API
│   ├── messages.js                Anthropic Messages API（Agent 核心）
│   ├── completions.js             OpenAI Legacy Completions API
│   ├── responses.js               OpenAI Responses API
│   └── cursor.js                  登录接口
├── adapters/
│   ├── detector.js                客户端类型检测
│   ├── base.js                    适配器基类
│   ├── claude-code.js             Claude Code 适配器
│   ├── opencode.js                opencode 适配器
│   └── openclaw.js                openclaw 适配器
├── utils/
│   ├── agentClient.js             Agent 协议客户端（RunSSE / BidiAppend）
│   ├── sessionManager.js          Session 管理（生命周期 / 并发锁 / 工具映射）
│   ├── bidiToolFlowAdapter.js     工具流适配（exec + KV 统一分发）
│   ├── kvToolAdapter.js           KV 工具适配（参数标准化 / 名字路由）
│   ├── toolsAdapter.js            工具分流（原生覆盖 vs MCP）
│   ├── tokenManager.js            Token 管理
│   ├── protoEncoder.js            Protobuf 编解码
│   ├── modelMapper.js             模型名称映射
│   └── utils.js                   工具函数
└── docs/                          文档
```

---

## 核心组件

### 1. 请求入口层

| 路由 | 文件 | 协议 | 说明 |
|------|------|------|------|
| `POST /v1/chat/completions` | `routes/v1.js` | OpenAI Chat | 通用聊天接口 |
| `POST /v1/messages` | `routes/messages.js` | Anthropic | Agent 模式核心，支持 tool_use/tool_result |
| `POST /v1/completions` | `routes/completions.js` | OpenAI Legacy | 兼容旧版 API |
| `POST /v1/responses` | `routes/responses.js` | OpenAI Responses | 新版 API |
| `GET /v1/models` | `routes/v1.js` | OpenAI | 模型列表 |

### 2. 多客户端适配层

`src/adapters/` 下的适配器负责将不同客户端的工具名和参数格式映射到内部标准（Canonical）格式。

```text
Claude Code 工具: Read, Write, StrReplace, Bash, Grep, Glob, ...
opencode 工具:    read_file, write_file, replace_string, bash, ...
openclaw 工具:    Read, Write, Edit, Bash, ...
                    │
                    ▼
              Canonical 格式（代理内部标准）
                    │
                    ▼
              Cursor 原生 exec / MCP
```

客户端检测逻辑在 `src/adapters/detector.js`，通过 `x-api-key` 值或工具名特征自动识别。

### 3. Agent 协议层

#### agentClient.js

核心职责：
- 构建 `AgentRunRequest`（含 MCP 工具注册）
- 发送 `POST /agent.v1.AgentService/RunSSE`
- 解析 SSE 响应（InteractionUpdate / ExecServerMessage / KV）
- 通过 `POST /agent.v1.AgentService/BidiAppend` 返回工具结果
- 管理跨轮次去重状态（`_handledExecIds` / `_handledExecSignatures`）

#### sessionManager.js

核心职责：
- Session 生命周期管理（创建、查找、清理）
- `execRequestToToolUse()` — 将 Cursor exec 参数转为客户端格式
- `sendToolResult()` — 将客户端执行结果编码回 Cursor 格式
- 并发锁（`acquireContinuationLock` / `releaseContinuationLock`）

### 4. 工具分流层

#### 原生覆盖工具

不注册 MCP。模型使用 Cursor 内置工具（read/write/shell/grep 等），代理通过 `execRequestToToolUse()` 映射回客户端工具格式。

覆盖工具列表：Read, Write, StrReplace, Bash/Shell, Grep, Glob, LS, Delete

#### MCP 扩展工具

注册为 MCP + prompt 注入。通过 `ExecServerMessage` field 11（McpArgs）接收调用。

MCP 工具列表：WebFetch, WebSearch, TodoWrite, TodoRead, Task, EditNotebook, ListMcpResources, FetchMcpResource

> 详细机制见 [docs/tool-calling.md](./tool-calling.md)

### 5. 参数标准化层

`kvToolAdapter.js` 的 `normalizeInputForTool()` 根据客户端工具 schema 动态适配参数名：

```text
Cursor exec 输出: { file_path: "/a.txt", content: "hello" }
                          │
                adaptKvToolUseToIde(toolUse, clientTools)
                          │
Claude Code schema: { path: "/a.txt", contents: "hello" }
```

关键点：不硬编码参数名，根据客户端实际提供的 `input_schema` 中 `required` / `properties` 字段动态决定。

---

## 通信协议

### 与 Cursor API 的通信

- **协议**：Connect Protocol（基于 HTTP/2）
- **数据格式**：Protobuf
- **初始请求**：`POST /agent.v1.AgentService/RunSSE`（SSE 流）
- **续传/结果**：`POST /agent.v1.AgentService/BidiAppend`

SSE 帧格式：`[flags:1字节][length:4字节 BE][data:protobuf]`

> 详细 proto 定义见 [docs/cursor-agent-proto-schema.md](./cursor-agent-proto-schema.md)

### 与客户端的通信

- **协议**：HTTP/1.1 + SSE
- **数据格式**：JSON（OpenAI / Anthropic 格式）
- **流式**：Server-Sent Events

---

## 超时策略

| 超时项 | 默认值 | 说明 |
|-------|-------|------|
| chatStream 空闲 | 3 分钟 | 无数据超时 |
| continueStream 首事件 | 120 秒 | 等待第一个有效事件 |
| continueStream 空闲 | 60 秒 | 两次有效事件间隔 |
| BidiAppend 单次 | 15 秒 | 网络请求超时 |
| BidiAppend 重试 | 3 次 | 指数退避 1s → 2s → 4s |
| 并发锁等待 | 90 秒 | 等待前一个 continuation 完成 |

心跳（`InteractionUpdate.heartbeat`）不算有意义活动，不重置超时。

---

## 支持的 API 端点

| 端点 | 方法 | 协议 | 说明 |
|------|------|------|------|
| `/v1/models` | GET | OpenAI | 可用模型列表 |
| `/v1/chat/completions` | POST | OpenAI Chat | 聊天补全（流式/非流式） |
| `/v1/completions` | POST | OpenAI Legacy | 文本补全 |
| `/responses` | POST | OpenAI Responses | 新版 Responses API |
| `/v1/messages` | POST | Anthropic | Anthropic Messages API（Agent 模式核心） |
| `/cursor/loginDeepControl` | GET | 内部 | 浏览器登录获取 Token |

---

## 双端协议详解

### 与调用端的协议（南向）

调用端（Codex / Claude Code / opencode 等）使用标准 HTTP/1.1 + SSE，数据格式 JSON：

| 调用端 | 接口 | 协议 |
|--------|------|------|
| Codex | `POST /responses` | OpenAI Responses API |
| Claude Code | `POST /v1/messages` | Anthropic Messages API |
| opencode | `POST /v1/messages` | Anthropic Messages API |

Gateway 通过 `src/adapters/detector.js` 根据 API Key 特征或工具名自动检测客户端类型，再用对应适配器做工具名和参数格式的双向转换：

```text
Claude Code: Read, Write, StrReplace, Bash
opencode:    read_file, write_file, replace_string, bash
Codex:       apply_patch, exec_command
                 ↓ 适配器标准化
           Canonical 格式（内部统一）
                 ↓
           Cursor exec / MCP
```

### 与 Cursor API 的协议（北向）

Cursor 使用 **Connect Protocol**（基于 HTTP/2），数据格式 Protobuf，是带工具路由的**双向通信协议**，而非普通 chat API。

SSE 帧格式：`[1字节 flags][4字节长度 BE][Protobuf 数据]`，`flags & 0x80` 表示 trailer（含 grpc-status）。

#### 上行（Gateway → Cursor）

**1. 初始请求** `POST /agent.v1.AgentService/RunSSE`

发送 `AgentRunRequest`：

| Field | 内容 |
|-------|------|
| 1 | 用户消息 + RequestContext（工作区、OS、工具列表） |
| 2 | 模型名 |
| 4 | McpTools 注册（告诉 Cursor 路由层哪些工具需转发） |
| 5 | conversation_id |
| 8 | 自定义 system prompt |

**2. 工具结果/续传** `POST /agent.v1.AgentService/BidiAppend`

发送 `ExecClientMessage`，按工具类型用不同 field 编码结果（shell/read/write/mcp 等）。

#### 下行（Cursor → Gateway，SSE 流）

| SSE 消息类型 | Field | 说明 |
|------------|-------|------|
| `InteractionUpdate` | 1 | 模型输出（流式文本、心跳、完成状态） |
| `ExecServerMessage` | 2 | **工具调用请求**（Cursor 要求 Gateway 执行工具） |
| `KvServerMessage` | 4 | **KV blob** 大数据（模型完整响应 + 工具调用） |
| `InteractionQuery` | 7 | Web search/fetch 审批请求 |

---

## 工具调用的三条通道

### 通道 1：ExecServerMessage（原生 exec）

Cursor 通过 `ExecServerMessage` 的不同 protobuf field 发出工具调用：

| Field | 工具 | Gateway 处理 |
|-------|------|-------------|
| 2/14 | Shell 执行 | 转发给调用端 |
| 3 | Write 写文件 | 转发给调用端 |
| 4 | Delete 删除 | 转发给调用端 |
| 5 | Grep 搜索 | 内部直接执行 |
| 7 | Read 读文件 | 内部直接执行 |
| 8 | LS 列目录 | 内部直接执行 |
| 10 | RequestContext | 自动响应工作区信息 |
| 11 | MCP 扩展工具 | 解析后转发给调用端 |
| 20 | Fetch HTTP 请求 | 内部执行 |

### 通道 2：MCP 扩展工具（field 11）

原生 exec 未覆盖的工具（WebFetch、TodoWrite、Task 等）通过 MCP 机制注册，并经 `ExecServerMessage field 11`（McpArgs）发回。

Gateway 在每次请求时做**双重注册**：
- `AgentRunRequest field 4`（McpTools）：告诉 Cursor **路由层**把调用转发给我
- `RequestContext field 7`（McpToolDefinition）：告诉 **模型层**这些工具存在可以调用

注册时保留名（TodoWrite、WebFetch 等）加 `mcp_` 前缀规避冲突，回调时还原。

### 通道 3：KV Blob（kv_server_message）

`KvServerMessage`（SSE field 4）是大数据传输辅助通道。模型的完整响应（含工具调用）有时走这里而非 ExecServerMessage。

- KV blob 中 `role: "assistant"` 且有 `id` 字段的 JSON 是**模型最终响应（FINAL）**
- 其 `content` 数组里可能含 `tool-call` 类型的工具调用块（PascalCase 命名：`ReadFile`、`EditFile`、`ApplyPatch` 等）
- 同一工具调用可能同时出现在 Exec 和 KV 两个通道，Gateway 通过**签名去重**（`toolName + sortedArgs JSON`）避免重复执行

---

## RequestContext 详解

RequestContext 嵌套在 `UserMessageAction` 里，是 Gateway 向 Cursor 声明"运行环境 + 可用工具"的唯一通道。

```protobuf
message RequestContext {
  RequestContextEnv env = 4;                       // 环境信息
  repeated McpToolDefinition tools = 7;            // 模型可见的 MCP 工具列表
  repeated McpInstructions mcp_instructions = 14;  // MCP 工具使用说明（注入给模型的 prompt）
  string cloud_rule = 16;                          // 云端规则
  optional bool web_search_enabled = 17;           // 启用 Cursor 服务端 web search
  optional bool web_fetch_enabled = 24;            // 启用 Cursor 服务端 web fetch
}
```

### Field 4 — RequestContextEnv（环境信息）

```protobuf
message RequestContextEnv {
  string os_version    = 1;    // 如 "darwin 24.6.0"
  string cwd           = 2;    // 当前工作目录
  string shell         = 3;    // 如 "/bin/zsh"
  string timezone      = 10;   // 如 "Asia/Shanghai"
  string workspace_path = 11;  // 工作区路径（通常同 cwd）
}
```

告诉 Cursor 服务端工作目录和系统信息。模型生成路径和 shell 命令的默认 cwd 来源于此。

### Field 7 — McpToolDefinition（模型可见工具）

```protobuf
message McpToolDefinition {
  string name                        = 1;
  string description                 = 2;
  google.protobuf.Value input_schema = 3;  // 注意：Value 类型，不是 Struct
  string provider_identifier         = 4;  // "cursor-tools"
  string tool_name                   = 5;
}
```

告诉**模型层**有哪些 MCP 工具可调用（提供结构化 JSON Schema）。缺少此 field，模型不知道工具存在，不会发起调用。

> **踩坑**：`input_schema` 必须用 `encodeProtobufValue()` 编码为 `google.protobuf.Value`，而非 `google.protobuf.Struct`。用错类型会导致 SSE 流静默卡死（silent stall）。

### Field 14 — McpInstructions（注入给模型的 prompt）

```protobuf
message McpInstructions {
  string identifier   = 1;  // "cursor-tools"
  string instructions = 2;  // "Available MCP tools:\n- mcp_TodoWrite: ...\n..."
}
```

field 7 提供工具的结构化定义，field 14 提供文本指引，两者配合让模型正确理解和调用工具。

### Field 17 / 24 — web_search_enabled / web_fetch_enabled

两个 bool 字段告诉 Cursor **服务端**允许触发 web search / web fetch 审批流程（`InteractionQuery`）。不设置则 Cursor 服务端不会发出 `InteractionQuery`，模型的搜索请求会被静默忽略。

### RequestContext 与 ExecServerMessage field 10 的关系

RequestContext 还出现在 `ExecServerMessage field 10`（`RequestContextArgs`）——Cursor 在运行期间主动问 Gateway"你的工作区路径是什么"。Gateway 必须立即通过 `BidiAppend` 响应同样的 env 信息，相当于运行时再确认。

```text
请求时  → RequestContext（field 4 env + field 7 tools + field 17/24）
运行中  ← ExecServerMessage field 10（Cursor 主动查询）
响应    → ExecClientMessage field 10（RequestContextResult，env 内容相同）
```

---

## Session 生命周期与多轮对话

```text
第一轮（新请求）
  → createSession → chatStream(RunSSE)
  → 收到 ExecServerMessage/KV → 映射工具调用 → yield tool_use 给客户端
  → session 挂起等待

第 N 轮（有 tool_result 的续请求）
  → findSessionByToolCallId → acquireContinuationLock（防并发重发）
  → sendToolResult → BidiAppend 回 Cursor
  → sendResumeAction → continueStream 继续读 SSE
  → 新工具调用 → 继续循环
  → 无更多工具 → cleanupSession → 返回最终文本
```

**并发控制**：调用端超时重发相同请求时，后到的请求等待锁释放（最多 90s），超时则 fallback 为带完整历史的 fresh request。

---

## 短连接客户端 ↔ 长连接 Cursor：状态衔接

### 问题的本质

```text
客户端（Codex / Claude Code）
  第1次 HTTP 请求 ──→ [连接断开]
  第2次 HTTP 请求 ──→ [连接断开]   ← 全是短连接
  第3次 HTTP 请求 ──→ [连接断开]

CursorGateway
  RunSSE 长连接 ═══════════════════════════════ Cursor
                ← ExecServerMsg → BidiAppend →  ← 长连接保持中
```

客户端每次 HTTP 断开，但 Cursor 那侧的 SSE 流还活着，还在等工具结果。Gateway 用**内存 Session 对象**做桥梁，把两侧生命周期解耦。

### SSE reader 如何跨请求保活

`AgentClient` 实例上的 `this.sseReader` 在有工具调用时不释放：

```text
chatStream()：
  reader = sseResponse.body.getReader()
  this.sseReader = reader

  遇到工具调用 → keepReaderForContinuation = true
  chatStream 结束（HTTP 响应返回给客户端）

  finally 块：
    if (keepReaderForContinuation)
      this.sseReader = reader   ← reader 不释放，继续挂在 AgentClient 上
      this.sseBuffer = buffer   ← 残余 buffer 也保存

continueStream()：
  let buffer = this.sseBuffer  ← 恢复上次残余 buffer
  this.sseReader.read()        ← 继续从同一个 SSE 流读数据（TCP 连接未断）
```

客户端 HTTP 断了，但 Cursor 那侧的 TCP 连接和 SSE 流**完全没有断开**，只是暂停读取。

### 续请求如何找回同一 Session

**方式一（主路径）**：响应头 `x-cursor-session-id` → 直接 `sessions.get(sessionId)`

**方式二（兜底）**：通过 `tool_result.tool_use_id` 反查

```javascript
function findSessionByToolCallId(toolCallId) {
  for (const [sessionId, session] of sessions.entries()) {
    if (session.toolCallMapping.has(toolCallId)) return session;
  }
}
```

`toolCallMapping`（`toolu_xxx → { cursorId, cursorType, ... }`）在第一次请求时建立，是跨请求定位 Session 的关键索引。

---

## Cursor SSE 流回放与去重

### 回放的根本原因

Cursor 的 `continueStream` **不是从断点续读，而是从头重放当前轮次的完整响应**，然后才接上新内容：

```text
第一轮 chatStream 收到：
  [text: "我来读一下这个文件"]
  [ExecServerMessage: Read file.txt]     ← 工具调用，暂停发给客户端

发完 BidiAppend + ResumeAction 后，continueStream 收到：
  [text: "我来读一下这个文件"]            ← 重放！已经发过
  [ExecServerMessage: Read file.txt]     ← 重放！已经处理过
  [text: "文件内容是..."]                 ← 新内容
```

不做去重就会把重放的工具调用当作新调用再次执行，向客户端重复 yield `tool_use`。

### 三层去重防线

**第一层：execId 去重**

每个 `ExecServerMessage` 有 `field 15: exec_id`（字符串，全局唯一）。处理过的 execId 持久化到 `AgentClient` 实例，跨轮次继承：

```javascript
// chatStream 结束时持久化
for (const id of locallyExecutedToolIds) this._handledExecIds.add(id);

// continueStream 启动时继承
const locallyExecutedToolIds = new Set(this._handledExecIds);

// 收到 ExecServerMessage 时检查
if (locallyExecutedToolIds.has(execRequest.execId)) {
  continue;  // 跳过，不 yield
}
```

**第二层：签名去重（execId 变化时的兜底）**

有时重放时 `exec_id` 会变，但工具名+参数完全一致。用内容签名兜底：

```javascript
签名 = stableStringify({ tool: "read", path: "/foo.txt" })
// 签名同样跨轮次持久化到 this._handledExecSignatures
```

**第三层：KV FINAL 去重**

KV blob 里的 FINAL 响应包含的工具调用同样会重放，同样对照去重集合过滤。若过滤后全部是重放（`filteredToolCalls.length === 0`），进入 `allSkippedFinalSeen` 状态，等待最多 10 秒看有无新内容，无则认为本轮结束。

### 客户端重发（HTTP retry）的去重

Claude Code 在 3~5 秒无响应时会重发相同的 `tool_result` 请求，两个请求同时到达 Gateway：

```text
请求A: POST  { tool_result, tool_use_id: "toolu_abc" }
请求B: POST  { tool_result, tool_use_id: "toolu_abc" }  ← 重发，同时到达
```

靠 `_continuationLock`（Promise 互斥锁）解决：请求A 拿到锁正常执行，执行完后从 `toolCallMapping` 删除 `toolu_abc`；请求B 等锁，拿到锁后发现 `toolCallMapping` 里已无该 ID，直接返回，不重复发给 Cursor。

### 两种回放的关系

```text
Cursor SSE 流回放（Cursor 协议设计如此）
  → chatStream 处理了工具A → continueStream 时 Cursor 重放工具A
  → 靠 execId / 签名去重 屏蔽

客户端 HTTP 重发
  → 同一个 tool_result 被发两次
  → 靠 continuationLock + toolCallMapping 删除后检查 屏蔽
```

两者根源相同：**Cursor 协议是无状态重放设计，Gateway 必须在两端都做幂等防护**。
