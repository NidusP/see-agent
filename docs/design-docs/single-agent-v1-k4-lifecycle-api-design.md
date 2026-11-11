# 单智能体 V1 K4 管理员知识生命周期 API 设计

## 文档控制

- 状态：`active`
- 版本：`v1.0`
- 更新日期：`2026-07-14`
- 工作包：`K4`
- 上位设计：[知识演进设计](single-agent-v1-knowledge-evolution-design.md)、[知识演进契约](single-agent-v1-knowledge-evolution-contract.md)、[K3-K6 交付计划](../exec-plans/active/single-agent-v1-k3-k6-delivery-plan.md)。

## 1. 决策

- 新管理员写入只通过 `document -> immutable revision -> K3 delivery -> explicit publish` 主链；`KnowledgeFile` 继续作为 legacy compatibility facade，不可成为 revision/snapshot 的第二写入者。
- PostgreSQL 是 document、revision、snapshot、update event 与 idempotency record 的唯一权威。对象存储写入早于数据库事务时，失败只留下可回收 orphan object，不得创建可见 revision 或改变 active snapshot。
- K4 Backend 是 lifecycle 状态机和 active snapshot 的唯一 writer。Web 只展示服务端状态，不能根据轮询或 Runtime 成功自行推断 published。
- 所有 K4 mutation 必须由 admin 角色调用，并带 `Idempotency-Key`。同一 actor、operation、key、request hash 的重试返回原结果；同 key 不同请求返回 `409 idempotency_key_reused`。

## 2. K4 API 契约

所有路径均为 admin-only，并使用现有 JSON 错误 envelope。`GET` 路径不要求 idempotency key；所有 `POST` / `DELETE` 都要求该 header。

| 路径 | 行为 | 成功 | 关键失败 |
| --- | --- | --- | --- |
| `GET /api/kb/documents` | 分页列出逻辑文档及其当前 revision 摘要 | `200` | `401/403` |
| `POST /api/kb/documents` | multipart 上传初始内容，原子创建 document 与 initial revision，并 queue K3 | `202` | `400 missing_idempotency_key`、`409 idempotency_key_reused`、`422 invalid_upload` |
| `GET /api/kb/documents/{document_id}` | 读取 document、全部 revision 与 active snapshot membership 摘要 | `200` | `404 document_not_found` |
| `POST /api/kb/documents/{document_id}/revisions` | multipart 上传替换内容，创建新 immutable revision 并 queue K3 | `202` | `404 document_not_found`、`409 document_not_active` |
| `POST /api/kb/revisions/{revision_id}/publish` | 显式将 ready revision 发布为新的 active snapshot | `200` | `404 revision_not_found`、`409 revision_not_ready` |
| `POST /api/kb/documents/{document_id}/disable` | 从后续可见知识中移除该 document，创建后继 snapshot 或清空 active pointer | `200` | `409 document_not_active` |
| `DELETE /api/kb/documents/{document_id}` | 仅允许已 disabled document 软删除；物理对象/vector 清理留给后续可重试工作 | `200` | `409 document_not_disabled` |

### 2.1 Multipart 字段

`POST /api/kb/documents`：`file` 必填；`display_name`、`effective_at` 可选。  
`POST /api/kb/documents/{document_id}/revisions`：`file` 必填；`effective_at` 可选。  

服务端从文件内容计算 `checksum_sha256`，拒绝空文件、未知格式和与现有 immutable revision 发生 identity 冲突的请求。新对象 URI 必须以新的 revision id 隔离；同内容的新替换仍是新的 revision，除非相同 idempotency request 的重试。

### 2.2 返回形状

`KnowledgeDocument` 最小字段：`document_id`、`display_name`、`owner_id`、`status`、`created_at`、`updated_at`。  
`KnowledgeRevision` 最小字段：`revision_id`、`document_id`、`filename`、`mime_type`、`source_format`、`checksum_sha256`、`parse_version`、`index_version`、`embedding_model`、`effective_at`、`chunk_manifest_sha256`、`status`、`created_at`、`updated_at`。  
`RevisionDeliverySummary`：`delivery_id`、`status`、`attempt_count`、`last_error`、`updated_at`。  

create/revision upload 统一返回 `{ document, revision, delivery }`；publish/disable 返回 `{ document, snapshot, update_event }`；delete 返回 `{ document }`。detail 返回 document、revisions、active snapshot id 和每个 revision 的 delivery 摘要。Web 只依赖这些公开字段。

## 3. 写入路径与状态机

### 3.1 创建与替换

1. 校验 admin、idempotency key、multipart 文件和 document 状态。
2. 写入 revision-id 命名的对象；计算 checksum。
3. 单一 Postgres transaction 创建 document（初次）和 revision `uploaded`，写 uploaded event/outbox，再转为 `queued` 并写 index-requested outbox。
4. K3 relay 把 outbox 投递到 Runtime。Runtime ready 回写只改变该 revision；此时仍不改变 active snapshot。

事务失败时不创建 document/revision/event/outbox；对象留为 orphan 的清理候选。重复 request 不重复上传或新建 revision。

### 3.2 发布

仅 `ready` revision 可以发布。沿用 K2 的 active-pointer transaction：锁 pointer，复制旧成员、替换同 document 的旧 member，写 immutable manifest、snapshot、update event/outbox，再切换 pointer。重复 publish 对同一 active revision 返回同一 snapshot。K4 不允许空 snapshot。

### 3.3 Disable 与 delete

disable 在同一 transaction 中锁 document 和 active pointer，移除该 document 的 active member。若 document 仍包含 `uploaded`、`queued` 或 `indexing` revision，则返回 `409 document_has_inflight_revision`；K4 不把 in-flight revision 强行改为 `disabled`，以免留下无法 ack 的 K3 Streams pending delivery，也不放宽 K1 已冻结的 revision 状态图。

没有 in-flight revision 时：

- 仍有成员：创建新的 published snapshot，旧 active snapshot retired，document/revision 进入 `disabled`，写 `disabled` update event/outbox。
- 没有成员：retire 旧 snapshot，并将 active pointer 设为 `NULL`，不创建空 snapshot；document/revision 进入 `disabled`，写事件/outbox。

仅 `ready`、`published`、`superseded` revision 转为 `disabled`；`failed` 保持 failed，随后可直接转为 deleted。这样 disable 不会篡改失败诊断或 K3 delivery 状态。

delete 只允许 `disabled` document，写 document/revision `deleted` 和 `deleted` update event；不物理删除对象、Qdrant point 或 provenance。K5 的 scoped retrieval 会首先保证其不再可见，物理回收是 K9 运维任务。

## 4. 兼容、权限与迁移

- K4 追加 migration 创建 idempotency request ledger 和生命周期必要索引；不重写 K2 `001/002` 与 K3 `003` migration。
- 旧 `/api/kb/files` 继续可读。配置 Postgres 后，legacy upload 要映射到 K4 document/revision 主写入，返回旧 envelope 中可用的 `file_id` compatibility value；未配置 Postgres 的本地 smoke 可继续走 legacy in-memory path。
- 后端根据 session 中的 actor 做 admin 与 owner 校验；本单租户版本的 document owner 默认创建者。资源 ownership 的跨用户读模型强化留给 K9，但 K4 必须不允许普通用户访问任何 lifecycle route。

## 5. 验收与失败矩阵

- Backend：创建 DOCX/Markdown revision，K3 成功变 `ready`，显式 publish；同 document replacement 生成新 snapshot；disable/delete 不改变旧 snapshot/provenance。
- API：未登录 `401`、普通用户 `403`、缺 idempotency key `400`、相同 key 同请求稳定返回、相同 key 不同请求 `409`、非 ready publish `409`、含 in-flight revision 的 disable `409`、active document delete `409`。
- Web：管理员可上传、替换、刷新 indexing/ready/published/failed、publish、disable、delete；所有 destructive 操作有确认和错误态。
- Integration：真实 Postgres/Redis/MinIO/Qdrant 链覆盖 initial/replacement/disable/delete；最后一个 active document disable 后 active pointer 为 `NULL`，不产生空 snapshot。

## 6. 非目标

- 不在 K4 将 chat 改为 snapshot-scoped retrieval，也不写 assistant provenance。它们属于 K5。
- 不在 K4 自动 stale scan、update calendar 查询或 revalidation。它们属于 K6。
- 不在 K4 物理删除 MinIO/Qdrant 数据，避免破坏历史审计与 K5/K6 的证据链。
