# services/backend-go

这里将承载 `Go` 业务后端。

预期首批职责：

- 鉴权与会话管理
- 文件元数据管理
- 配置版本化
- API 网关与流式转发
- 检索 / 生成日志记录

当前命令：

- 启动：`make backend-dev`
- 真实 RAG 启动：`make backend-rag-dev`
- 测试：`make backend-test`

启动成功后控制台会脱敏报告 repository、queue/session、Runtime/object store、K3 delivery，以及 active quality release 绑定的 Agent model 和 Retrieval/embedding 配置。该摘要用于与 Runtime 的实际 provider 启动日志交叉核对，不改变 `/healthz` 的存活语义。

当前已落地的最小接口骨架：

- `GET /healthz`
- `GET/PUT /api/agent/config`
- `GET/PUT /api/agent/retrieval-config`
- `GET /api/agent/config/versions`
- `GET/POST /api/chat/conversations`
- `GET /api/chat/conversations/{conversation_id}`
- `POST /api/chat/conversations/{conversation_id}/messages`
- `GET /api/chat/conversations/{conversation_id}/sources/{message_id}`
- `POST /api/chat/conversations/{conversation_id}/messages/{message_id}/revalidate`
- `GET /api/chat/conversations/{conversation_id}/revalidations/{revalidation_id}`
- `GET/POST /api/kb/files`
- `GET /api/kb/files/{file_id}`
- `POST /api/kb/files/{file_id}/reindex`
- `GET /api/kb/files/{file_id}/chunks`
- `GET /api/kb/updates`
- `GET /api/kb/snapshots/{snapshot_id}`
- `GET /api/admin/evidence/executions`
- `GET /api/admin/evidence/messages/{message_id}`
- `GET /api/admin/evidence/deliveries`
- `GET/POST /api/eval/reports`
- `GET /api/eval/reports/{report_id}`

Chat 当前支持两种响应形态：

- `stream=false` 或未请求 SSE：返回 `application/json` 的 `user_message + assistant_message`
- `stream=true` 或 `Accept: text/event-stream`：返回 SSE，事件顺序为 `message.started`、`message.delta`、`message.completed`；配置 `AI_RUNTIME_BASE_URL` 时 delta 来自 Runtime `/v1/chat/stream`，未配置 runtime 时使用 Backend 本地 fallback 分块

当前 `P4` 已落地的最小摄取编排行为：

- 上传文件后立即返回 `queued` 的 `ingestion_task`
- backend 默认会把上传内容写入本地临时目录，再通过进程内本地 ingestion queue 调用 runtime `/v1/ingestion/run`
- 配置 `OBJECT_STORE_ENDPOINT` 和 `OBJECT_STORE_BUCKET` 后，backend 会把上传内容写入 S3-compatible object store / MinIO，并把 `s3://...` 作为 `object_uri` 传给 runtime
- 配置 `REDIS_URL` 后，backend 会把 ingestion job 写入 Redis list，并由 Backend worker 消费；队列名可通过 `REDIS_INGESTION_QUEUE` 设置
- `GET /api/kb/files/{file_id}` 可观察 `latest_task.status` 从 `queued/running` 进入 `succeeded/failed`
- 失败任务会带 `error.code` 和 `error.message`

当前本地联调相关环境变量：

- `AI_RUNTIME_BASE_URL`
  - 默认 `http://127.0.0.1:8000`
- `BACKEND_UPLOAD_DIR`
  - 默认落到系统临时目录下的 `see-agent-backend-uploads`
- `OBJECT_STORE_ENDPOINT`
- `OBJECT_STORE_BUCKET`
- `OBJECT_STORE_ACCESS_KEY`
- `OBJECT_STORE_SECRET_KEY`
- `OBJECT_STORE_REGION`
- `DATABASE_URL`
  - 未配置时使用内存 repository
  - 配置后自动启用 Postgres repository，以 versioned migration ledger 接管 legacy schema、seed 与幂等 legacy revision 回填
- `REDIS_URL`
  - 未配置时使用进程内本地 ingestion queue
  - 配置后自动启用 Redis-backed ingestion queue，并默认启用 Redis-backed session store
- `REDIS_INGESTION_QUEUE`
  - 默认 `see_agent:ingestion`
- `AUTH_SESSION_STORE`
  - 未配置且 `REDIS_URL` 存在时默认使用 `redis`
  - 可设置为 `memory` 强制使用内存 session
- `REDIS_SESSION_PREFIX`
  - 默认 `see_agent:session:`
- `SEE_AGENT_SYSTEM_ADMIN_USERNAME` / `SEE_AGENT_SYSTEM_ADMIN_PASSWORD`
  - 系统管理员凭据；旧 `SEE_AGENT_ADMIN_USERNAME/PASSWORD` 仍兼容，且新变量优先
- `SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME` / `SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD`
  - 知识库管理员凭据；只能管理知识 lifecycle/onboarding，不能修改或发布配置

真实 MinIO 上传 / 摄取 smoke：

- 先启动本地 MinIO：`docker compose -f infra/compose/docker-compose.yml up -d minio`
- 再运行：`make minio-ingestion-smoke`

真实 Postgres 元数据持久化 smoke：

- 先启动本地 Postgres：`docker compose -f infra/compose/docker-compose.yml up -d postgres`
- 再运行：`make postgres-repository-smoke`

K2 知识演进持久化 smoke：

- 先启动本地 Postgres：`docker compose -f infra/compose/docker-compose.yml up -d postgres`
- 再运行：`make postgres-k2-smoke`
- 它在隔离 schema 中验证 migration、legacy 回填、publication transaction、回滚与并发发布。

真实 Redis 摄取队列 smoke：

- 先启动本地 Redis：`docker compose -f infra/compose/docker-compose.yml up -d redis`
- 再运行：`make redis-ingestion-smoke`

真实 Redis session 持久化 smoke：

- 先启动本地 Redis：`docker compose -f infra/compose/docker-compose.yml up -d redis`
- 再运行：`make redis-session-smoke`

K3 revision delivery：

- 保持默认关闭；只有同时配置 `DATABASE_URL`、`REDIS_URL`、`AI_RUNTIME_BASE_URL` 和 `K3_DELIVERY_ENABLED=true` 时才启动。
- Backend 从 Postgres `knowledge_outbox` relay 到 Redis Streams，再以 consumer group 调用 Runtime；成功后 revision 进入 `ready`，投递记录进入 `delivered`。retry、PEL reclaim 和 DLQ 不会改变 active snapshot。
- 可选配置：`REDIS_K3_REVISION_STREAM`、`REDIS_K3_REVISION_GROUP`、`REDIS_K3_REVISION_CONSUMER`、`K3_DELIVERY_RELAY_INTERVAL_MS`。
- 验收：设置 `K3_TEST_DATABASE_URL` 与 `K3_TEST_REDIS_URL` 后运行 `go test ./internal/repository -run TestPostgresKnowledgeDeliveryLifecycle -count=1`、`go test ./internal/taskqueue -run TestRedisRevisionStreamDeliveryLifecycle -count=1` 和 `go test ./internal/server -run TestServerK3DeliveryWiring -count=1`。
- 本地产品开发应使用 `make backend-fullstack-dev`，它显式打开 K3，并和 `make runtime-fullstack-dev`、`make bootstrap-rag-knowledge` 组成可上传、索引、发布和问答的闭环；默认 `backend-dev` 继续服务轻量 smoke。`BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS` 默认 60 秒，必须为 5–600 秒且不超过 Runtime 的 `LOCAL_LLM_TIMEOUT_SECONDS`；超时 execution attempt 会记录 `backend_deadline_exceeded/backend_runtime_deadline`，不会伪造答案或引用。新的 Postgres 库只 seed Agent/retrieval config，不自动写入示例知识，避免绕开不可变 revision/snapshot lifecycle。

K6 知识时效与核验：

- `DATABASE_URL` 启用 Postgres 时，Backend 自动启动轻量 worker：它从 `published/disabled/deleted` update event 创建可恢复 freshness scan，只按历史 citation 的 document/revision 交集标记 `possibly_stale`。
- 管理员可读取 `/api/kb/updates` 和 immutable snapshot；已登录用户以 `Idempotency-Key` 发起核验。核验会固定 latest snapshot、调用 Runtime `answer_mode=revalidate`，并写入独立 result，不会改写原 answer/citation。
- 验收：`K6_TEST_DATABASE_URL=postgres://see_agent:see_agent_dev@127.0.0.1:15432/see_agent?sslmode=disable /Users/populus/.local/go1.26.4/bin/go test ./internal/repository ./internal/server -run 'Test(PostgresK6FreshnessAndRevalidationLifecycle|ServerK6CalendarAndRuntimeRevalidation)' -count=1 -v`。

C1 回答内证据锚点：

- Runtime response 可带 `citation_anchors`（Unicode rune range + citation rank）；Backend 在写入前验证范围、顺序和 rank，非法结果返回 `runtime_citation_anchor_violation`，不写成功 exchange。
- Postgres `006_citation_anchors` 将 message、citation、anchor 放进同一 transaction；旧消息 anchors 为空且保持兼容。

K7 管理证据与配置闭环：

- Postgres `007_k7_admin_evidence` 与 `008_k7_eval_request_hash` 追加 message execution evidence、eval report、Agent config 扩展字段与语义 request hash；Postgres 仍是唯一权威。
- 正常回答的 assistant message、K5 provenance、citations、C1 anchors 与 execution evidence 在同一 transaction 写入；失败时整体回滚，不补写或改写历史回答。
- 管理员可按 conversation/snapshot/revision 查询 execution evidence，并读取 delivery 与 eval evidence。eval report 使用 `report_id` + `Idempotency-Key` 做语义幂等登记；同键不同内容返回冲突。
- Agent config 保存只接受已登记的 `prompt_v1` 至 `prompt_v5`；v5 是未发布的 Qwen dense 候选，必须经自己的 Q1 release 才能影响新会话。缺失 K7 新字段的旧客户端仍默认 `response_style=concise`、`top_p=1`。新 repository 默认 seed `prompt_v2`，旧 repository/conversation 不迁移 prompt version。
- 验收：先启动本地 Postgres，再运行 `make postgres-k7-smoke`。

K8-R2a 受控检索策略：

- migration `011_retrieval_strategy_registry` 为既有 immutable retrieval config 回填 `lexical_compat_v1`；不改写历史回答或 K7 evidence。
- `GET/PUT /api/agent/retrieval-config` 读取/创建的 config 必须带已注册 `retrieval_strategy`（当前为 `lexical_compat_v1` 或 `native_dense_v1`）；未知值返回 validation error。
- chat 与 K6 revalidation 将 strategy、embedding model、top_k/rerank 一起传给 Runtime；Postgres execution evidence 的 retrieval JSON 记录该 strategy，供管理员追溯。

Q1 质量运营与受控发布：

- migration `012_quality_operations_release` 追加 append-only answer feedback、受控 experiment/report binding、immutable release/event 和 active release pointer；Postgres/Backend 是唯一写入边界。
- 新 config/retrieval config 默认是候选，不能因创建时间更晚自动改变新 conversation；只有 gate-passing experiment 经管理员 approval/publish 后才原子切换未来 conversation 的 config pair。
- feedback 必须锚定已有 assistant execution evidence，且期望 citation rank 必须属于该 message；报告必须精确匹配冻结 scope/manifest/dataset/config/conditions。rollback 以前滚的新 release 恢复旧 pair，历史 conversation、回答与 provenance 不改写。

K9-S0 会话归属与越权隔离：

- migration `010` 为 conversation 新增不可为空的 `owner_id`；legacy conversation 回填为稳定管理员 subject `user_admin`，不修改历史消息、citation 或 provenance。
- chat create/list/detail/message/source/revalidation 全部使用当前 session subject；admin chat 不自动越权，admin evidence API 仍提供全局审计。
- 验收：先启动本地 Postgres，再运行 `make postgres-k9-ownership-smoke`；该 smoke 覆盖 `001-010`、legacy backfill、NOT NULL、空 owner fail-closed 与 owner-filtered list/read。

实现前请先对齐：

- `docs/api/single-agent-v1-api-design.md`
- `docs/design-docs/single-agent-v1-backend-design.md`
- `packages/contracts/openapi.yaml`
