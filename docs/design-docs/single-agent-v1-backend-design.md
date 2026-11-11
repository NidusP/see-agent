# 单智能体 V1 Go Backend 设计

## 文档控制

- 状态：活动设计
- 版本：`v1.1`
- 目标：为 `services/backend-go/` 提供可直接指导实现的模块分层与状态设计

## 1. 设计目标

先理解跨服务职责时，阅读 [摄取、检索与回答职责导读](single-agent-v1-ingestion-chat-boundary-guide.md)；本文定义 Go Backend 的权威写入、授权与状态机细节。

Go Backend 在本项目中的目标不是承载所有 AI 逻辑，而是：

- 提供对外产品 API
- 管理会话、文件、配置和日志元数据
- 固定会话快照
- 把对外请求转换为内部 runtime 请求
- 接收 runtime 任务结果并回写状态

### 1.1 F0 真实性写入边界

Backend 是产品执行状态的唯一写入权威，而不是 Runtime 缺省输出的修复器。每个聊天 `request_id` 都进入 append-only attempt 状态机：

```text
accepted -> running -> succeeded
                    -> failed
                    -> cancelled
```

- `succeeded` 仅在 Backend 验证 identity、scope、answer disposition、execution observation、citation provenance/anchor 后，将 assistant `completed`、真实正文、citations、anchors、snapshot provenance 与 success evidence 一次事务提交。
- `failed/cancelled` 必须记录 error code、stage、时间和 retry relation；不得调用固定回答路径，不得写 completed assistant/citation，也不得把错误文案当 assistant 正文保存。
- Backend 绝不补造 Runtime 缺失的 intent、retrieval/generation、knowledge-used/provider、citation rank 或 label。字段缺失、空业务回答、scope/hash 不匹配、非法 rank/anchor 均是 `runtime_contract_violation`。
- 流式通道只有在成功事务提交后可发 `message.completed`；任何更晚的验证或写回失败发唯一 terminal `message.error`。临时 delta 不属于权威消息。

具体严格字段和迁移顺序由 [P0 fail-closed 计划](../exec-plans/active/single-agent-v1-production-fail-closed-hardening-plan.md) 与 `chat-runtime-strict-*.schema.json` 冻结；当前 legacy response schema 只在 F1/F2 实现迁移期间兼容读取，不能再作为产品成功语义依据。

## 2. Backend 在系统中的边界

### 2.1 Backend 负责

- 鉴权与登录态
- 会话主记录
- 文件元数据主记录
- 配置版本与 retrieval config 版本
- 上传入口和任务调度入口
- 对外 API 返回整形
- retrieval / generation 日志主索引

### 2.2 Backend 不负责

- 文档解析
- chunk 生成
- embedding
- reranking
- LLM 推理

这些属于 AI Runtime。

## 3. 模块分层

建议在 `services/backend-go/internal/` 内按以下结构拆分：

```text
internal/
  server/
  httpapi/
    auth.go
    chat.go
    knowledge.go
    config.go
    eval.go
    logs.go
  service/
    auth_service.go
    conversation_service.go
    file_service.go
    config_service.go
    eval_service.go
  runtime/
    client.go
    ingestion_dispatcher.go
    chat_dispatcher.go
  repository/
    conversations.go
    files.go
    tasks.go
    configs.go
    logs.go
  domain/
    conversation.go
    message.go
    file.go
    ingestion_task.go
    config_version.go
```

### 3.1 `httpapi/`

- 处理 HTTP request / response
- 路由参数和请求体校验
- 调 service 层
- 不直接写数据库

### 3.2 `service/`

- 实现产品级业务逻辑
- 维护状态机
- 处理快照、权限、删除语义

### 3.3 `runtime/`

- 封装 Backend 到 AI Runtime 的调用
- 统一管理 `chat-runtime-request` 和 `ingestion-task` 契约

### 3.4 `repository/`

- 封装持久化读写
- 避免 handler 直接访问数据库

### 3.5 `domain/`

- 存放核心业务对象与状态枚举

## 4. 核心领域对象

### 4.0 角色授权

认证 session 的角色为 `user`、`knowledge_admin` 或 `system_admin`。Backend route middleware 是权限的最终权威：`knowledge_admin` 访问知识 lifecycle/onboarding，`system_admin` 访问参数、evidence、Q1 发布和全部知识路由；两者都不绕过 `Conversation.owner_id`。已存在的 legacy `admin` session 仅兼容映射为 `system_admin`，新 session 不再签发该值。详细 API 矩阵以 [角色分离设计](single-agent-v1-role-separation-design.md) 为准。

### 4.1 Conversation

最小字段：

- `conversation_id`
- `owner_id`
- `title`
- `config_version`
- `retrieval_config_version`
- `model_id`
- `created_at`
- `updated_at`

Conversation 只冻结生成与 retrieval 配置，不永久冻结知识 snapshot。

`owner_id` 是 Backend/Postgres 的资源授权权威。Web 的 role cookie 只用于体验优化，不能参与 conversation 授权。所有外部 chat entrypoint 必须在读取内容或触发 Runtime 前校验当前 session subject 与 `owner_id` 相等；admin chat 也不自动 bypass。后台 freshness/revalidation worker 可以通过独立内部 repository path 访问，但不得暴露为普通产品 API。

会话标题是独立于消息和 provenance 的可变展示元数据。持久化层以内部 `title_source` 区分 `system_default`、`first_user_message`、`user_edited` 与 `legacy`；该字段不作为外部产品 DTO。创建时未提供标题写入“新对话”与 `system_default`；显式 create title 或 `PATCH` 写入 `user_edited`。第一个成功持久化的 user message 仅在 `title_source=system_default` 时，于同一 repository transaction 内把标题改写为规范化后的问题摘要并标记 `first_user_message`。因此并发首发消息只有先取得会话写锁的成功 transaction 能命名，手动重命名与自动命名的并发结果按 transaction 顺序决定，后完成的手动重命名始终可覆盖自动结果。历史行迁移为 `legacy`，不批量改名。

### 4.2 Message

最小字段：

- `message_id`
- `conversation_id`
- `role`
- `content`
- `status`
- `finish_reason`
- `created_at`
- `knowledge_snapshot_id`（仅 assistant message）
- `knowledge_snapshot_manifest_hash`（仅 assistant message）
- `answered_at`（仅 assistant message）
- `freshness_status`（仅 assistant message）

### 4.3 KnowledgeDocument 与 KnowledgeRevision

`KnowledgeDocument` 是管理员可见的稳定逻辑文档，包含 `document_id`、display name、owner 与 lifecycle status。`KnowledgeRevision` 是不可变内容版本，包含 `revision_id`、`document_id`、object URI、content hash、parse/index/embedding version、effective time 和 revision status。

现有 `KnowledgeFile` 仅作为 `/api/kb/files` 过渡兼容 facade；K2 起的新写入必须映射为 document + revision，不能覆盖历史 revision。

### 4.4 KnowledgeSnapshot 与 KnowledgeUpdateEvent

`KnowledgeSnapshot` 记录 immutable member manifest、parent snapshot、status 与 publish time。`KnowledgeUpdateEvent` 是 append-only 记录，关联 document/revision/snapshot、actor、effective time 与 correlation id。它们只由 Backend 的发布事务写入。

### 4.5 Legacy KnowledgeFile

最小字段：

- `file_id`
- `filename`
- `mime_type`
- `source_format`
- `status`
- `uploaded_by`
- `uploaded_at`
- `object_uri`
- `parse_version`
- `index_version`

### 4.6 IngestionTask 与 OutboxDelivery

最小字段：

- `task_id`
- `document_id`
- `revision_id`
- `delivery_id`
- `status`
- `requested_at`
- `updated_at`
- `force_reindex`
- `error_code`
- `error_message`

`IngestionTask` 反映 revision 的解析/索引状态；`OutboxDelivery` 反映投递状态。Redis Streams 的 ack、pending、retry 和 DLQ 不得改变 revision/snapshot 的业务状态。

### 4.7 ConfigVersion

最小字段：

- `config_version`
- `retrieval_config_version`
- `created_at`
- `created_by`

## 5. 配置快照、知识选择与回答 provenance

### 5.1 创建时机

在 `POST /api/chat/conversations` 时固定 config/retrieval/model snapshot；知识 snapshot 不在此时固定。

### 5.2 为什么由 Backend 选择知识并固定配置

因为：

- 前端不应自行拼快照
- Runtime 不应决定产品级会话版本语义
- Backend 最适合同时看到 conversation config、active knowledge pointer 与用户权限

### 5.3 快照内容

Backend 在创建会话时应固定：

- `config_version`
- `retrieval_config_version`
- `model_id`

后续对话只引用这些配置快照，不重新读取“当前最新配置”。每条新用户消息开始时，Backend 在单一读取路径解析 active published knowledge snapshot，生成 `knowledge_scope`（snapshot id、manifest hash、member revision ids）交给 Runtime；完成后将实际 scope 写到 assistant message 与 citations。Runtime 不得自己猜测或替换 scope。

### 5.3.1 生成参数的快照解析

`Conversation.config_version` 不只是展示字段。Backend 在每次构造 Runtime chat request 时，必须通过 `ConfigRepository` 读取该精确版本的 `AgentConfig`，并把其中的 `temperature`、`max_output_tokens` 映射到已存在的 `response_overrides`。`model_id` 继续使用会话快照值，并应与读出的版本配置一致。

这条读取是只读、按版本、可重复的：新建 Agent Config 或修改当前活跃配置不得改变已有 conversation 的生成参数。若会话引用的 config version 不存在，Backend 不得静默改用当前配置或本地 fallback；应作为 Runtime 无法安全执行的服务错误返回，并且不写入 user/assistant exchange。

`system_prompt_version` 本阶段仍由 Runtime 的版本化 prompt 资产解析；在建立 prompt registry 前，不把该字段伪装为已经动态生效的生成参数。

### 5.3.2 历史回答核验

核验端点读取旧 assistant message 的 provenance 和相关 update event，再以 latest published snapshot 创建独立 `answer_revalidation_run`。它不覆盖旧 message，不修改旧 citation，也不复用旧 snapshot 作为 fallback。

### 5.4 MVP 鉴权与角色门禁

当前 MVP 采用单租户轻量登录，不引入企业级 IAM 或细粒度 RBAC：

- Backend 维护 `see_agent_session` HttpOnly cookie，作为 API 鉴权的唯一可信凭据。
- Backend 同时写入 `see_agent_role` cookie，供 Web 做导航隐藏和路由体验优化；该 cookie 不能作为后端授权依据。
- 默认角色只有 `user` 与 `admin`。
- `/api/chat/**` 要求已登录，允许 `user` 与 `admin`。
- `/api/chat/**` 在登录之后还必须校验 conversation ownership；认证成功不代表可以访问任意 `conversation_id`。
- `/api/kb/**`、`/api/agent/**` 要求 `admin`。
- `/healthz`、`POST /api/auth/login`、`POST /api/auth/logout` 保持公开。
- 开发默认账号可以通过环境变量覆盖；生产身份源接入留到后续设计。

### 5.5 T7e Redis Session Store Adapter

`T7e` 只替换 Backend session 存储边界，不改变账号来源、密码策略、cookie 名称或角色模型。

实现约定：

- `auth.Manager` 依赖 `SessionStore` 抽象；默认使用进程内 memory store，保持本地开发和普通 `make integration` 不依赖 Redis。
- 配置 `REDIS_URL` 后，Backend 默认启用 Redis-backed session store；如只想使用 Redis ingestion queue 而保留内存 session，可设置 `AUTH_SESSION_STORE=memory`。
- `AUTH_SESSION_STORE=redis` 时必须配置 `REDIS_URL`，启动时会 Ping Redis，失败应直接暴露为启动错误。
- Redis key prefix 通过 `REDIS_SESSION_PREFIX` 配置，默认 `see_agent:session:`。
- Redis session value 只保存当前 MVP 所需的 `User` 与 `expires_at`，并使用 session 剩余 TTL 作为 Redis key expiration。
- `POST /api/auth/logout` 必须删除 Redis session；session 过期或 Redis 读取失败时，后端按未登录处理。
- 首版不改变 `see_agent_role` cookie 的体验用途；后端授权仍只信任 session store 中的用户和角色。

验收重点：

- 未配置 Redis 时，现有登录、角色门禁和 `make integration` 行为不变。
- 配置 Redis session 后，登录获取的 `see_agent_session` 在 Backend 重启后仍可通过 `GET /api/auth/me` 验证。
- 新增 `make redis-session-smoke` 验证真实 Redis 下的登录、Backend 重启、session 读取和 logout 清理。

## 6. 知识 revision 状态机

revision 状态机：

- `uploaded`
- `queued`
- `indexing`
- `ready`
- `published`
- `superseded`
- `failed`
- `disabled`
- `deleted`

### 6.1 状态迁移

标准路径：

- `uploaded -> queued -> indexing -> ready -> published -> superseded`

失败路径：

- `uploaded -> queued -> indexing -> failed`

删除路径：

- `ready|published|superseded -> disabled -> deleted`
- `failed -> deleted`

### 6.2 Backend 责任

Backend 必须负责：

- 创建初始 revision 与 outbox event
- 接收 Runtime 结果后仅更新对应 revision 的 index 状态
- 只在发布事务中从 `ready` 创建 published snapshot
- 阻止非法状态回退和跨 document/revision 的 callback

例如：

- 不允许 `deleted -> indexing`
- 不允许 `published -> uploaded`
- 不允许 failed/queued/indexing revision 进入 active snapshot

## 7. 摄取任务编排

### 7.1 上传入口

`POST /api/kb/files` 的 Backend 流程建议：

1. 校验文件格式
2. 将原始文件存入对象存储
3. 创建 `KnowledgeFile`
4. 创建 `IngestionTask`
5. 调用或派发 runtime ingestion
6. 返回 `file + ingestion_task`

### 7.2 重新索引入口

`POST /api/kb/files/{file_id}/reindex`

流程建议：

1. 读取既有文件元数据
2. 生成新的 `task_id`
3. 保留 `file_id`
4. 提升 `parse_version` / `index_version` 或绑定新的运行版本
5. 派发 runtime ingestion

### 7.3 任务结果回写

Backend 需要能接收 runtime 结果：

- `queued`
- `running`
- `succeeded`
- `failed`

回写时至少更新：

- `files.status`
- `tasks.status`
- `tasks.updated_at`
- `tasks.error_*`

### 7.4 P4 本地联调基线

为保证 `P4` 能在无独立队列和无对象存储真依赖的情况下先联通，首版允许采用以下本地基线：

- Backend 先把上传文件写入本地临时目录，再把绝对路径作为 `object_uri`
- `POST /api/kb/files` 和 `POST /api/kb/files/{file_id}/reindex` 返回时任务状态保持 `queued`
- Backend 在后台 goroutine 中把任务切到 `running`，再调用 runtime `/v1/ingestion/run`
- runtime 返回后，Backend 统一回写 `succeeded` 或 `failed`
- 当 runtime 不可达或解析异常时，任务必须带稳定错误类别，例如 `runtime_unavailable`、`parse_failure` 或 `unexpected_parse_failure`

这个基线的目标是先打通“上传 -> 派发 -> 回写”的可追踪链路，后续再替换为真正的对象存储、队列和回调机制。

### 7.5 T7b MinIO 对象存储 Adapter

`T7b` 只替换上传对象的存储边界，不同时替换 Backend repository、异步队列或 runtime 回写机制。

实现约定：

- 未配置 `OBJECT_STORE_ENDPOINT` 或 `OBJECT_STORE_BUCKET` 时，Backend 继续使用 `BACKEND_UPLOAD_DIR` / 系统临时目录作为本地 fallback。
- 配置对象存储后，Backend 将上传内容写入 S3-compatible object store，默认目标为 MinIO。
- `object_uri` 格式固定为 `s3://{bucket}/{object_key}`，其中 object key 由 `kb/{file_id}/{sanitized_filename}` 组成。
- Backend 在上传前应确保 bucket 存在；bucket 不存在时允许创建。
- 对象存储访问使用 `OBJECT_STORE_ACCESS_KEY` / `OBJECT_STORE_SECRET_KEY`，本地 compose 可回退到 `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`。
- Runtime 负责在 ingestion 前把 `s3://` 对象下载到临时文件，再复用现有 parser registry；parser 本身不感知 MinIO。
- 对象存储失败必须让 `POST /api/kb/files` 返回上传失败，不创建 file / task 元数据。

验收重点：

- 未配置对象存储时，现有 `make backend-test`、`make runtime-test`、`make integration` 行为不变。
- 配置本地 MinIO 后，上传返回的 `file.object_uri` 应为 `s3://...`，Runtime ingestion 应能解析该对象并进入 `succeeded`。

### 7.6 T7d Redis 摄取队列 Adapter

`T7d` 只替换 Backend 内部的 ingestion dispatch 机制，不同时替换 session store、runtime 回写接口或外部 worker pool。

实现约定：

- Service 层依赖 `taskqueue.Queue` 抽象，只处理 `file_id` / `task_id` job，不把完整文件内容塞进队列。
- 未配置 `REDIS_URL` 时，Backend 使用进程内本地队列，保持 `make backend-test` 和普通 `make integration` 不依赖 Docker。
- 配置 `REDIS_URL` 时，Backend 使用 Redis list 作为 ingestion queue；队列名通过 `REDIS_INGESTION_QUEUE` 配置，默认 `see_agent:ingestion`。
- `POST /api/kb/files` 与 reindex 仍在 repository 中创建 `queued` task，然后 enqueue job，由 Backend worker 消费后切到 `running` 并调用 runtime `/v1/ingestion/run`。
- Worker 消费 job 时必须重新读取 repository 中的 file / latest task；如果 task 已过期或不存在，应跳过该 job，避免旧 reindex job 覆盖新状态。
- Queue enqueue 失败时，Backend 应把 task 标为 `failed`，错误码为 `queue_unavailable`，并向调用方返回基础设施错误。
- Runtime 调用失败仍使用现有 `runtime_unavailable`；结果写回失败仍使用 `state_writeback_failed`。
- 首版不承诺进程 crash 后正在执行的 `running` task 自动恢复；Redis queue 只保证尚未被 worker 取出的 `queued` job 在 Backend 重启后仍可继续消费。后续如需要更强语义，再引入 pending / processing 双队列、visibility timeout 或外部 worker。

验收重点：

- 未配置 Redis 时，现有 `make backend-test` 和 `make integration` 行为不变。
- 配置本地 Redis 后，上传应先进入 Redis queue，再由 Backend worker 完成 ingestion，并最终进入 `succeeded`。
- 新增 `make redis-ingestion-smoke` 验证真实 Redis 下的上传、队列消费、runtime ingestion 和聊天检索可见。

## 8. Chat 编排

### 8.1 `POST /api/chat/conversations/{conversation_id}/messages`

流程建议：

1. 校验会话存在、用户身份和资源 ownership。
2. 读取会话固定的 config/retrieval/model snapshot。
3. 在同一请求幂等上下文中解析 active published knowledge snapshot，并固定本次 `knowledge_scope`；同一请求的重试必须复用首次选择的 scope。
4. scope 不存在、manifest 不可读或成员不完整时返回 `knowledge_snapshot_unavailable`，不写 user/assistant exchange。
5. 持久化 user message，并读取最近会话消息；若它是 `system_default` 会话首个成功持久化的用户问题，在同一 transaction 内更新标题。
6. 构造带 config snapshot 与 `knowledge_scope` 的 `chat-runtime-request`。
7. 调 runtime `/v1/chat/run` 或 `/v1/chat/stream`。
8. 在一个写入路径中持久化 assistant message provenance、citation revision/hash 和日志。
9. 返回非流式响应或转发流式事件。

### 8.2 为什么聊天入口必须经 Backend

因为 Backend 需要：

- 固定会话配置并解析每次消息的 active knowledge scope
- 持久化消息
- 写 retrieval / generation 日志索引
- 隐藏 runtime 内部细节

### 8.3 T3 本地联调基线

为保证纯 Backend 单元测试不依赖额外 runtime 进程，当前本地基线采用以下规则：

- 只有当 `AI_RUNTIME_BASE_URL` 被显式配置时，`POST /api/chat/conversations/{conversation_id}/messages` 才同步调用 runtime `/v1/chat/run`。
- 集成测试和本地全栈启动应显式设置 `AI_RUNTIME_BASE_URL`，并验证 assistant 内容不再是 Backend placeholder。
- Runtime 未配置或缺少 active scope 时，任何知识回答请求都必须返回明确失败，不能写入看似成功但没有 provenance 的回答。测试只可通过显式 Runtime fake 注入覆盖，不存在产品 fallback 分支。
- 已显式配置 runtime 但调用失败时，Backend 不应返回看似成功的 fallback answer，而应返回 `runtime_unavailable` 类基础设施错误。
- `chat-runtime-request.messages` 只承载历史消息；当前用户问题只放入 `user_query`，避免 prompt assembly 中重复注入当前问题。

### 8.4 T12 SSE 流式骨架

`T12` 先打通浏览器到 Backend 的 SSE 通道，不把真实本地模型 token stream 作为前置条件：

- 当请求体 `stream=true`，或请求头 `Accept` 包含 `text/event-stream` 时，Backend 返回 `text/event-stream`。
- Backend 使用 Runtime `/v1/chat/stream` 的原生 delta；Runtime 缺失、连接中断或终态契约无效时发送唯一 `message.error`，不再合成分块或完成事件。
- SSE 事件顺序固定为：
  - `message.started`：包含已持久化的 `user_message` 和一个 `status=streaming`、`content=""` 的 assistant 占位消息。
  - `message.delta`：包含 `message_id` 和 `delta`。
  - `message.completed`：包含最终 `user_message` 和 `assistant_message`，其中 assistant message 必须是已持久化的完成态。
- `message.error`：保留给后续“流已开始后失败”的错误事件；当前实现会尽量在写 SSE header 前返回标准 JSON 错误。
- `T14` 已按这个边界接入 Runtime 原生 token stream，只替换 Backend 内部 delta 来源，Web 和外部 API 事件契约保持稳定。

### 8.5 T14 Runtime 原生 Token Stream

`T14` 把 Backend SSE 的 delta 来源从“完整回答本地切片”替换为 Runtime 流式输出，同时保持浏览器和 Web proxy 事件契约不变。

实现约定：

- Browser-facing Backend SSE 事件仍固定为 `message.started`、`message.delta`、`message.completed`，Web 不需要区分 delta 来自 Backend 切片还是 Runtime token stream。
- Backend 在 `stream=true` 时优先调用 Runtime `/v1/chat/stream`；非流式 JSON 路径继续调用 `/v1/chat/run`。
- Runtime stream endpoint 对 Backend 输出内部 SSE：
  - `message.delta`：包含 `request_id`、`message_id`、`delta`。
  - `message.completed`：包含完整 `chat_runtime_response`，其 schema 与 `/v1/chat/run` 响应一致。
  - `message.error`：包含稳定错误摘要，用于流已开始后的失败。
- Backend 收到 Runtime `message.delta` 后立即转写为 browser-facing `message.delta`。
- Backend 收到 Runtime `message.completed` 后，用完整 `chat_runtime_response` 落库 user / assistant message 与 citations，再发送 browser-facing `message.completed`。
- 如果 Runtime 不支持 stream 或在写入 SSE header 前失败，Backend 返回标准 `runtime_unavailable` JSON 错误；如果 browser-facing stream 已开始后 Runtime 失败，Backend 发送 `message.error`。
- Runtime 无真实外部模型或模型调用失败时必须返回 `message.error`，Backend 不得持久化成功 exchange；integration 使用显式 test-only OpenAI-compatible provider，不依赖产品 fallback。

验收重点：

- `make integration` 中既有浏览器与 API SSE smoke 不需要改前端协议即可通过。
- 新增 Runtime stream 单测，断言 `/v1/chat/stream` 返回 `message.delta` 与 `message.completed`。
- 新增 Backend stream 单测，断言 Runtime delta 会被转发，而不是由 Backend 对完整回答本地切片。

## 9. Citation 持久化与读取

### 9.1 持久化目标

至少要能通过：

- `conversation_id`
- `message_id`

定位本次回答的 citation 列表。

assistant message 还必须保存实际 `knowledge_snapshot_id`、manifest hash、answered time 与 freshness status；每条 citation 保存 `document_id`、`revision_id`、`chunk_content_sha256`、rank、score 与 locator。历史 sources 不从当前 active snapshot 反推。

### 9.2 读取接口

`GET /api/chat/conversations/{conversation_id}/sources/{message_id}`

Backend 负责：

- 根据 message 读取 citation 记录
- 返回回答 provenance 与 citation revision/hash
- 整理成前端稳定展示对象
- 不直接暴露 runtime 内部中间结构

## 10. 配置版本化

### 10.1 Agent Config

`PUT /api/agent/config` 不应原地修改，而应：

1. 创建新版本
2. 标记为当前活动版本
3. 保留历史记录

### 10.2 Retrieval Config

`PUT /api/agent/retrieval-config` 同理：

1. 创建新版本
2. 更新当前活动版本引用
3. 保留历史

### 10.3 产品语义

Backend 返回时必须明确提示：

- 新配置只影响新会话
- 既有会话继续使用旧 config/retrieval/model snapshot；知识仍按每条新消息选择 latest published snapshot

## 11. 日志设计

建议将日志拆为两层：

### 11.1 产品级索引日志

Backend 保存：

- `request_id`
- `conversation_id`
- `message_id`
- `model_id`
- `created_at`

### 11.2 深度调试日志

Runtime 可保存更细日志，但 Backend 对外只暴露稳定摘要。

这样可以避免：

- 前端依赖 runtime 私有字段
- 调试日志结构频繁变化影响产品 API

### 11.3 本地开发启动摘要

Backend 在 repository、queue、session store 与 Runtime client 完成构造后，必须向控制台输出脱敏启动摘要：

- repository 是 Postgres 还是 memory；Postgres 只显示 host/port/database，不显示密码或 query；
- ingestion queue、session store 是 Redis 还是进程内实现；Redis 只显示 host/port/database；
- Runtime endpoint 与 K3 delivery 是否启用；
- object store 是本地目录还是 S3-compatible endpoint/bucket；
- 当前 active quality release 所绑定的 `config_version/model_id` 与 `retrieval_config_version/retrieval_strategy/embedding_model`。

该摘要由 Backend 自己的权威 store 和实际环境构造，不由 Web 推断。它用于判断“当前发布配置要求什么”，而 Runtime 启动摘要用于判断“当前进程实际连接了什么”；两边不一致时应被视为配置错误并由现有 fail-closed 请求边界暴露。摘要不新增产品 API，也不改变 `/healthz` 的进程存活语义。

## 12. Repository 设计建议

首版至少包含：

- `ConversationRepository`
- `MessageRepository`
- `FileRepository`
- `IngestionTaskRepository`
- `ConfigRepository`
- `CitationRepository`
- `LogRepository`

每个 repository 只做：

- 增删改查
- 列表查询
- 状态更新

不承担业务流程编排。

### 12.1 T7c Postgres Repository Adapter

`T7c` 只替换 Backend 产品元数据 repository，不同时替换 session store、Redis queue 或 Runtime 检索存储。

实现约定：

- `smoke/test` 可通过显式 profile 或依赖注入使用 `MemoryStore`；`fullstack_dev/production` 缺少 `DATABASE_URL` 时启动失败，不能切换为内存 repository。
- 配置 `DATABASE_URL` 后，Backend 启动时创建 Postgres-backed store，并自动执行最小 schema bootstrap。
- Postgres store 需要覆盖当前 `repository.Store` 的全部 MVP 方法：config、retrieval config、conversation、message、citation、knowledge file、latest ingestion task、chunk summary。
- 首版 schema 可内置在应用启动中，后续发布前再拆成 migration 文件。
- 默认 seed 数据与 `MemoryStore` 对齐：`cfg_001`、`rcfg_001` 和 `file_001` 示例文件，保证新库启动后页面和 smoke 仍有可见数据。
- 文件、任务、会话、消息 ID 继续保持当前 `conv_001` / `msg_001` / `file_001` / `task_001` 格式，便于前端和现有 smoke 不变。
- `IngestionTask` 历史可保留多条；产品 API 当前只读取每个文件的最新任务。
- chunk inspector 首版继续保存 summary chunk，不替代 Runtime/Qdrant 中的真实 chunk store。

验收重点：

- 未配置 `DATABASE_URL` 时，`make backend-test` 和 `make integration` 仍通过。
- 配置本地 Postgres 后，创建 conversation / 上传文件 / runtime 回写 / 读取详情在 Backend 重启后仍可见。
- 新增 `make postgres-repository-smoke` 验证真实 Postgres 持久化路径。
- 当前状态：以上验收已通过，真实 Postgres smoke 使用 `make postgres-repository-smoke`。

## 13. 错误处理

建议按三层分类：

- `validation_error`
- `domain_error`
- `infrastructure_error`

### 13.1 典型场景

- 文件不存在
- 会话不存在
- 非法状态迁移
- runtime 调用失败
- 对象存储写入失败

### 13.2 对外规则

对外统一走 `ErrorEnvelope`，不要把 Go 内部错误直接透传给前端。

## 14. 实现顺序建议

建议 Backend 实现按以下顺序推进：

1. 基础路由与错误中间件
2. conversation / message repository
3. conversation service 与快照绑定
4. knowledge file / ingestion task repository
5. runtime client
6. upload / reindex 编排
7. citations 与日志接口
8. config version API

## 15. 与当前代码骨架的关系

当前 [server.go](/Users/populus/workspace/agent/see-agent/services/backend-go/internal/server/server.go) 已只保留依赖装配和路由注册责任，最小实现已经拆出：

- `httpapi/`
- `service/`
- `repository/`
- `runtime/`
- `domain/`

当前实现仍是本地开发基线：

- repository 默认使用内存实现，适合 smoke test 和前后端联调；配置 `DATABASE_URL` 后可切到 Postgres-backed repository。
- 鉴权仍以内存账号为主；session 默认使用内存 store，配置 Redis 后可切到 Redis-backed session store。当前仍不是生产身份源形态。
- 上传文件默认写入本地临时目录，配置 MinIO 后写入 `s3://...` 对象存储。
- ingestion dispatch 默认使用进程内本地 queue；配置 `REDIS_URL` 后切到 Redis-backed queue，并由 Backend worker 消费后调用 runtime `/v1/ingestion/run`。
- 当 `AI_RUNTIME_BASE_URL` 显式配置时，非流式聊天消息会同步调用 runtime `/v1/chat/run`；流式聊天消息会调用 runtime `/v1/chat/stream`，并把 assistant message、finish reason 与 citations 写回 repository。
- 当 `AI_RUNTIME_BASE_URL` 未配置或 Runtime 不可用时，聊天直接返回 `runtime_unavailable`，或在流已开始后发送 `message.error`；不得生成或持久化本地 fallback answer。

下一步最自然的演进方式是：

- 把 `MemoryStore` 中的会话、文件、任务、配置职责拆成更细的 repository 文件或接口实现。
- 将 chat fallback answer 替换为更明确的 runtime unavailable 错误语义或可观测降级事件。
- 配置真实外部本地模型端点后重跑 `make eval-runtime-baseline`，并基于 failure report 优化检索 / 引用质量。
