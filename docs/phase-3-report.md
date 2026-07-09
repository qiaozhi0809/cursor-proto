# 阶段 3 报告：Go proto 定义

## 产出

| 文件 | 大小 | 说明 |
|---|---|---|
| `proto/cursor.proto` | 164 KB | 单文件 proto3 定义，`package cursor`，含 `AgentV1` + `AiserverV1` 两个 wrapper message |
| `gen/cursor.pb.go` | 2.5 MB | 生成的 Go 代码 |
| `gen/go.mod` | - | 独立模块，仅依赖 `google.golang.org/protobuf` |

## 关键 Go 类型（对应 chat + agent 主流程）

- `cursor.AgentV1_AgentRunRequest` — 23 字段，chat/agent 请求主体（`api3/agent.v1.AgentService/RunSSE`）
- `cursor.AgentV1_AgentServerMessage` — SSE server → client（含 InteractionUpdate / ExecServerMessage / KV 三个 oneof）
- `cursor.AgentV1_ExecClientMessage` — BidiAppend 工具结果回传
- `cursor.AgentV1_RequestContext` — 44 字段
- `cursor.AiserverV1_AvailableModelsRequest` / `AvailableModelsResponse` — 模型列表
- `cursor.AiserverV1_ErrorDetails` — 服务端错误

Wrapper 结构：所有 message 都放在 `AgentV1.` 或 `AiserverV1.` 前缀下，避免跨命名空间循环导入。Go 类型名如 `AgentV1_AgentRunRequest`。

## 提取工具改进（相对 CursorGateway）

CursorGateway 时代靠人工看代码抄 proto。我们**全自动**：

1. `scripts/extract_schema.py` —— 从 workbench.js 抽出所有 `makeMessageType(...)` / `makeEnum(...)` 定义
   - 5064 个 message + 395 个 enum 全部提取
   - 支持 JS 字面量 `!0` / `!1` → true/false 转换
   - 支持 `()=>[...]` lazy 语法
   - 类引用名（`ASs` / `MLd` 等）自动解析到 typeName

2. `scripts/gen_proto.py` —— 从 schema JSON 生成可编译的 .proto
   - Core mode: 从核心 RPC 入口做闭包，出 823 message + 52 enum
   - Nested message 保留 Cursor 的原始层级
   - proto3 optional / repeated / oneof / map 全支持
   - 避开循环导入（合并到单个 `cursor` package）

## 命令一键重生成

```bash
cd cursor-proto

# 从新版 Cursor.app 提取 schema
python3 scripts/extract_schema.py > captures/schema-3.10.20.raw.json

# 生成 proto
python3 scripts/gen_proto.py --mode core

# 编译成 Go
protoc --proto_path=proto --go_out=gen --go_opt=paths=source_relative proto/cursor.proto

# 编译验证
cd gen && go build ./...
```

## 下一步

阶段 4：写 auth 模块（`sdk/auth/cursor.go`）
- OAuth 登录流（loginDeepControl challenge/uuid）
- accessToken JSON 存储 + 刷新
- machineId / macMachineId 生成
- `x-cursor-checksum` 头生成（参考 [checksum-algorithm.md](checksum-algorithm.md)）
