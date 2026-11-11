# 单智能体 V1 并行工作流拆分

## 文档控制

- 状态：活动草案
- 版本：`v1.1`
- 目标：把编码阶段拆成可并发、可交接、可由多会话稳定接力的工作包

## 1. 使用方式

- 每个会话只领取一个工作包，避免跨栈同时改动造成上下文污染
- 开工前先读取：
  - `docs/product-specs/single-agent-v1-spec.md`
  - `docs/design-docs/single-agent-v1-sdd.md`
  - `docs/design-docs/single-agent-v1-workspace-layout.md`
  - `docs/harness/single-agent-v1-harness.md`
- 完工后必须同步：
  - 相关代码
  - 对应文档
  - `agent-registry/` 中受影响的命令或依赖信息

## 2. 依赖图

```text
P0 contract freeze
  -> P1 backend skeleton expansion
  -> P2 ai-runtime ingestion baseline
  -> P3 web chat shell

P1 + P2
  -> P4 ingestion orchestration
  -> P5 retrieval + answer pipeline

P1 + P3
  -> P6 admin upload workflow

P2 + P5
  -> P7 eval harness baseline

P1 + P2 + P3
  -> P8 integration tests
  -> P9 local infra bootstrap

P4 + P5 + P6 + P8 + P9
  -> P10 release-readiness sweep
```

## 3. P1 / P2 / P3 并行启动细则

`P1`、`P2`、`P3` 可以并行，但只在 `P0` 已经满足“可用冻结”时启动。

这里的“可用冻结”定义为：

- `packages/contracts/openapi.yaml`
- `packages/contracts/chat-runtime-request.schema.json`
- `packages/contracts/chat-runtime-response.schema.json`
- `packages/contracts/ingestion-task.schema.json`
- `packages/contracts/ingestion-task-result.schema.json`
- `packages/contracts/source-locator.schema.json`

以上文件在同一批并行开发期间默认视为稳定输入，而不是每个工作包都可以顺手改动的共享草稿。

### 3.1 并行启动前提

- 启动前先在 `docs/exec-plans/active/single-agent-v1-parallel-claims.md` 认领工作包。
- 每个会话一次只认领一个工作包。
- 如果 `P1/P2/P3` 任一会话发现契约字段必须变更，先把问题记录到认领板，再回切到 `P0` 串行处理。
- 契约未重新冻结前，其他并行会话只允许继续做不依赖新字段的局部工作，不允许私下扩字段。

### 3.2 文件写入边界

| 工作包 | 默认可写路径 | 默认只读输入 | 需要串行协调的共享文件 |
| --- | --- | --- | --- |
| `P1` | `services/backend-go/**` | `packages/contracts/**` `docs/design-docs/single-agent-v1-backend-design.md` `docs/api/single-agent-v1-api-design.md` | `packages/contracts/**` `Makefile` `agent-registry/registry/services.yaml` `tests/integration/**` |
| `P2` | `services/ai-runtime-py/**` | `packages/contracts/**` `docs/design-docs/single-agent-v1-runtime-ingestion-design.md` `data/sample-docs/**` | `packages/contracts/**` `Makefile` `agent-registry/registry/services.yaml` `tests/integration/**` `data/sample-docs/**` 中已有样例文件 |
| `P3` | `apps/web/**` | `packages/contracts/**` `docs/design-docs/single-agent-v1-web-ui-design.md` `docs/api/single-agent-v1-api-design.md` | `packages/contracts/**` `Makefile` `tests/e2e/**` |

补充规则：

- `P1` 不直接改 `apps/web/**` 或 `services/ai-runtime-py/**`。
- `P2` 不直接改 `apps/web/**` 或 `services/backend-go/**`。
- `P3` 不直接改 `services/backend-go/**` 或 `services/ai-runtime-py/**`。
- `P1/P2/P3` 都不应在并行阶段主动改 `infra/**`，那是 `P9` 的主责任域。
- 如果需要给 `data/sample-docs/` 增补新文件，优先新增独立文件名，不覆盖现有样例。
- `README.md`、根级 `AGENTS.md`、`docs/exec-plans/active/` 下的共享计划文件，默认只在收口时或明确协调窗口内改动。

### 3.3 允许的接口假设

- `P1` 可以按现有 `OpenAPI` 落最小 API handler、service、repository 骨架。
- `P2` 可以按现有 schema 落 preview / run / chat 的 pipeline 骨架和返回结构。
- `P3` 可以基于现有契约做页面壳层、假数据适配层和组件布局。
- `P3` 如果后端接口尚未落地，只能在前端本地 mock “契约中已存在的字段”，不能发明新字段。
- `P1` 如果 runtime 仍是占位实现，只能依赖契约和已成文设计，不依赖口头约定的额外字段。
- `P2` 如果后端状态机未落地，只能返回契约中已有状态和 locator 结构。

### 3.4 必须显式同步的节点

- `P1 -> P3`
  - 会话列表、会话详情、发送消息、文件列表、任务状态的请求 / 响应示例
- `P1 -> P2`
  - backend 调 runtime 的调用方式
  - 任务状态回写或查询边界
- `P2 -> P1`
  - `/v1/ingestion/preview`
  - `/v1/ingestion/run`
  - `/v1/chat/run`
  - 三条内部接口的最小稳定响应样例
- `P3 -> P1`
  - 页面壳层真正需要的最小字段集合
  - 任何看起来“前端需要但契约未定义”的缺口清单

推荐同步方式：

- 在认领板记录“需要谁确认、卡在哪个字段、最后一次确认时间”。
- 不通过临时聊天记录传递唯一事实来源。
- 一旦共享字段变化，先改契约与设计文档，再通知三个工作包更新本地实现。

### 3.5 启动与收尾模板

- 任务模板：
  - `agent-registry/task-templates/parallel-workstream-task-template.md`
- 交接模板：
  - `agent-registry/handoffs/parallel-workstream-handoff-template.md`
- 共享认领板：
  - `docs/exec-plans/active/single-agent-v1-parallel-claims.md`

## 4. 工作包清单

### P0 契约冻结

- 领域：`contracts`
- 目标：冻结 Web、Backend、AI Runtime 之间的最小接口和异步任务载荷
- 前置条件：无
- 主要输入：
  - `docs/design-docs/single-agent-v1-sdd.md`
  - `docs/product-specs/single-agent-v1-spec.md`
- 主要输出：
  - 完整化 `packages/contracts/openapi.yaml`
  - 完整化 `packages/contracts/chat-runtime-request.schema.json`
  - 完整化 `packages/contracts/ingestion-task.schema.json`
- 建议触达文件：
  - `packages/contracts/`
  - `docs/design-docs/single-agent-v1-sdd.md`
- 验收标准：
  - 聊天请求、上传请求、摄取任务状态至少有稳定字段定义
  - 错误码、状态枚举、source locator 字段不再含糊
  - 后端与运行时无需私下约定隐含字段
- 推荐命令：
  - `rg -n "chat|ingestion|locator|schema" packages/contracts docs/design-docs -S`

### P1 Go 后端骨架扩展

- 领域：`backend`
- 目标：从 health-only API 扩展到会话、上传、任务查询三个最小端点
- 前置条件：
  - `P0`
- 主要输入：
  - `packages/contracts/openapi.yaml`
  - `agent-registry/registry/services.yaml`
- 主要输出：
  - 上传元数据 API
  - 会话创建 / 查询 API
  - 摄取任务状态 API
- 建议触达文件：
  - `services/backend-go/cmd/api/`
  - `services/backend-go/internal/`
  - `services/backend-go/README.md`
  - `services/backend-go/AGENTS.md`
- 验收标准：
  - `make backend-test` 通过
  - 暴露 `/healthz`
  - 与契约字段一致
- 推荐命令：
  - `make backend-test`
  - `make backend-dev`

### P2 Python AI Runtime 摄取基线

- 领域：`runtime`
- 目标：建立七类文件的最小解析入口、标准化中间表示和任务回执
- 前置条件：
  - `P0`
- 主要输入：
  - `data/sample-docs/`
  - `packages/contracts/ingestion-task.schema.json`
- 主要输出：
  - `/healthz`
  - `/v1/ingestion/preview`
  - `/v1/ingestion/run`
  - parser registry 占位
- 建议触达文件：
  - `services/ai-runtime-py/app/`
  - `services/ai-runtime-py/tests/`
  - `services/ai-runtime-py/README.md`
  - `services/ai-runtime-py/AGENTS.md`
- 验收标准：
  - 七类文件都能返回统一的 preview 元数据
  - 至少对 `txt/md/json/pdf/docx/xlsx/pptx` 返回格式识别与 locator 占位
  - `make runtime-test` 通过
- 推荐命令：
  - `make runtime-test`
  - `make runtime-dev`

### P3 Next.js Web 聊天壳层

- 领域：`web`
- 目标：把当前页面扩展为可承接聊天、引用展示和管理员入口的基础 UI
- 前置条件：
  - `P0`
- 主要输入：
  - `docs/product-specs/single-agent-v1-spec.md`
  - `packages/contracts/openapi.yaml`
- 主要输出：
  - 聊天主界面
  - 引用卡片占位
  - 管理台入口占位
- 建议触达文件：
  - `apps/web/app/`
  - `apps/web/README.md`
  - `apps/web/AGENTS.md`
- 验收标准：
  - `make lint`
  - `make typecheck`
  - `cd apps/web && npm run build`
  - 页面结构能明确容纳聊天区、来源区、上传入口
- 推荐命令：
  - `make lint`
  - `make typecheck`
  - `make web-build`

### P4 摄取编排链路

- 领域：`backend + runtime`
- 目标：把上传、入队、解析、状态回写串成一条可追踪链路
- 前置条件：
  - `P1`
  - `P2`
- 主要输入：
  - `packages/contracts/ingestion-task.schema.json`
  - `agent-registry/registry/services.yaml`
- 主要输出：
  - 文件上传后创建任务
  - 后端可查询任务状态
  - 失败任务具备错误类别
- 建议触达文件：
  - `services/backend-go/`
  - `services/ai-runtime-py/`
  - `tests/integration/`
- 验收标准：
  - 至少 1 条 happy-path 集成测试
  - 至少 1 条失败路径测试
  - 状态机包含 `queued/running/succeeded/failed`
- 推荐命令：
  - `make integration`

### P5 检索与回答基线

- 领域：`runtime`
- 目标：建立最小 RAG 闭环，包括切块、embedding、检索、回答和引用
- 前置条件：
  - `P2`
- 主要输入：
  - `docs/harness/single-agent-v1-harness.md`
  - `packages/prompts/`
  - `data/eval-datasets/`
- 主要输出：
  - chunk schema
  - embedding adapter
  - OpenAI-compatible chat adapter
  - citation formatter
- 建议触达文件：
  - `services/ai-runtime-py/app/`
  - `packages/prompts/`
  - `packages/config-schema/`
- 验收标准：
  - 给定样例文档能返回带引用的回答
  - 本地模型调用层与 OpenAI-compatible 接口隔离清楚
  - 拒答路径可测试
- 推荐命令：
  - `make runtime-test`

### P6 管理员上传工作流

- 领域：`web + backend`
- 目标：落地上传页、任务状态列表和基础错误反馈
- 前置条件：
  - `P1`
  - `P3`
- 主要输入：
  - 上传与任务状态契约
  - `data/sample-docs/`
- 主要输出：
  - 上传控件
  - 文件列表
  - 状态刷新逻辑
- 建议触达文件：
  - `apps/web/`
  - `services/backend-go/`
  - `tests/e2e/`
- 验收标准：
  - 可上传样例文件
  - 能看到状态变化和失败提示
  - 至少 1 条 Web 端 happy-path e2e 草案
- 推荐命令：
  - `make web-build`

### P7 Eval Harness 基线

- 领域：`eval`
- 目标：把种子数据、评分结构和基线报告跑通
- 前置条件：
  - `P2`
  - `P5`
- 主要输入：
  - `docs/harness/single-agent-v1-harness.md`
  - `data/eval-datasets/seed-template.jsonl`
- 主要输出：
  - 可执行的 baseline eval runner
  - `30-50` 条首批题目
  - 结果报告格式
- 建议触达文件：
  - `data/eval-datasets/`
  - `scripts/`
  - `docs/harness/`
- 验收标准：
  - `make eval-baseline` 不再是占位
  - 报告包含命中率、拒答、引用完整性等维度
  - 至少能跑本地模型基线一次
- 推荐命令：
  - `make eval-baseline`

### P8 集成与端到端测试

- 领域：`qa`
- 目标：把跨服务测试从 README 占位升级为最小自动化
- 前置条件：
  - `P1`
  - `P2`
  - `P3`
- 主要输入：
  - `tests/integration/README.md`
  - `tests/e2e/README.md`
- 主要输出：
  - 集成测试脚手架
  - e2e 测试脚手架
  - 根命令入口接线
- 建议触达文件：
  - `tests/integration/`
  - `tests/e2e/`
  - `Makefile`
- 验收标准：
  - `make integration` 有真实检查
  - e2e 至少覆盖聊天首页可达和上传流程入口
- 推荐命令：
  - `make integration`

### P9 本地基础设施编排

- 领域：`infra`
- 目标：把 Postgres、Redis、MinIO、Qdrant 和本地模型联调入口补齐
- 前置条件：
  - 无强制前置，可与 `P1/P2/P3` 并行
- 主要输入：
  - `docs/design-docs/single-agent-v1-sdd.md`
  - `agent-registry/registry/services.yaml`
- 主要输出：
  - `infra/compose/` 初版 compose 文件
  - 本地模型环境变量模板
  - 服务启动顺序 runbook
- 建议触达文件：
  - `infra/compose/`
  - `docs/runbooks/`
  - `agent-registry/registry/services.yaml`
- 验收标准：
  - 至少能声明所有依赖服务
  - 启动顺序和端口无冲突
  - 本地模型地址、超时、上下文窗口有默认值
- 推荐命令：
  - `docker compose config`

### P10 发布前收口

- 领域：`cross-cutting`
- 目标：把命令、文档、观测、质量门槛和风险清单统一收口
- 前置条件：
  - `P4`
  - `P5`
  - `P6`
  - `P8`
  - `P9`
- 主要输入：
  - 所有活动文档
  - 所有服务 README / `AGENTS.md`
- 主要输出：
  - 更新后的 runbook
  - 上线检查清单
  - 风险与回滚说明
- 建议触达文件：
  - `docs/runbooks/`
  - `docs/exec-plans/active/`
  - `README.md`
  - `Makefile`
- 验收标准：
  - 根命令入口无失效项
  - 文档与实现一致
  - 可由新会话在 10 分钟内完成环境自检

## 5. 推荐并发分组

- 启动准备窗口：
  - `P0`
- 第一批可并行：
  - `P1`
  - `P2`
  - `P3`
  - `P9`
- 第二批可并行：
  - `P5`
  - `P6`
  - `P8`
- 第三批：
  - `P4`
  - `P7`
- 收口批次：
  - `P10`

说明：

- `P1/P2/P3` 是同一批并行，不需要彼此等待。
- `P9` 也可同时推进，但要避免反复改动应用层契约。
- `P4` 虽然名字上看像“编排”，但它依赖 `P1 + P2`，不应过早抢跑。

## 6. 交接模板

- 当前工作包：
- 当前分支或会话目标：
- 已修改文件：
- 已验证命令：
- 未完成项：
- 风险 / 阻塞：
- 下一会话第一步：
