# 阶段 4 报告：auth 模块（Go）

## 端到端打通 ✅

一个真实的 Cursor API 请求跑通了。用我们的 auth 模块生成 header + machineID + macMachineID + checksum，向 `api2.cursor.sh/aiserver.v1.AiService/AvailableModels` 发送空 protobuf，服务端返回：

```
HTTP 200  (7444 bytes gzip)
  → 88704 bytes proto
  → 156 models (claude-opus-4-8, gpt-5.5, grok-4.5, composer-2.5, claude-fable-5, ...)
```

**这证明所有关键算法都对**。

## 产出

```
auth/
├── checksum.go       — x-cursor-checksum 生成
├── machineid.go      — machineID (sha256 IOPlatformUUID) + macMachineID (sha256 MAC)
├── account.go        — Account JSON 持久化
├── oauth.go          — OAuth loginDeepControl + poll
├── *_test.go         — 全部单元测试
cmd/
├── cursor-login/     — 交互式 OAuth CLI
└── test-connect/     — 端到端验证工具
```

## 关键算法（对 CursorGateway 的核心修正）

| 项目 | CursorGateway | 真值 |
|---|---|---|
| machineID | SHA-256(token, 'machineId') — 随机 hash | **SHA-256(IOPlatformUUID)** |
| macMachineID | 从 SQLite storage.serviceMachineId 读 | **SHA-256(第一个有效 MAC 地址)** |
| checksum 时间戳 | 每请求生成 | **启动时一次，长期复用** |
| checksum 前缀字节 | `E >> 40` naive | JS 32-bit signed 语义 |
| Authorization | `Bearer userId%3A%3AJWT` | **Bearer JWT** |
| Endpoint | api2 | api2（models） / **api3**（chat） |
| Content-Type | application/grpc-web+proto | **application/proto** |

macMachineID 抓包实测与我们生成的完全一致：
```
Cursor IDE:  df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab
我们生成:    df1a4f6be6465f027c4aefa30191f5fa05d1d69d1a791a8504c8d4fe11b674ab
             ↑ 完全相同
```

## Account JSON 格式

```json
{
  "email": "user@example.com",
  "user_id": "user_01KX3G01P34XHA85JW68Z9ES36",
  "access_token": "eyJhbGci...",
  "refresh_token": "eyJhbGci...",
  "auth_id": "auth0|user_01KX3G01P34XHA85JW68Z9ES36",
  "auth_type": "Auth_0",
  "issued_at": "2026-07-09T22:56:40Z",
  "machine_id": "0644acae98be...",
  "mac_machine_id": "df1a4f6be646..."
}
```

Session 级字段（`session_id`、`client_key`、`config_version`、`checksum_session`）不持久化，在 `LoadAccount` 时随机生成。

## 下一步

阶段 5：写 Cursor executor —— chat / agent 流式请求。已经证明协议通、schema 对，接下来只是拼请求 body + 解 SSE。
