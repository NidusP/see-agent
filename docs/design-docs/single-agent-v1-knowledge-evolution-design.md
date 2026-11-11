# 单智能体 V1 知识演进与回答时效设计

## 文档控制

- 状态：`active`
- 版本：`v1.1`
- 更新日期：`2026-07-14`
- 目标：让知识库可以持续增量更新，并让 Agent 能解释一条历史回答使用了什么知识、何时可能因后续更新而过期，以及如何使用最新知识复核。

## 1. 产品语义

系统不修改历史回答。每条回答都是“在回答时、使用某个已发布知识快照、根据具体引用得出的结论”。普通新问题默认使用最新已发布快照；当用户查看旧回答、引用旧回答或要求核验时，系统说明该回答的知识时点，并按最新快照重新验证。

“知识更新日历”不是把所有历史事件塞进 prompt，而是一个可查询的、追加式的版本事件记录。Runtime 只接收与当前问题、当前回答引用或用户要求核验的历史回答有关的精简更新上下文。

## 2. 边界与所有权

| 边界 | 权威 | 职责 |
| --- | --- | --- |
| 知识文档、revision、snapshot、更新事件、消息 provenance | Go Backend + PostgreSQL | 事务写入、状态机、权限、审计读取与发布决策。 |
| 原始文件与不可变 revision 对象 | MinIO | 以 revision 路径保存，延迟物理清理。 |
| 可检索 chunk 投影 | Python Runtime + Qdrant | 为每个 ready revision 建索引，通过 `snapshot_id`、`revision_id`、visibility payload 过滤检索。 |
| 派发、重试、outbox delivery | Backend + Redis Streams | 以 consumer group、ack、pending recovery、有限重试和 DLQ 可靠地把 revision 建索引与后续失效复核任务交给 worker；不拥有版本状态。 |
| 展示时间线、过期提示、核验入口 | Web | 只调用 Backend API，不自行推断过期或修改状态。 |

PostgreSQL 是唯一的业务写入权威。Qdrant、MinIO、Redis 都可能暂时落后；任何时候都不能因其成功写入而直接把 revision 或 snapshot 标为已发布。

## 3. 数据模型

### 3.1 逻辑文档与不可变 revision

- `knowledge_documents`
  - 稳定 `document_id`、display name、owner、lifecycle status（`active` / `disabled` / `deleted`）。
- `knowledge_revisions`
  - 稳定 `revision_id`、`document_id`、object URI、content hash、source format、parse/index version、uploaded/indexed/effective time、状态。
  - 每次上传替换文件时新建 revision，绝不覆盖旧对象或旧 chunk。
- `knowledge_revision_chunks`
  - 保存可审计的 chunk metadata、locator、content hash 和 Qdrant point reference；不是 Qdrant 的替代品。

revision 状态机：

```text
uploaded -> queued -> indexing -> ready -> published -> superseded
                              |                  |
                              v                  v
                            failed             disabled -> deleted
```

`failed` 可以在同一 revision 上重试；内容变更必须创建新 revision。`deleted` 先表示“不再属于任何 active snapshot”，对象与向量由保留策略异步清理，避免破坏历史回答审计。

### 3.2 知识快照与更新日历

- `knowledge_snapshots`
  - `snapshot_id`、parent snapshot、status（`building` / `published` / `retired`）、content manifest hash、created/published timestamp。
- `knowledge_snapshot_members`
  - 将 published snapshot 连接到具体 revision，并记录 inclusion status 与 revision content hash。
- `knowledge_update_events`
  - append-only event：`uploaded`、`indexed`、`published`、`replaced`、`disabled`、`deleted`、`reindexed`。
  - 记录 actor、event time、effective-from/effective-to、affected document/revision/snapshot、简短 diff summary、correlation id。

一个 snapshot 只能由一个串行 Backend 发布事务创建。发布时锁住 active snapshot 指针、验证所有成员 revision 均为 `ready`、写入 snapshot/members/event/outbox，然后原子切换 `active_snapshot_id`。索引未完成、任务失败或 manifest 不完整时保留旧 active snapshot。

### 3.3 回答 provenance 与时效

- 每条 assistant message 新增 `knowledge_snapshot_id`、answered time、freshness status（`current` / `possibly_stale` / `revalidated` / `unknown`）。
- 每条 citation 记录 `revision_id`、chunk content hash、source locator、rank 与 score。
- `answer_revalidation_runs` 记录何时、以哪个最新 snapshot、由哪种触发原因完成核验及结果。

初版不需要在知识更新时同步重写全部历史回答。Backend 根据 update event 与 citation revision 的交集把潜在受影响回答标记为 `possibly_stale`；后台任务按优先级重核验，用户也可手动请求核验。

## 4. 读取与写入流程

### 4.1 上传、替换与发布

1. 管理员上传对象，Backend 创建 document 或 revision，状态为 `uploaded`，写入 outbox。
2. Worker 取任务，Runtime 解析并将 revision chunks 写入 Qdrant 的隔离 revision namespace/payload，结果回传 Backend。
3. Backend 校验结果、写入 chunk manifest，把 revision 标记为 `ready`。
4. 管理员显式发布或按已冻结策略自动发布。Backend 建立 `building` snapshot，复制上一 active snapshot 成员并替换目标 revision。
5. Backend 事务内发布 snapshot、记录 update event、切换 active pointer、写入 stale-detection outbox。
6. 新问题从此使用新 active snapshot；旧 snapshot 仍可审计和复放。

### 4.2 正常问答

1. Backend 在每条用户消息开始时读取 active published snapshot，而不是沿用 conversation 的旧 snapshot。
2. 将该 snapshot id、会话 config/retrieval version 和 query 传入 Runtime。
3. Runtime 仅检索该 snapshot 成员 revision 的 Qdrant points，返回 revision-aware citations。
4. Backend 以该 snapshot 和 citations 写 assistant message provenance。

### 4.3 历史核验

当用户选择“核验此回答”或问“之前的回答还准确吗”：

1. Backend 读取旧 message provenance 与随后发生的相关 update events。
2. Runtime 以最新 active snapshot 重新检索/回答，并收到最小必要的“旧回答/旧引用/相关更新”上下文。
3. Backend 创建新的 revalidation result，不覆盖旧 message；Web 并列展示“当时回答”和“当前核验”。

## 5. API 与 UI 契约方向

保持当前上传、聊天和 sources 路由兼容，新增或扩展：

- `POST /api/kb/documents/{document_id}/revisions`：新 revision。
- `POST /api/kb/documents/{document_id}/disable` 与 `DELETE /api/kb/documents/{document_id}`：先改变 active snapshot 可见性，再异步回收物理产物。
- `GET /api/kb/updates`：管理员更新日历，支持 document/snapshot/time filter。
- `GET /api/kb/snapshots/{snapshot_id}`：管理员查看 manifest 与成员。
- `POST /api/chat/conversations/{conversation_id}/messages/{message_id}/revalidate`：以 latest snapshot 核验历史回答。
- `GET /api/chat/conversations/{conversation_id}/sources/{message_id}`：补充 revision、snapshot 与 freshness metadata。

普通用户只可看到与自己可见回答关联的 freshness 摘要；知识日历、document revision、snapshot manifest 和全局日志均为 admin-only。

## 6. 一致性、失败与恢复

- PostgreSQL transaction + outbox 保证业务状态与异步投递不会出现“双写成功一半就发布”的状态。
- Qdrant/MinIO 写入采用 revision id 幂等；worker 重试不得创建第二个 published revision 或 snapshot。
- 发布使用 optimistic version/active pointer lock；并发上传可以分别索引，但 snapshot publication 串行。
- 删除/禁用优先从 active snapshot 移除；Qdrant/MinIO 清理失败只产生可重试运维任务，不能让已删除知识重新出现在新回答中。
- Runtime 无法读取指定 snapshot 时，返回明确可重试错误；Backend 不得静默改为另一个 snapshot。

## 7. 评测与验收

每个阶段至少覆盖：

- 新文件发布后，新问题使用新 snapshot。
- 文档 revision 替换后，旧回答保持原 provenance，新核验引用新 revision。
- disable/delete 后，新 snapshot 检索不到原 revision；历史审计仍可解释。
- indexing 失败、重复投递、并发发布和 Qdrant 清理失败不改变 active snapshot。
- effective date 在未来时，默认问答不把未来规则当作已生效事实。
- 日历查询、snapshot manifest、revalidate API 均验证权限与资源 ownership。
- 所有 RAG/prompt/model 实验重跑既有 R5 gate，并增加 knowledge-evolution eval suite。

## 8. 技术栈决定

保留现有 Next.js、Go、Python、PostgreSQL、Redis、MinIO、Qdrant 和 OpenAI-compatible Ollama/vLLM 边界。Phase 1 将现有 Redis List adapter 升级为 Redis Streams consumer group + Postgres outbox；Phase 4 将 Qdrant payload 全扫描替换为真实 embedding/reranker、metadata-filter 与 hybrid retrieval。当前不引入 Kafka、Temporal、LangChain/LlamaIndex 或新的向量数据库。

触发重新评估而非立即替换的条件：多 region 事件复制、跨服务长时审批工作流、Qdrant 无法满足经评测证明的检索吞吐/过滤需求，或模型服务并发已超过 Ollama 的开发定位。
