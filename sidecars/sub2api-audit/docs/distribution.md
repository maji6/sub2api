# sub2api-audit 发行建议

## 项目定位

`sub2api-audit` 适合被当成一个独立生态项目，而不是 `sub2api` 主仓库里的内建功能。

职责边界建议保持成这样：

- `sub2api`：采样、脱敏、截断、投递 Redis Stream
- `sub2api-audit`：消费、摘要提取、风险标签、落库、管理 API

这样做的好处是：

- 主项目只需要一个很薄的 bridge 改动
- sidecar 可以独立发版
- 用户可以按需接入，不要求上游主仓库强绑定

## 一个现实前提

如果上游官方 `sub2api` 镜像还没有合并 prompt audit bridge，那么：

- 官方原版 `sub2api` 可以正常跑
- `sub2api-audit` 也可以正常启动
- 但 sidecar 不会收到 `prompt_audit:sampled` 审计事件

所以想让用户真正用起来，至少要满足下面三种方式之一：

1. 上游主仓库已经合并 bridge
2. 你维护一个 bridge-enabled 的 `sub2api` fork / patch branch
3. 你发布一个 bridge-enabled 的 `sub2api` 兼容镜像

## 推荐发行模型

推荐把发布物拆成两类：

### 1. sidecar 镜像

例如：

```text
ghcr.io/<your-org>/sub2api-audit:v0.1.0
```

这个镜像由 `sub2api-audit` 仓库自己发布。

### 2. 兼容版 sub2api 镜像

例如：

```text
ghcr.io/<your-org>/sub2api-prompt-audit:v0.1.0
```

这个镜像可以来自：

- 你的 `sub2api` fork
- 你的 patch branch
- 你维护的二进制/容器再打包流程

关键点不在于仓库形式，而在于它必须带有 prompt audit bridge。

## 推荐分支结构

如果你自己维护 `sub2api` fork，推荐用下面这种结构：

```text
upstream/main                # 官方主线
your-fork/main               # 跟随 upstream/main 同步
your-fork/prompt-audit       # 只保留最小 bridge patch
```

`prompt-audit` 分支里建议只保留这些改动：

- bridge middleware
- gateway 路由接线
- prompt audit 配置项与配置示例
- 相关测试
- 复用脱敏/截断逻辑所需的最小公共函数

不建议把下面这些内容长期放进 `sub2api` fork 的审计分支：

- sidecar 的 bundle compose
- sidecar 的发布说明
- sidecar 的一键部署命令

这些都更适合放在 `sub2api-audit` 仓库里维护。

## 面向用户的推荐入口

普通用户不应该先被要求 clone 两个仓库。

更顺的入口是：

1. clone `sub2api-audit`
2. 复制 `deploy/.env.bundle.example` 为 `deploy/.env.bundle`
3. 把 `SUB2API_IMAGE` 和 `SUB2API_AUDIT_IMAGE` 改成你发布的镜像
4. 运行 `make bundle-up`

这也是 `deploy/docker-compose.bundle.yml` 的设计目的：

- 不依赖主仓库 checkout
- 直接用公开镜像起 `sub2api` + `sub2api-audit` + `postgres` + `redis`
- 默认把主服务的 prompt audit bridge 环境变量打开

## 版本兼容建议

当前这版更适合“成对发布”：

- `sub2api-audit vX.Y.Z`
- `sub2api-prompt-audit vX.Y.Z`

推荐在 release note 或 README 里明确写出对应关系，而不是让用户自己猜。

至少要固定这几个兼容面：

- Redis Stream 名称：默认 `prompt_audit:sampled`
- Redis Stream 字段结构：当前 V1 契约
- PostgreSQL `settings.admin_api_key` 复用逻辑
- sidecar 的 `prompt_audit_logs` 表结构

在还没有显式 `schema_version` 字段之前，最好避免“sidecar 新版本自动兼容所有旧 bridge”这种承诺。

## 上游同步工作流

推荐把日常维护流程固定成下面几步：

1. `git fetch upstream`
2. 更新 `your-fork/main` 到最新 upstream
3. 让 `your-fork/prompt-audit` rebase 或 merge 到新的 `your-fork/main`
4. 跑最小 bridge patch 相关测试
5. 从 `prompt-audit` 分支发布新的兼容镜像
6. 在 `sub2api-audit` 仓库更新 bundle 默认镜像标签

这样做的好处是：

- 上游同步时冲突范围很小
- sidecar 项目与主项目部署解耦
- 对外只需要维护“镜像兼容关系”，不需要要求用户理解你的 patch 细节

## 主仓库侧建议

如果你想继续跟进上游主仓库更新，主仓库里的改动应尽量压缩到：

- 一个 bridge middleware 文件
- 网关路由接线
- 配置项与配置示例

不要把 sidecar 的一键部署、镜像发布、版本兼容表放进主仓库当默认路径。

这些内容更适合放在 `sub2api-audit` 仓库。

## 对外文案建议

对外可以这样描述：

`sub2api-audit` is an optional prompt-audit sidecar for `sub2api`.
It works with bridge-enabled `sub2api` builds and consumes sanitized audit
events from Redis Stream into PostgreSQL-backed audit APIs.

重点是“optional sidecar”和“bridge-enabled builds”，这样用户预期会更准确。
