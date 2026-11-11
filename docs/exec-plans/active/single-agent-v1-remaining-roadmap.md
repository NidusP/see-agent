# 单智能体 V1 剩余任务总路线图

## 文档控制

- 状态：活动计划
- 版本：`v1.1`
- 更新日期：`2026-07-12`
- 目的：把 T14 之后的全部剩余任务、依赖关系、并行策略和验收方式集中到一个入口，后续按批次执行，而不是每完成一个任务再临时询问下一项

## 1. 推进规则

- 默认按本文档的批次推进；除非遇到真实阻塞、产品范围变更或需要外部凭据，不再每完成一项后停下来询问“下一个做什么”。
- 每一批开始前先更新 `docs/exec-plans/active/single-agent-v1-parallel-claims.md`，标记本批正在处理的工作包。
- 能并行的任务按模块隔离并行：Runtime/Eval、Web/UI、Backend/Infra、Docs/Release 分开推进。
- 会改同一 API、同一状态机、同一 eval 指标口径或同一核心 runtime retrieval 链路的任务必须串行。
- 每批结束时统一跑该批验收命令，并同步 README、AGENTS、design docs、harness docs 和本路线图状态。

## 2. 当前基线

已完成并验证：

- Web：登录、聊天、知识库、配置真实请求层，中文优先交互，Chat SSE UI，浏览器级 smoke。
- Backend：角色门禁、Postgres repository、MinIO object store、Redis ingestion queue、Redis session store、Runtime chat dispatcher、Runtime stream 转发。
- Runtime：七类 parser、ingestion pipeline、active retrieval index、Qdrant adapter、`s3://` 对象读取、OpenAI-compatible model adapter、`/v1/chat/stream`。
- Harness：`40` 条 baseline 样本、mock baseline、runtime-local baseline、integration/e2e smoke。
- Infra：Postgres、Redis、MinIO、Qdrant compose 基线与 smoke。

最新已验证命令：

- `make backend-test`
- `make runtime-test`
- `make integration`

## 3. 全量剩余任务清单

### R0 执行计划收口

- 状态：`done`
- 类型：计划 / 协作治理
- 目标：建立本文档作为后续任务唯一总入口。
- 并行性：串行；这是所有后续批次的调度前置。
- 验收：
  - 本文档存在且列出剩余任务、依赖和批次。
  - `AGENTS.md` 与 `single-agent-v1-next-dev-sequence.md` 指向本文档。
  - `single-agent-v1-parallel-claims.md` 已记录本路线图收口状态。

### R1 真实本地模型接入与 Eval

- 状态：`done`
- 类型：Eval / Runtime
- 目标：把新环境的 Agent Config 默认模型与可用 Ollama 模型统一为 `llama3.2:latest`，以无 fallback 的 smoke 确认 Runtime 的真实模型调用，再重跑 `make eval-runtime-baseline` 得到真实外部本地模型报告。
- 依赖：
  - 需要可访问的 `LOCAL_LLM_BASE_URL`。
  - 当前 runtime-local fallback baseline 已可运行。
- 并行性：可与 `U1`、`U2`、`O1` 并行；不要与 `R3/R4` 同时改 Runtime retrieval。
- 写入范围：
  - `docs/harness/**`
  - `data/eval-datasets/reports/**` 本地 ignored 报告
  - 必要时 `docs/runbooks/**` 或 `.env.example`
- 验收：
  - `LOCAL_LLM_BASE_URL=... LOCAL_LLM_ALLOW_FALLBACK=false make local-model-smoke`
  - `LOCAL_LLM_BASE_URL=... LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest LOCAL_LLM_ALLOW_FALLBACK=false make eval-runtime-baseline`
  - 报告记录 model id、失败分类和关键指标。
- 当前记录：
  - `2026-07-09` 已确认本机 Ollama endpoint 可访问，且只安装 `llama3.2:latest`（3.2B Q4_K_M）；此前真实 eval 因 `LOCAL_LLM_TIMEOUT_SECONDS=30` 出现大量超时。
  - `2026-07-12` 已完成新环境默认 Agent Config、无 fallback smoke 和 90 秒超时基线；真实 eval 为 `24/40` 通过，且 `unexpected_runtime_error=0`。已有数据库配置不做静默迁移。

### R2 Eval Failure Triage

- 状态：`done`
- 类型：Eval / 产品质量
- 目标：对 runtime-local 或真实模型报告中的失败样本分类，决定优先修检索、引用、拒答还是 prompt。
- 依赖：
  - 优先依赖 `R1`；若真实模型端点暂不可用，可先用现有 fallback baseline 做预分类。
- 并行性：可与 `U1`、`U2` 并行；是 `R3/R4/R5` 的串行前置。
- 写入范围：
  - `docs/harness/**`
  - `docs/exec-plans/active/**`
- 验收：
  - 失败样本按 `retrieval_miss`、`citation_mismatch`、`refusal_error`、`answer_incomplete`、`model_format_error` 分类。
  - 产出下一轮质量优化的指标目标。
- 当前记录：
  - 先用 Runtime deterministic fallback 预分类，随后已在关闭 fallback 的 `llama3.2:latest` 上完成真实 triage 与 R5 质量门复验。
  - `docs/harness/single-agent-v1-eval-failure-triage.md` 记录 `citation_mismatch`、`answer_point_miss`、`retrieval_miss` 和 `refusal_policy_failure` 分布，以及 PDF/XLSX/PPTX 的后续优先级。

### R3 检索与引用质量优化

- 状态：`done`
- 类型：Runtime / RAG
- 目标：优先提升 `citation_accuracy`、`retrieval_recall_at_k` 和拒答正确率。
- 依赖：
  - `R2` 失败分类。
- 并行性：Runtime retrieval 核心改动必须串行；可与纯 Web 任务并行。
- 可能动作：
  - 调整 chunk metadata 与 locator 保留策略。
  - 优化 lexical baseline scoring 或 Qdrant payload filter。
  - 强化引用选择、引用排序和证据不足拒答阈值。
  - 优化 prompt 中的引用和拒答约束。
- 写入范围：
  - `services/ai-runtime-py/**`
  - `packages/prompts/**`
  - `docs/harness/**`
  - `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
- 验收：
  - `make runtime-test`
  - `make eval-runtime-baseline`
  - `make integration`
  - 指标较基线不退化，并记录失败样本变化。
- 当前第一轮：
  - 先做中文 query -> 英文 DOCX 的受控 lexical aliases、泛 CJK 词弱证据拒答、XLSX cell/region 与 JSON id-selector locator 等价评分。
  - 保持模型、prompt、公共 API、source locator 生产语义和全局 `min_score` 不变。
  - 相对真实 `llama3.2:latest` 基线，`retrieval_recall_at_k` 不得低于 `0.8571`，`citation_accuracy` 不得低于 `0.6`，`refusal_correctness` 必须高于 `0.6`，且 `unexpected_runtime_error=0`。
  - 已验证第一轮 lexical retrieval/locator grading，及第二轮 Backend generation parameter propagation；真实 `llama3.2:latest` 结果为 `27/40` 通过、`retrieval_recall_at_k=0.9714`、`citation_accuracy=0.9714`、`refusal_correctness=1.0`、`grounded_correctness=0.5714`、`unexpected_runtime_error=0`。
  - 剩余失败集中在 PDF/XLSX/PPTX answer-point/retrieval；转交 R5 将本轮结果写为质量门槛和回归规则，不通过放宽 answer grader 代替产品质量修复。

### R4 Qdrant 原生检索增强

- 状态：`pending`
- 类型：Runtime / Vector Store
- 目标：从当前轻量 adapter 进化到更接近生产的 dense/sparse search、payload filtering 和可选 rerank。
- 依赖：
  - `T7a` 已完成。
  - 最好先完成 `R2`，避免盲目优化。
- 并行性：不要与 `R3` 同时改 retrieval pipeline；可与 Web 任务并行。
- 写入范围：
  - `services/ai-runtime-py/**`
  - `docs/design-docs/**`
  - `docs/harness/**`
- 验收：
  - `make runtime-test`
  - `QDRANT_URL=http://127.0.0.1:16333 make runtime-qdrant-smoke`
  - `make eval-runtime-baseline`

### R5 Eval Gate 与质量门槛

- 状态：`done`
- 类型：Harness / Release Quality
- 目标：把 eval 从“能跑报告”升级为“有最低质量门槛和回归判断”。
- 依赖：
  - `R2`
  - 至少一轮 `R3` 或 `R4`
- 并行性：可与 `O1/O2` 并行。
- 写入范围：
  - `scripts/eval/**`
  - `docs/harness/**`
  - `Makefile`
- 验收：
  - `make eval-baseline`
  - `make eval-runtime-baseline`
  - `make eval-runtime-gate`
  - 报告中明确 pass/fail gate、允许退化阈值，且 gate 失败在报告写入后返回非零。
- 已验收 gate：
  - 固定 `llama3.2:latest`、`temperature=0.2`、`max_output_tokens=1024`。
  - 要求 `>=26/40` 通过、retrieval/citation `>=0.95`、refusal `=1.0`、grounded `>=0.55`、`unexpected_runtime_error=0`。
  - 平均延迟只报告不阻断；模型或 generation config 不匹配必须失败。
  - `2026-07-12` 的真实 Ollama 复验为 `27/40`、`retrieval_recall_at_k=0.9714`、`citation_accuracy=0.9714`、`refusal_correctness=1.0`、`grounded_correctness=0.5786`、`unexpected_runtime_error=0`，gate 已通过。

### U1 知识库文件详情页与 Chunk Inspector

- 状态：`done`
- 类型：Web / Knowledge UX
- 目标：把已有 `GET /api/kb/files/{file_id}/chunks` 展示成管理员可检查的详情页或详情面板。
- 依赖：
  - 当前 `/knowledge` 已有列表、上传、详情右栏和 reindex。
  - 用户已明确暂不需要跳转到原始文件展示。
- 并行性：可与 `R1/R2/R3` 并行，只要不改后端契约。
- 写入范围：
  - `apps/web/**`
  - `tests/e2e/**`
  - `docs/design-docs/single-agent-v1-web-ui-design.md`
- 验收：
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - 管理员能查看 chunk 文本、locator、状态和错误；普通用户仍不可访问。
- 当前记录：
  - 已新增 Web `/api/kb/files/{file_id}/chunks` 同源 proxy。
  - 已在知识库详情右栏加入“片段检查”视图，展示 block type、token count、locator、文本和 chunk id。
  - 已补 e2e proxy smoke，断言 chunks API 返回 source locator。

### U2 Chat 错误态、重试与中止交互

- 状态：`pending`
- 类型：Web / Chat UX
- 目标：让流式对话失败、Runtime 不可用、用户重试和取消生成更清晰，页面交互继续优先中文。
- 依赖：
  - `T14` 已完成。
- 并行性：可与 Runtime eval 并行；如果需要新增 Backend stop API，则切到串行 API 设计。
- 写入范围：
  - `apps/web/**`
  - `tests/e2e/**`
  - `docs/design-docs/single-agent-v1-web-ui-design.md`
- 验收：
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - 浏览器 smoke 覆盖流式失败态或重试路径。

### U3 知识库生命周期：删除、禁用与检索不可见

- 状态：`pending`
- 类型：Product / Backend / Runtime / Web
- 目标：管理员可以让某个知识文件从后续检索中不可见，并保持 metadata、object store 和 vector store 状态一致。
- 依赖：
  - 需要先更新产品规格、API 设计和 Backend/Runtime 状态机。
- 并行性：串行；会同时影响 API、Backend repository、Runtime index 和 Web。
- 写入范围：
  - `docs/product-specs/**`
  - `docs/design-docs/**`
  - `packages/contracts/**`
  - `services/backend-go/**`
  - `services/ai-runtime-py/**`
  - `apps/web/**`
  - `tests/**`
- 验收：
  - `make backend-test`
  - `make runtime-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - 删除或禁用后，新会话检索不到该文件引用。

### U4 Config 回滚、校验与版本说明

- 状态：`pending`
- 类型：Admin UX / Backend
- 目标：把当前 Config 保存与版本历史扩展为可回滚、字段校验更清晰、版本影响范围更可解释。
- 依赖：
  - 需要先确认 MVP 是否要求回滚；当前不是基础流程阻塞。
- 并行性：可与 Runtime 检索优化并行，但如果改契约需串行冻结 API。
- 写入范围：
  - `packages/contracts/**`
  - `services/backend-go/**`
  - `apps/web/**`
  - `docs/design-docs/**`
- 验收：
  - `make backend-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`

### B1 Backend Repository 与 Migration 文件化

- 状态：`pending`
- 类型：Backend / Maintainability
- 目标：把当前自动 bootstrap schema 和较集中的 repository 代码整理成更清晰的迁移与文件结构。
- 依赖：
  - `T7c` 已完成。
- 并行性：不要与 `U3/U4` 这类 API/状态机变更并行；可与纯 Web 或 Eval 报告分析并行。
- 写入范围：
  - `services/backend-go/**`
  - `docs/runbooks/**`
  - `docs/design-docs/single-agent-v1-backend-design.md`
- 验收：
  - `make backend-test`
  - `make postgres-repository-smoke`
  - `make integration`

### B2 Redis Queue 可靠性增强

- 状态：`pending`
- 类型：Backend / Infra
- 目标：为 ingestion queue 增加重试、失败队列、幂等记录和 worker 可观测信息。
- 依赖：
  - `T7d` 已完成。
  - 最好先明确 `U3` 的删除 / 禁用语义，避免任务重试与删除语义冲突。
- 并行性：可与 Web 任务并行；不要与 Backend task 状态机大改并行。
- 写入范围：
  - `services/backend-go/**`
  - `docs/runbooks/**`
  - `tests/**`
- 验收：
  - `make backend-test`
  - `make redis-ingestion-smoke`
  - `make integration`

### B3 Runtime / Backend 可观测与失败分类日志

- 状态：`pending`
- 类型：Observability
- 目标：记录 request id、conversation id、model latency、retrieval latency、runtime error class，支撑 eval 与线上排障。
- 依赖：
  - 可在 `R2` 后做，优先围绕实际失败类别加日志。
- 并行性：可与 Web 任务并行；如果修改 response schema 需串行。
- 写入范围：
  - `services/backend-go/**`
  - `services/ai-runtime-py/**`
  - `docs/runbooks/**`
- 验收：
  - `make backend-test`
  - `make runtime-test`
  - `make integration`

### O1 本地模型与环境 Runbook

- 状态：`done`
- 类型：Ops / Docs
- 目标：把 Ollama / vLLM / OpenAI-compatible 本地模型端点配置、端口、超时、fallback 策略写成可操作说明。
- 依赖：
  - 与 `R1` 可相互促进。
- 并行性：可与几乎所有代码任务并行。
- 写入范围：
  - `docs/runbooks/**`
  - `README.md`
  - 服务级 README / `AGENTS.md`
- 验收：
  - 文档包含 `LOCAL_LLM_BASE_URL`、`LOCAL_LLM_API_KEY`、`LOCAL_LLM_ALLOW_FALLBACK` 和 eval 重跑示例。
- 当前记录：
  - 已新增 `docs/runbooks/local-model-eval.md`。
  - 已更新 `docs/runbooks/README.md`。

### O2 发布前 Runbook 与风险清单

- 状态：`pending`
- 类型：Release / Docs
- 目标：收口本地启动、依赖服务、验收命令、已知风险、回滚策略和非 MVP 延后项。
- 依赖：
  - 需要 `R3/R5/U1/U2` 至少完成一轮。
- 并行性：末期串行。
- 写入范围：
  - `docs/runbooks/**`
  - `docs/exec-plans/active/**`
  - `README.md`
  - `AGENTS.md`
- 验收：
  - `make backend-test`
  - `make runtime-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - `make eval-baseline`

### TST1 测试矩阵扩展

- 状态：`pending`
- 类型：Testing
- 目标：给新增的失败态、删除 / 禁用、config 回滚和 knowledge chunk inspector 补回归测试。
- 依赖：
  - 跟随对应功能批次执行。
- 并行性：通常与对应任务同批完成。
- 写入范围：
  - `tests/integration/**`
  - `tests/e2e/**`
  - 服务内 tests
  - `docs/harness/**`
- 验收：
  - 对应功能验收命令通过，且测试计划同步更新。

## 4. 推荐执行批次

### Batch 0：路线图与调度收口

- 任务：`R0`
- 执行方式：串行
- 完成标准：本文档成为后续任务总入口。

### Batch 1：质量基线 + UI 并行推进

- 状态：`handoff_ready`
- 并行任务：
  - Track A：`R1` 真实本地模型 eval；若缺少模型端点，则先做 `R2` 的 fallback baseline failure triage。
  - Track B：`U1` 知识库文件详情页与 chunk inspector。
  - Track C：`O1` 本地模型与环境 runbook。
- 串行点：
  - `R2` 必须在 `R3/R4` 前完成。
  - `U1` 不应改 Backend 契约；如发现接口不够用，暂停并转入 API 设计。
- 批次验收：
  - `make runtime-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - 有模型端点时运行 `make eval-runtime-baseline`
- 当前结果：
  - `make eval-runtime-baseline` 已在 fallback 模式通过，并产出 triage 文档。
  - `make web-lint` 已通过。
  - `make typecheck` 已通过。
  - `make integration` 已通过，`17` 条 integration/e2e smoke 全绿。
  - 后续已完成真实 Ollama eval、R3 与 R5；当前质量门复验为 `27/40` 通过，下一步按 failure triage 处理 PDF/XLSX/PPTX。

### Batch 2：RAG 质量优化主线

- 串行任务：
  - `R2`
  - `R3`
  - `R5`
  - 后续按 failure triage 决定是否进入 `R4`。
- 可并行任务：
  - `U2` Chat 错误态、重试与中止交互。
  - `B3` 可观测与失败分类日志。
- 批次验收：
  - `make runtime-test`
  - `make backend-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - `make eval-runtime-gate`

### Batch 3：知识库生命周期与管理增强

- 串行任务：
  - `U3` 知识库删除 / 禁用 / 检索不可见。
  - `TST1` 对应删除后不可见 smoke。
- 可并行任务：
  - `U4` Config 回滚与校验，前提是不与 `U3` 同时改同一契约区域。
  - `B1` Backend repository / migration 文件化，前提是不与 `U3` 同时改 repository 接口。
- 批次验收：
  - `make backend-test`
  - `make runtime-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`

### Batch 4：可靠性与发布收口

- 任务：
  - `B2` Redis queue 可靠性增强。
  - `O2` 发布前 runbook 与风险清单。
  - `TST1` 全量测试矩阵补齐。
- 执行方式：
  - `B2` 串行实现。
  - `O2` 可在 `B2` 后半段同步文档，但最终收口必须等验收命令通过。
- 批次验收：
  - `make backend-test`
  - `make runtime-test`
  - `make web-lint`
  - `make typecheck`
  - `make integration`
  - `make eval-baseline`
  - `make eval-runtime-baseline`
  - 真实依赖可用时跑 Postgres / Redis / MinIO / Qdrant smoke。

## 5. 延后非 MVP 项

这些任务不纳入当前 MVP 收口，除非用户明确调整范围：

- 原始文件在线预览器或从 citation 跳转打开原文件。
- 遗留 Office 二进制格式 `doc/xls/ppt` 转换链路。
- OCR fallback 与图片区域级引用。
- 多租户、外部身份源、细粒度 RBAC。
- 微调训练与模型管理平台。
- 多智能体协作与复杂工具调用编排。
- 生产级告警、审计报表和成本仪表盘。

## 6. 默认下一步

从下一次实际开发开始，默认进入 `Batch 2`：

1. 基于 `docs/harness/single-agent-v1-eval-failure-triage.md` 先做 `R3` 检索与引用质量优化。
2. 并行推进 `U2` Chat 错误态、重试与中止交互。
3. 视改动范围补 `B3` Runtime / Backend 可观测与失败分类日志。
4. `R1` 已完成；按 `docs/harness/single-agent-v1-eval-failure-triage.md` 进入 `R3`，优先修 locator normalization、DOCX 中英 lexical aliases 和弱证据 refusal gate。
