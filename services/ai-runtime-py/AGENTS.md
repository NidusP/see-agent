# services/ai-runtime-py

## 目的

本目录用于 `Python` AI 运行时实现。

## 编码前约定

- 本地开发默认接本地模型，接口保持 OpenAI-compatible。
- 文档解析必须保留结构定位信息，至少覆盖页码、标题、sheet、slide 和表格 / 单元格范围。
- 检索、引用和拒答策略的变更必须可被 harness 评测。

## 当前命令

- 启动：`make runtime-dev`
- 持久化全栈启动：`make runtime-rag-dev`
- 测试：`make runtime-test`

## Python 版本约定

- 本地开发推荐基线：`Python 3.13`
- runtime 当前按 `Python 3.12+` 维护，允许使用新式 `type` alias 与 `X | None` typing 语法
- `make runtime-install` 会优先选择满足 `3.12+` 的解释器：`python3.14 -> python3.13 -> python3.12 -> python3`
- 安装阶段会自动创建仓库根目录 `.venv/`，后续仓库命令默认复用该虚拟环境
- 不再依赖系统自带 `/usr/bin/python3`

## 当前内部接口

- `GET /healthz`
- `POST /v1/ingestion/preview`
- `POST /v1/ingestion/run`
- `POST /v1/chat/run`
- `POST /v1/chat/stream`

## 继续任务指南

如果新会话接手 AI Runtime 工作，默认按以下顺序继续：

1. 先确认 `make runtime-test` 与 `make integration` 当前可通过
2. 如果做 eval，优先进入 `T8`，把 runner 接 runtime chat endpoint
3. 如果做知识演进，K5/K6/K7、`K8-R1` 与 `K8-R2a` 已验证；按 harness 第 22 节实施已冻结的 K8-R2 embedding model comparison，后续才是 sparse/hybrid 和 rerank
4. 如果做模型质量，补 rerank、本地模型实连和失败分类日志

当前最适合直接开工的任务：

- `T8` Eval Harness 阶段 2：真实 runtime/model baseline
- 基于 eval failure report 优化检索 / 引用质量
- `P5` 后续增强：rerank、本地模型实连和失败分类日志

## 当前状态

- parser registry、标准化中间表示、七类格式 parser smoke 已落地
- `preview` 与 `run` route 已调用 pipeline
- `chat` 已走 retrieval / prompt assembly / model adapter / citations 基线
- `T6` 已完成本地 active index bridge：`/v1/ingestion/run` 成功后会把 chunks 写入与 `/v1/chat/run` 共用的 vector store
- `T7a` 已新增 Qdrant vector store adapter：默认仍是 `InMemoryVectorStore`，设置 `QDRANT_URL` 后切到 `QdrantVectorStore`
- `K8` local cutover 已验证：产品 Runtime 只允许 `RUNTIME_RETRIEVAL_STRATEGY=native_dense_v1`，要求 OpenAI-compatible `EMBEDDING_*`、独立 dense collection 与匹配维度；已退役的 `RUNTIME_RETRIEVAL_MODE` 拒绝启动。写入真实 float vector，查询走 Qdrant `/points/query` 的服务端 revision/file/source filter。`qrel_002` 使用 Qwen `1024` 维 embedding 与 `qwen3:14b` native Ollama generation；未知/历史 lexical/provider/config/collection 不匹配均 fail-closed，不允许回退。
- 当前 Qdrant adapter 单元测试已覆盖 upsert/search/delete；真实 Docker Qdrant smoke 已通过：`QDRANT_URL=http://127.0.0.1:16333 make runtime-qdrant-smoke`
- `T7b` 已支持 MinIO / S3-compatible 对象读取：配置 `OBJECT_STORE_ENDPOINT` 后，ingestion run 可下载 `s3://...` 到临时文件再解析，并已通过真实 MinIO smoke
- `T14` 已新增 `/v1/chat/stream` 和 model client stream adapter；Backend 可在 `stream=true` 时消费 Runtime delta 并保持 Web SSE 契约不变
- `K3` 已验证 immutable revision projection：revision request 必须携带 document/revision/checksum/delivery identity；chunk/manifest/point id 可重放，Qdrant payload 以 document/revision/visibility 隔离，revision retry 不会清理其他 revision。
- `K5` 已验证 snapshot-scoped retrieval：InMemory/Qdrant 都只查询 Backend 指定的 revision allow-list，scope/filter 失败不回退全量检索。
- `K6` 已验证 historical revalidation mode：它以 latest scope 和最小历史上下文生成受控 verdict；marker 不进入正文，拒答/无证据返回 `insufficient_evidence`。
- `C1` 已验证 normal-answer citation marker parser：跨 token 的 `[[cite:rank]]` 不进入 delta 或最终正文；有效 rank 生成 rune-safe `citation_anchors`，unknown rank 不伪造 anchor。
- `K7` 已把不可变 prompt、response style、`temperature`、`top_p`、`max_output_tokens` 与 retrieval boundary 接入实际 pipeline；`prompt_v7` 是当前已发布的 Qwen native version。`prompt_v8 + answer_policy_v1` 是 A1-3 未发布候选：先执行结构化 scope policy，再在 immutable scoped retrieval 后执行结构化 answer policy；policy/provider/model/call count、coverage、pending resolution 与 citation marker 任一不合法都以 `answer_policy_invalid` fail-closed，SSE 不得先泄漏 delta。未知 prompt version继续 fail-closed，实际配置随 response observation 交回 Backend 记录。
- `prompt_v2` 严格 K7 gate 的最新复验通过：`30/40, grounded=0.6571`；retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`。后续 K8 必须保持该门禁。
- 本地全栈 `make runtime-fullstack-dev` 连接本机 Ollama/`qwen3:14b`，使用 `ollama_native/think=false`；产品 Runtime 不保留 heuristic generation fallback，模型不可用时 fail-closed。启动摘要必须持续脱敏报告实际 vector/embedding/generation/object adapter，不能只凭 `/healthz` 推断模型已连接。`fullstack_dev`/`production` 必须在启动期完成不持久化的实际模型 warm-up，且 `/readyz` 只有 warm-up 成功后才可报告 ready。
- `C2/C2-R1` controlled intent 在 retrieval 前返回固定产品回复，JSON/SSE 均记录 intent 与 execution facts；问候/致谢/告别/能力说明和高置信的公共开放世界问题（天气、美国总统）不会调用 retrieval 或 model。固定回复明确说明大模型只基于已发布知识库的 RAG 检索结果作答；混合、业务和未知问题仍走现有检索/引用链路。最新 prompt_v2 严格 gate 为 `31/40, grounded=0.7357`。

开始前先确认：

- `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
- `packages/contracts/chat-runtime-request.schema.json`
- `packages/contracts/ingestion-task.schema.json`
- `make runtime-test` 当前可通过

## 后续需要补充

- lint / format 命令
- 本地模型切换说明与 `.env` 模板
- K8-R2 sparse/hybrid vector search、rerank 与模型比较；保持 K8-R1 native dense 的独立 collection 和 fail-closed 语义
