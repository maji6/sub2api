# sub2api-audit

`sub2api-audit` 是给 `sub2api` 配套的独立 sidecar 项目。

当前在 `prompt-audit` 分支里，这份 sidecar 代码被直接并入当前仓库，位置是：

```text
sidecars/sub2api-audit
```

这样做主要是为了内部维护和部署省事：

- 主服务 bridge patch 仍然保留在仓库主代码路径中
- sidecar 本身尽量整包放在独立目录里，不打散到主服务目录
- 以后同步上游时，主服务只需要重点关注 bridge patch 那几处改动

和后续 merge 相关的说明见：

- [`docs/merge-notes.md`](docs/merge-notes.md)

如果你把它作为独立开源项目发布，建议把它理解成一个“可插拔生态项目”：

- `sub2api-audit` 负责消费、分析、落库、管理接口
- `sub2api` 负责把脱敏后的 prompt audit 事件投递到 Redis Stream
- 如果上游官方 `sub2api` 还没有合并 prompt audit bridge，用户需要使用你维护的兼容镜像 / fork / patch build

当前这版先把 V1 骨架搭起来了：

- 从 Redis Stream `prompt_audit:sampled` 读取 bridge 投递的审计事件
- 解析并提取摘要、tool 列表、base64 附件摘要、风险标签
- 写入 PostgreSQL `prompt_audit_logs`
- 提供管理员 API：
  - `GET /api/v1/audit/logs`
  - `GET /api/v1/audit/stats`
  - `GET /api/v1/audit/config`
  - `PUT /api/v1/audit/config`
- 复用 `sub2api` 的管理员 key 约定：
  - 请求头 `x-api-key`
  - settings key `admin_api_key`

## 目录

```text
sub2api-audit/
├── cmd/sub2api-audit
├── internal/app
├── internal/config
├── internal/http
├── internal/consumer
├── internal/repository
├── internal/audit
├── internal/migrate
├── migrations
└── deploy/config.example.yaml
```

## 运行

1. 复制配置：

```bash
cp deploy/config.example.yaml config.yaml
```

2. 填好 PostgreSQL 和 Redis。

建议直接连 `sub2api` 正在使用的 PostgreSQL，这样可以直接读取 `settings.admin_api_key`。

3. 启动：

```bash
go run ./cmd/sub2api-audit
```

4. 访问：

- 健康检查：`GET /healthz`
- 管理接口：带 `x-api-key: <admin-api-key>`

## 配置说明

- `admin_auth.admin_api_key`
  - 可选
  - 为空时从 `settings.admin_api_key` 读取
- `audit.keyword_rules`
  - 默认关键字规则
  - 运行时可通过 `PUT /api/v1/audit/config` 覆盖
- `audit.consumer_enabled`
  - 只想先跑 API，不想消费 Redis 时可设为 `false`

## Docker Compose

已补好的部署文件：

- [`deploy/docker-compose.standalone.yml`](deploy/docker-compose.standalone.yml)
- [`Dockerfile`](Dockerfile)
- [`deploy/.env.example`](deploy/.env.example)
- [`deploy/docker-compose.bundle.yml`](deploy/docker-compose.bundle.yml)
- [`deploy/.env.bundle.example`](deploy/.env.bundle.example)
- [`deploy/docker-compose.linked.yml`](deploy/docker-compose.linked.yml)
- [`deploy/.env.linked.example`](deploy/.env.linked.example)

使用方式：

```bash
cd deploy
cp .env.example .env
docker compose -f docker-compose.standalone.yml up -d --build
```

联动一键版：

```bash
make stack-up
```

首次执行时，如果 `deploy/.env.linked` 还不存在，`make stack-up` 会自动按 `deploy/.env.linked.example` 生成一份默认配置。

Bundle 一键版（面向 GitHub 用户 / 独立发行）：

```bash
make bundle-up
```

首次执行时，如果 `deploy/.env.bundle` 还不存在，`make bundle-up` 会自动按 `deploy/.env.bundle.example` 生成一份默认配置。

注意先把下面两个镜像地址改成你实际发布的镜像：

- `SUB2API_IMAGE`
- `SUB2API_AUDIT_IMAGE`

其中 `SUB2API_IMAGE` 推荐来自你维护的 `sub2api` fork 的 `prompt-audit` 分支构建产物，而不是直接使用上游官方镜像。

常用命令：

```bash
make bundle-ps
make bundle-logs
make bundle-down
make bundle-pull
make stack-ps
make stack-logs
make stack-down
```

### 它和 sub2api 是怎么协作的

sidecar 可以独立部署，但它不是通过 admin 密钥去“连接 sub2api 服务”。

- 核心数据链路靠的是：
  - 直接连 Redis，消费 `prompt_audit:sampled`
  - 直接连 PostgreSQL，写 `prompt_audit_logs`
- `admin api key` 只用于保护 sidecar 自己的管理接口
  - 比如 `GET /api/v1/audit/logs`
  - 比如 `PUT /api/v1/audit/config`

如果你希望 sidecar 自动复用和 sub2api 完全相同的管理员密钥，有两种方式：

1. 推荐：让 sidecar 连接和 sub2api 相同的 PostgreSQL
   - sidecar 会直接读 `settings.admin_api_key`
2. 独立数据库时：显式设置 `SUB2API_AUDIT_ADMIN_AUTH_ADMIN_API_KEY`
   - 这样 sidecar 不依赖主项目的 `settings` 表也能工作

### 联动 compose 推荐怎么用

如果你想要“接近一键”的体验，推荐直接用联动 compose：

- `postgres`
- `redis`
- `sub2api`
- `sub2api-audit`

它会默认把 `sub2api` 的 prompt audit bridge 开关打开，所以 stack 起起来后 sidecar 就能直接消费同一个 Redis Stream。

### 独立开源时推荐怎么发

如果你后续又想把 sidecar 单独拆出去开源，推荐你这样发：

1. `sub2api-audit` 仓库独立发版、独立发 Docker 镜像
2. 额外维护一个 bridge-enabled 的 `sub2api` fork，并在 `prompt-audit` 分支发布兼容镜像
3. 让用户只 clone `sub2api-audit` 仓库，通过 bundle compose 起整套

也就是说，面对普通用户时，推荐入口不是“先 clone 两个仓库”，而是：

```bash
git clone <your-sub2api-audit-repo>
cd sub2api-audit
make bundle-up
```

这条路径依赖的是“你发布的镜像”，而不是“上游主仓库已经合并你的功能”。

更完整的发行建议见：

- [`docs/distribution.md`](docs/distribution.md)

## 当前实现取舍

- 运行时配置先存进 `settings` 表，key 为 `sidecar_prompt_audit_config`
- 管理员鉴权先只做 `x-api-key`，没有把主项目的 JWT admin auth 一起搬过来
- consumer 目前是单 worker、顺序消费、成功后 ACK
- `prompt_audit_logs` 由 sidecar 自己迁移管理，不改主项目 migrations
