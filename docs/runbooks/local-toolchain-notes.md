# 本地工具链说明

## 目的

记录当前工作区已经验证过的项目级依赖版本，以及本机环境下的实际注意事项，避免后续会话重复踩坑。

## 已验证的项目级版本

### Web

- 包管理器：`pnpm` `10.13.1`，由 `apps/web/package.json` 的 `packageManager` 字段固定
- `next`: `16.2.9`
- `react`: `19.2.7`
- `react-dom`: `19.2.7`
- `typescript`: `6.0.3`
- `eslint-config-next`: `16.2.9`
- `eslint`: `9.39.4`

说明：

- `Next.js 16` 已将 `next lint` 从推荐路径中移除，本仓库改为 `eslint .`
- `eslint 10.x` 在当前组合下会触发 `eslint-config-next` 运行错误，因此已回退并固定到稳定可用的 `9.x`

### AI Runtime

- `fastapi`: `0.128.8`
- `uvicorn`: `0.39.0`
- `pytest`: `8.4.2`
- `httpx`: `0.28.1`

Python 解释器策略：

- 本地开发推荐基线：`Python 3.13`
- 仓库当前最小要求：`Python 3.12+`
- `make runtime-install` 现会优先探测：`python3.14 -> python3.13 -> python3.12 -> python3`
- 如果最后回退到无版本后缀的 `python3`，也要求它本身满足 `3.12+`

说明：

- 新版 `starlette.testclient` 需要显式安装 `httpx`，否则测试收集阶段会失败
- Homebrew Python 默认受 `PEP 668` 保护，直接往解释器全局环境 `pip install` 会失败；仓库现统一改为通过 `make runtime-install` 创建根级 `.venv/` 再装依赖
- 当前这台机器的系统 `/usr/bin/python3` 仍是 `3.9.6`，但仓库命令会优先使用 `.venv/bin/python`；`.venv` 则由 `/opt/homebrew/bin/python3.13` 创建

### Go

- `go`: `1.26.4`

说明：

- 当前机器未在默认 `PATH` 中提供 `go`
- 仓库级命令入口统一使用：`/Users/populus/.local/go1.26.4/bin/go`

## 本机环境注意事项

- 不修改系统级 `node` 版本
- Web 依赖统一使用 `apps/web/pnpm-lock.yaml`；不要同时维护 `package-lock.json`

## 已验证命令

- `make runtime-install`
- `cd apps/web && pnpm run typecheck`
- `cd apps/web && pnpm run lint`
- `cd apps/web && pnpm run build`
- `make runtime-test`
- `make integration`

## 后续建议

- 如果后续需要把 Go 作为团队统一工具链，最好补充 shell profile 或 direnv 配置，把 `GO_BIN` 路径标准化
- 如果要进一步降低跨会话环境差异，可在后续用容器或 devbox/nix 再封装一层本地工具链
