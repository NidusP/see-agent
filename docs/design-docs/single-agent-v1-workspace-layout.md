# 单智能体 V1 工作区与目录布局

## 文档控制

- 状态：用于实现启动的草案
- 版本：`v1.0`
- 范围：多技术栈 monorepo 结构与 agent 调度约定

## 1. 目标

为 `Next.js` 前端、`Go` 业务后端、`Python` AI 运行时和共享契约资产提供统一工作区布局，使人类开发者和 agent 都能稳定定位边界、执行命令和完成交接。

## 2. 设计原则

1. 文档与实现分层
   - 产品、架构、评测和执行计划保留在根目录 `docs/`，不与具体服务代码混放。
2. 契约集中
   - 跨服务 API、事件、配置 schema 和提示词模板集中管理，避免多份复制。
3. 服务自包含
   - 每个服务目录都应具备独立启动、测试、说明和局部 agent 指引。
4. 根入口统一
   - 所有常用命令都通过根级 `Makefile` 或 `justfile` 暴露，避免 agent 猜测子项目命令。
5. 调度可机器读取
   - 服务端口、健康检查、依赖、启动顺序和负责人应记录在根级 `agent-registry/` 中。

## 3. 推荐目录结构

```text
see-agent/
  AGENTS.md
  README.md
  docs/
    product-specs/
    design-docs/
    harness/
    exec-plans/
      active/
      archive/
    references/
    api/
    runbooks/
  apps/
    web/
  services/
    backend-go/
    ai-runtime-py/
  packages/
    contracts/
    prompts/
    config-schema/
    shared-types/
  infra/
    compose/
    k8s/
  scripts/
    dev/
    ci/
    ops/
  data/
    sample-docs/
    eval-datasets/
    fixtures/
  tests/
    integration/
    e2e/
  agent-registry/
    registry/
    runbooks/
    handoffs/
    task-templates/
  Makefile
  justfile
```

## 4. 顶层目录职责

### 4.1 `apps/`

- `apps/web/`
  - `Next.js` Web 前端
  - 聊天 UI、管理台、上传界面、引用查看与登录页

### 4.2 `services/`

- `services/backend-go/`
  - 鉴权、会话、文件元数据、配置版本化、日志 API
- `services/ai-runtime-py/`
  - 文档解析、切块、embedding、检索、reranking、prompt 组装、模型调用、评测执行

### 4.3 `packages/`

- `packages/contracts/`
  - OpenAPI、事件 schema、共享 DTO、错误码定义
- `packages/prompts/`
  - 系统提示词、回答模板、拒答模板、版本化 prompt 资产
- `packages/config-schema/`
  - 模型配置、检索配置、快照 schema
- `packages/shared-types/`
  - 前后端共享类型，优先由契约自动生成

### 4.4 `infra/`

- `infra/compose/`
  - 本地开发与集成联调的 `docker compose` 文件
- `infra/k8s/`
  - staging / production 部署清单

### 4.5 `data/`

- `data/sample-docs/`
  - 各文件类型样例文档，覆盖 `txt/md/json/pdf/docx/xlsx/pptx`
- `data/eval-datasets/`
  - harness 使用的数据集与答案元信息
- `data/fixtures/`
  - 解析、检索、集成测试的固定输入输出

### 4.6 `tests/`

- `tests/integration/`
  - 跨服务 API 与索引链路测试
- `tests/e2e/`
  - Web UI 到回答展示的端到端测试

### 4.7 `agent-registry/`

- `registry/`
  - 机器可读的服务清单、命令入口、端口、依赖、健康检查
- `runbooks/`
  - 故障处理、常见操作、启动顺序
- `handoffs/`
  - 多会话或多 agent 交接记录
- `task-templates/`
  - 标准任务拆分模板，例如 `web`、`backend`、`runtime`、`eval`

## 5. agent 调度约定

### 5.1 根级导航

- 根目录 `AGENTS.md` 只描述全局原则、文档入口和工作流，不堆放具体命令细节。
- 每个服务目录都应补一个局部 `AGENTS.md`，记录：
  - 启动命令
  - 测试命令
  - lint / format 命令
  - 依赖服务
  - 常见陷阱

### 5.2 统一命令面

根目录统一暴露以下命令别名：

- `make dev`
- `make test`
- `make lint`
- `make format`
- `make typecheck`
- `make eval-baseline`
- `make integration`

agent 默认从根级命令入口开始，不直接猜测子项目脚本名。

### 5.3 契约优先

- API 契约先写入 `packages/contracts/`
- 前端与后端共享结构尽量由契约生成
- AI 运行时与后端之间的事件 payload 也通过契约文件版本化

### 5.4 启动顺序与依赖

`agent-registry/registry/services.yaml` 应至少记录：

- 服务名
- 目录路径
- 启动命令
- 默认端口
- 健康检查 URL
- 上游依赖
- 本地运行前置条件

## 6. 本地模型开发配合

为适应“本地模型开发调试”的要求，目录布局还应支持：

- `infra/compose/` 中声明 `PostgreSQL`、`Redis`、`MinIO`、`Qdrant`
- AI 运行时通过环境变量切换本地模型端点
- 在 `agent-registry/registry/` 中记录本地模型服务地址、超时、上下文窗口和速率限制

建议开发调试默认面向 OpenAI-compatible API，这样从本地模型切换到更高性能推理主机时无需重写调用层。

## 7. 编码启动前必须具备的目录资产

在正式写业务代码前，工作区至少应先具备：

- 根级 `Makefile` 或 `justfile`
- `apps/web/`、`services/backend-go/`、`services/ai-runtime-py/` 空目录与 README
- `packages/contracts/` 初始契约目录
- `data/sample-docs/` 的首批样例文档
- `agent-registry/registry/services.yaml` 初稿

## 8. 来源对齐

本布局文档服务于“文档优先、契约优先、评测驱动、便于 agent 接力”的实现准备目标，与 `docs/design-docs/single-agent-v1-sdd.md` 和 `docs/exec-plans/active/single-agent-v1-readiness-checklist.md` 配套使用。
