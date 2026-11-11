# 单智能体 V1 知识演进契约

## 文档控制

- 状态：`active`
- 版本：`v1.1`
- 更新日期：`2026-07-14`
- 工作包：`K1`
- 上位设计：[知识演进与回答时效设计](single-agent-v1-knowledge-evolution-design.md)

## 1. 契约目的

本文件冻结 K2-K6 共用的名称、状态、跨服务字段和 API 兼容规则。它不实现数据库迁移、队列 worker 或页面；实现阶段不得改变本文件定义的写入权威和 snapshot 语义，除非先更新产品规格、上位设计和本文件。

## 2. 领域对象

| 对象 | 稳定标识 | 权威 | 不变量 |
| --- | --- | --- | --- |
| Logical document | `document_id` | PostgreSQL | 同一业务文档稳定，内容不可直接覆盖。 |
| Immutable revision | `revision_id` | PostgreSQL + MinIO object | 内容变化创建新 revision；content hash 不变。 |
| Published snapshot | `snapshot_id` | PostgreSQL | 只引用 `ready` revision；member manifest 有 hash。 |
| Update event | `event_id` | PostgreSQL | append-only，关联有效时间和 correlation id。 |
| Outbox delivery | `delivery_id` | PostgreSQL | 可重投，不拥有 revision/snapshot 状态。 |
| Assistant provenance | `message_id` | PostgreSQL | 固定回答实际使用的 snapshot、时间和 citation revision。 |
| Revalidation run | `revalidation_id` | PostgreSQL | 只新增结果，绝不覆盖历史 assistant message。 |

`KnowledgeFile`、`file_id` 和 `/api/kb/files` 是过渡兼容层：旧读写可继续工作，但 K2 起必须映射到一个 logical document 与 immutable revision。

## 3. 状态和写入规则

```text
revision: legacy_unpublished|uploaded -> queued -> indexing -> ready -> published -> superseded
                                      \-> failed
published|superseded|ready -> disabled -> deleted
snapshot: building -> published -> retired
```

- 只有 Backend 的 publish transaction 可以创建 snapshot、写 member manifest、记录 `published` event 和切换 active pointer。
- Runtime 只能回报 revision 的索引结果；Redis Streams 的 delivery/ack/retry/DLQ 不能改变 published status。
- `deleted` 先移出 active snapshot；MinIO/Qdrant 物理清理是独立可重试工作，失败不能让新回答重新看到旧 revision。
- `legacy_unpublished` 只由 K2 的旧 `knowledge_files.status = succeeded` reconciliation 创建；它没有可靠 content hash 或 revision-aware vector projection，必须重新索引后才能变为 `ready`，不得进入 snapshot。

## 4. 运行时契约

### 4.1 摄取请求与结果

`ingestion-task` 与 `ingestion-task-result` 在保留 `file_id` 兼容字段的同时，新增可选的 `document_id`、`revision_id`、`delivery_id`、`attempt`、`content_hash`、`chunk_manifest_hash` 与 `embedding_model`。K3 启用 Streams 后，`revision_id`、`delivery_id` 和 `content_hash` 变为必填；同一 `delivery_id` 的重复执行必须返回等价结果。

### 4.2 正常问答

Backend 每条新用户消息都传递：

```json
{
  "knowledge_snapshot_id": "snap_20260714_001",
  "knowledge_scope": {
    "snapshot_id": "snap_20260714_001",
    "manifest_hash": "sha256:...",
    "revision_ids": ["rev_..."],
    "resolved_at": "2026-07-14T00:00:00Z"
  },
  "answer_mode": "normal"
}
```

`knowledge_snapshot_id` 保留是为了兼容当前 Runtime；K5 启用后，`knowledge_scope.snapshot_id` 必须与它相同。Runtime 只检索 `revision_ids`，无法建立 scope 时返回明确、可重试的失败，不执行全量或旧 snapshot 查询。

客户端普通发消息不能传入或决定 snapshot。Backend 为一个带幂等键的消息请求首次解析 scope 后，任何重试都复用该 scope，即使期间已有新 snapshot 发布。

### 4.3 Citation 与回答 provenance

每个 Runtime citation 追加 `document_id`、`revision_id`、`chunk_content_hash` 和 `knowledge_snapshot_id`。`chat-runtime-response` 追加实际 `knowledge_snapshot_id`、`knowledge_snapshot_manifest_hash` 与 `freshness_status`。K5 的 Backend 把这些字段与 assistant message 一次性持久化。

### 4.4 历史回答核验

`answer_mode` 取值为 `normal` 或 `revalidate`。`revalidate` 必须提供 `revalidation_context`：原 message id、原 snapshot id、原回答、原 citation revision ids 和相关 update event ids。Runtime 只能把这部分作为最小上下文重新检索最新 scope；它不得修改原回答或原 citation。

## 5. 对外 API 冻结与兼容

| 能力 | 冻结 API | 实现阶段 | 兼容规则 |
| --- | --- | --- | --- |
| 新逻辑文档 | `POST /api/kb/documents` | `K4` | 旧 `POST /api/kb/files` 映射到新 document + initial revision。 |
| 替换内容 | `POST /api/kb/documents/{document_id}/revisions` | `K4` | 不覆盖已有 revision。 |
| 发布 | `POST /api/kb/revisions/{revision_id}/publish` | `K4` | 只接受 `ready` revision，带 idempotency key。 |
| 失效/删除 | `POST /api/kb/documents/{document_id}/disable`、`DELETE /api/kb/documents/{document_id}` | `K4` | 先改变 snapshot 可见性，后异步清理。 |
| 日历/manifest | `GET /api/kb/updates`、`GET /api/kb/snapshots/{snapshot_id}` | `K6` | admin-only。 |
| 核验 | `POST /api/chat/conversations/{conversation_id}/messages/{message_id}/revalidate`、`GET /api/chat/conversations/{conversation_id}/revalidations/{revalidation_id}` | `K6` | 新建核验结果，不修改历史 message。 |

K4 的知识 lifecycle mutation 需要 admin 角色、`Idempotency-Key` 和明确的 `409` 非法状态错误。K6 revalidate 是已登录用户动作，同样要求 `Idempotency-Key`；当前单工作区实现的 conversation/resource ownership 留给 K9 补齐。K4 起将这些 path 与 schema 写入 OpenAPI；K1 的字段定义先由 `packages/contracts/*.schema.json` 冻结。

## 6. 错误与测试名称

| 情况 | 稳定错误码 | 最低测试 |
| --- | --- | --- |
| 无 active snapshot | `knowledge_snapshot_unavailable` | `test_chat_rejects_missing_snapshot_scope` |
| scope 与 snapshot id 不一致 | `knowledge_scope_mismatch` | `test_chat_rejects_scope_snapshot_mismatch` |
| 非 ready revision 发布 | `revision_not_ready` | `test_publish_rejects_non_ready_revision` |
| 重复投递 | `duplicate_delivery` | `test_delivery_is_idempotent` |
| 删除的 revision 回到检索 | `snapshot_scope_violation` | `test_retrieval_excludes_out_of_scope_point` |
| 非法历史核验 | `revalidation_invalid` | `test_revalidation_preserves_original_message` |

K1 只验证 schema/DTO 可表达这些场景；K2-K6 分别实现并通过对应 repository、Runtime、API 和浏览器测试。
