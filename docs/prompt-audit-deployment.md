# Prompt Audit Deployment Guide

这份说明对应当前仓库的 `prompt-audit` 分支，用来回答三个最常见的问题：

1. 现在这套到底怎么部署
2. sidecar 和主服务是怎么连起来的
3. 后续同步上游时，哪些地方需要人工判断

## 先看部署关系

这套方案不是 `sub2api-audit` 主动调用 `sub2api`。

真实链路是：

```text
client
  -> sub2api(prompt audit bridge)
  -> Redis Stream(prompt_audit:sampled)
  -> sub2api-audit
  -> PostgreSQL(prompt_audit_logs)
```

`admin api key` 只用于保护 `sub2api-audit` 自己的管理接口，比如：

- `GET /api/v1/audit/logs`
- `GET /api/v1/audit/stats`
- `GET /api/v1/audit/config`
- `PUT /api/v1/audit/config`

sidecar 有两种管理员密钥来源：

1. 推荐：和 `sub2api` 共用同一个 PostgreSQL，sidecar 直接读取 `settings.admin_api_key`
2. 备选：显式设置 `SUB2API_AUDIT_ADMIN_AUTH_ADMIN_API_KEY`

## 部署方式怎么选

### 方式 1：联动源码栈

适合你现在这种场景：

- 已经 checkout 当前仓库
- 使用 `prompt-audit` 分支
- 想本地或自建机一键把 `sub2api + sidecar + postgres + redis` 一起拉起来

命令：

```bash
git checkout prompt-audit
cd sidecars/sub2api-audit
cp deploy/.env.linked.example deploy/.env.linked
```

至少改这几个值：

- `POSTGRES_PASSWORD`
- `ADMIN_PASSWORD` 或后续从日志里取自动生成密码
- `JWT_SECRET`
- `TOTP_ENCRYPTION_KEY`

启动：

```bash
make stack-up
```

这条命令会使用：

- `deploy/docker-compose.linked.yml`
- 本仓库根目录里的 `sub2api` Dockerfile
- `sidecars/sub2api-audit/Dockerfile`

也就是直接从当前源码构建，不依赖你先发布镜像。

### 方式 2：Bundle 镜像栈

适合后面把 `sub2api-audit` 独立开源后，对外给别人“一键部署”。

前提：

- 你已经发布 `sub2api-audit` 镜像
- 你也发布了带 bridge 的 `sub2api` 兼容镜像

命令：

```bash
cd sidecars/sub2api-audit
cp deploy/.env.bundle.example deploy/.env.bundle
```

重点修改：

- `SUB2API_IMAGE`
- `SUB2API_AUDIT_IMAGE`
- `POSTGRES_PASSWORD`

启动：

```bash
make bundle-up
```

注意：

- `SUB2API_IMAGE` 不能是官方原版上游镜像
- 它必须来自你维护的 `prompt-audit` 分支构建产物，或者等价的 bridge-enabled 镜像

否则 sidecar 虽然能启动，但收不到 `prompt_audit:sampled` 事件。

### 方式 3：只部署 sidecar

适合你已经有一套 bridge-enabled `sub2api` 在跑，只想额外接上审计能力。

这种情况下需要保证 sidecar 能访问：

- 和主服务一致的 Redis
- 和主服务一致的 PostgreSQL，或者显式配置自己的管理员密钥

启动方式：

```bash
cd sidecars/sub2api-audit/deploy
cp .env.example .env
docker compose -f docker-compose.standalone.yml up -d --build
```

这时 `sub2api-audit` 只负责自己，不会帮你启动主服务。

## 推荐默认路径

如果是你自己内部使用，推荐优先走联动源码栈：

```bash
git checkout prompt-audit
cd sidecars/sub2api-audit
make stack-up
```

原因很简单：

- 不需要先发镜像
- `sub2api` 和 `sub2api-audit` 保证来自同一份代码
- 本地改 bridge 或 sidecar 后直接可验证

如果是以后给 GitHub 用户用，推荐改成 bundle 镜像栈：

- 用户只需要 clone `sub2api-audit`
- 通过你发布的两个镜像直接启动
- 不要求用户理解你的 fork patch 细节

## 启动后怎么验收

### 1. 先看健康检查

主服务：

```bash
curl http://127.0.0.1:8080/health
```

sidecar：

```bash
curl http://127.0.0.1:8087/healthz
```

### 2. 确认主服务桥接开关打开

当前默认环境变量是：

```text
GATEWAY_PROMPT_AUDIT_ENABLED=true
GATEWAY_PROMPT_AUDIT_STREAM_NAME=prompt_audit:sampled
GATEWAY_PROMPT_AUDIT_SAMPLE_RATE_BASIS_POINTS=200
GATEWAY_PROMPT_AUDIT_MAX_BODY_BYTES=131072
```

如果你想强制全量留样，可以临时把：

```text
GATEWAY_PROMPT_AUDIT_SAMPLE_RATE_BASIS_POINTS=10000
```

这样更容易验证链路。

### 3. 发一条真实请求

只要请求经过已经接线的 gateway 路由，并且鉴权成功，bridge 就会在 handler 执行后尝试写 Redis Stream。

当前已经覆盖的入口以 [`docs/sidecar.md`](/Users/maomao/CascadeProjects/sub2api/docs/sidecar.md) 为准，其中包括：

- 常规 HTTP 请求
- 需要计入审计的 WebSocket 请求

### 4. 用管理员密钥访问 sidecar API

如果 sidecar 和主服务共用同一个 PostgreSQL，默认可以直接复用 `sub2api` 的管理员密钥：

```bash
curl -H 'x-api-key: <admin-api-key>' \
  'http://127.0.0.1:8087/api/v1/audit/logs?limit=20'
```

如果这里能查到数据，说明：

- 主服务 bridge 已经投递 Redis
- sidecar 已经完成消费和入库
- 管理接口鉴权也正常

## 当前主项目里哪些改动算侵入性修改

这条分支刻意把侵入范围压缩在主项目少数文件里，后续同步 upstream 时重点看：

- [`docs/prompt-audit-merge-notes.md`](/Users/maomao/CascadeProjects/sub2api/docs/prompt-audit-merge-notes.md)

最值得关注的是：

- `backend/internal/server/routes/gateway.go`
- `backend/internal/server/router.go`
- `backend/internal/handler/prompt_audit_bridge.go`
- `backend/internal/config/config.go`
- `backend/internal/service/ops_service.go`

sidecar 目录本身：

- `sidecars/sub2api-audit/`

属于低耦合区域，通常不用拿去和 upstream 主线逐行比。

## 最小验证命令

主项目：

```bash
cd backend
go test ./internal/handler ./internal/server/routes ./internal/config ./internal/service
```

sidecar：

```bash
cd sidecars/sub2api-audit
go test ./...
docker compose --env-file deploy/.env.linked.example -f deploy/docker-compose.linked.yml config
docker compose --env-file deploy/.env.bundle.example -f deploy/docker-compose.bundle.yml config
```
