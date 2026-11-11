# infra/compose

本目录用于本地开发和集成联调所需的 `docker compose` 资产。

当前已落地：

- `docker-compose.yml`
  - `Postgres`
  - `Redis`
  - `MinIO`
  - `Qdrant`
- `.env.example`
  - 本地联调环境变量示例

当前命令入口：

- `make infra-up`
- `make infra-down`
- `make infra-ps`
- `make infra-logs`
- `make infra-check`

`make infra-check` 默认只校验本地文件、Compose 配置、服务声明和环境变量模板，不要求容器已经启动。

如果已经执行过 `make infra-up`，可运行：

```bash
RUNNING=1 make infra-check
```

该模式会额外检查 `Postgres`、`Redis`、`MinIO` 和 `Qdrant` 的运行状态与基础健康端点。

相关 runbook：

- `docs/runbooks/local-stack-bootstrap.md`
- `docs/runbooks/docker-compose-production-deployment.md`（未来 production Compose 的边界与上线要求；不是当前开发 Compose 的操作说明）
