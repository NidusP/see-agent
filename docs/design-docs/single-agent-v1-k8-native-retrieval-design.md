# 单智能体 V1 K8 原生 Dense Retrieval 设计

## 文档控制

- 状态：`verified`
- 版本：`v1.1`
- 更新日期：`2026-07-16`
- 工作包：`K8-R1`
- 依赖：K3 immutable revision projection、K5 snapshot scope、K7 retrieval config/evidence、固定 R5 harness

## 1. 决策

`K8-R1` 将 Runtime 的 Qdrant 路径从“scroll 全部 payload 后在 Python 词法评分”升级为真实 dense vector 写入和 Qdrant `/points/query`。首个切片只改变一个评测变量：检索实现从 lexical baseline 到 OpenAI-compatible dense embedding + Qdrant native query。

不在本切片实现 sparse vector、hybrid fusion、provider reranker、模型比较或新的 Web 控件；它们分别属于后续 K8 子阶段，不能与 dense 检索同时改变。

## 2. 外部与配置契约

### 2.1 Runtime 部署配置

| 变量 | dense 模式要求 | 说明 |
| --- | --- | --- |
| `RUNTIME_RETRIEVAL_MODE` | `native_dense` | 未设置时保持 `lexical` 兼容模式。 |
| `QDRANT_URL` | 必填 | native dense 不允许 in-memory fallback。 |
| `QDRANT_COLLECTION` | 建议新 collection | 默认 `see_agent_dense_chunks_v1`；不得复用旧的 size=1 lexical collection。 |
| `EMBEDDING_BASE_URL` | 必填 | OpenAI-compatible `/v1/embeddings` 服务地址；Ollama 兼容。 |
| `EMBEDDING_MODEL` | 必填 | 写入 chunk/revision 的真实 provider model 名称。 |
| `EMBEDDING_DIMENSIONS` | 必填、正整数 | 创建/校验 Qdrant collection 的 vector size。 |
| `EMBEDDING_API_KEY` | 可选 | 仅在 provider 需要时发送 Bearer token。 |
| `EMBEDDING_TIMEOUT_SECONDS` | 可选，默认 15 | embedding 请求超时。 |

默认 lexical 模式保留现有 `LexicalEmbeddingAdapter`、InMemory/Qdrant scroll 路径，仅用于既有轻量测试与开发兼容，不能被标记为 native dense。

### 2.2 Backend 到 Runtime

`RuntimeResponseOverrides` 新增可选 `embedding_model`。Backend 把 conversation 固定的 retrieval config 中该值发送给 Runtime；在 `native_dense` 模式，Runtime 必须与 `EMBEDDING_MODEL` 完全匹配，否则拒绝执行。这样管理台的 retrieval config 不会仅被记录而未真正参与回答链路。

`top_k` 与 `rerank_enabled` 契约保持不变。R1 仍使用现有确定性 rerank；它在 evidence 中如实记录，不能称作 provider reranker。

## 3. 数据与写入路径

```text
immutable revision delivery
  -> parser/chunker
  -> embedding provider (chunk batch)
  -> Qdrant upsert(real dense vector + immutable payload)
  -> Runtime returns manifest + EMBEDDING_MODEL
  -> Backend marks revision ready / publishes snapshot
```

- Qdrant 是派生索引，不拥有 document/revision/snapshot 状态。
- point id 继续由 immutable chunk id 派生，revision retry 只删除并重写同一 revision。
- dense revision retry 的删除计数和删除本身使用 Qdrant 的 filtered `points/count` 与 `points/delete`；不得为了计数 scroll payload。
- payload 至少包含 `document_id`、`revision_id`、`visibility`、`source_format`、`file_id`、`embedding_model`、chunk provenance 与 locator；为 query filter 创建对应 keyword index。
- dense collection 在创建时使用 `EMBEDDING_DIMENSIONS` 和 cosine distance。已存在 collection 的 size 或 distance 不匹配时，Runtime 启动失败并给出新 collection/reindex 指示，不得改变旧 collection schema。
- 旧 lexical revision、历史 citation、answer provenance 均不回填或改写。切换 dense 后必须重新索引并发布新的 immutable revision/snapshot。

## 4. 读取路径与 scope

```text
ChatRuntimeRequest(knowledge_scope + retrieval config)
  -> validate native embedding model
  -> query embedding
  -> POST /collections/{collection}/points/query
       filter: revision_ids + optional file_ids/source_formats
  -> validate returned payload provenance and scope
  -> optional deterministic rerank -> top_k -> citations
```

- `knowledge_scope.revision_ids` 仍是唯一 normal-answer allow list。
- Qdrant query 必须在服务端携带 revision filter；不得先无 filter query 再在 Python 过滤，也不得在失败后 scroll/full-scan 回退。
- returned payload 缺少 revision/document/chunk hash、或 revision 不在 scope 时，保持 K5 的 retryable `knowledge_snapshot_unavailable` / scope failure 语义，不写成功回答。
- 对 legacy no-scope 开发请求，native dense 仍可使用显式 file/source filters；生产 lifecycle chat 不允许缺 scope。

## 5. 失败与回滚

- dense 配置缺失、embedding model 与 retrieval config 不匹配、dimension 非法或 collection schema 不兼容：Runtime 启动 fail-closed。
- embedding provider 请求、响应形状、向量维度、NaN/Infinity 或 Qdrant query/upsert 失败：ingestion 标记失败；chat 返回可重试 Runtime failure；不得使用 lexical 结果伪装成功。
- Qdrant filter/query 失败：有 scope 的请求必须保持 K5 scope failure，不可改为全量检索。
- 回滚只需令 Runtime 使用旧 `lexical` deployment config 或旧服务版本；dense collection 和新 revision 保留为派生数据，Postgres 权威历史不受影响。

## 6. 验收

- provider adapter 覆盖成功、认证头、超时/HTTP/空向量/维度异常。
- Qdrant mock 覆盖 real vector upsert、`/points/query`、revision/file/source-format filters、scope payload 验证和“不调用 scroll”的负例。
- `make runtime-native-qdrant-smoke` 在真实 Docker Qdrant 上以固定向量覆盖 native collection、filtered query、scope 隔离和 filtered delete；它只验证 Qdrant transport，不能替代 provider smoke。
- Runtime startup 覆盖 lexical 默认、dense 缺配置、collection mismatch、embedding config mismatch。
- 真实 Docker Qdrant + embedding provider smoke 覆盖 ingest、Runtime reload、native query、scope 隔离与 citation provenance。
- 每次 K8 变更必须重跑 Runtime tests、Qdrant smoke、K5/K6/C1/C2 fixture、R5 prompt_v2 gate；报告记录 embedding provider/model/dimension/collection/retrieval config。

## 7. 明确非目标

- sparse/hybrid vector 和 fusion；
- provider reranker；
- 改写历史 revision/message/citation/provenance；
- 自动迁移或就地改变旧 Qdrant lexical collection；
- 放宽 K5 snapshot scope 或 R5 gate。

## 8. K8-R1 验收记录

- `2026-07-16`：本机 Ollama `all-minilm:latest` 经 OpenAI-compatible `/v1/embeddings` 实测返回 `384` 维向量。
- `make runtime-native-dense-smoke` 通过：使用独立 temporary Qdrant collection，完成 revision ingestion、真实 embedding 写入、Runtime reload、`/points/query` revision scope 和 citation document/revision provenance 断言；collection 已在 finally 清理。
- Runtime regression `89 passed`；词法 compatibility 与 native Qdrant transport smoke 通过。
- 最终 prompt_v2 R5 gate 通过：`30/40`、grounded correctness `0.6571`、retrieval/citation `0.9714`、refusal `1.0`、unexpected runtime error `0`。
- 结论：`K8-R1` 的 dense implementation 和 fail-closed provider boundary 已验证。现有 deployment 仍保持 lexical，任何切换仍须创建匹配 `embedding_model=all-minilm` 的 retrieval config、重新索引 immutable revision 并发布新 snapshot；历史回答和 provenance 不改写。

## 9. Dense-only 退役决策（待 K8-R2 后执行）

### 决策

- 目标：`K8-R2` 选定 embedding model 并完成业务知识 reindex/snapshot cutover 后，生产请求只走 native dense；lexical 不再作为正常请求的 fallback。
- 边界：PostgreSQL 继续是 document/revision/snapshot/config/message 的唯一权威；Qdrant dense collection 是由 immutable revision 派生的可重建投影。
- Owner 与写入路径：Backend 创建 revision、验证 Runtime 回执、原子发布 snapshot；Runtime 写同一 revision 的 Qdrant points；任何一方都不能通过直接修改 Qdrant 改写 snapshot。
- 一致性：使用 PostgreSQL authority、immutable identity、幂等 delivery、published snapshot allow-list 和 Runtime payload validation；不要求 PostgreSQL/Qdrant 跨库原子提交。
- 失败行为：dense provider、collection 或 scope 失败时 fail-closed；不得对同一请求回退 lexical。Qdrant 孤儿 point 或丢失 point 通过 reindex/reconcile 修复，不能成为错误 citation。
- 拒绝的选项：现在立即删除 lexical adapter/collection。原因是旧 conversation 固定 retrieval config、历史 revalidation 和回滚窗口仍可能需要可解释的兼容路径；删除会把可控切换变成不可逆的生产变更。
- 删除前验证：第二 embedding model 对比通过；要切换的 revision 全部以选定模型 reindex 并发布新 snapshot；新/旧 conversation、revalidation、retry、Qdrant 丢失和回滚均有验收；管理员 evidence 能显示模型/config/snapshot 事实。完成后再进行一次单独的 lexical removal 工作包。
