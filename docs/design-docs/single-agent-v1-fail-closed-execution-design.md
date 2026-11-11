# 单智能体 V1 Fail-Closed 执行与失败记录设计

## 文档控制

- 状态：`F0 frozen target design`
- 版本：`v1.0`
- 更新日期：`2026-07-24`
- 关联：[P0 生产 fail-closed 计划](../exec-plans/active/single-agent-v1-production-fail-closed-hardening-plan.md)、[Runtime 严格请求](../../packages/contracts/chat-runtime-strict-request.schema.json)、[严格响应](../../packages/contracts/chat-runtime-strict-response.schema.json)
- 实施顺序：F1 先实现 Backend 写入/失败语义；F2 再实施 Runtime strict request、服务认证与 response；F3–F8 不得绕开本文。

## 1. 不变量

1. 产品成功只能由 Backend 的原子写事务确定，不能由 Runtime HTTP `200`、SSE delta、配置中的模型名或 health endpoint 推断。
2. 每次提交都有 append-only execution attempt；assistant `completed`、answer citation、anchor 与 success evidence 必须属于同一 succeeded attempt。
3. 失败、取消和超时绝不保存可阅读的替代 assistant 正文或 citation。为保持对话顺序，允许一个 `failed` assistant placeholder，但其 `content=''`、`citations_count=0`、无 anchor、无用户 sources endpoint 结果。
4. `knowledge_query`/`revalidate` 在 Runtime 读取任何 index 前已具备完整 immutable scope；controlled intent 无 scope、无检索、无模型生成、无 citation。
5. 历史数据保持原样。新路径只能额外标记历史记录是否 `legacy_fallback` / `invalid_for_quality`，不能回填、修改回答文本或让其进入 Q1 审批。

## 2. Attempt 记录与状态机

F1 新增 forward-only `chat_execution_attempts`，Postgres 是唯一权威；MemoryStore 仅实现同名语义供单元测试，不能成为 fullstack/production adapter。

| 字段 | 规则 |
| --- | --- |
| `attempt_id` | 不可变主键；每次提交或 retry 新建。 |
| `request_id` | 客户端幂等键；同一 conversation + request id 只允许一个 initial attempt。重复请求返回该 attempt 的终态或 in-progress 状态，不重复写用户消息。 |
| `parent_attempt_id` | retry 时指向失败/取消的前次 attempt；不得覆盖旧状态。 |
| `conversation_id`、`user_message_id`、`assistant_message_id` | 所属资源；失败 placeholder 可有 assistant id，但其正文/引用为空。 |
| `state` | `accepted`、`running`、`succeeded`、`failed`、`cancelled`；状态只能前进，terminal 状态不可逆。 |
| `config_version`、`retrieval_config_version`、`knowledge_snapshot_id`、`manifest_hash` | accepted 前冻结；写入后不可更新。controlled intent 的 scope 字段为空。 |
| `runtime_request_id`、`answer_disposition`、`provider`、`strategy`、`embedding_model`、`collection` | 从 strict Runtime response 原样记录；禁止补造。 |
| `error_code`、`error_stage`、`retryable` | `failed/cancelled` 必填；用户安全文案不入该表，脱敏诊断只进管理员 evidence。 |
| `accepted_at`、`started_at`、`terminal_at` | 统一 UTC；构成可审计时间线。 |

```text
accepted --Backend starts Runtime--> running --validated atomic commit--> succeeded
                                      |--dependency/contract/write failure--> failed
                                      |--user stop/deadline cancel---------> cancelled
```

- `accepted` transaction 写入用户消息、冻结 conversation binding 与 attempt；若用户消息本身无法写入，根本不创建 attempt。
- `running` 在 Runtime 调用前写入。重复请求不得平行调用 Runtime。
- `succeeded` transaction 同时写 completed assistant、answer citations、anchors、K5 snapshot provenance、K7 success evidence 与 attempt terminal fields。任一写入失败则整个成功 transaction 回滚，再以独立事务写 failed attempt/placeholder。
- `failed/cancelled` 事务只能写 attempt terminal fields 和可选空 placeholder；数据库约束与 service invariant 必须确保没有 citation/anchor 引用该 placeholder。

## 3. 稳定错误映射

| code | HTTP（SSE 已开始后的 terminal） | retryable | `stage` 例子 | 语义 |
| --- | --- | --- | --- | --- |
| `authentication_required` | 401 | false | `request_authentication` | 未登录；Web 转 `/login?next=...`。 |
| `authorization_denied` | 403/404 | false | `conversation_ownership` | 无权或 owner 保护的不可见资源。 |
| `validation_error` | 422 | false | `request_validation` | 用户输入或 idempotency contract 无效。 |
| `knowledge_snapshot_unavailable` | 409 | true | `scope_resolution` | Backend 可以先让 Runtime 完成一次受限上下文路由，以区分普通受控回复和业务检索；若最终路由为业务检索而没有 active snapshot 或无法冻结 scope，则在 retrieval/generation 前失败。受控回复不需要知识 snapshot。 |
| `dependency_unavailable` | 503 / `message.error` | true | `runtime_call`、`model_provider`、`vector_store`、`state_writeback` | 已知依赖不可用或 deadline；不得降级。 |
| `runtime_contract_violation` | 502 / `message.error` | true | `runtime_response_validation` | Runtime response 缺 observation、identity/scope/provenance 不一致、空内容、非法 citation/anchor。 |
| `configuration_error` | 启动失败或 `/readyz=503` | false | `startup_profile_validation` | fullstack/production 缺必需变量、adapter/profile 不匹配。 |
| `no_indexable_content` | ingestion/revision failed | false | `chunk_validation` | parser 已执行但没有可索引 chunk。 |
| `ocr_unavailable` | ingestion/revision failed | true | `ocr_resolution` | 扫描 PDF 需要 OCR 但 provider 未配置/不可达。 |
| `cancelled` | 499 等价产品状态 / `message.error` | true | `request_cancelled` | 用户取消或服务端 deadline；不改写成 completed。 |

对外 JSON error 固定为 `{error:{code,message,retryable,request_id,stage}}`；SSE 中 `message.error` 携带相同 envelope。`message.completed` 与 `message.error` 互斥。

## 4. Backend ⇄ Runtime 服务认证

F2 首版使用仅限私有网络的 bearer service token：

- Backend 发送 `Authorization: Bearer <SEE_AGENT_RUNTIME_SERVICE_TOKEN>`；Runtime 仅接受该 header，并以 constant-time compare 校验。
- Runtime 允许 `SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS` 在有限 rotation 窗口并行校验；日志不记录 token/header。
- `fullstack_dev` 与 `production` 均要求当前 token 满足 secret policy：非空、至少 `32` 个字符，且不得等于仓库已知示例/占位值（包括 `replace-with-local-dev-secret`）；`test` 通过显式 test app factory 注入；`smoke` 可禁用 Runtime 产品接口，不允许匿名生产接口。该 policy 是启动/preflight 边界，不声称单凭长度即可证明熵；部署运行方仍须以 CSPRNG 或 Secret Manager 生成值。
- token 的唯一运行时来源是进程环境或部署 secret injection。`infra/compose/.env.example` 只能保留空变量与加载说明，不能提供可工作的 token；runbook 必须要求从本机权限受限、未纳入版本控制的 secret 文件或生产 Secret Manager 注入。Web public environment、API response、report、诊断与日志均不得携带 token。
- Backend、Runtime 和 `fullstack-preflight` 必须实施同一 policy；三者任一拒绝时以 `configuration_error` 启动失败或 `/readyz=503` 处理，绝不以匿名 Runtime、固定 token 或 fallback 继续运行。`SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS` 若设置也必须满足 policy 且不得与 current 相同。
- token 缺失/错误在 Runtime middleware 拒绝，且必须发生在 schema 解析、vector store、sample bootstrap 与模型调用之前。
- 运行网络只暴露 Backend 可达的 Runtime endpoint；token 不是外网暴露的替代措施。

## 5. Profile、健康与诊断

| profile | 允许用途 | 允许 adapter | 失败语义 |
| --- | --- | --- | --- |
| `test` | 单元、integration fixture | 明确依赖注入的 memory/fake provider | factory 创建失败；不可由生产命令选择。 |
| `smoke` | API 壳层/路由演示 | 显式 local/memory，知识聊天 capability disabled | 返回 `dependency_unavailable`/capability disabled；不生成回答。 |
| `fullstack_dev` | 本地生产拓扑开发 | PostgreSQL、Redis、MinIO、Qdrant、真实模型 provider | 任一必需依赖/密钥缺失则启动失败或 `/readyz=503`。 |
| `production` | 私有部署 | 与 fullstack 相同的持久化/认证 adapter | 禁止默认凭据、default adapter 与隐式回退。 |

- `/healthz` 仅回答进程是否存活，不能进行深度依赖断言。
- `/readyz` 以短超时检查其拥有的必需依赖：Backend 检查 Postgres、Redis、Runtime capability；Runtime 检查 Qdrant/profile、对象存储配置和 generation provider capability。`fullstack_dev/production` 的 Runtime 还必须在启动时完成一次不持久化、无用户可见的真实 generation warm-up；它只证明配置 model 能返回非空输出，失败时进程不得 ready。探针不读取知识、不创建 attempt、message 或 citation，也不返回 probe 文本。它不生成业务答案、不输出 secrets。
- Backend 调用 Runtime 的 HTTP timeout 与 Service normal/stream/revalidation deadline 必须来自同一个显式配置，并不得短于部署允许的模型首响应预算；公共 API 可保留安全的 `runtime_unavailable`，但 append-only attempt 必须细分 `backend_deadline`、Runtime HTTP、model provider 连接与 model generation timeout，供管理员 evidence 排障。
- 受保护 diagnostic endpoint/CLI 可返回 adapter 类型、脱敏 endpoint、provider/model、collection、strategy、embedding identity 和 active config pair；这些是配置事实，单次回答是否调用仍以 attempt observation 为准。

## 6. 历史兼容、迁移与回滚

- F1 migration 只添加表、外键、状态/零 citation constraints 和管理员只读质量标记；不回写或删除旧 message/citation/eval report。
- 旧 evidence 如果明确显示 fallback、固定回答或缺失严格 observation，投影为 `legacy_fallback` 或 `invalid_for_quality`。其原消息仍可按原 provenance 阅读，但禁止成为新的 Q1 approval/report input。
- 所有 schema/data 迁移 forward-only。紧急处置只能关闭聊天/摄取 capability 或发布前一个真实 release；不得重新打开固定 answer、unscoped retrieval 或默认 citation 路径。

## 7. F1/F2 验收门

- 对 Runtime 缺失、空 response、缺 execution observation、非法 rank/hash/anchor、scope mismatch 和 writeback failure：无 completed assistant、citation、anchor 或 success evidence。
- 对真实 scoped grounded answer：exact scope/revision/hash/provider/strategy 全部一致，且 citations 与 anchors 在同一 transaction 写入。
- 对 controlled response：`retrieval.status=generation.status=not_run`、零 scope/citation，且固定文案由 product policy 明确选择。
- Runtime 在无 scope、错误 token、provider down、collection/profile mismatch 下，在任何业务检索/生成前失败。
- SSE 在成功、失败、取消、重复 request 下都只给一个 terminal 事件，并能从 attempt audit 恢复原因。
