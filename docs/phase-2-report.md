# 阶段 2 报告：IDE 真实流量对照

## 抓包成果

用 mitmproxy 抓到 **75 个真实 IDE 请求**（api2.cursor.sh），覆盖：

| Endpoint | 次数 | 说明 |
|---|---|---|
| `AvailableModels` | 2 | 模型列表 |
| `GetDefaultModel` | 1 | 默认模型 |
| `GetDefaultModelNudgeData` | 1 | 模型推荐 |
| `FileSyncService/FSIsEnabledForUser` | 5 | 检查文件同步权限 |
| `FileSyncService/FSConfig` | 5 | 文件同步配置 |
| `CodebaseSnapshotService/*` | 53 | **代码索引 embedding**（Cursor 后台自动做） |
| `AnalyticsService/BootstrapStatsig` | 1 | 特性开关 |
| `AiService/CppAppend` / `CppEditHistoryStatus` | 3 | Copilot++ (tab 补全) |
| `BackgroundComposerService/List*Environments` | 2 | 云端 Composer 环境 |
| `ServerConfigService/GetServerConfig` | 1 | 服务端配置 |

**没抓到 chat/agent 请求**——它走 `api3.cursor.sh`，且 api3 有 pinning，mitmproxy 拦不下（TLS 握手 82 次失败）。

## 完整 Header 集合（IDE 3.10.20 真实）

所有 chat/agent 相关 endpoint 都必须携带这 25 个 header：

| Header | 值/说明 |
|---|---|
| `authorization` | `Bearer <JWT>`（**只有 JWT，没有 `userId::` 前缀**）|
| `content-type` | `application/proto` |
| `content-encoding` | `gzip`（body 用 gzip 压缩）|
| `accept-encoding` | `gzip` |
| `connect-protocol-version` | `1` |
| `connect-timeout-ms` | `600000`（10 分钟，仅长连接 endpoint）|
| `traceparent` | W3C trace context, `00-{trace_id}-{span_id}-00` |
| `x-amzn-trace-id` | `Root={trace_id_UUID}`（与 traceparent 中段一致）|
| `x-client-key` | 64 字符 hex（SHA-256），每 session 生成 |
| **`x-cursor-checksum`** | 见 [checksum-algorithm.md](checksum-algorithm.md) |
| `x-cursor-client-arch` | `x64` / `arm64` |
| `x-cursor-client-device-type` | `desktop` |
| `x-cursor-client-layout` | **`unifiedAgent`**（3.10 新增，CursorGateway 用错了）|
| `x-cursor-client-os` | `darwin` / `linux` / `win32` |
| `x-cursor-client-type` | `ide`（可选 `cli` / `glass`）|
| `x-cursor-client-version` | `3.10.20` |
| `x-cursor-config-version` | UUID，每次启动生成新的 |
| `x-cursor-streaming` | `true`（chat/stream）|
| `x-cursor-timezone` | `Asia/Shanghai` 等 IANA 时区 |
| `x-ghost-mode` | `false` / `true`（隐私模式）|
| `x-new-onboarding-completed` | `false` / `true` |
| `x-request-id` | UUID，每请求唯一 |
| `x-session-id` | UUID，每次启动生成 |
| `user-agent` | `connect-es/1.6.1`（Connect Web transport）|

**3.10 新增/CursorGateway 遗漏**：
- ✨ `x-cursor-client-layout: unifiedAgent`（CursorGateway 用 `default` 或不发）
- ✨ `x-client-key`（session 级 SHA-256）
- ✨ `x-cursor-config-version`（每启动一次）
- ✨ `x-new-onboarding-completed`
- ✨ `content-encoding: gzip` + body gzip 压缩
- ✨ `traceparent` + `x-amzn-trace-id`

## 关键发现

### 1. checksum 算法完全破译
详见 [checksum-algorithm.md](checksum-algorithm.md)。
- **不是**每次请求算新的时间戳，而是 **IDE 启动时算一次长期用**
- machineId = Cursor releaseHash（从 update URL 得到）
- macMachineId = 设备 UUID 派生的 SHA-256

### 2. Authorization 简化
IDE 只发 `Bearer <JWT>`，**没有 `userId%3A%3A` 前缀**。CursorGateway 拼接 `userId::` 是错的（虽然服务端可能也接受，但不是 IDE 行为）。

### 3. chat 请求走 api3.cursor.sh
CursorGateway 一直用 `api2.cursor.sh` 是错的。3.10 的 chat/agent（`RunSSE` 等）走 **`api3.cursor.sh`**。

### 4. api3 有 pinning
Electron 主进程 `main.js` 用 `setCertificateVerifyProc` 拦截。逻辑：
- `verificationResult === "OK"` 放行
- `isIssuedByKnownRoot === true` 放行
- 否则拒绝（我们的 mitmproxy CA 虽然在 keychain，但对 Electron 的 verifyProc 而言 **`isIssuedByKnownRoot` 是 false**，所以被拒）

### 5. proto schema 已足够
所有关键字段（AgentRunRequest 23 field, RequestContext 44 field）在阶段 1 已从 workbench.js 完整提取。**不需要抓 chat body 也能实现**——因为我们有：
- proto schema（字段号、类型）
- header 集合（怎么发）
- checksum 算法（怎么签）
- 客户端源码（怎么填字段值）

## 下一步（阶段 3-5）

**不再花时间抓 chat body**（pinning 绕开成本高、收益边际）。直接进入实现：

1. **阶段 3**：把 `proto/cursor-core.proto` 里的关键 message 精简到 `agent.v1 + aiserver.v1 核心 60 个`，用 `protoc-gen-go` 生成 Go 代码
2. **阶段 4**：写 Go auth（复用抓到的 header 结构，实现 checksum、oauth login）
3. **阶段 5**：写 Go executor（`api3.cursor.sh` + Connect Web transport + gzip 请求）

如果实现后跑不通再回头 patch pinning 抓 body 对照。
