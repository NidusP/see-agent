# P0 Fail-Closed 回归矩阵

## 文档控制

- 状态：`F0 baseline frozen`
- 更新日期：`2026-07-24`
- 目标：把目前仍存在的伪成功/隐式降级调用点变成可定位的负向验收项。本文不是“允许这些行为”的清单；每一行都必须在所列实现波次结束后反向通过。
- 关联：[P0 hardening plan](../exec-plans/active/single-agent-v1-production-fail-closed-hardening-plan.md)、[A1 disposition design](../design-docs/single-agent-v1-clarification-policy-design.md)

## 1. 使用规则

- F0 不把故意失败的断言加入默认绿色 suite；否则会阻断当前尚未实施的 F1–F8。每个案例先以 fixture/明确 repro 固化，实施波次把它变成常规负向自动化测试。
- 测试 provider 只能通过显式 fixture 或依赖注入安装，并在报告中标记 `test_provider=true`。它只能证明错误传播与数据事务，不证明模型质量。
- 每个案例同时验证 HTTP/JSON、SSE（若适用）、Postgres 持久化和 Web 可见状态；“没有异常”不是通过标准。

## 2. 当前 inventory 与反向验收

| id | 当前调用点/触发 | 当前伪成功或风险 | F0 fixture / 目标断言 | 实现波次 |
| --- | --- | --- | --- | --- |
| `FC-BE-001` | `internal/service/chat.go:createMessageWithOptionalRuntime`，Runtime 未配置且 intent 不属于 `knowledge_query` | 调用 `Store.AppendExchange`，把固定 assistant 文案及 citation 作为正常 exchange | `runtime_absent_never_completed`: 返回 `dependency_unavailable`；零 completed assistant、零 citation、attempt=`failed` | F1 |
| `FC-BE-002` | `internal/repository/{memory,postgres}.go:AppendExchange` | 固定“Runtime chat is not configured…”正文与固定 source/citation | `no_fixed_exchange_answer`: repository 不再拥有任何 assistant 生成逻辑；产品路径不可调用 | F1 |
| `FC-BE-003` | `internal/service/chat.go:normalizeRuntimeExecutionFacts`，`service.go` onboarding 路径也调用 | Runtime 缺 intent/facts 时由 Backend 猜测补齐 | `missing_execution_fact`: `runtime_contract_violation`；不得持久化 success evidence | F1 |
| `FC-BE-004` | `internal/repository/knowledge_chat.go` 的空 answer、rank/label 修复 | 空内容替换提示，非法 citation 自动获得 rank/label | `empty_or_repaired_citation`: response 失败并回滚，不创建 citation/anchor | F1 |
| `FC-RT-001` | `app/pipelines/retrieval_pipeline.py:_scope_revision_ids` | legacy 正常 chat 可返回 `None` scope，读取非 revision-scoped index | `knowledge_request_without_scope`: 在任何 search 前返回 stable validation/auth failure | F2 |
| `FC-RT-002` | `app/pipelines/retrieval_pipeline.py:_ensure_index` | 产品 app 自动写入 sample documents，污染真实 retrieval state | `product_app_never_bootstraps_samples`: 启动/首问后索引不出现 fixture document；测试 factory 可显式安装 | F2 |
| `FC-RT-003` | `app/adapters/vector_store.py` 与 profile bootstrap | `QDRANT_URL` 缺失时可隐式选 InMemoryVectorStore | `fullstack_profile_rejects_memory_vector`: 配置错误/not-ready；不产生产品答案 | F4 |
| `FC-ING-001` | `app/parsers/pdf_parser.py` / ingestion pipeline | parser 名称宣称 OCR fallback；零 chunk 可走成功路径 | `scanned_pdf_without_ocr`、`zero_chunk_input`: `ocr_unavailable` 或 `no_indexable_content`；revision 不能 ready/publish | F3 |
| `FC-WEB-001` | `apps/web/app/_components/config-workspace.tsx` 的 evidence/delivery/eval `.catch(() => ({items: []}))` | 依赖/API 错误被渲染成空管理数据 | `admin_api_503_not_empty`: 展示 unavailable/error、request id、重试，不展示空态 | F6 |
| `FC-WEB-002` | chat SSE/JSON error paths | 流式半成品可能同 completed/source 视图混用 | `sse_error_has_no_sources`: 只有一个 terminal error；临时正文不成为历史 completed message | F1 + F6 |
| `FC-PROFILE-001` | `internal/server/server.go:buildStore/buildQueue`、auth/session/object store bootstrap | 缺依赖时自动 memory/local adapter；默认开发凭据仍可能进入产品启动 | `fullstack_missing_dependency_not_ready`: 每个缺 PostgreSQL/Redis/MinIO/Qdrant/model/secret 的组合启动失败或 `/readyz=503` | F4 + F5 |
| `FC-QUALITY-001` | 旧 eval/历史 evidence | 旧 fallback 或 test provider 报告可能被误读为真实模型质量 | `quality_report_requires_real_observation`: `fallback_used=true` 或 `test_provider=true` 的报告不可审批 | F7 |

## 3. 完成标准

F1–F8 完成时，以上每项都必须拥有：

1. 一个直接的单元/API/浏览器负向测试；
2. 一个 assertion，证明没有 completed assistant、citation、anchor、ready revision 或 quality approval 被写入；
3. 对流式路径，`message.completed` 与 `message.error` 的互斥 assertion；
4. 真实 adapter smoke 或显式 test-provider fault injection 的证据；
5. 执行 attempt 中的稳定 `code`、`stage` 与 retry/cancel 状态。

任何新发现的降级分支必须先加到本矩阵，再进入实现；不得以“仅本地开发可见”为由绕开 profile 或 test-only 边界。
