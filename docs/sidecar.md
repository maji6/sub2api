# Prompt Audit Sidecar 设计文档

## 当前分支落地形态

当前 `prompt-audit` 分支采用的是“主服务最小 patch + sidecar 整包并入”的方式：

- 主服务 bridge patch 仍在原有主代码路径内
- sidecar 代码整包放在 `sidecars/sub2api-audit/`

这样做的目的有两个：

1. 内部部署时只维护一个仓库
2. 和上游同步时，把真正需要人工判断的侵入修改控制在少数几个主文件里

后续 merge 时的重点判断清单见：

- [`docs/prompt-audit-merge-notes.md`](prompt-audit-merge-notes.md)

如果你现在最关心的是“这套东西怎么部署、怎么联动、怎么验收”，优先看：

- [`docs/prompt-audit-deployment.md`](prompt-audit-deployment.md)

## 总体架构：Middleware Bridge + 独立 Sidecar

```
┌──────────────────────────────────────────────────────────┐
│  sub2api (原项目)                                         │
│                                                          │
│  每条 gateway 路由链的中间件顺序:                           │
│    bodyLimit → clientRequestID → opsErrorLogger           │
│    → ★PromptAuditBridge★ → endpointNorm                  │
│    → apiKeyAuth → requireGroup → handler                  │
│                                                          │
│  Bridge 工作方式 (与 OpsErrorLoggerMiddleware 完全相同):    │
│    1. 调用 c.Next()，等待后续全链路执行完毕                  │
│    2. c.Next() 返回后，从 gin.Context 读取 handler 写入的   │
│       ops_model / ops_request_body / ContextKeyAPIKey 等   │
│    3. 做采样决策                                           │
│    4. 命中则脱敏+截断后 XADD 到 Redis Stream               │
│                    │                                      │
│                    │  XADD (sampled + sanitized)           │
│                    ▼                                      │
│              Redis Stream                                 │
│           "prompt_audit:sampled"                          │
└──────────────────────────────────────────────────────────┘
                     │
                     │ XREADGROUP (consumer group)
                     ▼
┌──────────────────────────────────────────────────────────┐
│  sub2api-audit (独立项目/独立二进制)                        │
│                                                          │
│  ┌─ Consumer ──────────────────────────────┐              │
│  │ • 从 Redis Stream 消费                  │              │
│  │ • 提取 AuditEnvelope 摘要               │              │
│  │ • 风险标签检测 (关键词匹配)              │              │
│  │ • 写入 prompt_audit_logs 表             │              │
│  └─────────────────────────────────────────┘              │
│                                                          │
│  ┌─ Admin API ─────────────────────────────┐              │
│  │ • 用 sub2api 的管理员密钥做鉴权          │              │
│  │ • GET  /api/v1/audit/logs               │              │
│  │ • GET  /api/v1/audit/stats              │              │
│  │ • PUT  /api/v1/audit/config             │              │
│  └─────────────────────────────────────────┘              │
│                                                          │
│  ┌─ Web UI (V2) ──────────────────────────┐               │
│  │ • 按用户/组/模型/时间查看               │               │
│  │ • 风险告警看板                          │               │
│  └─────────────────────────────────────────┘              │
└──────────────────────────────────────────────────────────┘
```

---

## 对 sub2api 的改动

### 改动 1：新增一个中间件文件

`backend/internal/handler/prompt_audit_bridge.go`，放在 handler 包内。

**为什么放 handler 包**：bridge 需要读取 `opsModelKey` / `opsStreamKey` / `opsRequestBodyKey`
等常量，这些是 `ops_error_logger.go` 中的未导出常量。放在同一个包内是编译期可见的耦合——
上游改了 key 名，bridge 编译直接报错，比隐式约定更安全。

**中间件工作流程**：

```
c.Next()  ──────────────────────────────────────────────────
  │  (apiKeyAuth / handler 在这期间执行)                      │
  ◄──────────────────────────────────────────────────────────┘
  │
  ├─ 读 gin.Context: ops_request_body → 请求体 (handler 设置)
  ├─ 读 gin.Context: ops_model         → 模型   (handler 设置)
  ├─ 读 gin.Context: ops_stream        → 流式   (handler 设置)
  ├─ 读 gin.Context: ContextKeyAPIKey  → api_key_id, group_id (apiKeyAuth 设置)
  ├─ 读 gin.Context: ContextKeyUser    → user_id (apiKeyAuth 设置)
  ├─ 读 request.Context(): ctxkey.ClientRequestID → 请求追踪 ID
  ├─ 读 c.Request.URL.Path             → endpoint
  │
  ├─ 缺少 ops_model 或 ContextKeyAPIKey？ → 跳过 (鉴权失败等)
  ├─ 判断 transport: POST → "http"; WebSocket upgrade → "ws"
  │
  ├─ 采样决策: hash(client_request_id) % 10000 < sample_rate_bp
  │           OR 强制留样条件命中
  │
  ├─ 命中 → 脱敏 + 截断 (bridge 侧，发 Redis 之前)
  └─ XADD prompt_audit:sampled {sanitized payload}
```

**降级策略**：鉴权失败的请求不会有 `ContextKeyAPIKey` 和 `ops_model`，
bridge 检测到缺失字段直接 return，只审计鉴权成功且进入 handler 的请求。

### 改动 2：注册中间件（多点接线，单文件改动）

`backend/internal/server/routes/gateway.go` 中需要接入 bridge 的位置
(共 ~8 处，全在同一个文件内):

| # | 路由 | 类型 | 接入方式 | 说明 |
|---|------|------|----------|------|
| 1 | `/v1` | group (line 35) | `gateway.Use(bridge)` | 包含 POST 和 GET /responses (WS) |
| 2 | `/v1beta` | group (line 94) | `gemini.Use(bridge)` | |
| 3 | `/responses` POST | standalone (line 116) | 插入内联链 | |
| 4 | `/responses/*subpath` POST | standalone (line 117) | 插入内联链 | |
| 5 | `/responses` GET (WS) | standalone (line 118) | 插入内联链 | WS 首条消息审计 |
| 6 | `/chat/completions` POST | standalone (line 120) | 插入内联链 | |
| 7 | `/antigravity/v1` | group (line 132) | `antigravityV1.Use(bridge)` | |
| 8 | `/antigravity/v1beta` | group (line 147) | `antigravityV1Beta.Use(bridge)` | |

`RegisterGatewayRoutes` 函数签名需要增加 `redisClient *redis.Client` 参数：
- `backend/internal/server/router.go` 的调用处传入 (已有 `redisClient` 参数可透传)
- `backend/internal/server/routes/gateway_test.go` 的测试调用也需同步修改

**总结：主项目新增 1 个 bridge 中间件文件，接入到所有网关入口的中间件链。
改动小但不是单点——约 1 个新文件 + gateway.go 内 ~8 处插入 + router.go 传参
+ gateway_test.go 测试适配。**

### 数据字段与来源（修正版）

| 数据 | 来源 | 读取方式 |
|------|------|----------|
| 请求体 | handler 的 `setOpsRequestContext()` | `c.Get(opsRequestBodyKey)` → `[]byte` |
| 模型 | handler 的 `setOpsRequestContext()` | `c.Get(opsModelKey)` → `string` |
| 流式 | handler 的 `setOpsRequestContext()` | `c.Get(opsStreamKey)` → `bool` |
| API Key 信息 | apiKeyAuth 中间件 | `middleware.GetAPIKeyFromContext(c)` → api_key_id, group_id |
| 用户信息 | apiKeyAuth 中间件 | `middleware.GetAuthSubjectFromContext(c)` → user_id |
| 请求追踪 ID | ClientRequestID 中间件 | `c.Request.Context().Value(ctxkey.ClientRequestID)` |
| 请求路径 | Gin 原生 | `c.Request.URL.Path` |

**注意**：`ClientRequestID` 存在 `request.Context()` 中 (via `ctxkey.ClientRequestID`)，
不是 HTTP header。`RequestID` 由 `RequestLogger` 中间件生成/透传
（优先使用入站 `X-Request-ID` header，没有时才生成 UUID）。两者不要混用：
- `client_request_id`: 用于采样决策的 hash 种子 + 跨服务追踪
- `request_id`: 用于服务端日志关联

---

## 安全边界：Bridge 侧先脱敏，再发 Redis

遵循现有 Ops 模式 (`PrepareOpsRequestBodyForQueue`)，**bridge 在 XADD 之前** 完成：

1. **凭据脱敏**：正则替换 `sk-*`, `AKIA*`, `ghp_*`, `-----BEGIN`, bearer token 等
2. **截断**：超过 128KB 的 body 截断，标记 `truncated: true`
3. **base64 摘要化**：检测到 base64 块时替换为 `[base64: ~{size}bytes]`，不传原文

Redis Stream 中存储的是**已脱敏+已截断**的数据。即使 Redis 被运维/备份/监控接触到，
也不会直接暴露原始 prompt 中的凭据或大附件。

Sidecar 负责**进一步分析**（风险标签、摘要提取），不负责第一道数据降敏。

---

## Redis Stream 消息格式

```json
{
  "client_request_id": "550e8400-e29b-41d4-a716-446655440000",
  "request_id": "req_abc123",
  "timestamp": "2026-04-10T03:40:00Z",
  "user_id": 42,
  "api_key_id": 7,
  "group_id": 3,
  "endpoint": "/v1/messages",
  "model": "claude-sonnet-4-20250514",
  "stream": true,
  "transport": "http",
  "ws_turn": 0,
  "sample_reason": "random",
  "body_bytes": 15234,
  "body_truncated": false,
  "body": "{\"model\":\"claude-sonnet-4-20250514\",\"messages\":[...sanitized...]}"
}
```

- `body`: 已脱敏+已截断的 JSON 字符串，最大 128KB
- `body_bytes`: 原始 body 大小（截断前）
- `transport`: `http` | `ws`
- `ws_turn`: WebSocket 轮次 (HTTP 请求为 0；V1 WS 首条消息为 1；V2 逐 turn 递增)
- `sample_reason`: `random` | `forced_tools` | `forced_base64` | `forced_long_prompt`

---

## 独立 sidecar 项目（sub2api-audit）

### 鉴权：复用管理员密钥

admin_auth.go 中，管理员密钥存在 `settings` 表里，通过 `settingService.GetAdminAPIKey()` 读取，
验证使用 `crypto/subtle.ConstantTimeCompare`。

sidecar 鉴权方案：
- **方式 A（推荐）**：sidecar 连同一个 PostgreSQL，直接从 `settings` 表读 admin API key，
  做 constant-time compare
- **方式 B**：sidecar 启动时通过环境变量 `SUB2API_ADMIN_KEY` 配置，
  部署时和 sub2api 保持一致

访问审计 API 时用同一个 `x-api-key` header 即可，体验一致。

### sidecar 数据处理流

```
Redis Stream → Consumer → AuditEnvelope 提取 → 风险标签 → 入库
                               │
                               ├─ system_text (截断到 2KB)
                               ├─ conversation_excerpt (最近 3 轮)
                               ├─ tool_summary (工具名列表)
                               ├─ attachment_summary (图片数/base64 摘要数/原始大小)
                               └─ risk_tags ["credential", "tool_risk", ...]
```

### sidecar 自己管表

`prompt_audit_logs` 表由 sidecar 自己的迁移管理，**不动 sub2api 的 migrations 目录**。
这是"生态项目"最关键的隔离点。

---

## 耦合点与上游同步成本

### 显式耦合点清单

| 耦合点 | 位置 | 稳定性评估 |
|--------|------|-----------|
| `opsModelKey` / `opsStreamKey` / `opsRequestBodyKey` | handler 包内私有常量 | bridge 在同包内，编译期检查；上游改名会立即报错 |
| `middleware.GetAPIKeyFromContext` | middleware 包导出函数 | 公共 API，稳定性高 |
| `middleware.GetAuthSubjectFromContext` | middleware 包导出函数 | 公共 API，稳定性高 |
| `ctxkey.ClientRequestID` | ctxkey 包导出常量 | 公共 API，稳定性高 |
| `RegisterGatewayRoutes` 函数签名 | routes 包 | 上游可能变更，merge 时需注意 |

### 同步成本

| 场景 | 影响 |
|------|------|
| 上游更新 handler 逻辑 | **无影响** — bridge 只读 context |
| 上游改了 ops_* key 名 | **编译报错** — 因为 bridge 在同一个 handler 包内，修复即改名 |
| 上游改了 gateway.go 路由注册 | **需手动 merge** — 在新/变更的路由链中重新加 bridge |
| 上游新增网关入口 | **需检查** — 如果走已有 group 则自动覆盖；如果是新 standalone 路由则需手动加 |
| 上游改了 RegisterGatewayRoutes 签名 | **需适配** — 调整传参 |

**最坏情况**：上游大改 gateway.go 后，需要在该文件中重新接入 ~8 处 bridge 注册。

---

## WebSocket 审计策略

### V1：首条消息审计（Bridge 中间件覆盖）

WS 连接也走 gateway 中间件链。WS handler (`ResponsesWebSocket`) 在握手后
读取首条消息并调用 `setOpsRequestContext(c, reqModel, true, firstMessage)`
（见 `backend/internal/handler/openai_gateway_handler.go:1106`）。

**时序注意**：与 HTTP 请求不同，WS 的 `c.Next()` 会阻塞到整个 WebSocket 会话结束
（`ProxyResponsesWebSocketFromClient(...)` 返回后），bridge 才执行 XADD。
即：首条消息在**连接建立阶段被捕获**，但审计事件在**会话结束时才落地**。

这意味着：
- 长连接期间审计不会实时出现，存在延迟
- 异常断开（进程崩溃、网络中断）时，`c.Next()` 可能不正常返回，
  该次审计可能丢失
- V1 接受这个限制；V2 逐 turn 审计可解决实时性问题

`c.Next()` 返回后，gin.Context 中已有：
- `ops_request_body` → 首条 `response.create` 消息 (含 model、input 等)
- `ops_model` → 模型名
- `ContextKeyAPIKey` / `ContextKeyUser` → 用户鉴权信息

Bridge 的 WS 检测复用现有代码模式（同时检查 `Upgrade` 和 `Connection` header，
与 `admin_auth.go:isWebSocketUpgradeRequest` 和
`openai_gateway_handler.go:isOpenAIWSUpgradeRequest` 保持一致）：
```go
func isWebSocketUpgrade(c *gin.Context) bool {
    if !strings.EqualFold(strings.TrimSpace(c.GetHeader("Upgrade")), "websocket") {
        return false
    }
    return strings.Contains(strings.ToLower(c.GetHeader("Connection")), "upgrade")
}
```
WS 请求标记 `transport: "ws"`, `ws_turn: 1`。

**采样策略**：WS 采样以 `client_request_id` 做"会话级采样"——
同一条 WS 连接的采样决策在连接建立时就确定。

### V2：逐 turn 审计（需扩展 AfterTurn hook）

当前 `OpenAIWSIngressHooks.AfterTurn` 回调签名：
```go
AfterTurn func(turn int, result *OpenAIForwardResult, turnErr error)
```

`AfterTurn` 拿到 `OpenAIForwardResult`（model / usage / requestID），
但**不包含客户端消息体**——每轮 turn 的消息在 forwarder 内部循环中读取，
外部 hook 无法获取。

V2 需要扩展 hook 签名或新增 `OnClientMessage` 回调，将 per-turn 消息体
传出给审计逻辑。这会增加对 service 包的侵入（改 `OpenAIWSIngressHooks` 结构体），
但相比于在 forwarder 内部直接发 Redis，仍然是更干净的边界。

**payload 语义约定**：forwarder 在 turn 内会修改 `currentPayload`——
自动补/删 `previous_response_id`、重建 full input、做 strict recovery 等
（见 `openai_ws_forwarder.go:3133, 3188, 3393`）。
因此 hook 需要区分两种 payload：
- `originalClientPayload`: 用户实际发送的原始消息，审计默认记录这个
- `effectivePayloadHash`: forwarder 改写后实际转发的 payload 的 SHA-256 hash，
  仅作调试字段，不存原文

**V2 hook 设计草案**：
```go
type OpenAIWSIngressHooks struct {
    BeforeTurn func(turn int) error
    AfterTurn  func(turn int, result *OpenAIForwardResult, audit *TurnAuditData, turnErr error)
}

// TurnAuditData 每轮 turn 的审计数据
type TurnAuditData struct {
    OriginalClientPayload []byte // 用户原始消息（脱敏前，由共享的 audit sanitizer 负责脱敏）
    EffectivePayloadHash  string // forwarder 改写后转发的 payload 的 SHA-256
}
```

在 handler 的 `AfterTurn` 回调中注入审计逻辑（与 usage 记录并行），
每轮 turn 独立 XADD 到 Redis Stream。

**失败 turn 处理约定**：
- `turnErr != nil` 时，**仍然写入一条审计事件**，前提是 `audit != nil`
  且 `OriginalClientPayload` 可用
- 失败 turn 的审计事件仍记录：
  - `transport: "ws"`
  - `ws_turn: <turn>`
  - `body`: 由共享 `audit sanitizer` 脱敏+截断后的 `OriginalClientPayload`
  - `effective_payload_hash`
- 失败 turn 的 `request_id` / usage / upstream model 等字段允许为空，因为失败路径下
  `result` 可能为 `nil`
- 若失败发生在获取客户端消息体之前，导致 `audit == nil`，则**不写 prompt 审计事件**，
  只依赖现有 usage / ops 错误日志链路

**V2 采样规则**：
- 默认以 `client_request_id` 做会话级采样（整段保留或整段跳过）
- 如果某轮 turn 命中强制留样条件（tools / base64 / 超长输入），允许单轮补采

---

## V1 范围（MVP）

### 做什么
- 采样 POST 请求（/messages, /responses, /chat/completions 等）
- 采样 WebSocket 连接的首条消息（`response.create`），标记 `transport: "ws"`
- 2% 基础随机采样 + 风险特征强制留样 (tools / base64 / 长 prompt)
- Bridge 侧脱敏+截断后发 Redis Stream
- Sidecar 消费、提取摘要、打标签、入库
- Admin API: 按用户/组/模型/时间/transport 查询

### 不做什么
- WebSocket 逐 turn 审计 — 留到 V2 (需扩展 AfterTurn hook)
- 大附件原文保留 — V1 只存摘要化后的 body
- Web UI 看板 — V1 只提供 API
- 阻断/拦截 — V1 只观测，不阻断
- 重点人群加权采样 — V1 只做统一采样率 + 风险强制留样

---

## 实施计划

| 步骤 | 位置 | 内容 | 说明 |
|------|------|------|------|
| **1** | sub2api | `backend/internal/handler/prompt_audit_bridge.go` — 中间件文件 | ~120 行，含脱敏/采样/WS检测/XADD |
| **2** | sub2api | `backend/internal/server/routes/gateway.go` 接入 bridge | ~8 处插入 |
| **2b** | sub2api | `backend/internal/server/router.go` 传参 + `routes/gateway_test.go` 适配 | 签名调整 + 测试修复 |
| **3** | sub2api | 端到端测试：验证 Redis Stream 正常推送 | 用 redis-cli XREAD 手动验证 |
| **4** | sub2api-audit | 新项目骨架: go mod, config, main | 独立仓库 |
| **5** | sub2api-audit | Redis Stream consumer + PostgreSQL 存储 | ~250 行 |
| **6** | sub2api-audit | AuditEnvelope 提取 + 风险标签 (关键词匹配) | ~200 行 |
| **7** | sub2api-audit | Admin API (查询/统计/配置) + 管理员密钥鉴权 | ~200 行 |
| **V2** | sub2api | 扩展 AfterTurn hook 传递 `*TurnAuditData` (original + hash) | service 包改动 |
| **V2** | sub2api | handler AfterTurn 回调中注入逐 turn 审计 | handler 包改动 |
| **V2** | sub2api-audit | 逐 turn 消费、Web UI、加权采样、告警 | 后续迭代 |

步骤 1-3 是对 sub2api 的全部改动，步骤 4-7 全部在独立项目里。
