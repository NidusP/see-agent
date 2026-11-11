# 单智能体 V1 K5 快照检索与回答 Provenance 设计

## 文档控制

- 状态：`active`
- 版本：`v1.0`
- 更新日期：`2026-07-14`
- 工作包：`K5`
- 上位设计：[知识演进契约](single-agent-v1-knowledge-evolution-contract.md)、[知识演进设计](single-agent-v1-knowledge-evolution-design.md)、[K3-K6 交付计划](../exec-plans/active/single-agent-v1-k3-k6-delivery-plan.md)。

## 1. 决策与边界

- 每条新用户消息在 Backend 开始处理时，只读取一次 K4 active published snapshot。该 snapshot 的 id、manifest hash、member revision ids 构成不可变 `KnowledgeScope`；K4 在回答过程中发布新 snapshot 不会改变本次 scope。
- K5 生产路径要求 Postgres `KnowledgeLifecycleStore` 与 active snapshot。不存在 active snapshot、成员为空、manifest 不一致或 Runtime 无法建立 revision filter 时，返回 retryable `knowledge_snapshot_unavailable`，不写入 user/assistant exchange，也不退回全量或 legacy vector search。
- Runtime 不拥有 snapshot 指针，也不从 Qdrant 猜 scope。它只信任 Backend 传来的 scope，并逐一约束检索 candidate 与 citation revision。
- Backend 在同一个持久化 transaction 写 assistant message 的 `knowledge_snapshot_id`、manifest hash、answered time、freshness `current`，citation 的 document/revision/chunk content hash/snapshot/rank/score/locator，以及现有 answer provenance record。历史消息不会被 K5 改写。
- 未配置 Postgres 的 legacy memory smoke 保持显式开发兼容路径，标记为 `unknown` 且不得用于 K5 生产验收。只要启用 lifecycle Postgres store，缺 scope 必须失败。

## 2. Backend 读写路径

### 2.1 Non-stream 与 stream 统一准备

1. 读取 conversation 的 immutable config/retrieval/model version。
2. 对 lifecycle Postgres store 读取 active snapshot，并验证 `published`、manifest 和 member revision ids 非空。
3. 创建唯一 request/message ids，将 `KnowledgeScope` 附入 `ChatRuntimeRequest`。
4. 调用 Runtime；Runtime response 的 snapshot id/manifest、每条 citation document/revision/chunk hash 必须与 request scope 匹配。
5. 在单个 transaction 写 user/assistant exchange、provenance 与 citations；任何 mismatch 或写入失败都不产生成功 answer。

stream path 在 `message.started` 前已固定 scope。`message.completed` 使用同一 request scope 写入；stream 被中止或 Runtime 出错时不伪造 provenance。non-stream 与 stream 不可存在不同的 scope 选择规则。

### 2.2 Response 与 sources

- Runtime response 必须回显实际 `knowledge_snapshot_id` 和 manifest hash，`freshness_status=current`。
- `GET /sources` 从持久化 message/citation provenance 返回 snapshot id、manifest hash、freshness 与 revision-aware source fields，不从当前 active snapshot 反推。
- Backend 在同一 persistence transaction 中以 snapshot member/revision 权威关联验证每条 citation 的 `document_id -> revision_id` 归属；拒绝 scope 之外 revision、错误 document、缺失 revision/chunk content hash 或不一致 snapshot 的 response，稳定错误码为 `snapshot_scope_violation`。

## 3. Runtime 检索路径

- `KnowledgeScope.revision_ids` 是唯一的 normal-answer retrieval allow list。Runtime 在 vector-store boundary 接收 `revision_ids`，InMemory 与 Qdrant 都必须应用该 filter。
- Qdrant filter 构建失败、scroll/query 返回无 scope metadata、任一 returned payload 不在 allow list 时视为 retryable execution failure；不得再发一次无 filter query。
- retrieval pipeline 将 scope 的 snapshot id 写入 `RetrievedSource`，并从 revision payload 取 document/revision/chunk content hash。answer pipeline 原样回传这些 citation fields。
- legacy no-scope request 只保留给明确的 non-lifecycle dev smoke；当 Backend 传入 lifecycle execution marker/scope 时，scope 缺失或空成员一律拒绝。

## 4. Web 展示边界

- Chat message 与 citation panel 显示“回答知识版本”、snapshot manifest 摘要、freshness 和 source revision，帮助用户解释当时答案；不显示 K6 尚不存在的 stale/revalidation 控制。
- 无 snapshot 的 legacy answer 使用 `unknown` 明示为旧兼容数据，不能被渲染成 current。
- Runtime `knowledge_snapshot_unavailable` 或 Backend `snapshot_scope_violation` 使用局部发送失败态与重试，不能插入看似成功但无来源的 assistant message。

### 4.1 C1 回答内证据锚点

- `C1` 在 K5 已持久化的消息级 citation 之上，增加 `citation_anchors`：每项记录回答正文的 Unicode rune 起止位置以及一个或多个 citation rank。
- 锚点只引用本次 Runtime response 中的 citation rank；Backend 在写入前验证 rune 边界、非空范围、rank 唯一性和 snapshot-scoped citation 权威性。验证失败返回 `runtime_citation_anchor_violation`，整次 exchange 不写成功消息。
- 锚点与 assistant message、citation 在同一 Postgres transaction 写入。历史消息不回填、不改写；它们保持消息级 citation 展示，不被伪装为精确结论引用。
- Web 使用锚点给正文加下划线，hover/focus 懒加载既有 `/sources` 数据并展示来源卡；点击可固定详情。没有锚点、来源请求失败或用户使用旧消息时，都不得推断新的下划线范围。

## 5. 验收矩阵

- A/B snapshot：消息在 A 期间固定 A，之后发布 B 的新消息使用 B；旧消息显示 A。
- scope filter：Qdrant 中遗留、disabled 或不属于 scope 的 revision point 永不进入 citation；不同 document members 均能命中。
- failure：无 active snapshot、空 scope、manifest/snapshot mismatch、Runtime citation mismatch、Qdrant filter failure 都不写成功 exchange。
- persistence：重启 Backend 后 assistant message 与 sources 仍返回原 snapshot/manifest/revision/chunk hash；不读取最新 pointer 替换它。
- UI：citation panel 能显示 current/unknown、snapshot 与 revision；scope error 能重试。

## 6. 非目标

- 不创建 update calendar、stale detector 或 revalidation result；它们属于 K6。
- 不升级 dense/sparse/hybrid retrieval 或 reranker；它们属于 K8。
- 不改变 K3 delivery 和 K4 lifecycle 的写入语义。
