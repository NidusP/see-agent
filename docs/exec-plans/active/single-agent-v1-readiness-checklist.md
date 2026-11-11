# 单智能体 V1 编码前准备清单

## 文档控制

- 状态：活动草案
- 版本：`v1.0`
- 目的：冻结实现前决策，并明确启动编码前必须具备的最小条件

## 1. 已冻结决策

### 1.1 产品范围

- V1 必选知识输入格式：`txt`、`md`、`json`、`pdf`、`docx`、`xlsx`、`pptx`
- V1 不以遗留 Office 二进制格式 `doc`、`xls`、`ppt` 为首批上线门槛
- 终端用户反馈不是 V1 上线阻塞项

### 1.2 技术选型

- 前端固定为 `Next.js`
- 业务后端固定为 `Go`
- AI 运行时固定为 `Python`
- 向量库方向固定为 `Qdrant`
- 私有对象存储方向固定为 `MinIO`

### 1.3 模型开发路径

- 开发与调试默认使用本地模型
- 本地模型调用层必须兼容 OpenAI-compatible API
- 开发机推荐默认路径：`Ollama`
- GPU Linux 或更高并发环境推荐路径：`vLLM`

### 1.4 V1 治理边界

- V1 采用单租户内部登录
- V1 引用粒度以页码、标题路径、sheet、slide 和表格 / 单元格范围为主
- 图像区域级引用、精细 RBAC、微调管理与多智能体扩展均延后

## 2. 编码前必须产出的文档资产

在开始业务代码前，以下文档必须存在且可用：

- 产品规格：`docs/product-specs/single-agent-v1-spec.md`
- 系统设计：`docs/design-docs/single-agent-v1-sdd.md`
- 工作区布局：`docs/design-docs/single-agent-v1-workspace-layout.md`
- Runtime 设计：`docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
- Web 设计：`docs/design-docs/single-agent-v1-web-ui-design.md`
- Backend 设计：`docs/design-docs/single-agent-v1-backend-design.md`
- 评测 harness：`docs/harness/single-agent-v1-harness.md`
- 集成测试计划：`docs/harness/single-agent-v1-integration-test-plan.md`
- Eval baseline 计划：`docs/harness/single-agent-v1-eval-baseline-plan.md`
- 执行计划：`docs/exec-plans/active/single-agent-v1-plan.md`
- 并行任务拆分：`docs/exec-plans/active/single-agent-v1-parallel-workstreams.md`

## 3. 编码前必须补齐的工程资产

以下资产是进入真正编码阶段前的最低门槛：

1. 初始化 git 仓库并建立基础提交规范。
2. 建立 monorepo 目录骨架：`apps/`、`services/`、`packages/`、`infra/`、`data/`、`agent-registry/`。
3. 提供根级统一命令入口：`Makefile` 或 `justfile`。
4. 起草 `packages/contracts/` 下的首版 API 契约和异步任务 payload。
5. 在 `data/sample-docs/` 放入每种受支持格式的至少一个样例文档。
6. 在 `data/eval-datasets/` 准备首批 `30-50` 条种子评测题。
7. 为 `apps/web/`、`services/backend-go/`、`services/ai-runtime-py/` 各自补充局部 README 和 AGENTS 指引。
8. 在 `agent-registry/registry/services.yaml` 记录服务路径、启动命令、端口、依赖和健康检查。

### 3.1 当前完成状态

- 已完成：git 仓库初始化
- 已完成：monorepo 目录骨架与服务 README / `AGENTS.md`
- 已完成：根级 `Makefile` 统一命令入口
- 已完成：`packages/contracts/` 首版契约与 schema 基线
- 已完成：`data/sample-docs/` 七类格式真实样例
- 已完成：`data/eval-datasets/` 种子模板占位
- 已完成：`agent-registry/registry/services.yaml` 首版服务清单
- 未完成：首批 `30-50` 条评测题扩充到可运行基线集

## 4. 推荐但不阻塞启动的决策

以下建议强烈推荐在首周完成，但不要求在第一行代码写下前全部完结：

- 是否在 V1 首批就加入 `doc` / `xls` / `ppt` 转换链路
- `pptx` speaker notes 是否默认进入检索语料
- `xlsx` 引用在前端是否需要可展开到表格或单元格范围
- 本地模型默认选择的上下文窗口、最大输出长度与超时策略

## 5. 进入编码阶段的判断标准

当满足以下条件时，可以认为项目已完成“编码前准备”：

- 产品范围已冻结，不再反复变更首批文件格式
- 前后端与 AI 运行时的职责边界已写入 SDD
- 本地模型开发路径已明确，且调用接口保持稳定
- monorepo 布局和 agent 调度约定已成文
- harness 已明确数据集、评分和模型对比边界

## 6. 当前建议

建议按以下顺序进入真正实现：

1. 先初始化仓库骨架与统一命令入口
2. 再起草契约与样例文档
3. 之后再分别创建 `Next.js`、`Go`、`Python` 三个服务的最小可运行骨架
4. 然后按 `docs/exec-plans/active/single-agent-v1-parallel-workstreams.md` 并行推进

## 7. 来源对齐

这份清单的目标不是替代产品规格或系统设计，而是把“可以开始编码”这件事变成可检查、可交接、可由 agent 稳定执行的门槛。
