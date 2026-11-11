# 单智能体 V1 AI Runtime 摄取与检索设计

## 文档控制

- 状态：活动设计
- 版本：`v1.1`
- 目标：为 `services/ai-runtime-py/` 提供可直接指导实现的模块级设计

## 1. 设计目标

先理解跨服务职责时，阅读 [摄取、检索与回答职责导读](single-agent-v1-ingestion-chat-boundary-guide.md)；本文定义 Python Runtime 的 parser、chunk、embedding、retrieval 与模型调用细节。

本设计文档要解决的不是“是否需要 AI Runtime”，而是：

- AI Runtime 内部应如何分层
- 七类输入文档如何归一化到统一中间表示
- `preview` 与 `run` 两条摄取路径各自负责什么
- chunk、citation 和 source locator 应如何稳定生成
- 本地模型适配层应放在哪里，避免把模型细节泄漏到产品 API

## 2. 范围与非目标

### 2.1 本文覆盖

- 文档解析入口
- parser registry
- 标准化中间表示
- chunk 生成规则
- source locator 规则
- 检索与回答基线
- 本地模型适配边界

### 2.2 本文暂不覆盖

- 多租户隔离
- 图像区域级引用
- 生产级 OCR 基建
- 复杂工作流编排器
- 微调训练链路

## 3. Runtime 在系统中的位置

AI Runtime 是系统中唯一负责以下事情的服务：

- 解析文档
- 生成标准化块
- 执行 chunking
- 调 embedding 模型
- 执行检索与可选 rerank
- 组装 prompt
- 调用本地模型或 OpenAI-compatible 推理端点
- 生成 citation
- 跑 eval baseline

它不负责：

- 用户鉴权
- 登录态管理
- 文件元数据持久化主表
- 会话主记录
- 管理台产品逻辑

这些仍属于 Go 后端职责。

### 3.1 F0 生产真实性边界

Runtime 只返回可被验证的计算结果，不拥有产品 `completed` 语义，也不得为方便开发而代替 Backend、模型或检索伪造内容：

- `knowledge_query` 和 `revalidate` 必须携带完整、非空的 immutable `knowledge_scope`；缺 scope、scope identity 不一致或服务认证失败在读取任何 index 前拒绝。不存在全库或 legacy unscoped retrieval 的产品路径。
- 知识结果只能来自该 scope 中实际检索到的 citation provenance。provider、策略、embedding identity、collection、检索/生成是否执行都必须作为不可猜测 observation 返回；provider 不可用、模型输出为空或协议不合法时返回稳定 failure，不能输出替代答案。
- `controlled_response` 可以不调用检索或模型，但必须显式记录 `not_run` observation 且零 citation；它不是 generation fallback。
- 生产 app 不能自动 bootstrap sample documents、memory vector store、heuristic/fake model 或另一个 retrieval strategy。测试 fixture 必须通过 test-only factory/依赖注入显式提供。
- 正式摄取要求 parse 成功、`chunk_count > 0`、embedding 与向量投影成功并可验证 manifest。扫描 PDF 在 OCR 未实现或不可用时返回 `ocr_unavailable`；不得声称 OCR fallback 或摄取成功。

严格 request/response 的迁移目标由 `packages/contracts/chat-runtime-strict-*.schema.json` 定义。F2 将其替换为唯一产品 Runtime 入口契约；F0 不改变当前运行服务。

## 4. 模块分层

建议在 `services/ai-runtime-py/app/` 内按以下边界拆分：

```text
app/
  api/
    health.py
    ingestion.py
    chat.py
  domain/
    documents.py
    chunks.py
    citations.py
  parsers/
    registry.py
    txt_parser.py
    md_parser.py
    json_parser.py
    pdf_parser.py
    docx_parser.py
    xlsx_parser.py
    pptx_parser.py
  pipelines/
    preview_pipeline.py
    ingestion_pipeline.py
    retrieval_pipeline.py
    answer_pipeline.py
  adapters/
    embeddings.py
    reranker.py
    vector_store.py
    object_store.py
    model_client.py
  eval/
    runner.py
    graders.py
```

### 4.1 `api/`

- 接收 FastAPI 请求
- 做输入校验
- 调用 pipeline
- 返回契约层 DTO

### 4.2 `domain/`

- 定义标准化文档块
- 定义 chunk 记录
- 定义 citation 与 locator 结构

### 4.3 `parsers/`

- 每种格式一个 parser
- parser 只负责“提取并归一化”
- parser 不负责 embedding、向量写入或模型调用

### 4.4 `pipelines/`

- 负责把多个步骤串成流程
- 控制 preview、run、retrieve、answer 四类主链路

### 4.5 `adapters/`

- 封装外部依赖
- 例如 Qdrant、MinIO、embedding 模型、本地 LLM

### 4.6 T14 Chat Stream API

`T14` 在 Runtime 增加 `/v1/chat/stream`，用于向 Backend 提供原生 token/delta stream；现有 `/v1/chat/run` JSON 响应保持兼容。

实现约定：

- `/v1/chat/run` 继续返回完整 `ChatResponse`，供非流式调用、eval runner 和既有 smoke 使用。
- `/v1/chat/stream` 返回 `text/event-stream`，只作为 Backend 内部接口，不直接暴露给 Web。
- Runtime stream 事件：
  - `message.delta`：包含 `request_id`、`message_id`、`delta`。
  - `message.completed`：包含完整 `chat_runtime_response`，字段与 `/v1/chat/run` 的 `ChatResponse` 一致。
  - `message.error`：包含 `code`、`message`、`retryable`，用于流已开始后的失败。
- 检索、拒答判断和 prompt assembly 仍由 `AnswerPipeline` 完成；streaming 只改变 generation 的输出方式。
- OpenAI-compatible 模型端点支持 `stream=true` 时，`ModelClient.stream_generate` 应逐个 yield 模型 delta；未配置模型或模型调用失败时返回 `model_unavailable/model_request_failed`，不得生成替代回答。integration 通过显式 test-only OpenAI-compatible provider 覆盖 delta，不在产品 Runtime 内保留假模型。
- completed 事件必须包含完整 content、citations、finish_reason 和 usage，方便 Backend 以同一 repository 写入路径落库。

验收重点：

- Runtime 单测覆盖 `/v1/chat/stream` 的 delta 和 completed 事件。
- 现有 `/v1/chat/run` 测试继续通过。

## 5. 标准化中间表示

不同文件格式不应直接输出到各自定制的下游逻辑，而应先变成统一对象。

### 5.1 NormalizedDocument

最小字段建议：

- `document_id`
- `file_id`
- `filename`
- `source_format`
- `title`
- `metadata`
- `blocks`

### 5.2 NormalizedBlock

最小字段建议：

- `block_id`
- `document_id`
- `block_type`
- `text`
- `order_index`
- `heading_path`
- `source_locator`
- `block_metadata`

推荐 `block_type` 枚举：

- `paragraph`
- `heading`
- `list_item`
- `table`
- `table_row`
- `sheet_region`
- `slide_body`
- `json_entry`

### 5.3 设计原因

统一中间表示的目的有三点：

1. chunker 不需要知道输入原始格式细节
2. citation formatter 可以统一消费 locator
3. harness 可以在 parse / chunk / retrieve 三个阶段分别定位失败

## 6. Parser Registry 设计

### 6.1 注册方式

建议提供单一 registry：

- 输入：`source_format`
- 输出：对应 parser 实例

例如：

- `txt -> PlainTextParser`
- `md -> MarkdownParser`
- `json -> JsonStructureParser`
- `pdf -> PdfTextParser`
- `docx -> DocxStructureParser`
- `xlsx -> WorkbookRegionParser`
- `pptx -> SlideContentParser`

### 6.2 Parser 接口

每个 parser 至少实现：

- `preview(task) -> PreviewResult`
- `parse(task) -> NormalizedDocument`

`preview` 用于：

- 判断是否支持
- 返回 parser strategy
- 返回预计的 locator 能力

`parse` 用于：

- 真正提取结构
- 生成标准化 blocks

### 6.3 Parser 不应负责的事情

- 不直接写数据库
- 不直接写向量库
- 不直接返回产品 API DTO
- 不直接调用回答模型

## 7. 各格式标准化策略

### 7.1 `txt`

- 按空行和自然段切分
- 以段落顺序生成 `paragraph` block
- locator 使用：
  - `source_format`
  - `block_label`

### 7.2 `md`

- 保留标题层级
- 列表项独立成 block
- 代码块独立成 block
- locator 使用：
  - `heading_path`
  - `block_label`

### 7.3 `json`

- 将嵌套字段展平为可读路径
- 每个高价值键值项生成 `json_entry`
- locator 使用：
  - `json_path`

### 7.4 `pdf`

- 先走文本层提取
- 需要时允许 OCR 回退
- 页码必须保留
- 表格可先作为整块文本或表格块处理
- locator 使用：
  - `page_no`
  - 可选 `block_label`

### 7.5 `docx`

- 提取标题、正文、列表、表格
- 标题路径必须保留
- 对无标题段落保留段落序号
- locator 使用：
  - `heading_path`
  - `paragraph_index`

### 7.6 `xlsx`

- 以 workbook / sheet / region 为主
- 表头明显的区域优先单独成块
- 先不追求公式语义求值，优先保留可见文本和值
- locator 使用：
  - `sheet_name`
  - `cell_range`

### 7.7 `pptx`

- 以 slide 为一级边界
- 标题、正文、表格分别抽取
- speaker notes 是否纳入语料由解析选项控制
- locator 使用：
  - `slide_no`
  - `slide_title`
  - 可选 `block_label`

## 8. Source Locator 生成规则

统一目标：

- 同一格式内稳定
- 前端可展示
- harness 可比较

### 8.1 必须满足

- 每个可检索 chunk 必须有 `source_locator`
- locator 必须是结构化对象，不是拼接字符串
- locator 字段只包含定位信息，不混入长文本内容

### 8.2 示例

#### PDF

```json
{
  "source_format": "pdf",
  "page_no": 3,
  "block_label": "table-1"
}
```

#### DOCX

```json
{
  "source_format": "docx",
  "heading_path": ["2. Expense Reimbursement"],
  "paragraph_index": 1
}
```

#### XLSX

```json
{
  "source_format": "xlsx",
  "sheet_name": "Summary",
  "cell_range": "A1:F5"
}
```

#### PPTX

```json
{
  "source_format": "pptx",
  "slide_no": 2,
  "slide_title": "Rollout milestones",
  "block_label": "table-1"
}
```

## 9. Chunk 设计

### 9.1 ChunkRecord

建议最小字段：

- `chunk_id`
- `file_id`
- `document_id`
- `source_format`
- `block_type`
- `text`
- `token_count`
- `heading_path`
- `source_locator`
- `embedding_model`
- `index_version`

### 9.2 生成原则

- 优先遵循块级语义边界
- 单个块太长时再做二次切分
- 对表格类块尽量保持表头与数据邻近
- 对标题下的短段落可合并，但不能跨越明显章节

### 9.3 首版切块建议

- `txt` / `md` / `docx`
  - 以段落或短章节为单位
- `json`
  - 以逻辑字段组为单位
- `pdf`
  - 以页内段落或表格块为单位
- `xlsx`
  - 以 sheet 区域或表格为单位
- `pptx`
  - 以 slide 内标题 + 正文 / 表格组合为单位

## 10. Preview 与 Run 的边界

### 10.1 `POST /v1/ingestion/preview`

目标：

- 不做持久化写入
- 不做向量写入
- 不做完整 chunk 产出

只返回：

- 是否支持
- parser strategy
- 预计 locator 能力
- 可选的预估块数量或风险提示

适合：

- 上传前校验
- 管理员界面预检
- harness 中的 parse smoke check

### 10.2 `POST /v1/ingestion/run`

目标：

- 完整解析
- 标准化
- chunk 生成
- embedding
- 向量入库
- 结果回执

输出至少包括：

- `task_id`
- `status`
- `chunks_created`
- 失败时 `error`

### 10.3 为什么必须拆两条路

因为 preview 和 run 的代价与语义不同：

- preview 需要快、轻、可频繁调用
- run 需要完整、可追踪、可回放

如果混在一起，前端预检和后台正式摄取都会变得笨重。

## 11. 检索与回答基线

### 11.1 Retrieval Pipeline

建议顺序：

1. 根据请求中的固定 config/retrieval version 和 Backend 解析的 knowledge scope 读取检索配置
2. 对用户问题做 embedding
3. 先做 dense retrieval
4. 可选做 metadata filter
5. 可选做 rerank
6. 取 top-N chunk 组装上下文

### 11.2 Answer Pipeline

建议顺序：

1. 读取 `chat-runtime-request`
2. 拼装会话历史
3. 调 retrieval pipeline
4. 生成 citation 对象
5. 组装 prompt
6. 调本地模型适配层
7. 解析回答
8. 返回 `chat-runtime-response`

### 11.3 拒答规则

当满足以下情况时应拒答：

- 检索结果为空
- 检索分数明显低于阈值
- 检索内容与问题语义不一致

拒答仍应尽量返回：

- `refusal = true`
- `finish_reason = "refusal"`
- 可选的空 citation 或低置信度引用列表

### 11.3.1 R3 词法基线质量规则

在仍使用 lexical embedding 的阶段，Runtime 的 query 表征可包含有限的、版本化的业务术语别名，用于将中文业务问题与英文内部文档中的等价术语建立词法桥接。例如“年假”可补充 `annual leave`、“酒店”可补充 `hotel`。为避免污染其他格式，别名仅在调用方已将 `source_formats` 限制为 `docx`，或问题明确提到 `Word` / `DOCX` 时启用。别名只影响 query embedding 与 lexical match，不改写用户原问题、chunk 文本、模型 prompt 的原问题字段或 citation locator。

为避免“员工”“公司”“样例”等泛化 CJK 词单独触发标题或无关段落，tokenizer 必须把这类有限 stopword 从证据词中排除。Retrieval Pipeline 可以扩大候选池以判断证据质量，但只允许非 `heading` chunk 进入回答 context；只有标题命中与过滤后没有实际业务词命中一样，必须返回 refusal。拒答仍由 Retrieval Pipeline 单一写路径决定；Answer Pipeline 不通过模型回答文本反推 refusal。

Citation 的生产语义不由 eval 评分器重写。特别是 XLSX citation 继续返回实际检索 chunk 的 `sheet_name + cell_range` region；评测层可以把“同 sheet 且 region 覆盖期望 cell”视作等价，但不得把不同 sheet 或不相交 range 视为匹配。对于 JSON，评测层只能在同一源文件中将显式 `id` selector 解析到唯一数组下标后，才可将其与 citation 的 descendant `json_path` 视为等价；Runtime 仍只返回实际 parser 产生的结构化路径。

### 11.4 T6 本地 Knowledge Snapshot Bridge（历史基线）

`T6` 阶段先把“上传摄取结果可被聊天检索”作为本地基线目标，不在这一刀引入生产级向量库或对象存储替换。

实现约定：

- Backend 仍通过 `/v1/ingestion/run` 将本地上传落盘路径作为 `object_uri` 传给 Runtime。
- Runtime ingestion pipeline 在 parse 和 chunk 成功后，将 chunks 用当前 `LexicalEmbeddingAdapter` 写入与 chat retrieval pipeline 共用的 `InMemoryVectorStore`。
- `knowledge_snapshot_id = snapshot_active` 在本地基线中表示 Runtime 进程内 active index，包含启动时 bootstrap 的 `data/sample-docs/` 和运行期成功摄取的上传文件。
- `force_reindex` 在内存 store 中按 `file_id` 清理旧 chunks 后重新 upsert，避免同一文件重建索引后旧 chunk 残留。
- Runtime 进程重启会丢失运行期上传索引；这是本地基线限制，生产持久化与 Qdrant adapter 留给 `T7`。
- `/v1/ingestion/run` 响应契约暂不扩展真实 chunk 列表；Backend `/kb/files/{file_id}/chunks` 继续使用 summary chunk，真实 chunk inspector 可作为后续增强。

此段只说明已验证的 legacy baseline，不能作为 K1 后的目标语义。K3 将以 `document_id`、不可变 `revision_id`、content hash 与 outbox delivery 取代按可变 `file_id` 的重建；K5 将由 Backend 传入显式 `knowledge_scope`，Runtime 只检索 scope 中的 revision，不能把 `snapshot_active` 当作全量回退。

验收重点：

- Runtime 单测覆盖：先 ingestion run 一个测试文件，再 chat run，citation `file_id` 应来自该测试文件。
- Integration smoke 覆盖：通过 Backend 上传文件，等待 ingestion `succeeded`，再通过 Backend chat，assistant citations 应包含上传文件的 `file_id`。

### 11.5 T7a Qdrant 持久化 Adapter

`T7a` 先替换 Runtime active retrieval index 的持久化边界，不同时替换 Backend 对象存储或异步队列。

实现约定：

- Runtime 默认继续使用 `InMemoryVectorStore`，保证本地轻量测试与无 Docker 开发路径不变。
- 当环境变量 `QDRANT_URL` 非空时，Runtime 启动时改用 `QdrantVectorStore`。
- 默认 collection 名称为 `see_agent_chunks`，可通过 `QDRANT_COLLECTION` 覆盖。
- 首版 Qdrant adapter 负责持久化完整 `ChunkRecord`、lexical embedding payload 和辅助 token 字段；检索时先 `scroll` 回候选 payload，再复用现有 lexical scoring 逻辑。
- Qdrant adapter 的 HTTP client 不读取系统代理环境变量，避免全局代理把本机 `127.0.0.1` Qdrant 请求转发到外部代理导致联调失败。
- 这个实现优先保证“Runtime 重启后上传索引不丢”和行为与当前基线一致；后续再演进为 Qdrant 原生 dense/sparse vector search。
- `delete_by_file_id` 必须通过 Qdrant filter 删除同一 `file_id` 的旧 points，保持 `force_reindex` 不产生残留。
- Qdrant 不替代 Backend 文件元数据、任务状态和对象存储；MinIO object store 已由 `T7b` 接入，Postgres repository 与 Redis queue 仍留给后续生产化任务。

验收重点：

- 不设置 `QDRANT_URL` 时，`make runtime-test` 与 `make integration` 仍使用内存基线并通过。
- 启动 Qdrant 后，设置 `QDRANT_URL=http://127.0.0.1:16333` 运行 runtime smoke：ingestion run 写入测试文件，重建 Runtime app，再 chat run 应仍可检索该文件；该真实 Docker smoke 已通过。

### 11.6 T7b MinIO 对象读取 Adapter

`T7b` 让 Runtime ingestion 能消费 Backend 传入的 `s3://{bucket}/{object_key}`，但不改变 parser registry 的职责。

实现约定：

- Runtime 默认仍只解析本地可读路径，保持轻量开发和现有测试行为不变。
- 当 `object_uri` 为 `s3://...` 时，Runtime 使用 `OBJECT_STORE_ENDPOINT`、`OBJECT_STORE_ACCESS_KEY`、`OBJECT_STORE_SECRET_KEY` 和 `OBJECT_STORE_REGION` 读取对象。
- Runtime 将远端对象下载到临时文件后，把临时路径交给现有 parser；parser 不直接依赖 MinIO / S3 SDK。
- 下载 HTTP client 不读取系统代理环境变量，避免全局代理影响 `127.0.0.1` MinIO 联调。
- 首版只要求 ingestion run 支持远端对象；preview 对远端对象仍可保持能力说明，不作为 T7b 阻塞项。

验收重点：

- 本地路径 ingestion 单测继续通过。
- 对 `s3://` 的下载 adapter 增加 mock HTTP 测试，覆盖签名请求、对象写入临时文件和缺配置错误。
- 配置本地 MinIO 后，Backend 上传生成的 `s3://...` 应能被 Runtime ingestion 解析成功。
- 当前状态：以上验收已通过，真实 MinIO smoke 使用 `make minio-ingestion-smoke`。

## 12. 本地模型适配层

### 12.1 目标

- 屏蔽具体本地模型部署差异
- 保持 OpenAI-compatible 调用面

### 12.2 Adapter 责任

- 接收统一 chat request
- 转成具体模型服务请求

### 12.3 显式 provider 与 Qwen thinking 边界

`LOCAL_LLM_PROVIDER` 是 Runtime deployment identity，取值只能是 `openai_compatible` 或 `ollama_native`；未设置时只为既有环境兼容而使用 `openai_compatible`。未知值必须在启动时失败，不能猜测 provider 或试探多个 endpoint。

- `openai_compatible` 调用 `/v1/chat/completions`，适用于 vLLM、兼容代理或不具备 Ollama 专有能力的服务。
- `ollama_native` 从同一脱敏 `LOCAL_LLM_BASE_URL` 派生本机 `/api/chat`，并对 Qwen3 等 thinking-capable 模型显式发送 `think=false`。它读取 `message.content`，不把 `message.thinking` 当作用户答案、citation 输入或执行证据。
- thinking 开关是部署/adapter 条件，不是 Agent Config 中的业务模型身份。任何要发布的 Q1 experiment 都必须在 frozen conditions 中记录 provider adapter 与 thinking mode；切换 adapter 必须新建 experiment/report，不能复用旧报告。
- native provider HTTP/解析/空 content 失败仍是可重试 generation dependency failure；不得改走 OpenAI-compatible endpoint、拼接 thinking 字段或补 citation。
- 处理 timeout 和错误转换；未配置 endpoint 或调用失败时 fail-closed，不输出替代答案；当模型 endpoint 为 `localhost`、`127.0.0.1` 或 `::1` 时，HTTP client 不继承进程代理环境，避免本机 Ollama 请求被全局代理错误转发
- 返回统一 usage 与 finish_reason

### 12.3 不应泄漏到上层的细节

- Ollama 特定字段
- vLLM 特定参数命名
- 各模型原始响应差异

### 12.4 本地开发启动摘要

Runtime 在启动时必须向控制台输出一组脱敏、可判读的依赖摘要，至少包含：

- active retrieval strategy；
- vector store 类型、脱敏 endpoint 与 collection；
- embedding provider、模型与维度；词法兼容策略必须明确显示“不调用远程 embedding 模型”；
- generation provider、脱敏 endpoint、默认模型提示与可用状态；
- object resolver 是本地文件还是 S3-compatible endpoint/bucket。

启动摘要只描述进程实际构造出的 adapter 与配置，不把 Backend 数据库中的 `model_id` 猜成 Runtime 已调用的模型。正常生成请求仍由 Backend 固定的 conversation config 携带 `model_id`；实际请求日志应记录该 `model_id` 和 provider，但不得记录 prompt、知识正文、API Key 或 URL 凭据。

未配置 `LOCAL_LLM_BASE_URL` 时必须以 warning 明确显示 `provider=unavailable`，知识回答请求返回可重试模型错误。Runtime 不再提供 heuristic/deterministic generation fallback。`/healthz` 继续只表示 Runtime 进程可服务，不等价于模型、embedding 或 Qdrant 的端到端健康。

## 13. 失败分类映射

为配合 harness，Runtime 侧日志和错误应能映射到以下主因：

- `parse_failure`
- `chunking_failure`
- `retrieval_miss`
- `rerank_miss`
- `prompt_assembly_issue`
- `generation_hallucination`
- `citation_mismatch`
- `refusal_policy_failure`

这要求 Runtime 内部步骤日志至少能标识：

- parser 名称
- parse_version
- index_version
- model_id
- retrieval_config_version

## 14. 验证建议

实现阶段至少应具备：

- parser smoke tests
- 每种格式 1 条 preview 测试
- 每种格式 1 条 parse / chunk happy-path 测试
- citation formatter 单测
- 本地模型 adapter mock 测试

并优先基于 `data/sample-docs/` 中的真实样例构建测试。

## 15. 当前开放问题

- `pptx` notes 是否默认进入检索仍待最终产品决策
- `pdf` OCR 回退触发阈值需要后续 runbook 和基线测试共同确定
- `xlsx` 是否在首版就保留更多表格结构元数据，仍可在实现期微调

## 16. 与下一步的关系

本设计文档完成后，下一步最自然的工作是：

1. 按本文档拆出 runtime 内部模块目录
2. 让 `preview` 与 `run` 占位接口开始调用 pipeline，而不是在 route 内直接返回固定值
3. 为每种格式接入基于样例资产的 parser smoke tests
