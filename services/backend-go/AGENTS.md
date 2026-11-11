# services/backend-go

## 目的

本目录用于 `Go` 业务后端实现。

## 编码前约定

- 先完成 API 契约，再落具体 handler。
- 与 AI 运行时之间的同步 API 和异步任务 payload 统一从 `packages/contracts/` 管理。
- 会话快照、配置版本和文件状态变更需要优先保证可审计性。

## 当前命令

- 启动：`make backend-dev`
- 测试：`make backend-test`

## 当前骨架状态

- 已从 `server` 中拆出 `httpapi/`、`service/`、`repository/`、`domain/` 的最小内存版实现
- 已补上轻量 session 登录和 `user/knowledge_admin/system_admin` 角色门禁：聊天 API 要求登录；知识库/onboarding 允许知识库管理员或系统管理员；配置、证据与 Q1 发布只允许系统管理员；legacy `admin` session 兼容为系统管理员
- `P4` 已补上本地临时存储、runtime ingestion client 和异步状态回写
- `T7b` 已把上传对象抽到 object store interface：默认本地临时目录不变，配置 MinIO 后写入 `s3://...`，并已通过真实 MinIO smoke
- `T7c/K2` 已新增可选 Postgres repository：默认内存仓库不变；配置 `DATABASE_URL` 后通过 versioned migration ledger 接管旧 schema、seed 默认数据并幂等回填 `legacy_unpublished` revision。K2 的 revision/snapshot/event/outbox repository 不改变既有 API，已通过真实 Postgres smoke。
- `T7d` 已新增可选 Redis ingestion queue：默认本地队列不变，配置 `REDIS_URL` 后写入 Redis list 并由 Backend worker 消费，已补真实 Redis smoke 入口
- `T7e` 已新增可选 Redis session store：默认内存 session 不变，配置 `REDIS_URL` 后登录态可跨 Backend 重启保留，已补真实 Redis session smoke 入口
- `K3` 已新增可选 Redis Streams revision delivery：在 `K3_DELIVERY_ENABLED=true` 且同时配置 `DATABASE_URL`、`REDIS_URL`、`AI_RUNTIME_BASE_URL` 时，Postgres outbox 会经 Streams 可靠投递到 Runtime；包含 PEL reclaim、retry/backoff、DLQ 和 immutable identity 校验，默认关闭且不影响 legacy Redis List
- `K4-K6` 已验证管理员 document/revision lifecycle、每消息 snapshot/citation provenance，以及 Postgres update calendar、possibly-stale scan 和独立历史核验。K6 仅在 Postgres 路径启用，worker 可从 lease/retry 恢复；核验 result 不会覆盖原 assistant message。
- `C1` 已验证 answer-level citation anchors：service 会拒绝越界、重叠或引用不存在 rank 的 Runtime anchors；Postgres `006` 与 message/citation 同事务持久化，legacy message 保持无 anchor 兼容。
- `K7` 已追加 Postgres `007/008`：assistant message、K5 provenance、citations、C1 anchors 与 execution evidence 同事务写入；管理员可查询 execution/delivery/eval evidence，eval report 按语义内容幂等登记且冲突可诊断。
- `K9-S0` 已追加 Postgres `010`：conversation `owner_id` 是 chat 授权权威；外部 list/detail/message/source/revalidation 必须使用 session subject 的 owner-filtered path，admin chat 不 bypass，legacy conversation 属于 `user_admin`。
- `U7` 已追加 Postgres `017`：conversation 内部 `title_source` 区分默认、首问、手动与 legacy；空标题创建为“新对话”，首个成功持久化 exchange 在同一 transaction 内确定性命名，手动重命名仅走 owner-filtered `PATCH /api/chat/conversations/{conversation_id}`。该内部来源不进入产品 JSON，历史标题不回填。
- `Q1` 已追加 Postgres `012`：answer feedback、quality experiment/report binding、immutable quality release 与 active release pointer 均由 Backend/Postgres 权威控制；候选 config 不得因创建时间自动影响新 conversation，只有通过 gate 的 approved experiment 才能 publish，rollback 以新 release 前滚。
- `C2/C2-R1` 已追加 Postgres `009` 与 Backend `IntentRouter`：有限交流以及高置信的天气/美国总统等公共开放世界问题绕过 snapshot/retrieval；混合、业务和未知输入默认 RAG；非知识 message 与 intent/retrieval/generation/knowledge facts 同事务写入且不伪造 provenance。
- `T3` 已补上 chat runtime dispatcher；当 `AI_RUNTIME_BASE_URL` 显式配置时，chat message path 会调用 runtime `/v1/chat/run` 并持久化 assistant message / citations
- `T12` 已补上 Backend SSE 骨架；`stream=true` 或 `Accept: text/event-stream` 会返回 `message.started`、`message.delta`、`message.completed`
- `T14` 已接入 Runtime 原生 token stream：配置 `AI_RUNTIME_BASE_URL` 时 Backend 调用 Runtime `/v1/chat/stream` 并透传 delta，未配置 runtime 时保留本地 fallback 分块
- 会话 Runtime request 会按其 immutable `config_version` 回读 Agent Config，并把 prompt/style/model/`temperature`/`top_p`/`max_output_tokens` 与 retrieval strategy/embedding model/`top_k`/rerank 传入 Runtime；缺失版本会拒绝执行而不改用当前配置。当前已登记至 `prompt_v12`，但只有 `qrel_002` 已发布 `qwen3:14b/prompt_v7 + native_dense_v1`；`prompt_v8` 至 `prompt_v12` 及其成对 answer policy 仅可创建为 A1 实验候选，未获 Q1 approval 前不能 active。新 retrieval config 只允许 native dense，历史 lexical config 只能审计并在执行时 fail-closed。
- `/api/agent/config/versions` 是配置候选历史的只读 read model：保留 Agent `items`/`total`，并返回 retrieval `retrieval_items`/`retrieval_total`，均按创建顺序提供给 Web 相邻版本差异展示；它不修改 active release 或 conversation 绑定。
- `service` 层应依赖 repository / runtime interface，而不是具体内存实现，方便后续替换 Postgres、Redis、MinIO 和真实 runtime dispatcher
- Backend 启动摘要由实际环境与权威 store 构造，必须脱敏报告 repository/queue/session/Runtime/object store、K3 以及 active Agent/Retrieval 配置；不得输出数据库密码、对象存储密钥或 URL query。
- `fullstack_dev`/`production` 的 `BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS` 必须为 5–600 秒且不超过 Runtime 的 `LOCAL_LLM_TIMEOUT_SECONDS`；Runtime 调用失败要将 Backend deadline、transport、Runtime HTTP 和模型 provider 失败分别记录在 execution attempt，公共 API 仍不能泄露内部依赖细节。
- 当前目标是建立 R5 eval quality gate，并基于 failure report 优化剩余 PDF/XLSX/PPTX 答案质量

## 当前实现依据

- `docs/design-docs/single-agent-v1-backend-design.md`
- `docs/api/single-agent-v1-api-design.md`
- `packages/contracts/openapi.yaml`

## 继续任务指南

如果新会话接手后端工作，默认按以下顺序继续：

1. 先确认 `make backend-test` 仍通过
2. 再确认 `make integration` 中的 auth role gate、chat smoke 和 knowledge smoke 仍通过
3. 再把 `MemoryStore` 拆向更细的 repository interface / implementation 文件
4. 然后把 `P4` 的本地异步摄取编排替换成真实对象存储 / 队列 / 回调链路

当前最适合直接开工的任务：

- K8 原生检索与模型比较；保持 K7 prompt_v2 gate 持续通过

开始前先确认：

- `docs/design-docs/single-agent-v1-backend-design.md` 中的分层、状态机和快照规则
- `packages/contracts/openapi.yaml` 中相关端点的请求 / 响应字段
- `make backend-test` 当前可通过

## 后续需要补充

- lint / format 命令
- K9 后续 service auth、默认凭据、消息幂等、部署恢复与试用门槛
- runtime 回写接口的生产形态和异步任务调度说明
- Redis queue / worker 拆分说明
