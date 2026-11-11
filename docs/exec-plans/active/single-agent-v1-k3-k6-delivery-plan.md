# 单智能体 V1 K3-K6 交付计划

## 文档控制

- 状态：`active`
- 版本：`v1.1`
- 更新日期：`2026-07-15`
- 上位事实源：[知识演进执行队列](single-agent-v1-next-dev-sequence.md)、[知识演进设计](../../design-docs/single-agent-v1-knowledge-evolution-design.md) 与 [K1 契约](../../design-docs/single-agent-v1-knowledge-evolution-contract.md)。
- 调度：`p` 负责边界冻结、交接、跨服务集成和最终验收；工作包实现由独立 worker 认领。

## 1. 总体交付规则

1. PostgreSQL 的 document、revision、snapshot、update event、outbox、provenance 和 revalidation 记录始终是权威事实；Redis、Qdrant、MinIO 只承担投递或派生数据职责。
2. `packages/contracts/`、已有 `001/002` migration 和 K2 的 snapshot 发布事务均已冻结。K3 只可追加 delivery migration，不得重写这些历史写入路径。
3. 新的 revision 事件只走 Redis Streams；遗留 file ingestion List 仅服务 legacy 路径。两者不得消费同一种 production event。
4. 每个阶段只能在依赖阶段的真实 smoke 和回归测试通过后启动。阶段完成由 `p` 复核实现、故障路径和全栈证据后才可标记 `verified`。

## 2. K3 可靠摄取与 revision-aware 索引投影

### 目标

把 K2 事务内产生的 `knowledge.revision.index_requested` outbox 事件可靠投递给 Runtime，并使 Runtime 以 immutable revision 写入 Qdrant。重复、乱序、运行中断和失败只能影响该 revision 的投递状态，绝不能改变 active snapshot。

### K3-A Backend 投递链

**负责人：** K3 Backend worker  
**独占写入范围：** `services/backend-go/**` 中 task queue、delivery repository、ingestion worker、K3 migration 和其 Go 测试。不得修改 Runtime、Web、shared contract 或既有 K2 publication transaction。

实现边界：

- 追加 migration，补齐 `knowledge_outbox` 的可观测投递字段：Stream message id、delivery status、attempt count、available/lease 时间、最后错误和完成时间。历史 outbox 默认为可重放的 `pending`。
- 由 outbox relay 读取 PostgreSQL 中可投递记录，以 `outbox_id` 作为 Redis Streams 事件幂等键执行 `XADD`，随后记录 `streamed`。XADD 成功但数据库回写失败允许产生重复 event，不能丢失事件。
- 使用 consumer group 消费 stream；启动时创建 group，处理 PEL pending message 的 reclaim，调用 Runtime，成功后写 revision `ready`、delivery `delivered` 并 `XACK`。
- 可重试错误以指数退避保留在 pending/retry 队列；达到上限或不可重试时，写入 DLQ、记录 `dead_letter`，将 revision 标记为 `failed`，再 `XACK`。所有状态转换必须可安全重放。
- 允许新 revision 进入 `indexing`，但只有 Runtime 返回与 request identity 对应的 manifest 后才能进入 `ready`。任何 callback 的 revision、document、checksum 或 delivery 不匹配均拒绝。
- Redis 断连、Runtime 超时、worker 中途退出和 response 解码失败均产生可诊断 delivery 记录；不得发布或切换 snapshot。

交接物：新增/调整的 queue and repository API、migration 说明、单元/隔离 Postgres+Redis smoke，以及用于人工诊断的 stream/DLQ 检查命令。

### K3-B Runtime revision 索引链

**负责人：** K3 Runtime worker  
**独占写入范围：** `services/ai-runtime-py/app/**`、`services/ai-runtime-py/tests/**`。不得修改 Backend、Web、shared contract 或 Makefile。

实现边界：

- revision ingestion 必须把 request 的 `document_id`、`revision_id`、checksum、parse/index/embedding 版本带入 parsed document、chunk id 和 Qdrant payload；legacy file ingestion 行为保持兼容。
- 同一 `revision_id + checksum + index_version` 重放必须得到稳定 chunk manifest 和 point id。相同 revision 的 retry 可覆盖自身 point，不得先删除其他 revision 的 point。
- vector store 新增按 revision 删除/替换的最小接口；payload 至少包含 `document_id`、`revision_id`、`chunk_content_sha256`、`visibility`、source locator 和版本信息，并为 revision/document/visibility 建立 Qdrant payload index。
- ingestion 成功返回 deterministic `chunk_manifest_sha256` 和实际 embedding model；失败不得返回成功 manifest。Qdrant 清理失败要显式失败，不得伪造 ready。
- K3 仅提供 revision-scoped 能力，不在此阶段把 chat 的 default retrieval 改成 snapshot-scoped；该切换属于 K5。

交接物：revision idempotency、payload isolation、Qdrant request 断言和真实 Qdrant/object-store smoke；每个失败路径说明是否可重试。

### K3 集成门槛

- K3-A 和 K3-B 都完成自测后，由 `p` 串接一条 `queued -> streamed -> indexing -> ready` 的真实 revision。
- 必测：重复 event、XADD 后 crash、consumer pending recovery、Runtime retryable/non-retryable 失败、乱序 callback、Qdrant delete 失败、Redis 断连和 DLQ。
- 通过条件：active snapshot 在全程保持不变；同一 revision 不重复产生不同 manifest；所有未完成事件都能在 PostgreSQL/Streams/DLQ 中被定位和恢复。

### K3 验收记录

- 状态：`verified`。
- Backend：`003` append-only delivery migration、outbox relay、Redis Streams consumer group、PEL reclaim、retry/backoff、DLQ 和 `K3_DELIVERY_ENABLED` lifecycle 已落地；legacy Redis List 不受影响。
- Runtime：revision identity、deterministic chunk manifest、revision-scoped point replacement 和 Qdrant payload indexes 已落地。
- `p` 验收：K2/K3 PostgreSQL、Redis Streams、Server Runtime callback、Runtime/Qdrant、MinIO legacy compatibility 和全栈回归均通过；active snapshot 未被索引投影改变。

## 3. K4 管理员知识生命周期

### 目标与分工

K3 通过后，先由 K4 Backend worker 冻结 API，再让 K4 Web worker 依据该 API 实现界面。管理员将从“文件列表”操作升级为“逻辑文档 -> immutable revision -> snapshot”的完整生命周期。

### Backend/API 包

- 管理员创建 document、上传初始 revision、替换 revision、查看 revision/delivery、显式 publish、disable、delete 与 snapshot/update event 列表。
- 每个写入需要 admin 身份、resource ownership、idempotency key 和 correlation id；legacy `/api/kb/files` 保持过渡兼容，但新写入必须映射到 document/revision。
- publish 只能选择 `ready` 且已完成 K3 delivery 的 revision；disable/delete 生成新的 snapshot 与 update event，不删除审计历史或旧回答依赖的 provenance。
- 提供分页、筛选和明确的冲突/可重试错误，避免前端靠猜测状态轮询。

### Web 包

- 知识库显示 document、revision、索引与投递状态、active snapshot、manifest 和有效时间。
- destructive 操作使用确认流程，并直观展示“影响后续回答、不改写历史回答”。
- 上传、替换、失败重试、发布、禁用、删除和状态刷新都要具备成功/失败/空态。

### 验收门槛

在真实 MinIO/Redis/Qdrant/Postgres 环境完成 DOCX/Markdown 的新增、替换、disable、delete。新 snapshot 不能检索被禁用内容，旧 provenance 仍可读取；所有 API 和浏览器权限失败路径通过。

### K4 验收记录

- 状态：`verified`。
- Backend：append-only `004` idempotency migration、管理员 lifecycle API、legacy facade mapping、ready-only publish、last-member disable 与 soft delete 已落地。
- Web：同源 proxy 和完整管理员 lifecycle UI 已落地，含 in-flight revision、权限和 destructive action 失败路径。
- `p` 验收：isolated Postgres、Server HTTP、Backend、Web lint/typecheck/build、lifecycle browser 和全栈 integration 通过；最新 `make integration` 为 `41 passed`。

## 4. K5 回答 provenance 与快照约束检索

### 目标与分工

K4 稳定发布 active snapshot 后，K5 Backend/Runtime 串行接通每条消息的 snapshot，随后 Web worker 展示来源详情。没有可验证 scope 的 Runtime 检索必须失败而不是退回全量索引。

### Backend/Runtime 包

- 新用户消息开始时解析 active published snapshot，构造 `KnowledgeScope`，固定本次请求的 snapshot manifest 和 revision 成员后再调用 Runtime。
- Runtime 以 revision member filter 查询，不得因 Qdrant 滞后、filter 构建失败或不存在 snapshot 而检索全量/旧索引；返回可重试错误和可诊断原因。
- assistant message 与每条 citation 事务保存 snapshot id、revision id、chunk content hash、locator、score、model/config/retrieval version 与生成时刻。
- 同一 conversation 的下一条新消息可使用最新 snapshot；旧 assistant message 仍展示当时的 snapshot，不修改历史答案文本。

### Web 与验收

- 引用详情显示“回答时使用的知识版本”和 freshness 状态。
- 必测 A/B snapshot 切换、旧 point 残留、空 snapshot、Qdrant filter 失败、conversation ownership 与复放旧消息。新消息只能引用 B，旧消息可解释 A。

### K5 验收记录

- 状态：`verified`。
- Backend：active snapshot scope 一次性读取、response/citation authority validation、transactional message provenance 与 sources read model 已落地。
- Runtime：revision-scoped InMemory/Qdrant retrieval、scope A/B isolation、orphan exclusion 和 filtered failure 已落地；无 filter fallback 被禁止。
- Web：answer/citation provenance 与 scope failure retry 已落地。
- `p` 验收：Postgres、Server、Backend、Runtime/Qdrant、production Playwright 和全栈 `41 passed` 均通过。

## 5. K6 时效、更新日历与核验

### 目标与分工

K5 provenance 完整后，K6 Backend 保存 update calendar、受影响回答和 revalidation run；Runtime 执行最小上下文的最新知识核验；Web 展示日历与并列结果。详细状态机、持久化与契约以 [K6 时效、更新日历与历史核验设计](../../design-docs/single-agent-v1-k6-freshness-revalidation-design.md) 为准。

### Backend/Runtime/Web 包

- publish/disable/delete/future effective event 形成权威 update event。按 citation revision 与变更成员的交集，异步标记候选回答 `possibly_stale`，并保留扫描进度、部分失败和重试证据。
- 当前单工作区中，已登录用户可对历史回答发起核验；管理员可查看日历、影响范围和失败任务。资源 ownership 将在 K9 补齐。重复请求使用 idempotency key，不覆盖已有核验结果。
- Runtime 仅接收原回答、原 citation、关联 update event 和 latest snapshot 的最小上下文，重新检索并生成独立 result。核验是新记录，不会重写原 answer。
- Web 并列展示原回答、当时引用、后续更新、最新核验与“仍有效/需更新/证据不足”结果。

### 验收门槛

replacement、disable、delete 均能产生正确日历与 stale 标记；无交集的回答不被误标。验证权限、重复核验、Runtime 失败、partial stale scan 和恢复后的可观测性。未来生效的定时发布尚未在 K6 启用，不能将它算作 K6 验收证据。

### K6 验收记录

- 状态：`verified`。
- Backend：`005` migration、可恢复 scan、calendar/snapshot API、idempotent run/result 与 worker 已落地；失败 scan 在有限重试后进入可观察 `failed`。
- Runtime：latest-scope-only revalidation、verdict extraction 与 marker stripping 已落地。
- Web：日历、scan/snapshot 详情和不改写历史 answer 的独立核验 UI 已落地。
- `p` 验收：真实 Postgres repository/Server K6 smoke、Backend、Runtime `64 passed`、Qdrant、Web production Playwright `4 passed` 与全栈 `41 passed` 均通过。

## 6. 阶段依赖与验收接力

| 阶段 | 启动条件 | 并行边界 | `p` 的验收重点 | 通过后解锁 |
| --- | --- | --- | --- | --- |
| `K3` | K2 verified | Backend delivery 与 Runtime projection | 事件至少一次投递、revision 幂等、未发布快照不变 | `K4` |
| `K4` | K3 integration green | API 后 Web | admin lifecycle、旧 API 兼容、审计不可变 | `K5` |
| `K5` | K4 active snapshot green | Backend/Runtime 后 Web | 每消息 scope、citation provenance、严禁 fallback | `K6` |
| `K6` | K5 provenance green | Backend/Runtime 后 Web | 日历准确性、stale 边界、独立核验记录 | `K7`、`K8` |

## 7. 后续包预览

### K7 管理证据与配置闭环

- 完成 revision、snapshot、calendar、delivery/DLQ、retrieval/generation/eval trace 的管理员查询与筛选。
- 让 prompt/style/`temperature`/`top_p`/检索参数真正传入 Runtime，并以不可变版本记录每次回答所用配置。
- 管理员能够从任意回答回溯“用什么知识、什么配置、何时生成、被什么更新影响”，且配置改动不会污染历史 provenance 或 latest snapshot 选择。

### K8 原生检索质量与模型比较

- 以冻结的 snapshot contract 为基线，把当前过渡性的 Python payload 扫描替换为真实 embedding、Qdrant dense+sparse/hybrid 查询、metadata filter 和 reranker。
- 修复 PDF、XLSX、PPTX 等七格式的解析、chunk、locator 与引用质量问题；所有改动先更新 harness，再跑 R5 和知识演进 fixture。
- 在相同数据集、snapshot、embedding、reranker 和 prompt 条件下比较至少两个本地模型，保存质量指标、失败分类、延迟和配置，不以主观体感选模型。

### K9 可靠性与内部试用

- 补齐会话、document、snapshot、回答和核验结果的 resource ownership；移除默认凭据与不受保护的 fallback 路径。
- 完成从空环境部署、migration、备份、升级、回滚、Streams pending/DLQ 恢复、健康检查和告警 runbook。
- 对未来工具能力建立权限、审批、审计、超时和失败隔离契约。完成内部试用的端到端权限、恢复和业务任务验收。
