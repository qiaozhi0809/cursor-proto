# Cursor 工具调用机制详解

本文档详细说明 CursorGateway 中工具调用的完整运行机制，包括原生工具（Native Exec）和扩展工具（MCP）两条路径。

---

## 概述

Cursor 的 Agent 模式支持两种工具调用通道：

| 通道 | 触发方式 | 适用工具 | 通信协议 |
|------|---------|---------|---------|
| **原生 Exec** | `ExecServerMessage` field 2-8, 10, 14 | 文件读写、Shell、搜索等内置工具 | Protobuf 双向流（BidiAppend） |
| **MCP 扩展** | `ExecServerMessage` field 11（McpArgs） | 任意自定义工具（web_fetch、todo_write 等） | Protobuf 双向流 + MCP 注册 |

此外还有一个辅助通道：

| 通道 | 触发方式 | 说明 |
|------|---------|------|
| **KV Blob** | `kv_server_message` field 4 | 模型最终响应中的工具调用，可能与 Exec 通道重复 |

---

## 一、原生 Exec 工具调用

### 1.1 流程

```text
Cursor API                         CursorGateway                        客户端 (Claude Code 等)
    │                                    │                                    │
    │ ── ExecServerMessage ──────────>   │                                    │
    │    (field 2: ShellArgs)            │                                    │
    │                                    │ parseExecServerMessage()           │
    │                                    │ execRequestToToolUse()             │
    │                                    │ adaptKvToolUseToIde()              │
    │                                    │                                    │
    │                                    │ ── tool_use (Bash) ──────────────> │
    │                                    │                                    │
    │                                    │ <── tool_result ─────────────────  │
    │                                    │                                    │
    │                                    │ sendToolResult()                   │
    │                                    │ buildShellResultMessage()          │
    │                                    │                                    │
    │ <── BidiAppend (结果) ────────────  │                                    │
    │                                    │                                    │
```

### 1.2 ExecServerMessage 字段映射

Cursor 通过 `ExecServerMessage` 发送原生工具调用。每种工具对应一个 protobuf field：

| Field | Wire Type | 工具类型 | 参数结构 |
|-------|-----------|---------|---------|
| 1 | uint32 | — | `id`（请求序号） |
| 2 | message | Shell | `ShellArgs { 1: command, 2: cwd }` |
| 3 | message | Write | `WriteArgs { 1: path, 2: fileText, 3: toolCallId }` |
| 4 | message | Delete | `DeleteArgs { 1: path }` |
| 5 | message | Grep | `GrepArgs { 1: pattern, 2: path, 3: glob }` |
| 7 | message | Read | `ReadArgs { 1: path }` |
| 8 | message | LS | `LsArgs { 1: path }` |
| 10 | message | RequestContext | 请求工作区上下文（必须本地处理） |
| 11 | message | MCP | `McpArgs`（见 MCP 章节） |
| 14 | message | Shell v2 | `ShellArgs`（流式 shell，同 field 2 格式） |
| 15 | string | — | `exec_id`（执行标识） |
| 20 | message | Fetch | `FetchArgs`（网络请求） |
| 28 | message | Subagent | `SubagentArgs`（子代理） |

> **注意**：field 4 是 `DeleteArgs`，不是 `ReadArgs`。这是早期逆向时的常见错误。

### 1.3 参数映射

`execRequestToToolUse()` 将 Cursor 原生参数转为中间格式：

| Cursor 工具 | 中间格式参数 |
|------------|------------|
| Shell (field 2/14) | `{ command, working_directory }` |
| Write (field 3) | `{ file_path, content }` |
| Delete (field 4) | `{ file_path }` |
| Grep (field 5) | `{ pattern, path, glob }` |
| Read (field 7) | `{ file_path }` |
| LS (field 8) | `{ path }` |

然后 `adaptKvToolUseToIde()` → `normalizeInputForTool()` 根据客户端工具 schema 动态标准化参数名：

```text
Cursor exec: { file_path: "/a.txt", content: "hello" }
     ↓ normalizeInputForTool (根据 Claude Code schema)
Claude Code: { path: "/a.txt", contents: "hello" }
     ↓ normalizeInputForTool (根据 opencode schema)
opencode:    { file_path: "/a.txt", content: "hello" }
```

### 1.4 结果返回

工具执行完成后，`agentClient.sendToolResult()` 根据工具类型构建对应的 Protobuf 结果消息：

| 工具类型 | 构建函数 | 结果结构 |
|---------|---------|---------|
| Shell | `buildShellResultMessage` | `{ output, exitCode, cwd }` |
| Read | `buildReadResultMessage` | `{ content, path }` |
| Write | `buildWriteResultMessage` | `{ success: { path, linesWritten } }` 或 `{ error: { path, error } }` |
| Delete | `buildDeleteResultMessage` | `{ path }` |
| Grep | `buildGrepResultMessage` | `{ results }` |
| LS | `buildLsResultMessage` | `{ entries }` |
| RequestContext | `buildRequestContextResultMessage` | `{ workspacePath, os, shell }` |

结果通过 `bidiAppend()` 发送回 Cursor API。

### 1.5 Edit → StrReplace 智能路由

Cursor 模型的文件编辑操作使用 `Edit` 工具名，但根据参数不同，语义可能是 StrReplace 或 Write：

```text
Edit + { old_string, new_string }  →  StrReplace（部分替换）
Edit + { content }                  →  Write（全文件写入）
```

`adaptKvToolUseToIde()` 中的输入感知路由：

```javascript
if ((name === 'edit' || name === 'edit_file') &&
    ('old_string' in input || 'new_string' in input)) {
  // → 映射为 StrReplace
}
```

---

## 二、MCP 扩展工具调用

### 2.1 什么工具走 MCP

没有直接对应 Cursor 原生 exec 的工具通过 MCP 机制注册：

| MCP 工具 | 说明 |
|----------|------|
| `web_fetch` / `WebFetch` | 获取网页内容 |
| `web_search` / `WebSearch` | 网页搜索 |
| `todo_write` / `TodoWrite` | 写入/更新待办事项 |
| `todo_read` / `TodoRead` | 读取待办事项 |
| `Task` | 子任务管理 |
| `EditNotebook` | 编辑 Jupyter Notebook |
| `ListMcpResources` | 列出 MCP 资源 |
| `FetchMcpResource` | 获取 MCP 资源 |

### 2.2 MCP 注册（双重注册）

MCP 工具需要在两个位置注册，缺一不可：

#### 位置一：AgentRunRequest.field 4 — McpTools wrapper

告诉 Cursor **路由层**："我的客户端支持这些 MCP 工具，请通过 `ExecServerMessage` field 11 转发给我"。

```text
AgentRunRequest {
  field 4 (McpTools) {
    repeated McpToolDescriptor {
      1: name          // 工具名（已 sanitize）
      2: description   // 工具描述
      3: input_schema  // JSON Schema → google.protobuf.Value
      4: provider_identifier  // "cursor-tools"
      5: tool_name     // 同 name
    }
  }
}
```

#### 位置二：RequestContext.field 7 — 模型可见工具列表

告诉 Cursor **模型层**："这些工具存在，你可以在回复中调用它们"。

```text
RequestContext {
  field 7 (repeated McpToolDefinition) {
    1: name          // 工具名（已 sanitize）
    2: description   // 工具描述
    3: input_schema  // JSON Schema → google.protobuf.Value
    4: provider_identifier  // "cursor-tools"
    5: tool_name     // 同 name
  }
  field 14 (McpInstructions) {
    1: identifier    // "cursor-tools"
    2: instructions  // 文本描述，指导模型如何使用工具
  }
}
```

**缺少 field 4**：模型知道工具但 Cursor 不路由 → 工具调用请求不会到达客户端。

**缺少 field 7**：Cursor 路由就绪但模型不知道工具 → 模型不会发起调用。

### 2.3 工具名冲突处理

Cursor 服务端有内部保留工具名，以相同名字注册 MCP 会导致 `grpc-status: 8 (Provider Error)`。

保留名列表：

```
TodoWrite, WebFetch, Task, EditNotebook, FetchMcpResource, Delete
```

解决方案 — 注册时加 `mcp_` 前缀，回调时去掉前缀：

```text
注册: TodoWrite → mcp_TodoWrite（发给 Cursor）
回调: mcp_TodoWrite → TodoWrite（还原给客户端）
```

### 2.4 MCP 调用流程

```text
Cursor API                         CursorGateway                        客户端
    │                                    │                                    │
    │ ── ExecServerMessage ──────────>   │                                    │
    │    field 11 (McpArgs) {            │                                    │
    │      name: "mcp_WebFetch"          │                                    │
    │      args: { url: "..." }          │ parseExecServerMessage()           │
    │      tool_call_id: "xxx"           │ restoreMcpToolName()               │
    │    }                               │   → name: "WebFetch"              │
    │                                    │ execRequestToToolUse()             │
    │                                    │                                    │
    │                                    │ ── tool_use (WebFetch) ──────────> │
    │                                    │                                    │
    │                                    │ <── tool_result ─────────────────  │
    │                                    │                                    │
    │                                    │ buildMcpResultMessage()            │
    │ <── BidiAppend (McpResult) ──────  │                                    │
    │                                    │                                    │
```

### 2.5 McpArgs 解析

`ExecServerMessage` field 11 包含 `McpArgs` 消息：

```text
McpArgs {
  1: string name              // MCP 工具名（可能有 mcp_ 前缀）
  2: google.protobuf.Struct args  // 工具参数（Struct 编码）
  3: string tool_call_id
  4: string provider_identifier
  5: string tool_name
}
```

**关键**：`args` 是 `google.protobuf.Struct`（field 2），不是 JSON 字符串。需要用 `decodeProtobufStruct()` 解码。

### 2.6 McpResult 编码

```text
McpResult (ExecClientMessage field 11) {
  oneof {
    1: McpSuccess success {
      repeated McpToolResultContentItem content {
        oneof {
          1: McpTextContent text {
            1: string text    // 实际文本内容
          }
          2: McpImageContent image
        }
      }
      bool is_error
    }
    2: McpError error {
      1: string error
    }
    3: McpRejected rejected
    4: McpPermissionDenied permission_denied
    5: McpToolNotFound tool_not_found
    6: McpToolError tool_error
  }
}
```

---

## 三、KV Blob 辅助通道

### 3.1 什么是 KV

`kv_server_message`（SSE 响应中的 field 4）是 Cursor 的大数据传输通道。模型的完整响应（含文本和工具调用）可能通过 KV blob 传递，而非 `interaction_update`。

### 3.2 KV FINAL 响应

KV blob 中的 JSON 包含 `role: "assistant"` 且有 `id` 字段时，这是**最终响应**。其 `content` 数组可能包含 `tool-call` / `tool-use` 类型的块。

### 3.3 与 Exec 通道的去重

同一个工具调用可能同时出现在 Exec 和 KV 两个通道。代理使用**签名去重**避免重复执行：

```text
签名 = toolName + JSON.stringify(sortedArgs)
```

- `_handledExecIds`：记录已处理的 exec id
- `_handledExecSignatures`：记录已处理的工具签名
- 这两个集合在 `chatStream` → `continueStream` 之间**跨轮次传递**

---

## 四、原生工具分流策略

### 4.1 核心原则：不和 Cursor 内置工具对抗

将客户端工具分为两类处理：

| 类别 | 工具 | 注册方式 | 调用路径 |
|------|------|---------|---------|
| **原生覆盖** | Read, Write, StrReplace, Bash, Grep, Glob, LS, Delete | 不注册 MCP | 通过 ExecServerMessage → execRequestToToolUse 映射 |
| **MCP 扩展** | WebFetch, WebSearch, TodoWrite, Task 等 | 注册 MCP + prompt 注入 | 通过 McpArgs 或文本解析 |

**为什么不把所有工具都注册为 MCP？** Cursor 模型在文件操作场景会优先使用原生工具（read/write/shell），忽略同名的 MCP 工具。如果把 StrReplace 注册为 MCP，模型会在"想调 StrReplace 但调不了"的循环中死锁。

### 4.2 text_fallback 机制

当 MCP 工具调用没有通过 `ExecServerMessage` field 11 到达（可能因为注册失败或模型通过文本描述工具调用），代理会解析模型输出的文本，提取工具调用：

```text
模型输出文本中包含:
  <tool_use>
    <name>WebFetch</name>
    <input>{"url": "..."}</input>
  </tool_use>

代理解析后生成 tool_use 块发给客户端
```

这是最后的兜底机制。text_fallback 工具结果无法通过 BidiAppend 发回，需要关闭当前 session 用 fresh request 重建。

---

## 五、Session 生命周期

### 5.1 完整流程

```text
新请求（无 tool_result）
    │
    ├─ createSession(agentClient)
    ├─ agentClient.chatStream()
    │      ↓
    │   ExecServerMessage / KV 工具调用
    │      ↓
    │   mapAgentChunkToToolUse() → 去重 → 参数标准化
    │      ↓
    │   yield tool_use → 客户端执行
    │
    └─ 有工具调用 → session 保持
       无工具调用 → cleanupSession()

续请求（有 tool_result）
    │
    ├─ findSessionByToolCallId() 或 getSession()
    ├─ acquireContinuationLock()  // 防并发
    ├─ sendToolResult()
    │      ↓
    │   构建 Protobuf 结果 → bidiAppend()
    │      ↓
    │   sendResumeAction()
    │
    ├─ continueStream()
    │      ↓
    │   继续读取 SSE 流（继承去重状态）
    │      ↓
    │   新的文本 / 新的工具调用
    │
    ├─ releaseContinuationLock()
    └─ 有工具调用 → session 保持
       无工具调用 → cleanupSession()
```

### 5.2 并发控制

Claude Code / opencode 等客户端在代理响应较慢时（>3-5秒）会**重发相同请求**。代理通过 `acquireContinuationLock()` / `releaseContinuationLock()` 确保同一 session 同时只有一个请求在处理 continuation。

后到的请求等待锁释放，超时后回退为 fresh request（用完整对话历史新建 session）。

---

## 六、Codex 客户端工具列表

### 6.1 Codex 原生工具

Codex CLI 自身只定义了两个原生工具（通过 `function` 类型注册到 Responses API）：

| Codex 工具名 | 说明 | 参数 |
|-------------|------|------|
| `apply_patch` | 通过 unified diff 补丁编辑文件（新建、修改、删除） | `{ input: "*** Begin Patch\n..." }` |
| `exec_command` | 执行 shell 命令 | `{ cmd: "...", workdir: "...", timeout: N }` |

### 6.2 Codex → Cursor 工具映射

Codex adapter（`src/adapters/codex.js`）的 canonical 映射：

| Canonical 名 | Codex 工具名 | Cursor exec type | 说明 |
|-------------|-------------|-----------------|------|
| `shell_exec` | `exec_command` | `shell` (field 2/14) | Shell 命令 |
| `file_edit` | `apply_patch` | `write` (field 3) | 文件编辑（Codex 用 patch 格式） |

**关键设计**：Codex 没有独立的 read、ls、grep、delete 工具。当 Cursor 模型发出这些 exec 命令时：
- `read`、`ls`、`grep` → Gateway **内部直接执行**（`READONLY_EXEC_TYPES`），结果通过 protobuf 发回 Cursor，Codex 不感知
- `write`、`shell`、`delete` → Gateway 通过 `cursorExecToShellCommand()` 转成 shell 命令，映射到 Codex 的 `exec_command`
- `ApplyPatch`（Cursor KV tool call）→ 通过 `KV_TOOL_NAME_TO_CLIENT` 映射到 Codex 的 `apply_patch`

### 6.3 Codex 内建工具类型

Codex 请求中还可能包含 OpenAI Responses API 的内建工具类型（非 function tool）：

| 内建类型 | 格式 | Gateway 处理 |
|---------|------|-------------|
| `web_search` | `{ type: "web_search" }` | 不注册 MCP；由 Cursor 原生 web_search 处理（RequestContext field 17） |
| `file_search` | `{ type: "file_search" }` | 转为 function 格式注册 MCP |
| `code_interpreter` | `{ type: "code_interpreter" }` | 转为 function 格式注册 MCP |

---

## 七、Cursor 自定义工具分类

### 7.1 Cursor 原生 Exec 工具

这些是 Cursor Agent 协议内建的工具，通过 `ExecServerMessage` 的不同 protobuf field 触发：

| Exec Type | Protobuf Field | 说明 | Gateway 内部执行 |
|-----------|---------------|------|-----------------|
| `shell` | field 2 / 14 | 执行 Shell 命令 | ❌ 转发给客户端 |
| `write` | field 3 | 写文件 | ❌ 转发给客户端 |
| `delete` | field 4 | 删除文件 | ❌ 转发给客户端 |
| `grep` | field 5 | 搜索文件内容 | ✅ 内部执行 |
| `read` | field 7 | 读文件 | ✅ 内部执行 |
| `ls` | field 8 | 列目录 | ✅ 内部执行 |
| `request_context` | field 10 | 获取工作区上下文 | ✅ 内部自动响应 |
| `mcp` | field 11 | MCP 扩展工具调用 | ❌ 转发给客户端 |
| `fetch` | field 20 | HTTP 请求 | ✅ 内部执行 |
| `subagent` | field 28 | 子代理 | — |

### 7.2 Gateway 注册的 MCP 工具

这些工具不属于 Cursor 原生 exec，通过 MCP 机制注册给 Cursor 模型使用。来源是客户端（如 Codex）请求中的 `tools` 数组，经过 `filterNonNativeTools()` 过滤后的非原生工具：

| 工具 | 注册方式 | 说明 |
|------|---------|------|
| 客户端自定义 function tools | MCP（field 4 + field 7 双重注册） | 不在 `NATIVE_COVERED_TOOLS_LOWER` 中的所有 function tool |

**不注册为 MCP 的工具**（`NATIVE_COVERED_TOOLS_LOWER`）：
- `read`, `write`, `strreplace`, `bash`, `shell`, `grep`, `glob`, `ls`, `delete`
- `web_search`, `websearch`, `web_fetch`, `webfetch`

### 7.3 KV Blob 工具

Cursor 模型有时通过 KV blob（`kv_server_message` field 4）发出工具调用，而非 `ExecServerMessage`。KV 工具名使用 **PascalCase** 命名：

| KV 工具名 | 对应 exec type | 处理方式 |
|-----------|---------------|---------|
| `ReadFile` / `Read` | `read` | `kvToolToExecRequest` → 内部执行 |
| `ListDir` / `ListDirectory` | `ls` | `kvToolToExecRequest` → 内部执行 |
| `Glob` / `GlobFileSearch` | `ls` | `kvToolToExecRequest` → 内部执行 |
| `Grep` / `RipgrepSearch` | `grep` | `kvToolToExecRequest` → 内部执行 |
| `EditFile` / `Write` | `write` | `kvToolToExecRequest` → 转发客户端 |
| `DeleteFile` | `delete` | `kvToolToExecRequest` → 转发客户端 |
| `RunTerminalCommand` / `Shell` | `shell` | `kvToolToExecRequest` → 转发客户端 |
| `ApplyPatch` | — | `KV_TOOL_NAME_TO_CLIENT` → `apply_patch`（直接映射） |
| `WebSearch` / `web_search` | — | **跳过**（Cursor 服务端处理） |
| `WebFetch` / `web_fetch` | — | **跳过**（Cursor 服务端处理） |

---

## 八、WebSearch / WebFetch 处理机制

### 8.1 概述

WebSearch 和 WebFetch 是 **Cursor 服务端执行**的能力，不走 `ExecServerMessage`，走独立的 **InteractionQuery / InteractionResponse 审批流程**。

### 8.2 启用

在 `buildRequestContext()`（`agentClient.js`）中设置：

```
RequestContext {
  field 17: web_search_enabled = true   // 启用 web search
  field 24: web_fetch_enabled  = true   // 启用 web fetch
}
```

### 8.3 完整调用流程

```text
Cursor 模型                    Cursor 服务端                    CursorGateway
    │                              │                               │
    │  "我需要搜索..."             │                               │
    │ ──────────────────────────>  │                               │
    │                              │                               │
    │                              │  AgentServerMessage field 7   │
    │                              │  InteractionQuery {           │
    │                              │    id: 42                     │
    │                              │    web_search_request_query { │
    │                              │      args { searchTerm }      │
    │                              │    }                          │
    │                              │  }                            │
    │                              │ ───────────────────────────>  │
    │                              │                               │
    │                              │                               │ parseInteractionQuery()
    │                              │                               │ → type=web_search, id=42
    │                              │                               │
    │                              │  AgentClientMessage field 6   │
    │                              │  InteractionResponse {        │
    │                              │    id: 42                     │
    │                              │    web_search { approved {} } │
    │                              │  }                            │
    │                              │ <─────────────────────────── │
    │                              │                               │ buildInteractionResponseApproved()
    │                              │                               │ → 自动批准
    │                              │                               │
    │                              │  (Cursor 服务端执行搜索)       │
    │                              │                               │
    │  搜索结果嵌入上下文           │                               │
    │ <──────────────────────────  │                               │
    │                              │                               │
    │  继续生成文本/工具调用        │                               │
```

### 8.4 Gateway 中的多层处理

WebSearch/WebFetch 在 Gateway 中涉及 **5 层处理**，确保不会被错误地转发给客户端或注册为 MCP：

| 层 | 位置 | 作用 |
|----|------|------|
| **1. 不注册 MCP** | `toolsAdapter.js` `NATIVE_COVERED_TOOLS_LOWER` | `web_search`/`websearch`/`web_fetch`/`webfetch` 在过滤时被移除，不会注册到 Cursor 的 MCP 机制 |
| **2. 启用原生能力** | `agentClient.js` `buildRequestContext()` | 在 RequestContext 中设置 `field 17 = true`（web_search）和 `field 24 = true`（web_fetch） |
| **3. 自动审批** | `agentClient.js` `chatStream/continueStream` | 解析 `AgentServerMessage` field 7 的 InteractionQuery，自动批准 web_search 和 web_fetch 请求 |
| **4. 过滤历史** | `responses.js` `inputToMessages()` | 客户端请求中如果有 `web_search`/`web_fetch` 的 function_call + function_call_output，通过 `skipCallIds` 机制跳过不发给 Cursor |
| **5. 过滤 KV 调用** | `responses.js` 内部循环 + `agentClient.js` KV 去重 | KV blob 中的 `WebSearch`/`WebFetch` 工具调用被 `WEB_NATIVE_TOOL_NAMES` / `WEB_NATIVE_KV_TOOLS` 过滤，不转发给客户端 |

### 8.5 WebFetch 的特殊处理

除了 InteractionQuery 审批流程外，Cursor 模型还可能通过 `ExecServerMessage` field 20（`FetchArgs`）发起 HTTP 请求。这走的是另一条路径：

```text
ExecServerMessage field 20 (FetchArgs) { url }
  → responses.js 中 chunk.execRequest.type === 'fetch'
  → executeInternalFetch(execRequest)  // Gateway 内部用 fetch() 发 HTTP 请求
  → chunk.sendResult(result)           // 结果发回 Cursor
  → needContinue = true                // 继续内部循环
```

这意味着 Cursor 模型的 `fetch` 操作也是 Gateway 内部闭环，客户端不感知。

### 8.6 为什么这么复杂

| 问题 | 解决 |
|------|------|
| Cursor 的 web_search 不走 exec 通道 | 实现 InteractionQuery 审批机制 |
| 客户端可能发送含 web_search 的历史 | inputToMessages 中 skipCallIds 过滤 |
| KV blob 可能重复包含 WebSearch 调用 | WEB_NATIVE_KV_TOOLS 去重过滤 |
| 不能注册为 MCP（会和 Cursor 内部冲突） | NATIVE_COVERED_TOOLS_LOWER 阻止注册 |
| 模型可能用 WebFetch 替代 WebSearch | fetch exec 内部执行兜底 |

---

## 九、关键代码位置

| 模块 | 文件 | 职责 |
|------|------|------|
| Agent 协议客户端 | `src/utils/agentClient.js` | 构建 AgentRunRequest、解析 ExecServerMessage、MCP 注册/结果编码、InteractionQuery 审批 |
| 工具流适配 | `src/utils/bidiToolFlowAdapter.js` | `mapAgentChunkToToolUse()` — 统一 exec/KV 两条路径的工具调用分发 |
| KV 工具适配 | `src/utils/kvToolAdapter.js` | `adaptKvToolUseToIde()` / `normalizeInputForTool()` — 参数标准化 |
| 工具过滤 | `src/utils/toolsAdapter.js` | `filterNonNativeTools()` — 分离原生覆盖 vs MCP 工具 |
| Responses API 路由 | `src/routes/responses.js` | Codex 内部工具循环、KV 工具转 function_call、WebSearch 过滤 |
| Session 管理 | `src/utils/sessionManager.js` | Session 生命周期、`execRequestToToolUse()`、`sendToolResult()` |
| Protobuf 编解码 | `src/utils/protoEncoder.js` | 底层 encode/decode，含 `google.protobuf.Struct/Value` |
| 客户端适配器 | `src/adapters/` | 多客户端工具名/参数映射（Codex: `codex.js`） |
| Canonical 工具定义 | `src/adapters/canonical.js` | 标准化工具名与参数定义，Cursor exec type 映射 |
