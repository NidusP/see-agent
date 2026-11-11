# 单智能体 V1 系统设计文档

## 文档控制

- 状态：活动设计
- 版本：`v1.1`
- 更新日期：`2026-07-14`
- 范围：单智能体 V1 的架构与接口

## 1. 设计目标

设计一个私有、可维护的单智能体系统，支持：

- 文档摄取
- 增量知识索引
- 知识 revision、snapshot 与回答时效
- 有依据的问题回答
- 由管理员控制的运行时配置
- 评测驱动的质量迭代

## 2. 架构摘要

系统拆分为五层：

1. Web 客户端
2. 业务后端
3. AI 运行时
4. 存储与索引层
5. 模型服务层

这种拆分方式使业务 CRUD 关注点与解析、检索和模型编排解耦。

## 3. 推荐技术方向

- 前端：`Next.js`
- 业务后端：`Go`
- AI 运行时：`Python`
- 关系型存储：`PostgreSQL`
- 队列 / 缓存：`Redis`
- 对象存储：`MinIO`
- 向量库：`Qdrant`
- 本地开发模型服务：`Ollama`
- 生产级模型服务：`vLLM`
- 基线聊天模型：`Qwen`
- 基线 embedding：`BGE-M3`
- 基线 reranker：`bge-reranker-v2-m3`

## 4. 系统组件

### 4.1 Web 客户端

职责：

- 登录流程
- 聊天界面
- 流式回答渲染
- 会话历史
- 来源展示
- 管理员知识上传
- 管理员配置界面

### 4.2 业务后端

职责：

- 鉴权与会话管理
- 文件元数据管理
- 配置版本化
- 会话元数据管理
- 将请求路由到 AI 运行时
- 生成日志与审计记录

### 4.3 AI 运行时

职责：

- 文档解析
- 归一化
- 切块
- embedding 编排
- 检索
- reranking
- prompt 组装
- 模型调用
- 来源归因打包
- 执行评测

### 4.4 存储与索引层

- `PostgreSQL`
  - users
  - config versions
  - file metadata
  - knowledge document revisions, snapshots and update events
  - conversations
  - messages
  - eval metadata
- `Redis`
  - Streams consumer groups for asynchronous ingestion and revalidation tasks
  - 瞬时缓存
- `MinIO`
  - 原始文件
  - 解析器输出
  - 可选的图片裁剪结果
- `Qdrant`
  - revision-aware chunk vectors
  - snapshot and visibility-filtered retrieval metadata

### 4.5 模型服务层

- 本地或远端的私有推理主机
- 开发调试默认面向开发者本机上的本地模型
- 优先采用 OpenAI-compatible serving，以提高后端可移植性
- 开发机默认推荐 `Ollama`，便于本地快速联调
- staging / production 推荐 `vLLM`，以获得更稳定的吞吐与并发能力
- 生产环境一次只配置一个聊天模型
- 评测模式允许使用多个模型进行受控对比

## 5. 运行流程

面向学习的端到端职责、权威边界和失败处理说明见 [摄取、检索与回答职责导读](single-agent-v1-ingestion-chat-boundary-guide.md)。本节保留架构级摘要；详细领域状态机与 API 契约仍以各专项设计为准。

### 5.1 摄取流程

1. 管理员通过后端上传文件。
2. 后端存储文件元数据和对象引用。
3. 后端创建 `queued` 摄取任务并返回受理结果。
4. 后端异步派发摄取任务到 AI 运行时。
5. 解析器提取结构化内容。
6. 切块器生成检索单元。
7. embedding 服务将 chunk 向量化。
8. Qdrant 存储向量及其元数据。
9. 后端根据运行时结果将对应 revision 标记为 `ready` 或 `failed`，并保留 delivery 审计。
10. 只有 revision 完整 ready 后，Backend 才发布新的 knowledge snapshot 并记录更新事件。

### 5.2 问答流程

1. 用户提交问题。
2. 后端解析会话配置快照，并在每条新问题开始时解析最新 published knowledge snapshot。
3. 后端将请求转发给 AI 运行时。
4. 运行时可选地重写查询。
5. 运行时检索候选 chunk。
6. 运行时对候选结果 rerank。
7. 运行时构建最终回答 prompt。
8. 运行时调用选定的模型服务。
9. 运行时返回流式回答和来源元数据。
10. 后端持久化日志、消息、回答使用的 knowledge snapshot 与 revision-aware citations。

### 5.3 配置更新流程

1. 管理员编辑运行时配置。
2. 后端写入新的配置版本。
3. 新会话绑定到最新配置版本。
4. 既有会话继续固定在原始快照上。

## 6. 数据契约

### 6.1 文件元数据

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

### 6.2 Chunk 记录

最小字段：

- `chunk_id`
- `file_id`
- `document_id`
- `source_format`
- `block_type`
- `heading_path`
- `page_no`
- `source_locator`
- `text`
- `token_count`
- `embedding_model`
- `index_version`

### 6.3 会话配置快照

最小字段：

- `conversation_id`
- `config_version`
- `retrieval_config_version`
- `model_id`
- `created_at`

`knowledge_snapshot_id` 不是永久 conversation 属性。Backend 在每条新用户消息开始时读取最新 published snapshot，并将其写入该次 assistant message 的 provenance。

### 6.4 回答 provenance 与来源

最小字段：

- `message_id`
- `knowledge_snapshot_id`
- `knowledge_snapshot_manifest_hash`
- `answered_at`
- `freshness_status`
- `chunk_id`
- `file_id`
- `document_id`
- `revision_id`
- `chunk_content_hash`
- `rank`
- `score`
- `citation_label`
- `source_locator`

### 6.5 知识演进

逻辑文档、不可变 revision、published knowledge snapshot 与 append-only update event 是不同对象。PostgreSQL 是它们的权威写入源；MinIO 与 Qdrant 是 revision 的对象/检索投影。完整状态机、事务和核验语义见 `single-agent-v1-knowledge-evolution-design.md`。

## 7. API 面

初始 API 面按以下分组组织。

### 7.1 鉴权

- `POST /api/auth/login`
- `GET /api/auth/me`
- `POST /api/auth/logout`

V1 MVP 鉴权采用 Backend 维护的单租户轻量 session：

- 登录成功后设置 HttpOnly session cookie，并额外设置只用于 Web 体验判断的 `role` cookie。
- `/api/chat/**` 要求已登录，`user` 与 `admin` 均可访问。
- `/api/kb/**` 与 `/api/agent/**` 要求 `admin`。
- Web 可以用 `role` cookie 隐藏管理员导航，但后端必须重新验证 session 和角色。
- 开发默认账号可由环境变量覆盖；生产接入企业身份源属于后续版本。

### 7.2 聊天

- `POST /api/chat/conversations`
- `GET /api/chat/conversations`
- `GET /api/chat/conversations/{id}`
- `POST /api/chat/conversations/{id}/messages`
- `GET /api/chat/conversations/{id}/sources/{message_id}`
- `POST /api/chat/conversations/{id}/messages/{message_id}/revalidate`

### 7.3 知识库

- `POST /api/kb/files`
- `GET /api/kb/files`
- `GET /api/kb/files/{file_id}`
- `DELETE /api/kb/files/{file_id}`
- `POST /api/kb/files/{file_id}/reindex`
- `GET /api/kb/files/{file_id}/chunks`
- `GET /api/kb/updates`
- `GET /api/kb/snapshots/{snapshot_id}`
- `POST /api/kb/documents/{document_id}/revisions`
- `POST /api/kb/documents/{document_id}/disable`

### 7.4 智能体配置

- `GET /api/agent/config`
- `PUT /api/agent/config`
- `GET /api/agent/retrieval-config`
- `PUT /api/agent/retrieval-config`
- `GET /api/agent/config/versions`

### 7.5 评测与日志

- `POST /api/eval/run`
- `GET /api/eval/reports/{id}`
- `GET /api/logs/retrieval`
- `GET /api/logs/generation`

## 8. 检索设计

### 8.1 解析

V1 支持的摄取类型：

- txt
- markdown
- json
- pdf
- docx
- xlsx
- pptx

V1 首批实现不将 `doc`、`xls`、`ppt` 视为强制门槛。如需支持，应先通过转换流程归一化到 `docx`、`xlsx`、`pptx` 或 `pdf`。

各格式建议采用如下标准化策略：

- `txt`
  - 按自然段落与空行切分，保留文件名和段落顺序作为最基础结构信息
- `md`
  - 保留标题层级、列表结构和代码块边界
- `json`
  - 根据业务字段选择可读路径，将嵌套结构展开为稳定文本块
- `pdf`
  - 先走文本提取，必要时对页面执行 OCR，并保留页码定位
- `docx`
  - 提取标题、正文、表格和列表，保留标题路径
- `xlsx`
  - 以 workbook / sheet / table / cell-range 为单位生成结构化块
- `pptx`
  - 以 slide 为主单位提取标题、正文、表格和可选 notes

### 8.2 切块规则

基线规则：

- 优先遵循语义边界，而不是只按固定字符长度切分
- 在元数据中保留标题结构
- 保留页码、sheet、slide、表格或单元格范围等结构定位信息
- 在可行时将 FAQ 条目、政策要点和表格隔离成独立单元
- 目标 chunk 大小保持在中等范围，而不是把上下文一股脑塞满

### 8.3 检索策略

基线目标：

- dense retrieval
- 可选 sparse 或 hybrid retrieval
- 在最终上下文组装前先做 reranking
- 当需要文档分区时使用元数据过滤

### 8.4 回答策略

- 优先基于证据作答
- 证据不足时拒答
- 对有依据的内容提供引用，并优先展示页码、标题、sheet 或 slide 等结构定位
- 避免通过管理员配置隐式注入未公开的知识

## 9. 部署形态

### 9.1 最小私有部署

- 一个 Web 前端服务
- 一个 Go 后端服务
- 一个 Python AI 运行时服务
- 一个 Postgres 实例
- 一个 Redis 实例
- 一个 MinIO 实例
- 一个 Qdrant 实例
- 一个私有推理主机

### 9.2 环境划分

- 本地开发
- staging
- 私有生产环境

### 9.3 本地开发假设

普通开发者笔记本足以支持：

- 前端和后端开发
- 解析器集成工作
- 向量库与检索调试
- 通过本地模型运行小规模 harness 和联调

如果开发机资源不足，可以临时切换到同网络内的私有开发推理主机，但接口协议必须保持不变。

但要进行生产级大模型对比与性能验证，仍然需要高规格 GPU 基础设施。

## 10. 安全与隐私约束

- 生产环境中不得向外部导出知识
- 对象存储与向量存储必须保持私有
- 日志在可能的情况下应避免泄露原始敏感文档内容
- 模型调用必须限制在获批的私有端点上

## 11. 可观测性

必须采集：

- 文件摄取状态
- 解析器失败
- 每个文件的 chunk 数量
- 检索候选与最终来源
- 回答时延
- 每次回答对应的模型标识
- 在可用时记录 token 用量

## 12. 失败模式

- 解析失败
- embedding 超时
- 向量写入失败
- 检索结果为空
- 模型超时
- 来源归因无效

每种失败都必须在日志中可见，并且能够映射到一次重试或运维操作。

## 13. 延后设计主题

- 遗留 Office 二进制格式转换链路
- 图像区域级引用的 UX
- 由用户反馈驱动的主动评测集扩展
- 微调流水线
- 多智能体扩展路径

## 14. 来源对齐

本 SDD 以 `docs/references/openai-source-notes.md` 记录的“文档优先、评测驱动”工作流为基础，将产品意图翻译为可拆分的服务和可度量的接口。
