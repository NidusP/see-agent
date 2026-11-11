# 单智能体 V1 知识演进持久化设计

## 文档控制

- 状态：`active`
- 版本：`v1.0`
- 更新日期：`2026-07-14`
- 工作包：`K2`
- 上位设计：[知识演进与回答时效设计](single-agent-v1-knowledge-evolution-design.md)、[知识演进契约](single-agent-v1-knowledge-evolution-contract.md)

## 1. 目标与非目标

K2 将当前 Backend 的启动期 schema bootstrap 演进为 versioned Postgres migration，并提供 revision、snapshot、event 和 outbox 的唯一持久化写入路径。它不接 Redis Streams、Runtime/Qdrant revision 投影、管理员 API/UI、普通问答 snapshot scope 或历史回答核验。

新旧文件 API 继续可用。`knowledge_files` 仍服务现有 UI；新的表才是将来知识语义的权威。K2 不把 legacy file 直接发布为新知识。

## 2. 迁移与启动顺序

1. 创建 `schema_migrations(version, name, applied_at)`，在事务中按版本顺序应用 migration。
2. `001_legacy_core` 创建当前 config/conversation/file/task/chunk 表和索引，全部使用 `IF NOT EXISTS`，可接管已有开发库。
3. `002_knowledge_evolution` 创建新表、扩展 message/citation provenance 列和索引；不删除或改写旧列。
4. 启动时保留既有默认 seed，然后运行幂等 `reconcileLegacyKnowledge`：每个 legacy `knowledge_files.status = succeeded` 至多生成一个 `knowledge_document` 和一个 `legacy_unpublished` revision。
5. 最后同步各 ID counter。任何 migration 或 reconciliation 失败都终止 Backend 启动，不切换 active snapshot。

Migration 只追加，不使用 schema reset。reconciliation 可在每次启动运行，因为它通过唯一 `legacy_file_id` 保证幂等；这样新空库的 seed 和已有库都能得到一致处理。

## 3. 权威表

| 表 | 主键与关键字段 | 约束与作用 |
| --- | --- | --- |
| `knowledge_documents` | `document_id`, `display_name`, `owner_id`, `status` | 逻辑文档，状态仅为 `active/disabled/deleted`。 |
| `knowledge_revisions` | `revision_id`, `document_id`, `legacy_file_id`, object URI, checksum, parse/index/embedding version, effective time, status | 不可变内容版本；`legacy_file_id` unique 且可空；`legacy_unpublished` 不能发布。 |
| `knowledge_revision_chunks` | `(revision_id, chunk_id)`, content hash, locator, vector point ref | 审计 chunk manifest，不替代 Qdrant。 |
| `knowledge_snapshots` | `snapshot_id`, parent id, `status`, `manifest_hash`, timestamps | `building/published/retired` 的 immutable member manifest。 |
| `knowledge_snapshot_members` | `(snapshot_id, revision_id)`, content hash | 一份 snapshot 只包含指定 revision 的 hash。 |
| `knowledge_active_snapshot` | singleton row, `snapshot_id`, `lock_version` | publication 时 `FOR UPDATE` 的唯一 active pointer。 |
| `knowledge_update_events` | `event_id`, type, document/revision/snapshot, actor, correlation id | append-only 更新日历基础。 |
| `knowledge_outbox` | `delivery_id`, topic, payload, attempt/status/available time | 事务内写入，K3 再派发到 Redis Streams。 |
| `message_knowledge_provenance` | `message_id`, snapshot, manifest, answered time, freshness | K5 写入；K2 只建立表。 |
| `answer_revalidation_runs` | `revalidation_id`, original message, snapshot, status | K6 写入；K2 只建立表。 |

`messages` 和 `citations` 也只增不删地追加 provenance 列，维持现有读取兼容。

## 4. Repository 写入契约

K2 新增独立 `KnowledgeEvolutionRepository`，不扩展旧 `repository.Store`，因此 MemoryStore 和现有 Web API 不被迫伪实现新语义。

- `CreateDocument`：创建 logical document；不发布。
- `CreateRevision`：写 immutable revision，初始为 `uploaded`；revision id 已存在时只接受完全相同内容的幂等重试。
- `MarkRevisionReady`：只允许 `queued/indexing -> ready`，要求 checksum 与 chunk manifest hash，写 `revision.ready` outbox。
- `PublishRevision`：唯一的发布事务。锁 active pointer 和目标 revision，验证状态 `ready`、effective time 已到、manifest hash 一致；复制旧 snapshot 成员并替换同 document revision；写新 snapshot/members/event/outbox；将旧同 document revision 标为 `superseded`；把旧 snapshot `retired`；更新 active pointer；提交。
- `GetActivePublishedSnapshot`：只返回 pointer 指向且状态为 `published` 的 snapshot，否则返回稳定 `ErrActiveSnapshotUnavailable`。

同一 `revision_id` 的重复 publish 返回已创建的 snapshot，不创建第二个 snapshot。不同 revision 的并发 publish 由 active-pointer row lock 串行化。

## 5. Manifest 与错误

manifest hash 是按 `revision_id + checksum_sha256` 的字典序列表计算的 SHA-256。发布时 Repository 从数据库成员重算，不信任调用方传入的 hash。

稳定错误包括：`ErrRevisionNotFound`、`ErrRevisionNotReady`、`ErrRevisionManifestMismatch`、`ErrRevisionNotEffective`、`ErrActiveSnapshotUnavailable`。它们是 K4/K5 HTTP 错误映射的依据，K2 不新增 HTTP handler。

## 6. Legacy reconciliation

每条成功 legacy file 生成：

- `document_id = doc_legacy_{file_id}`
- `revision_id = rev_legacy_{file_id}`
- `status = legacy_unpublished`
- `legacy_file_id = file_id`
- object metadata 从 `knowledge_files` 复制

由于旧 file 没有稳定原始内容 hash、revision-qualified Qdrant point 或 manifest，K2 不给它伪造 `ready`。K4/K3 的受控 reindex 会创建实际 revision 并最终发布。旧 file、task、chunk、message 和 citation 永远不删除。

## 7. 验收与回滚

- 空库与现有库重复启动后 migration version 和 legacy revision 数量稳定。
- 非 ready、future effective 或 manifest 不一致的 revision 不能改变 active pointer。
- 两个并发 publish 最终线性化为两个可审计 snapshot，后一个包含正确成员。
- 发布事务任一写入失败时旧 active snapshot 完好，且没有半份 published snapshot。
- 真实 Postgres smoke 查询 migration ledger、legacy revision、outbox 和 active snapshot。

回滚只通过停止部署并使用旧已发布 snapshot 读取；不删除 migration 或已发布 snapshot。修复通过新的 migration 或事务重试完成。
