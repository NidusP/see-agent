# 单智能体 V1 一键开发启动计划

## 文档控制

- 状态：`verified`
- 版本：`v1.0`
- 更新日期：`2026-11-05`
- 目的：将根目录 `make dev` 从启动顺序提示升级为可复现的一键全栈开发启动入口。

## 目标

开发人员在仓库根目录执行 `make dev` 后，命令按固定顺序完成本地环境检查、依赖准备、服务启动和全栈就绪检查；任一关键步骤失败时停止后续启动，并输出可定位的问题与对应日志位置。

## 范围

- 检查 Docker、Docker Compose、Ollama、Go、Python、Node.js、pnpm、curl、openssl 等本地工具。
- 检查 Ollama 服务和 `qwen3:14b`、`qwen3-embedding:0.6b` 模型是否可用。
- 启动并检查 Postgres、Redis、MinIO、Qdrant。
- 校验仓库外 Runtime service token；缺失、占位、权限过宽或长度不足时重新生成。
- 按 Runtime → Backend → Web 顺序启动应用，并等待各自 readiness/HTTP 端点。
- 自动安装缺失或过期的 Python、Go、Web 项目依赖。
- 记录应用日志，失败时输出对应日志尾部。
- 成功后提示 Web 地址和可选的 `make bootstrap-rag-knowledge` 命令。

## 非目标

- 不自动安装 Docker、Ollama、Go、Node.js 或 pnpm 等系统级工具。
- 不自动执行示例知识导入；开发人员自行决定是否运行。
- 不修改业务 API、数据库 schema、Runtime 检索策略或模型配置。
- 不停止或删除 Docker Compose 数据卷。

## 启动设计

1. 解析仓库根目录并加载 `infra/compose/.env.example`。
2. 检查必需工具、Ollama 模型和关键端口；环境缺失时退出并给出修复命令。
3. 创建或修复 `$HOME/.config/see-agent/fullstack-secrets.env`，仅保存 `SEE_AGENT_RUNTIME_SERVICE_TOKEN`。
4. 执行 `make infra-up` 和 `RUNNING=1 make infra-check`。
5. 按需执行 `make runtime-install`、`go mod download`、`make web-install`。
6. 执行 `make fullstack-preflight`。
7. 后台启动 Runtime，等待 `http://127.0.0.1:8000/readyz`；失败时输出 Runtime 日志并回收本次已启动进程。
8. 后台启动 Backend，等待 `http://127.0.0.1:8080/readyz`；失败时输出 Backend 日志并回收本次已启动进程。
9. 后台启动 Web，等待 `http://127.0.0.1:3000/`；失败时输出 Web 日志并回收本次已启动进程。
10. 执行 `make fullstack-smoke`，成功后输出访问地址、日志目录和可选知识导入命令。

## 验收标准

- `make dev` 在干净但具备系统级工具的环境中能自动启动 Docker 依赖、Runtime、Backend、Web。
- 缺少系统级工具、Ollama 未运行、模型未下载、端口被占用、预检失败或服务未就绪时，命令返回非零状态，并明确指出失败阶段。
- 缺失或无效 Runtime service token 会在本次启动中重新生成，且文件权限为用户私有。
- Python、Go、Web 依赖缺失时自动安装；依赖已就绪时不重复执行完整安装。
- Runtime、Backend、Web 中任一进程提前退出时，`make dev` 能失败退出并显示对应日志尾部。
- `Ctrl-C` 只回收本次启动的应用进程；Postgres、Redis、MinIO、Qdrant 数据卷保持不变。
- 成功输出 `http://127.0.0.1:3000` 和 `make bootstrap-rag-knowledge` 提示。

## 验证计划

- 静态检查：`sh -n scripts/ops/dev_start.sh`、`make -n dev`、`make help`。
- 脚本失败路径：模拟缺少工具、无效 token、端口占用和 readiness 超时，确认非零退出与错误提示。
- 真实本地路径：在 Docker/Ollama 可用时执行 `make dev`，确认 Runtime 启动期真实模型 warm-up、三项 readiness 和 `make fullstack-smoke` 通过。
- 文档同步：更新根 `README.md`、`AGENTS.md` 和 `docs/runbooks/local-stack-bootstrap.md`。

## 风险与处理

- Next.js、Uvicorn reload 和 `go run` 可能生成子进程；脚本需要递归回收本次启动的进程树。
- 模型预热可能超过普通 HTTP 探测时间；Runtime readiness 等待应允许较长超时，并输出当前等待阶段。
- 已存在的可用 Runtime、Backend 或 Web 不应重复启动；端口占用但 readiness 失败时必须停止并提示冲突。

## 验证记录

- `sh -n scripts/ops/dev_start.sh`：通过。
- `make -n dev`：确认根级入口调用一键启动脚本。
- 缺少 Docker 的模拟环境：命令以非零状态停止，并输出“缺少命令 docker”。
- 真实本地启动：`make dev` 成功完成基础设施健康检查、Runtime warm-up、Runtime/Backend/Web readiness 和 `make fullstack-smoke`。
- `Ctrl-C` 回收：Runtime、Backend、Web 的 readiness 端点停止响应；Postgres、Redis、MinIO、Qdrant 保持健康。
- 运行日志：保存于 `.local/dev/logs/20261105-091026/`；`.local/` 已加入 `.gitignore`。
