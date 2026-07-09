# CursorGateway 踩坑记录

本文档记录项目开发过程中遇到的关键问题和解决方案，避免重复踩坑。

---

## 目录

- [1. Protobuf 编码陷阱](#1-protobuf-编码陷阱)
- [2. 工具调用去重](#2-工具调用去重)
- [3. KV Blob 机制](#3-kv-blob-机制)
- [4. Session 管理](#4-session-管理)
- [5. 编码与解码](#5-编码与解码)
- [6. 超时与心跳](#6-超时与心跳)
- [7. 原生工具分流策略](#7-原生工具分流策略)
- [8. Protobuf 编码防御性处理](#8-protobuf-编码防御性处理)
- [9. KV 工具调用与原生 exec 重复问题](#9-kv-工具调用与原生-exec-重复问题)
- [10. Session 并发竞争](#10-session-并发竞争)
- [11. 工作目录传递错误](#11-工作目录传递错误)
- [12. exec 路径参数未标准化](#12-exec-路径参数未标准化)
- [13. Edit 工具映射为 Write](#13-edit-工具映射为-write)
- [14. BidiAppend 重试与自动重连](#14-bidiappend-重试与自动重连)
- [15. MCP 工具名与 Cursor 保留名冲突](#15-mcp-工具名与-cursor-保留名冲突)
- [16. Claude Code CLI 适配器缺失](#16-claude-code-cli-适配器缺失导致工具调用失败和-session-断裂)
- [17. exec path fallback 使用 process.cwd()](#17-exec-path-fallback-使用-processcwd-导致工具操作错误目录)
- [18. Shell 命令输出丢失 — field 2 vs field 14](#18-shell-命令输出丢失--field-2-shellresult-vs-field-14-shellstream-不匹配)
- [19. Codex 客户端未接入检测（已修复，多次迭代）](#19-codex-客户端未接入检测导致工具调用不生效已修复多次迭代)
- [20. `/responses` 路由把 tools 误清空](#20-responses-路由把-tools-误清空导致不触发工具调用)
- [21. Codex 工具注册导致 Provider Error](#21-codex-工具注册导致-provider-error-grpc-status-8)
- [22. OpenAI 内置工具类型 web_search（已被 #23 取代）](#22-openai-内置工具类型导致-cursor-模型无法调用-web_search已被-23-取代)
- [23. Cursor 原生 WebSearch 需要 InteractionQuery 批准](#23-cursor-原生-websearch-需要-interactionquery-批准机制)
- [24. ApplyPatch KV 工具名映射缺失](#24-applypatch-kv-工具名映射缺失导致-codex-死循环)
- [25. KV 工具名映射全面排查](#25-kv-工具名映射全面排查测试盲区与防御性修复)

---

## 1. Protobuf 编码陷阱

### 1.1 ListValue 多元素编码（已修复）

**问题**：`encodeProtobufValue` 处理数组时，把所有元素拼在一个 field 1 里：

```javascript
// 错误写法 — 所有元素被包在单个 field 1，protobuf 解析器只保留最后一个
const listData = encodeMessageField(1, concatBytes(...values));
```

**影响**：`required: ["path", "old_string", "new_string"]` 只看到 `["new_string"]`，导致 MCP 工具的 `input_schema` 残缺。

**修复**：每个元素必须是独立的 field 1（repeated field 编码规则）：

```javascript
const encodedValues = value.map(v => encodeMessageField(1, encodeProtobufValue(v)));
const listData = concatBytes(...encodedValues);
```

### 1.2 Null 值编码缺少 value 字节（已修复）

**问题**：null 值只写了 tag（0x08）没写 varint 0，会污染后续字段解析。

```javascript
// 错误：只返回 tag，缺少 value
return encodeVarint((1 << 3) | 0);

// 正确：tag + value
const tag = encodeTag(1, 0);
const data = encodeVarint(0);
return Buffer.concat([tag, data]);
```

### 1.3 Connect/gRPC-Web 帧格式

SSE 流中的每个帧格式为 `[flags:1字节][length:4字节 BE][data]`：

- `flags & 0x80` = trailer（包含 grpc-status / grpc-message）
- 否则为数据帧，data 即 protobuf 消息

解析时必须处理**跨帧的不完整消息**，用 buffer 持续拼接。

---

## 2. 工具调用去重

### 2.1 Anthropic tool ID 不能用于去重（已修复）

**问题**：`execRequestToToolUse()` 每次调用都 `uuidv4()` 生成新 ID，即使是同一个 Cursor exec_server_message 被重放，也会得到不同的 Anthropic ID。

**修复**：用 Cursor 层面的标识符（`exec.id` + `exec.execId`）做去重：

```javascript
function getCursorExecKey(chunk) {
  if (chunk.type === 'tool_call' && chunk.execRequest) {
    const er = chunk.execRequest;
    const parts = [];
    if (er.id !== undefined && er.id !== null) parts.push(`id:${er.id}`);
    if (er.execId) parts.push(`execId:${er.execId}`);
    return parts.join('|') || null;
  }
  if (chunk.type === 'tool_call_kv' && chunk.toolUse) {
    return chunk.toolUse.id ? `kv:${chunk.toolUse.id}` : null;
  }
  return null;
}
```

### 2.2 continuation stream 会重放整个会话

Cursor 的 `continueStream()` 在收到 tool_result 后，可能从头重放整个响应。必须：

- **文本去重**：记录 `sentText`，只发送超出已发送长度的新增部分
- **工具去重**：记录 `sentCursorExecKeys`，在调用 `mapAgentChunkToToolUse` **之前**检查

### 2.3 跨请求的历史工具重放

Claude Code 每次请求带完整对话历史，包含之前的 `tool_use` / `tool_result`。如果序列化格式不当，Cursor 的模型会重新执行历史中的工具。

**修复**：将历史工具调用标记为已完成：

```javascript
// 错误：模型误认为需要执行
textParts.push(`[Tool call: ${block.name}(${JSON.stringify(block.input)})]`);

// 正确：明确标记为已完成
textParts.push(`[Already executed tool "${block.name}" with args: ${argsSummary}]`);
textParts.push(`[Tool execution result]: ${resultContent}`);
```

---

## 3. KV Blob 机制

### 3.1 什么是 KV

Cursor 的 agent 协议中，`kv_server_message`（field 4）用于大数据传输（类似 blob store）。模型的最终响应（包含文本和工具调用）可能通过 KV blob 而非 `interaction_update` 传递。

### 3.2 KV FINAL 响应

当 KV blob 中的 JSON 包含 `role: "assistant"` 且有 `id` 字段时，表示这是**最终响应**。其中 `content` 可能包含文本和工具调用（`tool-call` / `tool-use` 类型的 block）。

### 3.3 KV 工具调用与 exec 工具调用的去重

同一个工具调用可能同时出现在 `exec_server_message` 和 KV FINAL 中。需要用签名（工具名 + 参数 hash）去重，避免发送重复的 `tool_use` 给客户端。

---

## 4. Session 管理

### 4.1 Session 生命周期

```text
新请求（无 tool_result）→ 创建 session + AgentClient → chatStream()
         ↓
  工具调用 → 返回 tool_use → 保持 session
         ↓
续请求（有 tool_result）→ 查找 session → sendToolResult → continueStream()
         ↓
  无更多工具调用 → cleanupSession()
```

### 4.2 Session 查找

Claude Code 不一定会带 `x-cursor-session-id` header。退而求其次，通过 `tool_use_id` 在所有 session 的 `toolCallMapping` 中查找匹配的 session。

### 4.3 text_fallback 工具

`text_fallback` 类工具没有对应的 `exec_server_message`，发送 BidiAppend 结果会被忽略。处理方式：关闭当前 session，用完整对话历史发起新请求（fresh request）。

**注意**：`text_fallback` 和 KV-mapped 工具（如通过 KV 路径的 Edit → StrReplace）都没有对应的 `exec_server_message`，因此都会触发 `needsFreshRequest = true`。这是正确行为——BidiAppend 无法将结果发送到对应的 exec stream。

---

## 5. 编码与解码

### 5.1 UTF-8 多字节字符损坏

**问题**：`Buffer.from(textField, 'binary')` 会把 UTF-8 字符串当 Latin-1 处理，破坏中文等多字节字符。

**场景**：`CursorStreamDecoder._processMessage` 解析 protobuf text 字段时，先按 binary 转 Buffer 再转 UTF-8，导致工具参数中的中文变成乱码。

**修复**：优先使用结构化 `toolCallV2` 数据，仅在无结构化数据时回退解析 text 字段。

### 5.2 工作目录上下文

Cursor 的模型在处理长 system prompt 时，可能忽略其中的工作目录信息，导致生成 `find / -name "file"` 这类从根目录搜索的命令。

**修复**：在 system prompt 最前面添加显眼的 `[Workspace]` 块：

```text
[Workspace]
Working directory: /Users/xxx/project
All file operations should use paths relative to or within this directory.
```

---

## 6. 超时与心跳

### 6.1 chatStream 超时

- 空闲超时：3 分钟无数据
- 通过 `AbortController` 和读取超时双重控制

### 6.2 continueStream 超时

- 首事件超时（`CURSOR_CONTINUE_FIRST_EVENT_TIMEOUT_MS`）：等待第一个有效事件，默认 120 秒
- 空闲超时（`CURSOR_CONTINUE_IDLE_TIMEOUT_MS`）：两次有效事件间隔，默认 60 秒
- 绝对超时：无有意义事件（只有心跳）时的兜底，等于首事件超时

### 6.3 心跳不算有意义活动

Cursor 的 `interaction_update` 中 `field 13 = heartbeat` 只是保活信号。必须区分心跳和真正的文本/工具调用事件，否则心跳会不断重置超时，导致无限等待。

---

## 7. 原生工具分流策略

### 7.1 问题

StrReplace、TodoWrite 等 Claude Code 工具注册为 MCP 后，Cursor 模型在文件操作场景会忽略 MCP 工具，坚持用原生工具（read/write/shell），导致模型在"想调 StrReplace 但调不了"的循环中死锁。

### 7.2 解决方案：不和原生工具对抗

将 Claude Code 工具分为两类，分别处理：

| 类别 | 工具 | 处理方式 |
|------|------|----------|
| **原生覆盖** | Read, Write, StrReplace, Bash, Shell, Grep, Glob, LS, Delete | **不注册 MCP**，模型自然使用原生工具，代理通过 `execRequestToToolUse` 映射回客户端格式 |
| **无原生对应** | TodoWrite, Task, WebFetch, EditNotebook, ListMcpResources 等 | **注册 MCP** + prompt 注入 |

> **Codex 注意**：以上分类针对 Claude Code。Codex 只有 `exec_command` 和 `apply_patch` 两个工具，其中 `apply_patch` 的 patch 格式与 Cursor 原生 write 不兼容，因此注册为 MCP 而非原生覆盖。详见 #24。

### 7.3 StrReplace 的处理

StrReplace 没有直接对应的 Cursor 原生工具，但模型会自然用 read + write 两步完成同样的操作：

1. 模型调用原生 `read` → 代理返回 `Read` tool_use → 客户端执行
2. 模型调用原生 `write`（带修改后的全文）→ 代理返回 `Write` tool_use → 客户端执行

### 7.4 参数映射（动态标准化）

**关键教训：参数名不能硬编码，必须根据客户端实际发送的 tool schema 动态适配！**

```text
exec_server_message → execRequestToToolUse (原始名) → adaptKvToolUseToIde (标准化) → 客户端
```

### 7.5 Edit → StrReplace 映射

Cursor 模型发出的文件编辑操作使用 `Edit` 工具名（带 `old_string` + `new_string`），这是 StrReplace 语义。`adaptKvToolUseToIde` 通过输入感知路由处理：

- `Edit` + `old_string`/`new_string` → **StrReplace**（部分修改）
- `Edit` + `content`（无 old_string）→ **Write**（全文件写入）

### 7.6 相关代码

- `toolsAdapter.js`：`NATIVE_COVERED_TOOLS_LOWER`、`filterNonNativeTools()`
- `sessionManager.js`：`execRequestToToolUse()` 原始参数映射
- `kvToolAdapter.js`：`adaptKvToolUseToIde()`、`normalizeInputForTool()` 动态标准化
- `bidiToolFlowAdapter.js`：`mapAgentChunkToToolUse()` exec 路径也走标准化

---

## 8. Protobuf 编码防御性处理

### 8.1 问题

`encodeStringField(fieldNumber, undefined)` 会导致 `Buffer.from(undefined)` 抛出 TypeError。这类错误很难通过单元测试发现，因为单元测试通常 mock 了底层编码函数。

### 8.2 根因：错误路径的 result 结构不一致

通用错误处理器创建 `{ error: "字符串" }`，但 `buildWriteResultMessage` 假设 `result.error` 是对象，访问 `result.error.path` 和 `result.error.error` 得到 undefined。

### 8.3 修复：双层防御

**第一层 — 编码函数兜底**：所有 `encode*Field` 函数处理 undefined/null：

```javascript
function encodeStringField(fieldNumber, value) {
  const data = Buffer.from(String(value ?? ''), 'utf-8');  // undefined → ''
}
```

**第二层 — result builder 处理两种 error 格式**：

```javascript
} else if (result.error) {
  const errorPath = typeof result.error === 'object' ? (result.error.path || '') : '';
  const errorMsg = typeof result.error === 'object'
    ? (result.error.error || 'Write failed')
    : (typeof result.error === 'string' ? result.error : 'Write failed');
}
```

### 8.4 Write 成功结果回填路径

Write 工具结果通常是纯文本（如 "Wrote 1 lines to file.txt"），JSON.parse 会失败。修复后使用原始请求的 path 做回填。

### 8.5 教训

- **单元测试 mock 太深会隐藏 bug**：原有测试 mock 了 `agentClient.sendToolResult`，导致 Protobuf 编码路径从未被执行
- **端到端测试必须覆盖编码层**：用真实的 `AgentClient`（mock `bidiAppend` 替代网络 I/O）确保完整编码路径被测试
- **所有 builder 必须处理两种 error 格式**：`{ error: "string" }` 和 `{ error: { path, error } }`

---

## 9. KV 工具调用与原生 exec 重复问题

### 9.1 问题

Cursor 模型的同一个工具调用可能同时出现在两个通道：

1. **原生 exec**（`exec_server_message` field 3 = write）— 正确处理并返回结果
2. **KV FINAL 响应**（`kv_server_message` 中的 `tool-call` 块）— 重复！

由于原生覆盖工具没有注册为 MCP，Cursor 服务端对 KV 中的这些工具调用返回 `<tool_use_error>InputValidationError`。

### 9.2 现象

```text
Bash(echo "fdafdaowerqr" > 2.txt)     ← Cursor 用 shell 创建了文件
文件 2.txt 已成功创建
Error writing file                      ← KV 重复的 Write 工具失败
Write(2.txt) → Error writing file
Read 2 files → Read 2 files → ...（无限循环）
```

### 9.3 continueStream 重放 KV FINAL 导致工具调用提前终止

**根因**：`continueStream` 的 `locallyExecutedToolIds` / `locallyExecutedToolSignatures` 每次调用都重新初始化为空。当 Cursor 在 continuation 中重放第一轮的 KV FINAL，`continueStream` 无法识别它是旧数据。

**修复**：`AgentClient` 实例的 `_handledExecIds` / `_handledExecSignatures` 在 `chatStream` 结束时写入，`continueStream` 开始时继承：

```javascript
// chatStream 结束时
for (const id of locallyExecutedToolIds) this._handledExecIds.add(id);
for (const sig of locallyExecutedToolSignatures) this._handledExecSignatures.add(sig);

// continueStream 开始时
const locallyExecutedToolIds = new Set(this._handledExecIds);
const locallyExecutedToolSignatures = new Set(this._handledExecSignatures);
```

### 9.4 教训

- **同一操作可能出现在多个通道**：exec 和 KV 是独立通道
- **去重状态必须跨轮次持久化**：每个 generator 函数独立的局部变量不够
- **硬编码的工具名过滤太脆弱**：`isNativeCoveredTool` 无法区分"旧的重放"和"新的调用"

---

## 10. Session 并发竞争

### 10.1 问题

Claude Code 在代理响应较慢时（>几秒），会**重发相同的 tool_result 请求**。两个并发请求竞争同一个 `AgentClient.sseReader`，导致数据丢失和超时。

### 10.2 日志特征

```text
[Messages API] Continuing session: xxx tool_results: 1
[AgentClient] BidiAppend seqno=44, data=175bytes
...
[Messages API] Continuing session: xxx tool_results: 1    ← 重复请求！
[AgentClient] BidiAppend seqno=44, data=175bytes          ← 同一 seqno！
...
[AgentClient] Read timeout after 60000ms (idle), ending continueStream
```

### 10.3 修复：Session 并发锁

```javascript
// messages.js — continuation 处理入口
if (!session.acquireContinuationLock()) {
  await Promise.race([
    session.waitForContinuation(),
    new Promise((_, reject) => setTimeout(() => reject(new Error('lock timeout')), 90000)),
  ]);
  session = null;  // 回退到 fresh request
}

// 所有退出路径都必须释放锁
session.releaseContinuationLock();
```

### 10.4 教训

- **Claude Code 会重试**：代理响应慢时（>3-5s），客户端会重发请求
- **共享 reader 是致命的**：`ReadableStream.getReader()` 一次只能一个活跃 reader
- **seqno 冲突**：并发 BidiAppend 用相同 seqno，Cursor 可能忽略重复

---

## 11. 工作目录传递错误

### 11.1 问题

用户在 `cursor-proxy` 目录运行 Claude Code，Cursor 模型却尝试操作代理服务的目录。

### 11.2 根因（两层问题）

**第一层**：`extractWorkingDirectory` 不识别 Claude Code 的 system prompt 格式。Claude Code 发送 `Workspace Path:` 但代理只匹配 `Working directory:`。

**第二层**：`buildRequestContextResultMessage` 硬编码 `process.cwd()` 而不接收 `workspacePath` 参数。

### 11.3 修复

```javascript
// 支持多种格式
const patterns = [
  /Workspace Path:\s*([^\n]+)/i,     // Claude Code CLI
  /Working directory:\s*([^\n]+)/i,  // Cursor IDE
  /Workspace Root:\s*([^\n]+)/i,
  /CWD:\s*([^\n]+)/i,
];

// buildRequestContextResultMessage 接收 workspacePath 参数
function buildRequestContextResultMessage(id, execId, workspacePath) {
  const resolvedPath = workspacePath || process.cwd();
}
```

### 11.4 教训

- **`process.cwd()` 是代理进程的目录，不是用户的**
- **必须用真实数据测试**：测试用 `Working directory:` 格式，线上是 `Workspace Path:` 格式

---

## 12. exec 路径参数未标准化

### 12.1 问题

Claude Code 通过代理执行 Write 时始终报 "Error editing file"，模型不断重试。

### 12.2 根因

**第一层**：exec 路径绕过了参数标准化。`bidiToolFlowAdapter.js` 中 KV 路径走 `adaptKvToolUseToIde()`，但 exec 路径直接用原始输出。

**第二层**：`normalizeInputForTool` 缺少 `content → contents` 映射。Claude Code 的 Write 工具 schema 用 `contents`（复数），但映射只处理了 `content → fileText`。

**第三层**：`setIfAllowed('content', value)` 因 `content` 不在 schema 的 allowed 列表而被**静默丢弃**。

### 12.3 修复

exec 路径也走 `adaptKvToolUseToIde` 标准化，`normalizeInputForTool` 新增 `contents` 分支。

### 12.4 教训

- **两条路径必须走同一套标准化**
- **`setIfAllowed` 静默丢弃是隐蔽的 bug**
- **测试必须验证值，不能只验证 key**

---

## 13. Edit 工具映射为 Write

### 13.1 问题

模型反复输出分析但每次编辑都失败："Error editing file"。

### 13.2 根因

Cursor 模型发出 `Edit` 工具调用（带 `old_string` + `new_string`），是 StrReplace 语义。但代理把它当成 Write：

```text
Edit + { old_string, new_string }
  → TOOL_NAME_CANDIDATES 把 edit 归入 write 组
  → normalizeInputForTool 走 write 分支
  → old_string/new_string 被丢弃
```

### 13.3 修复

**输入感知路由**：

```javascript
if ((name === 'edit' || name === 'edit_file') &&
    ('old_string' in input || 'new_string' in input)) {
  // → 映射为 StrReplace 而非 Write
}
```

### 13.4 教训

- **工具名不能只看名字，要看语义**：`Edit` 可以是 StrReplace 也可以是 Write
- **Cursor 和客户端的工具名不同**：Cursor 用 `Edit`，客户端用 `StrReplace`

---

## 14. BidiAppend 重试与自动重连

### 14.1 BidiAppend 自动重试

- 网络错误、超时、5xx 服务端错误自动重试 3 次
- 指数退避：1s → 2s → 4s（最大 5s）
- 4xx 客户端错误不浪费重试
- 单次超时 15s

### 14.2 continueStream 自动降级

延迟写 SSE headers 到第一个有意义事件到达后。如果 `continueStream` 在发送任何内容之前失败，透明降级为 fresh request。

### 14.3 教训

- **网络不可靠要重试**：Cursor API 偶尔超时或连接重置
- **尽量延迟不可逆操作**：SSE headers 一旦发出不能撤回

---

## 15. MCP 工具名与 Cursor 保留名冲突

### 15.1 问题

启用 protobuf MCP 注册后，每次请求都报 `grpc-status: 8 (Provider Error)`。

### 15.2 根因

Cursor 服务端有保留工具名（`TodoWrite`、`WebFetch`、`Task`、`EditNotebook`、`FetchMcpResource`、`Delete`），以相同名字注册 MCP 会冲突。

### 15.3 修复

注册时加 `mcp_` 前缀，回调时去掉前缀还原。

```javascript
const CURSOR_RESERVED_TOOL_NAMES = new Set([
  'TodoWrite', 'WebFetch', 'Task', 'EditNotebook', 'FetchMcpResource', 'Delete',
]);

function sanitizeMcpToolName(name) {
  if (CURSOR_RESERVED_TOOL_NAMES.has(name)) return `mcp_${name}`;
  return name;
}
```

### 15.4 教训

- **Cursor 有保留工具名列表**
- **PascalCase 工具名更容易冲突**：第三方 MCP 应用 snake_case 或加前缀

---

## 16. Claude Code CLI 适配器缺失导致工具调用失败和 Session 断裂

### 16.1 问题描述

Claude Code CLI（独立命令行版本）连接代理服务后：
1. 工具调用报 "invalid arguments" 错误
2. 模型访问了错误的目录（代理服务器的工作目录而非用户的工作目录）

### 16.2 根因分析

**三层问题叠加：**

**问题 A：适配器缺失** — Claude Code CLI 使用全小写工具名（`bash`, `read`, `write`, `edit`, `grep`, `glob`），与现有的 `claude-code` 适配器（PascalCase: `Bash`, `Read`, `Write`, `StrReplace`）和 `opencode` 适配器（`read_file`, `write_file`, `str_replace`）都不匹配。

**问题 B：检测器优先级错误** — `detectClient()` 以 API key 优先匹配，如果用户配置了错误的 key（如 `opencode`），会短路工具名启发式检测，导致错误的 adapter 被选中。

**问题 C：KV-only 工具调用导致 Session 断裂** — Cursor 模型同时调用多个工具时，部分工具只出现在 KV FINAL response 中（没有对应的 `exec_server_message`）。当客户端返回这些 KV-mapped 工具的结果时，系统无法通过 BidiAppend 发送回 Cursor，只能 `signalling fresh request` 销毁 session 重新开始。新 session 中模型重新推理，可能做出不同决策。

### 16.3 修复

**A. 新增 `claude-code-cli` 适配器** — `src/adapters/claude-code-cli.js`

```javascript
toolNameMap: {
  file_read: 'read', file_write: 'write', file_edit: 'edit',
  shell_exec: 'bash', content_search: 'grep', file_search: 'glob',
  dir_list: 'ls', file_delete: 'delete',
  web_fetch: 'webfetch', web_search: 'websearch',
  todo_write: 'todowrite', todo_read: 'todoread',
}
```

**B. 检测器改为"工具名优先"** — `src/adapters/detector.js`

工具名启发式现在优先于 API key 匹配。关键区分逻辑：
- PascalCase 原始名（`StrReplace`, `Bash`）→ `claude-code`
- 全小写 + bash + read + grep 组合 → `claude-code-cli`
- `read_file` / `write_file` → `opencode`
- `exec` → `openclaw`

**C. 问题 C 暂未修复** — KV-only 工具调用导致 session 断裂是更深层的架构问题，需要在 agentClient 层面支持对 KV-mapped 工具结果的 BidiAppend 传输。

### 16.4 教训

- **不同版本的同一客户端可能有完全不同的工具命名风格** — Claude Code Cursor 集成版用 PascalCase，CLI 版用全小写
- **检测策略应以"做了什么"（工具名）优先于"说了什么"（API key）** — 用户可能配置错误的 key
- **Cursor 的 exec_server_message 并非覆盖所有工具调用** — 部分只出现在 KV FINAL 中，需要有降级策略

---

## 17. exec path fallback 使用 process.cwd() 导致工具操作错误目录

### 17.1 问题描述

Claude Code CLI 在 `~/Downloads/cursor逆向agent客户端` 目录下运行，但模型的 `Grep` 工具调用却搜索了代理服务器的目录 `/Users/taxue/Documents/AI/Cursor-To-OpenAI`。

### 17.2 根因分析

**触发链路：**

1. Cursor 模型同时调用 `Glob` + `Read`，其中 `Glob` 只出现在 KV FINAL（无 exec_server_message）
2. `Glob` 的 KV-mapped 结果触发 `needsFreshRequest: true`，session 被销毁
3. fresh request 中模型重新推理，发出 `grep` exec，但 `execRequest.path` 为 `undefined`
4. `execRequestToToolUse` 中 `path: execRequest.path || process.cwd()` fallback 到了代理进程的工作目录

**直接原因：** `sessionManager.js` 的 `execRequestToToolUse` 和 `sendToolResult` 中，当 Cursor 的 exec request 没有提供 `path` 时，fallback 使用 `process.cwd()` —— 这是代理服务器自身的工作目录，不是用户的。

### 17.3 修复

将所有 `process.cwd()` fallback 替换为 `session.agentClient?.workspacePath`：

```javascript
// execRequestToToolUse 函数开头
const sessionCwd = session.agentClient?.workspacePath || process.cwd();

// grep/ls 的 path fallback
canonInput = { pattern: execRequest.pattern, path: execRequest.path || sessionCwd };

// sendToolResult 的 cwd fallback
cwd: cursorRequest.cwd || session.agentClient?.workspacePath || process.cwd(),
```

共修改 4 处 `process.cwd()` fallback（adapter 路径 2 处 + legacy 路径 2 处）+ 1 处 `sendToolResult` 中的 cwd。

### 17.4 教训

- **`process.cwd()` 在代理服务中几乎总是错误的** — 应该用从客户端 system prompt 提取的 `workspacePath`
- **KV-mapped 工具 → fresh request → 新 exec 的 path 可能为空** — 这种路径需要正确的 fallback
- **session 对象应该是唯一的"真相来源"** — 工作目录信息应通过 `session.agentClient.workspacePath` 获取，而非全局状态

---

## 18. Shell 命令输出丢失 — field 2 (ShellResult) vs field 14 (ShellStream) 不匹配

### 18.1 问题描述

通过代理使用 Claude Code CLI 时，shell 命令执行后模型反复说"输出为空"，不断用不同方式重试相同命令。每次重试都触发 Claude Code 的权限确认弹窗。

### 18.2 根因分析

Cursor 的 `ExecServerMessage` 有两种 shell 命令格式：
- **field 2**: `shell_args` (ShellArgs) — 旧版，期望 `ExecClientMessage.shell_result` (field 2, ShellResult) 回复
- **field 14**: `shell_stream_args` (ShellArgs) — 新版流式 shell，期望 `ExecClientMessage.shell_stream` (field 14, ShellStream) 回复

两种请求格式的 args 结构相同（都是 `ShellArgs`），但 **回复格式完全不同**：
- `ShellResult` (field 2): `{ success/failure { command, cwd, exit_code, stdout, stderr } }`
- `ShellStream` (field 14): 多条消息序列 `{ stdout { data }, stderr { data }, exit { code, cwd } }`

代理将两种请求统一解析为 `type: 'shell'`，且统一用 field 2 的 `ShellResult` 格式回复。当 Cursor 服务端发送 field 14 的请求时，它期望收到 field 14 的 `ShellStream` 回复，但收到的是 field 2 的 `ShellResult`——Cursor 忽略了这个不匹配的回复，导致模型看不到输出。

### 18.3 修复方案

1. **在 `parseExecServerMessage` 中记录原始 field number**：
```javascript
case 2:  // shell
case 14: { // shell v2
  const args = parseArgs(field.value, { 1: 'command', 2: 'cwd' });
  result = { type: 'shell', id, execId, command: args.command, cwd: args.cwd, shellField: field.fieldNumber };
  break;
}
```

2. **新增 `buildShellStreamMessages` 函数**，返回多条 `ExecClientMessage` buffer：
```javascript
function buildShellStreamMessages(id, execId, cwd, stdout, stderr, exitCode) {
  // 返回 [ShellStreamStdout (field 14.1), ShellStreamStderr (field 14.2), ShellStreamExit (field 14.3)]
  // 每条消息的 ExecClientMessage 使用 field 14 (shell_stream) 而非 field 2 (shell_result)
}
```

3. **在 `sendToolResult` 中根据 `shellField` 分流**：
- `shellField === 14` → 使用 `buildShellStreamMessages` + 逐条发送 + control message
- `shellField === 2` 或未设置 → 使用原来的 `buildShellResultMessage`

### 18.4 教训

- **请求和回复的 field number 必须匹配** — 同类型工具的不同版本（v1/v2）虽然请求 args 相同，但回复格式可能完全不同
- **Cursor 客户端源码是唯一可靠参考** — `workbench.desktop.main.js` 中 `handleShellStream` (BWA) 的实现明确展示了流式 shell 的回复协议
- **"输出为空"的表象可能是 protobuf field mismatch** — 如果 Cursor 用 oneof 解析，错误的 field number 直接被忽略而不报错

---

## 19. Codex 客户端未接入检测导致工具调用不生效（已修复，多次迭代）

### 19.1 问题描述

`codex` 请求可以走到接口，但在工具调用阶段没有出现 `codex_*` 工具，表现为“Codex 没有调用工具”。

### 19.2 根因分析

1. `detector.js` 的 `ADAPTERS` 未注册 `codex`，`detectFromTools()` 也没有 `codex_*` 规则，导致请求无法命中 Codex 适配器。
2. 原 `src/adapters/codex.js` 使用了旧接口（类 + `mapToolName` 风格），与当前 `ClientAdapter` 的 canonical 映射机制不一致。
3. 原 `test/unit/adapter-codex.test.js` / `test/integration/codex-e2e.test.js` 使用 `describe` 风格，在当前 `node test/*.test.js` 执行方式下无法稳定覆盖真实链路。

### 19.3 修复方案（当前状态）

> **⚠️ 注意**：早期版本曾使用 `codex_file_read`, `codex_exec` 等虚构名字，后来改为匹配 Codex 实际发送的工具名。以下为当前实际映射。

Codex 适配器（`src/adapters/codex.js`）：
- `shell_exec → exec_command`（param: `command→cmd`, `working_directory→workdir`）
- `file_edit → apply_patch`（**注册为 MCP 工具**，不在 nativeCoveredTools 中）
- `nativeCoveredTools` 只包含 `shell_exec`

检测器（`src/adapters/detector.js`）通过 `exec_command && apply_patch` 组合识别 Codex。

### 19.4 教训

- **适配器升级到 canonical 架构后，新增客户端必须同时打通“适配器 + 检测器 + 可执行测试”三件套。**
- **adapter 的工具名必须从客户端实际请求中抓包确认**，不能凭猜测定义虚构名字。
- **测试风格必须与仓库 runner 一致**，否则会形成“看起来有测试，实际上没执行”的盲区。

---

## 20. `/responses` 路由把 tools 误清空导致不触发工具调用

### 20.1 问题描述

本地调试中，`POST /responses` 即使请求体携带 `tools`，模型也不触发任何工具调用。

### 20.2 根因分析

`src/routes/responses.js` 在构造 Cursor 请求时写死了：

```javascript
generateCursorBody(messages, model, { agentMode, tools: [] });
```

导致 `supportedTools` 始终为空。Cursor 侧看不到可用工具，自然不会产出 `tool_call`。

### 20.3 修复方案

1. 根据请求是否含工具动态设置 `supportedTools`：
   - 有工具：`DEFAULT_AGENT_TOOLS`
   - 无工具：`[]`
2. 为 `/responses` 增加双向工具会话（`tool_call -> function_call_output -> continueStream`），避免 Cursor 因缺少 tool result 报 `ERROR_USER_ABORTED_REQUEST`。
3. continuation 阶段补充 Cursor exec 去重（`session.sentCursorExecKeys`），过滤 `continueStream` 中被重放的同一工具调用。
4. 新增回归测试：
   - `test/unit/responses-tools-forwarding.test.js`：验证有工具时走 AgentClient 路径，无工具时走 legacy 路径
   - `test/unit/responses-tool-roundtrip.test.js`：验证 `/responses` 两阶段工具闭环
- 通过重放真实请求验证：首轮返回 `requires_action/function_call`，续轮发送 `function_call_output` 后不再出现 aborted 错误，链路可继续完成。

### 20.4 教训

- **路由层接收了 tools，不代表真正透传到 Cursor 协议层；必须验证最终 `supportedTools`。**
- **工具链路不止“发出 tool_call”，还要验证 continuation 阶段是否正确去重与续跑。**
- **这类“参数被覆盖/清空”问题需要在请求构造边界加单测，避免功能静默失效。**

---

## 21. Codex 工具注册导致 Provider Error (grpc-status: 8)

### 21.1 问题描述

Codex 客户端通过 `/responses` 端点发送请求时，请求体中携带 19 个工具。Gateway 将全部工具直接传给 Cursor 的 `AgentClient.chatStream`，Cursor 服务端返回 `grpc-status: 8` (RESOURCE_EXHAUSTED) Provider Error，请求始终失败。

### 21.2 根因分析

三个层面的问题叠加：

1. **Codex adapter 工具名不匹配**：adapter 定义的是虚构工具名，但 Codex 实际发送的工具名是 `exec_command`, `apply_patch`, `spawn_agent` 等。detector 无法识别 Codex 客户端，回退到默认的 `claude-code` adapter。

2. **非 function 工具导致注册失败**：Codex 请求里包含 `{ "type": "web_search", "external_web_access": false }` 条目，这是 OpenAI 的内建 tool type，没有 `name`/`description`/`parameters`。将它注册为 MCP 工具会导致 Cursor 直接报 Provider Error。

3. **原生工具被误注册为 MCP**：`exec_command` 对应 Cursor 的原生 `shell`，不应注册为 MCP。其余工具（`spawn_agent`, `js_repl` 等）是 Codex 特有的，需要注册 MCP。

### 21.3 修复方案

**文件改动**：

1. **`src/adapters/codex.js`**：重写工具映射：
   - `shell_exec → exec_command`（param: `command→cmd`, `working_directory→workdir`）
   - `file_edit → apply_patch`（**注册为 MCP 工具**，不在 nativeCoveredTools 中——因为 patch 格式与 Cursor 原生 write 不兼容）
   - `nativeCoveredTools` 只包含 `shell_exec`

2. **`src/adapters/detector.js`**：Codex 检测签名为 `exec_command && apply_patch`

3. **`src/routes/responses.js`**：
   - 新增 `cursorExecToShellCommand()` 函数：将 Cursor 原生工具调用（read, ls, grep, delete, write）转换为等价 shell 命令
   - `execRequestToFunctionCall()` 增加 shell fallback：当 adapter 对某个 canonical tool 没有映射时（如 `file_read`），自动转为 `exec_command` + shell 命令
   - 工具过滤链：`tools → filter(有 name 且是 function type) → filterNonNativeTools(adapter)` → 只注册非原生的 MCP 工具

4. **关于 `web_search`**：Codex 的 `{ "type": "web_search" }` 是 OpenAI 内建 tool type（非 function tool），且 `external_web_access: false` 表示禁用。Cursor 有自己的原生 web_search（ID: 18，服务端执行），在 `DEFAULT_AGENT_TOOLS` 中已包含。直接跳过不注册不会丢失能力。

### 21.4 教训

- **adapter 的工具名必须从客户端实际请求中获取**，不能凭猜测。应该先抓包确认真实工具名再定义映射。
- **detector 的检测签名必须和 adapter 工具名一致**，否则客户端无法被正确识别。
- **注册 MCP 工具前必须验证格式**：无 name 的条目、非 function 类型的条目都不能注册。
- **不同客户端的工具哲学不同**：Claude Code 有 Read/Write/Grep 等细粒度工具；Codex 只有 `exec_command`（通过 shell 完成一切）和 `apply_patch`（文件编辑）。Gateway 的映射层必须适配这种差异。
- **Cursor 的 web_search 是服务端执行的**，走 InteractionQuery/InteractionResponse 审批流程（详见 #23）。
- **native covered 不等于格式兼容**：`apply_patch` 的 patch 格式与 Cursor 原生 write 不兼容，不应标为 native covered（详见 #24）。

---

## 22. OpenAI 内置工具类型导致 Cursor 模型无法调用 web_search（已被 #23 取代）

### 22.1 问题描述

> **⚠️ 本节描述的 MCP 注册方案已过时。** 当前 web_search/web_fetch 改为启用 Cursor 原生能力 + InteractionQuery 自动审批，不再注册为 MCP 工具。详见 [#23](#23-cursor-原生-websearch-需要-interactionquery-批准机制)。

用户让 Codex 访问一个网页时，模型输出文本"我先抓取这个页面"后就卡住了。日志显示 Checkpoint 后没有 exec_server_message 也没有 KV FINAL，最终 SSE 超时。

### 22.2 根因分析

Codex 的工具列表中 `web_search` 的格式是 `{ "type": "web_search" }`——这是 **OpenAI Responses API 的内置工具类型**，不是标准的 function tool。它没有 `name` 字段，也没有 `function` 字段。

现有的过滤逻辑 `tools.filter(t => (t.type === 'function' || t.name) && (t.name || t.function?.name))` 会将其过滤掉（因为 `name` 为 `undefined`，`type` 不是 `function`）。结果就是 `web_search` 工具没有注册为 MCP 工具，Cursor 模型不知道有这个工具可用。

虽然 Cursor 有原生的 `WEB_SEARCH`（ClientSideToolV2 = 18）能力，但它需要特定的请求参数（如 `webTool: "full search"`）才能在 agent.v1 协议中启用。此外，`ExecServerMessage` 中没有专门的 web_search field——原生 web search 在 Cursor 服务端内部执行。

对于 Gateway 而言，最可靠的方案是：将 `web_search` 作为 MCP 工具注册给 Cursor，让模型通过 McpArgs 发起调用，Gateway 内部执行后返回结果。

同时，`ExecServerMessage` field 20 (`FetchArgs`) 是 Cursor 的原生 fetch 能力（获取网页内容），我们的 `parseExecServerMessage` 之前缺少对它的解析。

### 22.3 修复方案（已被 #23 取代）

- 移除 web_search 相关 MCP 拦截逻辑（改为 Cursor 原生处理，见 #23）

~~1. **`src/routes/responses.js`**：~~
   - ~~新增 `BUILTIN_TOOL_SCHEMAS` 和 `normalizeBuiltinTools()` 函数~~
   - 在 MCP 过滤之前调用 `normalizeBuiltinTools(tools)` 将 `{type: "web_search"}` 转为标准 function 格式 `{type: "function", name: "web_search", function: {...}}`
   - 新增 `executeInternalFetch()` 函数处理 Cursor 发来的 fetch exec request
   - 主循环中添加 `fetch` 类型的内部执行分支

2. **`src/utils/agentClient.js`**：
   - `parseExecServerMessage` 新增 `case 20` 解析 `FetchArgs { url=1 }`
   - 新增 `buildFetchResultMessage()` 构建 `FetchResult` (field 20)
   - `sendToolResult` 新增 `case 'fetch'`

### 22.4 教训

- **OpenAI 的内置工具类型和 function 工具格式不同**：`{type: "web_search"}` 没有 name/function 字段，必须在注册 MCP 前归一化
- **不能假设 Cursor 服务端会自动启用所有能力**：原生 web_search 需要特定的请求参数，没有这些参数模型可能看不到搜索工具
- **proto 解析器必须覆盖所有已知的 ExecServerMessage field**：遗漏 field 20 (fetch) 会导致 Cursor 的 fetch 请求被静默忽略

---

## 23. Cursor 原生 WebSearch 需要 InteractionQuery 批准机制

### 23.1 问题描述

设置 `RequestContext.web_search_enabled = true` 后，模型仍不调用 web_search，超时后只输出文本。

### 23.2 根因分析

通过逆向 Cursor 源码发现，`agent.v1` 中的 web_search 完整流程：

1. `RequestContext` 中设置 `web_search_enabled` (field 17) 和 `web_fetch_enabled` (field 24) 为 true
2. 模型决定搜索时，Cursor 服务端通过 **`AgentServerMessage.interaction_query`** (field 7) 发送审批请求
3. 客户端解析 `InteractionQuery` 中的 `web_search_request_query` (field 2) 或 `web_fetch_request_query` (field 9)
4. 客户端通过 **`AgentClientMessage.interaction_response`** (field 6) 返回 approved/rejected
5. 如果 approved，Cursor 服务端执行搜索，结果通过 `InteractionUpdate.tool_call_completed` 中的 `WebSearchToolCall` (field 18) 返回

Gateway 之前完全没有处理 `AgentServerMessage` 的 field 7（`interaction_query`），导致 Cursor 服务端发出搜索审批请求后永远收不到回复。

### 23.3 关键协议结构

```
AgentServerMessage {
  field 1: interaction_update
  field 2: exec_server_message
  field 3: conversation_checkpoint_update
  field 4: kv_server_message
  field 5: exec_server_control_message
  field 7: interaction_query  ← NEW
}

InteractionQuery {
  uint32 id = 1;
  WebSearchRequestQuery web_search_request_query = 2;  // { args: WebSearchArgs }
  AskQuestionInteractionQuery ask_question_interaction_query = 3;
  SwitchModeRequestQuery switch_mode_request_query = 4;
  WebFetchRequestQuery web_fetch_request_query = 9;
  ...
}

AgentClientMessage {
  field 6: interaction_response  ← NEW
}

InteractionResponse {
  uint32 id = 1;
  WebSearchRequestResponse web_search_request_response = 2;  // { approved {} | rejected { reason } }
  WebFetchRequestResponse web_fetch_request_response = 9;
  ...
}
```

### 23.4 修复方案

**`src/utils/agentClient.js`**：
1. `buildRequestContext` 新增 `field 17 = true` (web_search_enabled) 和 `field 24 = true` (web_fetch_enabled)
2. 新增 `parseInteractionQuery()` 解析 field 7 的 InteractionQuery
3. 新增 `buildInteractionResponseApproved()` 编码 approved 响应（`AgentClientMessage.field 6`）
4. `chatStream` 和 `continueStream` 的 SSE 主循环中添加 field 7 处理：自动批准 web_search 和 web_fetch

**`src/utils/toolsAdapter.js`**：
- `NATIVE_COVERED_TOOLS_LOWER` 新增 `'web_search'` 和 `'websearch'`，阻止注册为 MCP（由 Cursor 原生处理）

**`src/routes/responses.js`**：
- 清理 #22 的临时方案：移除 `BUILTIN_TOOL_SCHEMAS` 中的 `web_search`（改为原生处理）
- 清理 #22 的临时方案：移除 `executeInternalWebSearch()` 等 MCP 拦截逻辑

### 23.5 教训

- **Cursor 的 web_search 不走 ExecServerMessage**，走的是 `InteractionQuery/InteractionResponse` 审批流程
- **`AgentServerMessage` 不止 field 1-5**，field 7 (`interaction_query`) 是关键的交互查询通道
- **Cursor IDE 源码中搜索 `webSearchEnabled` 可以找到完整的启用和处理逻辑**
- **仅设置 enabled flag 不够**，必须实现完整的审批回调（`interaction_response` approved）
- **模型可能选择 `WebFetch` 而非 `WebSearch`** 来完成搜索任务——这是正常行为

---

## 24. ApplyPatch KV 工具名映射缺失导致 Codex 死循环

### 24.1 问题描述

Codex 客户端请求"生成一个文件 7.txt"时，Cursor 模型通过 KV tool call 发出 `ApplyPatch` 工具调用，Gateway 将其透传给 Codex。但 Codex 返回 `"unsupported call: ApplyPatch"`，因为 Codex 只识别 `apply_patch`（下划线格式）。模型不断重试，陷入死循环。

**注意**：`ApplyPatch` **不是 Cursor 的原生工具**。Cursor 的 `ClientSideToolV2` 枚举中没有 `APPLY_PATCH`，原生写文件用的是 `EDIT_FILE` / `EDIT_FILE_V2`。`ApplyPatch` 是 Cursor 模型从 Codex 的 system prompt 中学到的——prompt 里有 `apply_patch` 的工具描述，模型据此在 KV blob 中"自发"生成了 PascalCase 格式的 tool-call。

### 24.2 根因分析

**核心矛盾**：Codex adapter 把 `apply_patch` 标记为 `nativeCoveredTools`（canonical: `file_edit`），声称 Cursor 原生 exec 能处理。但这是一个**设计缺陷**——Cursor 原生 write（field 3）接受 `path + fileText`，而 `apply_patch` 使用 `*** Begin Patch` 格式，两者完全不兼容。

完整因果链：

1. Codex 发请求给 Gateway，`tools` 数组中有 `apply_patch`，system prompt 中有 `apply_patch` 的使用描述
2. `filterNonNativeTools` 过滤时：`toCanonical('apply_patch')` → `file_edit` → `nativeCoveredTools` 包含 → **不注册为 MCP**
3. 但 system prompt **原样传给了 Cursor 模型**，prompt 里仍然有 `apply_patch` 的工具描述
4. **工具注册和 prompt 不同步**：Cursor 模型看到了工具描述，但 Cursor 服务端不知道有这个工具
5. Cursor 模型想创建文件时，读到 prompt 中的 patch 格式描述，在 **KV blob 中自行生成** `ApplyPatch` tool-call（PascalCase 命名）
6. `ApplyPatch` 不是注册过的 MCP 工具也不是原生 exec 类型——它是模型从 prompt "学来的"，通过 KV 强行输出
7. `kvToolToExecRequest('ApplyPatch')` 无匹配 → 返回 null
8. `kvToolUseToFunctionCall()` fallback 直接用原始名字 `ApplyPatch` 发给 Codex
9. Codex 只识别 `apply_patch`（snake_case），返回 `unsupported call: ApplyPatch`
10. 多轮 unsupported 累积 → 模型反复重试 → 死循环

**为什么 `apply_patch` 标记为 native covered 是有缺陷的**：

| 对比项 | Cursor 原生 write (field 3) | Codex apply_patch |
|--------|---------------------------|-------------------|
| 参数格式 | `{ path, fileText }` | `{ input: "*** Begin Patch\n..." }` |
| 语义 | 整文件覆盖写入 | 增量补丁（新建/修改/删除） |
| 调用方式 | ExecServerMessage field 3 | Codex function tool |

Cursor 的原生 write 只能"覆盖写"，无法处理 patch 格式。声称 native covered 后，Gateway 把 Cursor 的 `write` exec 通过 `cursorExecToShellCommand` 转成 `cat > file << HEREDOC` 的 shell 命令交给 Codex 的 `exec_command` 执行——**完全绕过了 `apply_patch`**。

> **⚠️ 更新**：当前已改为**方案 A+B 组合**——从 `nativeCoveredTools` 移除 `file_edit`，让 `apply_patch` 注册为 MCP 工具（方案 A），同时保留 `KV_TOOL_NAME_TO_CLIENT` 作为防御性兜底（方案 B）。实测 MCP 注册的 `apply_patch` 不会与 Cursor 原生 write 冲突，因为两者参数格式完全不同。

### 24.3 修复方案

**`src/routes/responses.js`**：
1. 新增 `KV_TOOL_NAME_TO_CLIENT` 映射表，将 KV 中的 PascalCase 工具名映射到 Codex snake_case 工具名
2. `kvToolUseToFunctionCall()` 在 adapter canonical 映射也查不到时，用该映射表做最后兜底

```javascript
const KV_TOOL_NAME_TO_CLIENT = {
  applypatch: 'apply_patch',
  apply_patch: 'apply_patch',
  applyedit: 'apply_patch',
};
```

### 24.4 教训

- **native covered 声明必须精确**：`file_edit` 映射到 `apply_patch` 然后标记为 native covered 是不准确的——Cursor 原生 write 和 Codex 的 patch 格式不兼容。**已修复**：`file_edit` 已从 `nativeCoveredTools` 移除，`apply_patch` 注册为 MCP
- **工具注册和 prompt 必须同步**：如果一个工具被 `filterNonNativeTools` 过滤掉了（不注册 MCP），但它的描述仍在 prompt 中，Cursor 模型会根据 prompt 描述"强行"生成 KV tool-call
- **Cursor 模型的 KV tool-call 使用 PascalCase**：即使 prompt 里写的是 `apply_patch`，模型输出的 KV 工具名是 `ApplyPatch`
- **`kvToolToExecRequest` 只覆盖能转为 exec 的工具**：对于无法转为 exec 但需要名字映射的工具（如 `ApplyPatch`），需要 `KV_TOOL_NAME_TO_CLIENT` 额外映射层
- **死循环的根因通常是 "unsupported call" 回传**——Codex 不认识的工具名会被标记为 unsupported，模型会反复重试

---

## 25. KV 工具名映射全面排查：测试盲区与防御性修复

### 25.1 问题描述

修复 #24 的 `ApplyPatch` 映射后，进一步排查发现：

1. **`kvToolUseToFunctionCall` 函数完全没有测试覆盖**——这是 ApplyPatch 问题未被发现的根本原因
2. Cursor 服务端专有工具（`SearchSymbols`、`GoToDefinition` 等）也可能通过 KV blob 泄漏到客户端，导致 "unsupported call" 错误
3. 测试只覆盖了 exec 通道（`ExecServerMessage` → `execRequestToFunctionCall`），KV 通道是独立路径

### 25.2 根因分析

Gateway 有两条工具调用通道，但只有一条有测试覆盖：

```text
通道 A (exec)：ExecServerMessage → execRequestToFunctionCall → Codex
  ✅ 有完整的单元测试和端到端测试

通道 B (KV)：kv_server_message → kvToolUseToFunctionCall → Codex
  ❌ 完全没有测试
```

KV 通道的决策树：

```text
KV tool call (name, input)
  │
  ├─ kvToolToExecRequest(name) 匹配？
  │   ├─ 是 → 转为 execRequest → execRequestToFunctionCall（和 exec 通道合流）
  │   └─ 否（返回 null）→ 进入 fallback
  │
  └─ fallback:
      ├─ adapter.toCanonical(name) 有结果？→ 用 adapter 映射
      ├─ KV_TOOL_NAME_TO_CLIENT[name] 有结果？→ 用静态映射
      └─ 都没有 → 直接用原始名字（⚠️ 风险点）
```

**ApplyPatch 走到了最后一步**——原始名字 `ApplyPatch` 直接发给 Codex，Codex 不认识。

### 25.3 完整排查结果

#### 已覆盖的 KV 工具名（在 `kvToolToExecRequest` switch 中）

| KV 工具名 | 映射到 exec type | Gateway 处理 |
|-----------|-----------------|-------------|
| `ReadFile` / `Read` / `read_file` | `read` | 内部执行 or fallback `exec_command` |
| `ListDir` / `ListDirectory` / `list_dir` | `ls` | 内部执行 or fallback `exec_command` |
| `Glob` / `GlobFileSearch` / `glob_file_search` | `ls` | 内部执行 or fallback `exec_command` |
| `Grep` / `RipgrepSearch` / `rg` / `ripgrep_search` | `grep` | 内部执行 or fallback `exec_command` |
| `EditFile` / `Write` / `write` / `edit_file` | `write` | fallback → `exec_command` (cat > heredoc) |
| `DeleteFile` / `delete_file` | `delete` | fallback → `exec_command` (rm) |
| `RunTerminalCommand` / `Shell` / `run_terminal_command` | `shell` | 直接映射 → `exec_command` |
| `FileSearch` / `file_search` | `grep` | 内部执行 or fallback `exec_command` |
| `WebSearch` / `web_search` | `null` | 被 `WEB_NATIVE_TOOL_NAMES` 跳过 |

#### 需要额外映射的 KV 工具名

| KV 工具名 | 映射目标 | 说明 |
|-----------|---------|------|
| `ApplyPatch` | `apply_patch` | Cursor 模型的补丁工具，只通过 KV 发出，不走 exec 通道 |

#### Cursor 服务端专有工具（不应转发给客户端）

| KV 工具名 | 说明 |
|-----------|------|
| `SearchSymbols` / `search_symbols` | IDE 符号搜索 |
| `GoToDefinition` / `go_to_definition` | IDE 跳转到定义 |
| `Reapply` | 重新应用编辑 |
| `FetchRules` / `fetch_rules` | 获取规则 |
| `CreateDiagram` / `create_diagram` | 创建图表 |
| `FixLints` / `fix_lints` | 修复 lint 错误 |
| `ReadLints` / `read_lints` | 读取 lint 错误 |
| `DeepSearch` / `deep_search` | 深度搜索 |
| `KnowledgeBase` / `knowledge_base` | 知识库 |
| `ReadProject` / `read_project` | 读取项目 |
| `UpdateProject` / `update_project` | 更新项目 |
| `CreatePlan` / `create_plan` | 创建计划 |
| `FetchPullRequest` / `fetch_pull_request` | 获取 PR |

这些工具正常情况下通过 exec 通道处理，不会出现在 KV 中。但为了防御性编程，增加了 `CURSOR_SERVER_ONLY_KV_TOOLS` 集合进行过滤。

### 25.4 修复方案

**`src/routes/responses.js`**：

1. 新增 `CURSOR_SERVER_ONLY_KV_TOOLS` 集合，列出所有 Cursor 服务端专有工具
2. KV 流处理中增加过滤：`CURSOR_SERVER_ONLY_KV_TOOLS.has(kvName)` → 跳过不转发

```javascript
const CURSOR_SERVER_ONLY_KV_TOOLS = new Set([
  'searchsymbols', 'search_symbols',
  'gotodefinition', 'go_to_definition',
  'reapply',
  'fetchrules', 'fetch_rules',
  'creatediagram', 'create_diagram',
  'fixlints', 'fix_lints',
  'readlints', 'read_lints',
  'readproject', 'read_project',
  'updateproject', 'update_project',
  'createplan', 'create_plan',
  'fetchpullrequest', 'fetch_pull_request',
  'deepsearch', 'deep_search',
  'knowledgebase', 'knowledge_base',
]);
```

**`test/unit/responses-tools-forwarding.test.js`**：

全新测试文件，44 个测试用例覆盖 `kvToolUseToFunctionCall` 的所有路径：

| 测试组 | 数量 | 覆盖范围 |
|--------|------|---------|
| exec 路径工具 | 12 | ReadFile, Write, Shell 等全部 KV→exec 映射 |
| KV_TOOL_NAME_TO_CLIENT | 3 | ApplyPatch → apply_patch 正确性 |
| 映射表完整性 | 3 | applypatch/apply_patch/applyedit 都指向 apply_patch |
| 服务端专有工具过滤 | 15 | CURSOR_SERVER_ONLY_KV_TOOLS 集合完整性 |
| Web 原生工具跳过 | 4 | WEB_NATIVE_TOOL_NAMES 集合完整性 |
| 无 adapter fallback | 3 | 无 Codex adapter 时的通用行为 |
| 结构正确性 | 4 | function_call 格式、JSON 合法性 |

### 25.5 教训

- **两条通道都需要测试覆盖**：exec 通道和 KV 通道是独立的代码路径，只测其中一条会留下盲区
- **"测试全绿" ≠ "没有问题"**：如果被测函数根本没有对应的测试文件，100% 通过率毫无意义
- **防御性过滤优于事后修补**：对已知不该转发的工具名，应该主动建立过滤集合，而不是等它们触发 "unsupported call" 才发现
- **每新增一个映射函数，必须同步新增测试文件**：`module.exports._xxx` 导出了但没有测试 = 技术债
- **Cursor 工具名命名风格不统一**：exec 通道用 lowercase（`read`, `shell`），KV 通道用 PascalCase（`ReadFile`, `ApplyPatch`），必须在映射层处理大小写
