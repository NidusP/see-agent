# 单智能体 V1 集成与端到端测试计划

## 文档控制

- 状态：用于实现启动的草案
- 版本：`v1.0`
- 目标：把 `tests/integration/` 和 `tests/e2e/` 从目录占位提升为可执行的测试计划

## 1. 设计目标

本测试计划关注的是“产品主链路是否在多服务协作下仍然成立”，而不是替代单元测试。

需要覆盖：

- 文件上传到任务创建
- 摄取结果回写
- 聊天请求到引用返回
- 删除或重建索引后的可见性变化
- 登录与角色门禁
- Web 关键路径可达

## 2. 测试分层

### 2.1 单元测试

不在本文重点范围，但默认由各服务自行承担。

### 2.2 集成测试

目标：

- 验证 Backend、AI Runtime、对象存储、向量库之间的协作
- 不经过真实浏览器

### 2.3 端到端测试

目标：

- 验证 Web 到 Backend 的关键用户路径
- 可用浏览器自动化或轻量 e2e runner

## 3. 环境前置

集成 / e2e 默认依赖：

- `make infra-up`
- 本地模型服务已就绪
- `make runtime-dev`
- `make backend-dev`
- `make web-dev`

如需轻量模式，可先用 mock runtime 或固定 fixture 替代真实模型调用。

### 3.1 当前已落地的轻量模式

当前仓库已经提供：

- `make integration`
  - 先构建 `apps/web`
  - 再运行 `tests/integration/` 与 `tests/e2e/`
- `make web-ui-smoke`
  - 需要已有运行中的 Runtime、Backend 和 Web
  - 通过 Playwright 驱动真实浏览器完成登录、聊天发送、SSE 完成态、citation 面板检查，以及宽/中/窄视口下会话栏与引用检查器的布局层级检查
- 轻量模式不依赖 `infra-up`
- 轻量模式通过子进程启动：
  - Go backend
  - Python AI runtime
  - Next.js production server

这批 smoke test 当前主要覆盖：

- runtime `preview/run`
- backend conversation / sources
- backend chat SSE stream smoke
- backend auth role gate：未登录返回 `401`，普通用户访问知识库返回 `403`，管理员 `/api/auth/me` 正常
- backend upload / chunks / reindex
- backend `queued -> running -> succeeded/failed` 摄取状态回写
- backend upload -> runtime active retrieval index -> backend chat citation
- web 登录跳转、普通用户导航隐藏、管理员页面可达、Web proxy 登录后 cookie 转发
- web config proxy 读取 active config、保存 agent config、保存 retrieval config 和刷新版本历史
- web chat proxy SSE stream smoke
- web browser UI smoke：登录、进入聊天、发送消息、等待 SSE 回答完成、点击引用并验证 citation 面板
- web `/chat`、`/chat/[conversation_id]`、`/knowledge`、`/config`

### 3.2 A1-4 处置展示与质量反馈回归

`A1-4` 不把浏览器状态当作 policy 判断。定向测试要以 Backend 持久化/SSE 的 completed message fixture 驱动四种处置，并覆盖：

- `clarification_required` 显示“需要补充范围”，没有 sources、knowledge version 或 revalidation action；`insufficient_evidence` 显示“当前知识不足”，同样不请求/显示 sources。
- `partial_answer` 显示“已覆盖部分”和结构化 coverage，sources 只来自实际 citation rank；`grounded_answer` 保持现有锚点和来源抽屉；无 disposition 的 legacy message 维持兼容来源入口。
- 管理员 evidence 同时显示 disposition、retrieval/generation/knowledge-used、clarification 或 coverage；retrieval candidates 不进入普通用户 API/UI。
- Backend 正向接受三种新增 feedback kind 的合法 target，负向拒绝错误 disposition、空 comment、citation rank、legacy/controlled/non-knowledge/failed target，并验证 idempotency 和原有 `wrong_evidence` rank 行为不退化。
- 管理员 Web 只提供与所选 evidence 相容的反馈类型；直接伪造 Next.js 请求、URL 或表单状态仍由 Backend system-admin role gate 和 validation 拒绝。

## 4. 样例资产映射

首批测试优先使用：

- `data/sample-docs/employee-handbook.txt`
- `data/sample-docs/employee-handbook.md`
- `data/sample-docs/policy-catalog.json`
- `data/sample-docs/sample-policy.pdf`
- `data/sample-docs/sample-handbook.docx`
- `data/sample-docs/sample-budget.xlsx`
- `data/sample-docs/sample-training-deck.pptx`

理由：

- 已经存在真实样例
- 覆盖七类格式
- 便于把 parse / chunk / citation 问题追溯回具体文件

## 5. 集成测试矩阵

### I1 Health 联通性

- 目标：
  - 验证 Web、Backend、Runtime、基础依赖均可达
- 输入：
  - 无
- 断言：
  - `/healthz` 返回正常
  - Postgres / Redis / MinIO / Qdrant 健康可探测

### I2 上传到任务创建

- 目标：
  - 上传文件后能创建 `KnowledgeFile` 和 `IngestionTask`
- 输入：
  - `employee-handbook.txt`
- 断言：
  - `POST /api/kb/files` 返回 `file + ingestion_task`
  - 文件状态为 `uploaded` / `queued`
  - `task_id` 可回查

### I3 任务状态回写

- 目标：
  - Runtime 回传后，Backend 更新 `running/succeeded/failed`
- 输入：
  - 任意样例文件
- 断言：
- `latest_task.status` 随回执变化
- 非法状态不被接受

当前已落地的轻量 smoke 覆盖：

- 1 条 happy-path
  - 上传有效 `pdf` 后轮询到 `succeeded`
- 1 条 failure-path
  - 上传损坏的 `docx` 后轮询到 `failed`
  - `latest_task.error.code` 返回稳定错误类别

### I4 Chunk 可见性

- 目标：
  - 文件摄取完成后，chunk 可通过 `GET /api/kb/files/{file_id}/chunks` 查看
- 输入：
  - `sample-handbook.docx`
- 断言：
  - 返回至少一个 chunk
  - chunk 携带 `source_locator`

### I5 聊天到引用返回

- 目标：
  - 聊天请求能返回 assistant message，并能取回 citations
- 输入：
  - 已摄取样例文件
  - 一个可回答问题
- 断言：
  - `POST /messages` 成功
  - `GET /sources/{message_id}` 返回 citation 列表
  - citation 含结构化 locator

当前已落地的轻量 smoke 覆盖：

- 1 条默认样例语料 happy-path
  - 创建会话后提问差旅报销期限
  - assistant answer 不再是 Backend placeholder
  - sources 返回结构化 locator
- 1 条 SSE stream happy-path
  - 创建会话后用 `stream=true` 提问差旅报销期限
  - 返回 `message.started`、`message.delta`、`message.completed`
  - stream body 不包含 Backend fallback 文案
- 1 条上传知识 happy-path
  - 通过 Backend 上传唯一文本文件
  - 等待 Runtime ingestion `succeeded`
  - 再通过 Backend chat 提问唯一问题
  - assistant answer 和 sources 均引用新上传 `file_id`

### I6 删除后的检索不可见

- 目标：
  - 删除文件后，被删内容不再作为检索来源返回
- 输入：
  - 已成功摄取文件
- 断言：
  - 删除后聊天结果不再引用对应 `file_id`

### I7 重新索引

- 目标：
  - 重新索引能创建新任务并更新版本信息
- 输入：
  - 已存在文件
- 断言：
  - `POST /reindex` 返回新 `task_id`
  - `parse_version` / `index_version` 或任务时间戳发生变化

### I8 K3 revision 投递与索引投影

- 目标：
  - 验证 immutable revision 从 PostgreSQL outbox 经 Redis Streams 到 Runtime/Qdrant 的可靠投递，不复用 legacy List ingestion。
- 前置：
  - 已启动本地 Postgres、Redis、MinIO、Qdrant。
  - 使用隔离 schema、隔离 stream/consumer group 和独立 Qdrant collection，测试结束后可安全清理。
- 输入：
  - 一个 queued revision 及其 `knowledge.revision.index_requested` outbox。
- 断言：
  - relay 写入含 `outbox_id`、`document_id`、`revision_id`、checksum 和 delivery id 的 Streams event；同一 outbox 因 relay crash 重放时 Runtime 最终只有同一 revision manifest 与稳定 point id。
  - consumer 成功处理后，PostgreSQL revision 为 `ready`、delivery 为 `delivered` 且 Redis event 已 `XACK`；active snapshot pointer 保持不变。
  - consumer 在 Runtime 调用后、ack 前中断时，pending entry 可 reclaim 并安全完成；不会产生不同 manifest 或重复 ready event。
  - Runtime 可重试失败会按退避保留 delivery；不可重试或超过上限时进入 DLQ，revision 为 `failed`，且 active snapshot pointer 保持不变。
  - Qdrant 同时存在同一 document 的两个 revision 时，重试/删除其中一个 revision 不影响另一个 revision 的 point。
- 通过条件：
  - K3 worker 的 Go/Python focused tests 与本用例都通过，且数据面可由 `knowledge_outbox`、Streams pending/DLQ 和 Qdrant payload 交叉定位。

当前状态：

- `verified`：隔离 Postgres delivery lifecycle、真实 Redis PEL/DLQ、Server 到 Runtime immutable callback、Runtime/Qdrant 和 legacy MinIO compatibility 均已通过；全栈 `make integration` 为 `41 passed`。

## 6. 端到端测试矩阵

### E1 登录

- 路径：
  - 打开 `/login`
  - 提交登录
- 断言：
  - 跳转成功
  - 工作台可见

### E2 新建会话

- 路径：
  - 进入 `/chat`
  - 点击新建会话
- 断言：
  - 左栏出现新会话
  - 主区进入聊天空态或详情页

### E3 提问并查看引用

- 路径：
  - 打开发送框
  - 输入问题
  - 等待 assistant 返回
  - 点击引用入口
- 断言：
  - 回答可见
  - 右栏 citation 面板可见

当前已落地的浏览器级 smoke 覆盖：

- 通过 `/login?next=/chat` 使用管理员账号登录
- 在 `/chat` 空态下输入差旅报销问题并发送
- 等待 assistant SSE 回答进入 `completed`，且回答包含 `10`
- 点击 `1 条引用`，确认右侧 citation 面板展示 `employee-handbook.txt`
  - citation 至少展示文件名和 locator

### E4 上传文件并观察状态

- 路径：
  - 打开 `/knowledge`
  - 上传样例文件
  - 观察状态变化
- 断言：
  - 文件出现在列表
  - 状态从 `queued/running` 进入 `succeeded` 或 `failed`

### E5 查看文件详情

- 路径：
  - 在列表中打开文件详情
- 断言：
  - `latest_task` 可见
  - chunk / locator 检查区可见

### E6 配置变更提示

- 路径：
  - 打开 `/config`
  - 修改配置并提交
- 断言：
  - 出现新版本提示
  - UI 明示“仅新会话生效”

## 7. Fixture 策略

建议将 `data/fixtures/` 拆为：

- `parse/`
- `chunks/`
- `retrieval/`
- `citations/`
- `api/`

### 7.1 集成测试优先使用的 fixture

- runtime preview 结果样本
- runtime run 结果样本
- citation 响应样本
- 非法状态迁移样本

### 7.2 适合 mock 的外部依赖

- 本地模型生成结果
- reranker 结果

尽量不要 mock：

- 文件元数据状态流
- citation 结构
- 上传 / 删除 / 重建索引路径

## 8. 执行顺序建议

建议按以下顺序补测试：

1. `I1 Health 联通性`
2. `I2 上传到任务创建`
3. `I3 任务状态回写`
4. `E1 登录`
5. `E2 新建会话`
6. `I5 聊天到引用返回`
7. `E3 提问并查看引用`
8. `I6 删除后的检索不可见`
9. `I7 重新索引`
10. `I8 K3 revision 投递与索引投影`

## 9. 通过标准

进入首轮并行开发后，测试层至少应满足：

- `make integration` 不再是占位
- 至少有 1 条上传链路集成测试
- 至少有 1 条聊天引用链路集成测试
- 至少有 1 条 Web 冒烟 e2e

当前状态：

- 以上最低门槛均已落地
- 更完整的跨服务编排和真实模型联调仍留待后续 `P4/P5/P8`

## 10. 与 Harness 的关系

本测试计划负责：

- 系统主链路是否可用
- 产品交互是否打通

`single-agent-v1-harness.md` 负责：

- 检索质量
- 回答质量
- 引用质量
- 模型对比

二者互补，不应混为一谈。
