# services/ai-runtime-py

这里将承载 `Python` AI 运行时。

预期首批职责：

- 文档解析与标准化
- 切块与 embedding 编排
- 检索与 reranking
- prompt 组装
- 本地模型调用
- 评测 harness 执行

当前已落地的最小占位接口：

- `GET /healthz`
- `POST /v1/ingestion/preview`
- `POST /v1/ingestion/run`
- `POST /v1/chat/run`
- `POST /v1/chat/stream`

本地模型服务稳定性探针：`make provider-stability-probe`。它对真实 generation provider（实际负责文本生成的模型服务）执行连续短文本、结构化 JSON 和 SSE（服务端事件流）检查，只生成稳定性与延迟证据，不写产品状态，也不能替代 A1 质量评测。

P2 / P5 基线现状：

- route 已改为调用 `pipeline + parser registry`，不再把解析策略硬编码在 `FastAPI` route 内
- `preview`、`run`、`chat` 三条 route 都直接返回各自的 typed response model，便于后续继续扩展 pipeline 而不把 DTO 组装散落回 route
- 已建立七类格式 parser 基线：`txt`、`md`、`json`、`pdf`、`docx`、`xlsx`、`pptx`
- `preview` 统一返回 parser strategy、locator 能力、格式级 preview 元数据、预计 block/chunk 数和 warning
- `run` 统一走 `parse -> normalized document -> chunk baseline`，并返回最小摄取结果回执
- `/v1/chat/run` 已切到最小 `retrieval -> prompt assembly -> model adapter -> citations` 闭环
- `/v1/chat/stream` 已提供 Backend 内部 SSE：输出 `message.delta` 与 `message.completed`，generation 统一走 OpenAI-compatible stream；未配置模型或调用失败时返回 `message.error`
- 正常回答使用内部 `[[cite:<rank>]]` marker 生成 `citation_anchors`；Runtime 在 stream delta 前移除 marker，并以 Unicode rune range 回传与 citation rank 对应的结论范围
- retrieval 默认使用 runtime 进程内样例语料索引和轻量词法 embedding 基线；`K8-R2a` 将其收敛为内建 `lexical_compat_v1` profile，`native_dense_v1` 通过 OpenAI-compatible embedding endpoint 写入实际向量，并调用 Qdrant `/points/query` 做服务端 scoped vector search。管理员只能选择已注册策略，Runtime 不动态加载 Python plugin。
- Qdrant 与本地模型 adapter 对 `localhost` / loopback endpoint 会忽略代理环境变量，避免全局代理影响 `127.0.0.1` 联调；远程 OpenAI-compatible endpoint 保留环境代理语义
- ingestion run 支持在配置 `OBJECT_STORE_ENDPOINT` 后读取 `s3://...` 对象 URI：Runtime 会先把对象下载到临时文件，再复用现有 parser registry
- K3 revision ingestion 已接入 immutable identity：带 `document_id`、`revision_id`、`checksum_sha256`、`delivery_id` 的请求会生成稳定 chunk/manifest/point id，并以 revision payload 隔离；同一 revision retry 只替换自身 point，不影响其他 revision
- 本地模型调用层已隔离到 OpenAI-compatible adapter；未配置 endpoint 或调用失败时 fail-closed，不再提供 heuristic/deterministic generation fallback
- Runtime 启动控制台会脱敏报告 retrieval strategy、Qdrant collection、embedding provider/model、generation provider/model hint/status 与 object resolver；每次真实生成请求额外记录实际 `model_id`，便于区分 Backend 会话绑定与 Runtime provider
- K7 Runtime request 会实际解析已登记的 `system_prompt_version` 与 `response_style`，并应用 `temperature`、`top_p`、`max_output_tokens`、retrieval `top_k` 和 lexical rerank 开关；未知 prompt version fail-closed，不静默回退

当前目录结构：

```text
app/
  api/
    chat.py
    health.py
    ingestion.py
    schemas.py
  adapters/
    embeddings.py
    model_client.py
    vector_store.py
  domain/
    models.py
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
    answer_pipeline.py
    chat_pipeline.py
    chunking.py
    ingestion_pipeline.py
    preview_pipeline.py
    retrieval_pipeline.py
  main.py
tests/
  test_health.py
  test_model_client.py
```

当前命令：

- 启动：`make runtime-dev`
- 持久化全栈启动：`make runtime-rag-dev`（默认 Ollama `llama3.2:latest`）
- 测试：`make runtime-test`
- Qdrant 词法兼容 smoke：启动 Qdrant 后运行 `QDRANT_URL=http://127.0.0.1:16333 make runtime-qdrant-smoke`
- 原生 Qdrant transport smoke：运行 `QDRANT_URL=http://127.0.0.1:16333 make runtime-native-qdrant-smoke`，以固定三维向量验证真实 collection、filtered `/points/query`、scope 隔离和 filtered delete；它不替代 embedding provider 验收
- 原生 dense smoke：先配置下方 `EMBEDDING_*` 变量，再运行 `make runtime-native-dense-smoke`；该命令会使用临时 collection，验证真实 embedding、Qdrant vector query 和 snapshot provenance 后自动清理
- Revision/snapshot/revalidation/config 回归：`make runtime-test` 覆盖 manifest 幂等、revision 隔离、snapshot scope、K6 verdict、K7 配置应用与 prompt version fail-closed；当前为 `89 passed`

Python 版本约定：

- 本地开发推荐基线：`Python 3.13`
- 运行时代码当前按 `Python 3.12+` 维护，已开始使用新式 `type` alias 与 `X | None` typing 语法
- `make runtime-install` 会优先选择满足 `3.12+` 的解释器：`python3.14 -> python3.13 -> python3.12 -> python3`
- `make runtime-install` 会在仓库根目录自动创建 `.venv/` 并安装依赖，后续 `make runtime-dev` / `make runtime-test` / `make integration` 默认复用该虚拟环境
- 这样可以避开 Homebrew Python 的 `PEP 668` 外部管理限制，也不会碰系统自带 `/usr/bin/python3`

当前说明：

- P2 baseline 只对本地可读文件做真实 `run` 解析；远端 `object_uri` 当前只支持 preview 能力说明
- P5 baseline 当前以 `data/sample-docs/` 构建最小可检索语料，后续再接真实 knowledge snapshot、向量库和 rerank
- PDF 目前只走文本层提取基线，OCR fallback 仍未实现
- `preview` 扩展元数据尚未冻结到共享 `packages/contracts/**`，当前属于 runtime 内部先行实现
- runtime 内部 `Pydantic` schema 已收紧到与只读共享 contract 一致的格式枚举和值域，但共享 preview 契约仍待 `P0 contract freeze` 明确
- 当前本地模型环境变量：
  - `LOCAL_LLM_BASE_URL`
  - `LOCAL_LLM_API_KEY`
  - `LOCAL_LLM_DEFAULT_MODEL_ID`（新建 Backend repository 的初始模型；全栈环境由显式配置决定）
  - `LOCAL_LLM_TIMEOUT_SECONDS`
- 本机 Ollama 严格连通性验收：`make local-model-smoke`。该命令先校验指定 model id 可见，再经本 adapter 请求非空生成结果。`fullstack_dev`/`production` 启动时也会进行相同语义的、非持久化短生成 warm-up；warm-up 未成功时 `/readyz` 不会报告 ready。
- 当前向量库环境变量：
  - `QDRANT_URL`
  - `QDRANT_COLLECTION`
  - `QDRANT_TIMEOUT_SECONDS`
  - `RUNTIME_RETRIEVAL_STRATEGY=lexical_compat_v1|native_dense_v1`（推荐；默认 `lexical_compat_v1`）
  - `RUNTIME_RETRIEVAL_MODE=lexical|native_dense`（兼容旧部署；映射到对应 strategy，和新变量冲突时启动失败）
  - 原生 dense 专用：`EMBEDDING_BASE_URL`、`EMBEDDING_MODEL`、`EMBEDDING_DIMENSIONS`、可选 `EMBEDDING_API_KEY` 与 `EMBEDDING_TIMEOUT_SECONDS`
  - `native_dense` 必须使用新 collection（默认 `see_agent_dense_chunks_v1`）；既有 size=1 的词法 collection 不能复用。embedding provider、维度或 Qdrant collection 配置不匹配时 Runtime 启动或请求会明确失败，绝不退回词法检索。
- K3 Runtime 请求约束：当 `revision_id` 非空时，`document_id`、`checksum_sha256` 和 `delivery_id` 必须同时存在；成功响应必须返回 document/revision/delivery、chunk manifest 和 embedding model。K5 已把 chat 查询限制到 snapshot 的 revision members。
- K6 revalidation：`answer_mode=revalidate` 必须携带 latest `knowledge_scope` 与 `revalidation_context`；Runtime 只查询该 scope，生成 `still_valid`、`needs_update` 或 `insufficient_evidence` verdict，并从用户正文中移除控制 marker。
- K7 prompt registry：`prompt_v1` 保留历史模板；`prompt_v2` 在跨语言回答后逐字保留决定性证据短句或目标表格行；`prompt_v3/v4` 是历史 dense protocol candidates；`prompt_v5` 是 Qwen dense candidate，要求模型输出可解析 citation/disposition marker，并完整表述默认规则与例外。未知版本失败，不能回退到 v1；候选只可在通过自己的 Q1 release 后用于新会话。
- K8-R2a query/ingestion：Backend 会把 immutable retrieval `strategy`、`embedding_model`、`top_k`/rerank 连同请求传入 Runtime；知识问题的 strategy 必须等于 active Runtime profile，native dense 还要求 embedding model 一致。任一不匹配 fail-closed，不能回退 lexical；实际 `embedding_model` 写进 ingestion evidence 与检索 chunk payload。
- 当前对象存储环境变量：
  - `OBJECT_STORE_ENDPOINT`
  - `OBJECT_STORE_BUCKET`
  - `OBJECT_STORE_ACCESS_KEY`
  - `OBJECT_STORE_SECRET_KEY`
  - `OBJECT_STORE_REGION`
  - `OBJECT_STORE_TIMEOUT_SECONDS`
- 后续详细实现设计见：
  - `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
