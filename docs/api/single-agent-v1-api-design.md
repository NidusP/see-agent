# 单智能体 V1 API 设计说明

## 文档控制

- 状态：用于 `P0` 契约冻结的草案
- 版本：`v1.0`
- 目标：把 SDD 中的 API 面补充到“足以冻结契约字段和模块边界”的细度

## 1. 设计目标

V1 API 设计服务于以下目标：

- 让 `Next.js` 前端和 `Go` 后端围绕一份稳定接口协作
- 让 `Go` 后端与 `Python` AI Runtime 的边界保持清晰
- 让“会话快照固定”“知识文件状态可追踪”“引用可稳定展示”成为契约层能力
- 保持本地模型调试与后续私有推理主机切换时的接口稳定

## 2. API 分层原则

### 2.1 外部产品 API

由 Web 前端调用，面向产品功能，统一挂在 `/api/` 下。

包括：

- 鉴权
- 聊天
- 知识库文件
- 配置
- 评测
- 日志

### 2.2 内部运行时 API

由 Go 后端调用 Python AI Runtime，不直接暴露给终端用户。

首批建议：

- `POST /v1/chat/run`
- `POST /v1/ingestion/preview`
- `POST /v1/ingestion/run`

这部分不放在 `openapi.yaml` 的首批范围内，而是先通过 JSON Schema 与局部实现文档冻结。

## 3. 通用约定

### 3.1 命名

- 对外 JSON 字段统一使用 `snake_case`
- ID 字段统一显式命名，不使用通用 `id` 代替
- 版本字段必须显式标出对象含义，例如：
  - `config_version`
  - `retrieval_config_version`
  - `parse_version`
  - `index_version`

### 3.2 时间

- 所有时间字段统一使用 RFC 3339 / ISO 8601 字符串
- 统一使用 UTC 存储和传输

### 3.3 分页

首批列表接口统一使用游标前的简单分页：

- `limit`
- `next_cursor`

如果首批实现阶段不做游标，也必须至少保留：

- `items`
- `total`

### 3.4 错误返回

严格产品错误 envelope（F0 目标）为：

```json
{
  "error": {
    "code": "dependency_unavailable",
    "message": "当前无法安全完成操作，请稍后重试。",
    "retryable": true,
    "request_id": "req_123",
    "stage": "runtime_response_validation"
  }
}
```

字段语义：

- `code`
- `message`：对用户安全的中文提示，不含 DSN、凭据、内部 host 或 prompt。
- `retryable`
- `request_id`
- `stage`：稳定、受控的产品阶段，不是原始异常；管理员可在 execution evidence 中读取脱敏诊断。

HTTP `4xx/5xx` 只表示未能完成执行。`clarification_required`、`insufficient_evidence` 和 `partial_answer` 是已完成的业务处置，必须作为成功 message 返回，不能包装为错误。

### 3.5 流式回答

聊天回答默认支持流式。

V1 可以先用以下两种方式之一：

1. `text/event-stream`
2. 非流式 `application/json` 兜底

但契约中必须明确：

- 最终 assistant message 的 `message_id`
- 是否完成
- 最终 citations 如何返回
- `message.completed` 与 `message.error` 必须互斥且最多各出现一次；SSE 已开始后若 Runtime、验证或持久化失败，只能以 `message.error` 终止，不能发出 completed 或留下一组来源。
- 流中 delta 不是持久化事实。最终 response 通过 strict execution validator 且 Backend 写事务提交前，Web 不得将其作为已完成回答或来源展示。

## 4. 核心对象

### 4.1 Conversation

最小字段：

- `conversation_id`
- `owner_id`
- `title`
- `created_at`
- `updated_at`
- `config_version`
- `retrieval_config_version`
- `knowledge_snapshot_id`
- `model_id`

设计原因：

- 会话必须绑定快照，才能保证“旧会话不被新配置污染”

### 4.2 Message

最小字段：

- `message_id`
- `conversation_id`
- `role`
- `content`
- `status`
- `created_at`

assistant message 额外可带：

- `finish_reason`
- `citations_count`

### 4.3 KnowledgeFile

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

建议状态枚举：

- `uploaded`
- `queued`
- `running`
- `succeeded`
- `failed`
- `deleted`

### 4.4 Citation

最小字段：

- `message_id`
- `chunk_id`
- `file_id`
- `filename`
- `rank`
- `score`
- `citation_label`
- `source_locator`
- `snippet`

`source_locator` 要求是结构化对象，不是单纯字符串。

示例：

```json
{
  "source_format": "xlsx",
  "sheet_name": "Summary",
  "cell_range": "A1:F5"
}
```

## 5. 端点分组设计

### 5.1 鉴权 API

#### `POST /api/auth/login`

目标：

- 建立内部单租户登录态

请求体最小字段：

- `username`
- `password`

响应体最小字段：

- `user_id`
- `display_name`
- `session_expires_at`

#### `POST /api/auth/logout`

目标：

- 清理当前登录态

响应体最小字段：

- `ok`

### 5.2 聊天 API

#### `GET /api/chat/conversations`

目标：

- 只返回当前 session subject 拥有的会话列表

响应体最小字段：

- `items`
  - `conversation_id`
  - `title`
  - `updated_at`
- `total`

#### `POST /api/chat/conversations`

目标：

- 创建一个新会话，写入当前 session subject 为 `owner_id`，并冻结当时的配置快照

请求体最小字段：

- `title`
  - 可选；空白时 Backend 写入“新对话”，非空值视为用户手动标题

响应体最小字段：

- `conversation`

#### `GET /api/chat/conversations/{conversation_id}`

目标：

- 仅当当前 session subject 是 owner 时返回会话元数据和最近消息；不存在与非 owner 均返回 `404 conversation_not_found`

响应体最小字段：

- `conversation`
- `messages`

#### `PATCH /api/chat/conversations/{conversation_id}`

目标：

- 仅 owner 可手动重命名会话；不存在与非 owner 均返回 `404 conversation_not_found`。

请求体：

- `title`：必填；服务端合并空白后必须为 1–48 个 Unicode rune。

响应体：

- `conversation`

规则：

- 返回 `422 validation_error` 时不修改标题。
- 成功写入将内部来源标记为 `user_edited`，之后不能被首问自动命名覆盖。

#### `POST /api/chat/conversations/{conversation_id}/messages`

- 仅 owner 可发送；ownership 在解析 snapshot、调用 Runtime 和写 message 之前校验。

目标：

- 提交用户问题，并触发运行时回答
- 对尚未命名的会话，首个成功持久化的用户问题会在同一持久化 transaction 内生成标题；失败或未持久化的请求不改标题。

请求体最小字段：

- `content`
- `stream`

响应策略：

- 流式模式返回 token 增量和完成事件
- 非流式模式返回完整 assistant message

非流式最小响应字段：

- `user_message`
- `assistant_message`

执行规则：

- 请求以 `request_id`（或服务端生成的等价 idempotency key）对应一个 attempt；重试创建关联的新 attempt，不覆盖失败记录。
- `assistant_message.status=completed` 只允许 `grounded_answer`、`partial_answer`、`clarification_required`、`insufficient_evidence` 和 `controlled_response`。运行失败不返回 completed assistant；如持久化 placeholder，只能是无正文、零 citation 的 `failed` 状态。
- 只读 sources endpoint 只返回本次 answer 实际使用的 citation，不能暴露 retrieval candidates、默认 fixture 或失败 attempt 的数据。

#### `GET /api/chat/conversations/{conversation_id}/sources/{message_id}`

- 仅 owner 可读取，且 message 必须属于 path 中的 conversation；不存在、错配或非 owner 均返回 `404`。

目标：

- 返回指定 assistant message 的来源列表

响应体最小字段：

- `message_id`
- `items`
  - `chunk_id`
  - `file_id`
  - `filename`
  - `citation_label`
  - `source_locator`
  - `snippet`
  - `score`

### 5.3 知识库 API

#### `GET /api/kb/files`

目标：

- 返回知识文件列表

查询参数建议：

- `status`
- `source_format`
- `limit`

响应体最小字段：

- `items`
- `total`

#### `POST /api/kb/files`

目标：

- 上传文件并创建摄取任务

请求体：

- 首选 `multipart/form-data`

首批字段：

- `file`
- `force_reindex`

响应体最小字段：

- `file`
- `ingestion_task`

#### `GET /api/kb/files/{file_id}`

目标：

- 返回单个文件元数据与最近任务状态

响应体最小字段：

- `file`
- `latest_task`

#### `DELETE /api/kb/files/{file_id}`

目标：

- 删除文件及其索引可见性

响应体最小字段：

- `ok`
- `file_id`

#### `POST /api/kb/files/{file_id}/reindex`

目标：

- 为既有文件触发重新解析 / 重建索引

请求体最小字段：

- `force_reindex`

响应体最小字段：

- `ingestion_task`

#### `GET /api/kb/files/{file_id}/chunks`

目标：

- 供管理员检查 chunk 与 source locator

查询参数建议：

- `limit`

响应体最小字段：

- `items`
  - `chunk_id`
  - `block_type`
  - `heading_path`
  - `page_no`
  - `source_locator`
  - `text`
- `total`

### 5.4 配置 API

#### `GET /api/agent/config`

目标：

- 读取当前有效模型与提示词配置

#### `PUT /api/agent/config`

目标：

- 创建新的**候选**配置版本，而不是原地覆盖；候选必须经 Q1 质量实验、审批和显式 publish 后才会成为后续新会话的 active pair

请求体最小字段：

- `model_id`
- `system_prompt_version`
- `temperature`
- `max_output_tokens`

响应体最小字段：

- `config_version`
- `created_at`

#### `GET /api/agent/retrieval-config`

目标：

- 读取当前检索配置

#### `PUT /api/agent/retrieval-config`

目标：

- 创建新的候选检索配置版本；不得因创建时间更晚而自动影响线上会话

请求体最小字段：

- `retrieval_strategy`：已注册的受控策略；当前为 `lexical_compat_v1` 或 `native_dense_v1`
- `embedding_model`
- `top_k`
- `rerank_enabled`

Backend 会将 `retrieval_strategy`、`embedding_model`、`top_k`/`rerank_enabled` 写入 response override。Runtime profile 必须与 strategy 精确相同；native dense 的 embedding model 还必须与 active provider 相同，否则 Runtime fail-closed，不能回退词法检索或动态加载其他实现。

响应体最小字段：

- `retrieval_config_version`
- `created_at`

#### `GET /api/agent/config/versions`

目标：

- 查看历史版本列表

响应体最小字段：

- `items`
- `total`

### 5.5 评测与日志 API

#### `POST /api/eval/reports`

目标：

- 幂等登记已完成的独立评测报告。它是原始证据，不是发布许可。

请求体最小字段：

- `dataset_id`
- `model_id`
- `config_version`
- `retrieval_config_version`

#### `GET /api/eval/reports/{report_id}`

目标：

- 查看评测报告摘要

响应体最小字段：

- `report_id`
- `status`
- `metrics`
- `created_at`

### 5.6 质量反馈、实验与发布 API

所有 `/api/admin/quality/**` 接口均为管理员接口；写操作要求 `Idempotency-Key`。完整机器可读契约以 `packages/contracts/openapi.yaml` 为准。

- `POST/GET /api/admin/quality/feedback`：将正确、错误、应拒答、证据问题、知识缺口或 A1 处置问题锚定到一条已有 execution evidence 的 assistant message；`should_clarify`、`unnecessary_clarification`、`incomplete_coverage` 必须分别匹配 target 的实际 answer disposition、非空 comment 且无 citation rank。不会改写消息、citation 或 provenance。
- `POST/GET /api/admin/quality/experiments`、`GET /{experiment_id}`：创建并读取固定 scope/dataset/config condition 的受控实验。
- `POST /{experiment_id}/feedback`、`/freeze`、`/reports`、`/approve`、`/publish`：按状态机附着反馈、冻结条件、由 runner 登记 exact-match 报告、审批和原子发布。
- `GET /api/admin/quality/releases`、`POST /{release_id}/rollback`：读取 immutable release 历史，或以新 release 前滚回退。

发布只影响之后创建的 conversation；Backend 必须从 Postgres active release pointer 一次性取得 agent/retrieval config pair。候选、report mismatch、未通过 gate、非法状态或冲突不得改变该 pointer。

#### `GET /api/logs/retrieval`

目标：

- 查看检索日志

建议查询参数：

- `conversation_id`
- `message_id`
- `limit`

#### `GET /api/logs/generation`

目标：

- 查看生成日志

建议查询参数：

- `conversation_id`
- `message_id`
- `limit`

## 6. 与运行时契约的边界

以下内容不应直接塞进对外 API：

- runtime 内部 parser 细节
- embedding 向量内容
- reranker 中间分数的全部内部字段
- 原始模型服务请求细节

这些信息应由后端在内部边界上转换后，再以稳定产品对象返回给前端。

## 7. 本文档如何驱动下一步

本文档完成后，后续应按以下顺序推进：

1. 把 `packages/contracts/openapi.yaml` 从端点占位升级为真实请求 / 响应契约
2. 把 `chat-runtime-request.schema.json` 和 `ingestion-task.schema.json` 与本文档对齐
3. 基于冻结后的契约补 Web、Backend、Runtime 三侧实现设计
