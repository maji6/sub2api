# Prompt Audit Merge Notes

这份说明只服务于 `prompt-audit` 分支后续和上游同步时的判断。

## 当前落地方式

- sidecar 代码整包并入当前仓库：
  - `sidecars/sub2api-audit/`
- 主服务只保留最小 bridge patch
- 目标是把“侵入上游的改动”控制在少数几个主文件里

## 以后和上游 merge 时，优先关注这些主项目文件

这些文件属于真正会影响上游同步的“侵入修改”：

- `backend/internal/handler/prompt_audit_bridge.go`
- `backend/internal/handler/prompt_audit_bridge_test.go`
- `backend/internal/server/routes/gateway.go`
- `backend/internal/server/routes/gateway_test.go`
- `backend/internal/server/router.go`
- `backend/internal/config/config.go`
- `backend/internal/config/config_test.go`
- `backend/internal/service/ops_service.go`
- `backend/internal/service/ops_service_prepare_queue_test.go`
- `deploy/config.example.yaml`

## 这些文件为什么要重点看

### 1. 网关接线

最容易和上游冲突的是：

- `backend/internal/server/routes/gateway.go`
- `backend/internal/server/router.go`

判断方式：

- 上游如果改了中间件链顺序、新增了 gateway 入口、改了 `RegisterGatewayRoutes` 签名
- 就需要重新确认 `PromptAuditBridgeMiddleware(...)` 还挂在正确的位置

### 2. handler 私有 key 耦合

`backend/internal/handler/prompt_audit_bridge.go` 放在 `handler` 包内，是因为它直接读取：

- `opsModelKey`
- `opsStreamKey`
- `opsRequestBodyKey`

如果上游改了这些私有 key 名称，通常会直接编译报错。
这是预期行为，修起来也最直接。

### 3. 配置面

如果上游动了配置结构或校验逻辑，要重点看：

- `backend/internal/config/config.go`
- `backend/internal/config/config_test.go`
- `deploy/config.example.yaml`

当前 prompt audit 相关配置键是：

```yaml
gateway:
  prompt_audit:
    enabled: false
    stream_name: "prompt_audit:sampled"
    sample_rate_basis_points: 200
    max_body_bytes: 131072
```

### 4. 脱敏/截断逻辑复用

为了避免 bridge 自己复制一套清洗逻辑，当前复用了：

- `backend/internal/service/ops_service.go`

新增的是：

- `PrepareOpsRequestBodyForQueueWithLimit`

如果上游以后重构这块逻辑，优先保证 bridge 继续复用同一套脱敏/截断能力，而不是分叉出第二套实现。

## sidecar 目录怎么处理

`sidecars/sub2api-audit/` 本身是低耦合区域。

一般和上游同步时：

- 不需要拿它去和上游逐行比对
- 只需要保证它自己的 linked compose 构建路径仍然指向当前仓库
- 以及它依赖的 Redis Stream 契约没有失配

## 一个简单的 merge 判断原则

可以按这个顺序判断：

1. 先 merge / rebase 上游到 `main`
2. 再让 `prompt-audit` 跟上新的 `main`
3. 优先检查上面那 10 个主项目文件
4. 主项目 patch 通过编译和测试后，再检查 `sidecars/sub2api-audit/` 是否需要跟着调

## 建议的最小验证

主项目：

```bash
cd backend
go test ./internal/handler ./internal/server/routes ./internal/config
```

sidecar：

```bash
cd sidecars/sub2api-audit
go test ./...
docker compose --env-file deploy/.env.linked.example -f deploy/docker-compose.linked.yml config
```
