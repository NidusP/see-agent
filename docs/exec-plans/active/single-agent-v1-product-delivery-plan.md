# 单智能体 V1 产品交付计划

## 文档控制

- 状态：`active`
- 版本：`v1.5`
- 更新日期：`2026-07-16`
- 目的：以“可私有部署、可持续维护、可供内部团队试用”的单智能体 RAG 产品为交付目标，统一现有代码、质量证据与剩余工作。

## 1. 交付定义

### 目标

内部用户可在私有网络中登录 Web 工作台，管理员能上传、检查、重建、删除或禁用知识，普通用户能得到带结构化引用的流式回答；所有关键行为可由配置快照、日志与可重复评测追溯。

### 当前范围

- 单租户、`user/admin` 两种角色。
- 私有部署的 Web、Go Backend、Python Runtime、Postgres、Redis、MinIO、Qdrant 与 OpenAI-compatible 本地模型服务。
- RAG-first；模型可替换，微调和多智能体不在本计划范围。

### 非目标

- Git 工作流不作为本计划或本地试用的阻塞条件。
- 公网暴露、多租户、细粒度 RBAC、外部 SaaS 工具编排和微调管理不进入 V1 交付门槛。

## 2. 已有可验证能力

- 真实登录、聊天、SSE、引用、知识上传、reindex、chunk inspector 与配置版本历史已存在。
- Postgres、Redis、MinIO、Qdrant 和 Ollama 的可选适配与独立 smoke 已存在。
- `llama3.2:latest` 的 runtime-local quality gate 已通过：`27/40`，检索/引用 `0.9714`，拒答 `1.0`，grounded `0.5786`，runtime error `0`。
- K7 新增不可变 `prompt_v2` 后连续两次严格 gate 通过：`30/40, grounded=0.6714` 与 `28/40, grounded=0.5857`；其余核心指标保持。
- C2 已验证有界会话意图：有限普通交流不调用检索/模型且不伪造 snapshot；混合、业务和未知输入继续按 K5 走 RAG；越界闲聊得到范围提示。
- 默认 deployment 仍保持受控词法基线；`K8-R1` 已验证 OpenAI-compatible embedding、Qdrant 原生 dense query 与 fail-closed 配置边界。`K8-R2a` 已验证 lexical compatibility/native dense 的受控策略注册、配置版本与 execution evidence 闭环，以及真实 Postgres/Qdrant/`all-minilm` provider smoke。经产品选择，`K8-R2` 已冻结 `all-minilm` 对照 `qwen3-embedding:0.6b` 的单变量 dense 比较条件；sparse/hybrid、provider rerank 和至少两模型质量比较仍未完成。

## 3. 交付门槛与缺口

| 优先级 | 工作包 | 当前状态 | 交付门槛 |
| --- | --- | --- | --- |
| `D0` | 全栈本地开发环境 | `verified` | 持久化依赖、Runtime、Backend、Web 同时运行；健康检查、管理员登录、真实 Runtime 聊天与上传/reindex smoke 通过。 |
| `D1` | 文档与实现状态对齐 | `verified` | `K1` 已冻结 revision/snapshot/provenance 契约与 legacy 边界；规格、主设计、详细设计、路线图和 contract fixture 持续与 K2-K6 实现同步。 |
| `D2` | 知识生命周期与 revision | `verified` | `K2` 已验证 versioned migration、revision/snapshot/event/outbox 的权威写入与 legacy backfill；`K3` 已验证 Streams 投递和 immutable revision/Qdrant 投影；`K4` 已验证管理员 lifecycle API/UI；`K5` 已验证 snapshot-scoped retrieval 与回答 provenance。 |
| `D3` | Snapshot、更新日历与回答时效 | `verified` | K5 已固定每条回答的 snapshot/revision provenance；K6 已验证可恢复 update scan、possibly-stale 标记、管理员日历/manifest 和独立 latest-snapshot revalidation。 |
| `D4` | 管理、配置与可观测闭环 | `verified` | K7 execution/delivery/eval 管理查询、评测报告幂等登记、配置实际应用与 prompt_v2 严格质量门，以及 Q1 feedback、受控 experiment、审批发布/回滚与候选配置隔离均已通过。 |
| `D5` | 检索、回答策略与模型对比 | `in_progress` | `K8-R1/K8-R2a` 已闭合 native/strategy 基础；`A1-1` 已冻结 clarification/coverage 开发集、离线评分和 provider evidence，`A1-2` 待冻结线上契约与持久化；仍需七格式端到端 evidence、至少两种生成模型受控比较，以及 PDF/XLSX/PPTX 失败逐类降低且不退化既有 gate。 |
| `D6` | 持续能力、可靠性与试用发布 | `in_progress` | K9-S0 会话归属授权已验证；仍需 service security、消息恢复、观测、部署/回滚与默认凭据替换。 |

## 4. 顺序与并行

1. 先完成 `D0`，把开发与验收变成可随时重现的真实全栈环境。
2. `D1` 与 `D0` 收尾同步完成，避免旧计划继续误导后续实现。
3. `D2` 和 `D3` 共同构成知识演进核心，是内部试用前的硬缺口；先冻结 snapshot 发布状态机，再实现删除与时效。
4. `D4` 可与不改共享契约的质量实验并行，但管理员读模型、日志与配置必须在内部试用前完成。
5. `D5` 依赖已冻结的 snapshot/retrieval/评测契约；模型、Prompt 或 RAG 改动都要重跑 R5 gate 和知识演进评测。
6. `D6` 在 `D2` 明确状态机后收口，形成可持续扩展能力的内部试用版本。

## 5. 验收总门槛

内部试用前必须具备：

- `D0` 全栈启动与真实模型问答可复现。
- `D2` revision、disable/delete 与发布 snapshot 的端到端证据。
- `D3` 历史回答 provenance、更新日历、可能过期标记与最新知识核验入口。
- `D4` 管理员可查询日志/eval，且已声明为可配置的字段均真实生效。
- `D4` 的候选质量配置不因创建而影响线上；每次发布/回滚均能追溯到固定条件、报告、审批人与前一 release。
- `D5` 至少两模型比较、七格式端到端覆盖、R5 gate 持续通过，并对当前失败样本给出改进或明确限制。
- `D6` 的能力契约、关键持久化、队列失败、资源 ownership 和回滚操作具有测试与 runbook。

## 6. 知识演进分阶段计划

### Phase 1: Knowledge Core

- 范围：`D1/D2`，冻结 document/revision/snapshot/event 契约，采用 Postgres migration 与 outbox；实现 revision-ready 后的原子 snapshot publication、disable/delete 可见性和 reconciliation。
- 不做：自动重写历史回答、复杂能力工具或模型替换。
- 退出标准：新 snapshot 不会在索引失败或重复任务后提前发布；新会话检索不到 disabled/deleted revision。

### Phase 2: Fresh Answers

- 范围：`D3`，为 assistant message/citation 记录 snapshot/revision provenance；增加 update calendar、潜在过期标记和 revalidate API/UI。
- 不做：全文历史消息的同步重生成；只有影响到 citation revision 的回答进入候选复核。
- 退出标准：旧回答可解释、可核验，新问题使用 latest snapshot，历史复放可显式指定原 snapshot。

### Phase 3: Admin And Evidence

- 范围：`D4`，完成 revision/snapshot/calendar 管理视图，持久化 retrieval/generation/eval 记录，并让 prompt/style/retrieval 配置真实生效。
- 退出标准：管理员能解释“何时发生了什么更新、它影响了哪些回答、当前 Agent 用了什么配置”。
- `2026-07-15`：`K7` 已验证，设计冻结为 message execution evidence 与 eval report registration 的 Postgres 权威写路径；K5/K6/C1/prompt_v1 历史记录未回填或重写。新增 prompt_v2 后连续两次严格 gate 通过；Backend/Runtime/Web、isolated Postgres、全栈 integration 与生产 Playwright 均通过。
- `2026-07-16`：`C2` 已验证。Backend exact allowlist router、Runtime controlled response、Postgres `009` execution facts 与 Web “未使用知识库”标签落地；真实三路径、integration `42 passed`、Playwright `17 passed` 和 prompt_v2 gate `29/40, grounded=0.6286` 通过。
- `2026-07-16`：`K9-S0` 已验证。Postgres migration `010` 将 legacy conversation 隔离回填为 `user_admin`；chat list/detail/message/source/revalidation 全部按 session subject 校验 owner，admin chat 不 bypass，admin evidence 保留全局审计。Backend full/race、isolated/开发 Postgres、integration `42 passed` 与 production Playwright `18 passed` 通过。
- `2026-07-17`：`Q1` 已验证。Postgres `012` 将 feedback、experiment、release pointer 与 forward rollback 置于 Backend authority；candidate config 不会自动成为 active，runner 仅登记 exact-match report，Web 在 `/config` 提供反馈/报告对照/审批发布。isolated real Postgres、Backend full、Runtime `91`、eval `18`、Web 与 full integration `52` 均通过。它不会自动放宽 K8-R2 dense cutover 门槛。

### Phase 4: Retrieval Quality And Models

- 范围：`D5`，接入真实 embedding/reranker provider 与 Qdrant metadata-filter/hybrid retrieval；按固定 harness 对比至少两个模型。
- 退出标准：七格式端到端回归、知识更新评测与 R5 gate 都通过，模型选择有证据。
- `2026-07-16`：`K8-R1` 已验证。Ollama `all-minilm:latest` 实测 `384` 维，真实 embedding + Docker Qdrant 的 ingest/reload/revision-scope/citation smoke 通过；Runtime `89 passed`，最终 prompt_v2 gate `30/40, grounded=0.6571`、retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`。R2 必须另行完成第二 embedding model 的受控比较，不能以 R1 功能验收替代质量选型。
- `2026-07-17`：`K8-R2a` 已验证 [受控检索策略注册表](../../design-docs/single-agent-v1-k8-retrieval-strategy-registry-design.md)：策略 id 随 retrieval config 版本固化，Runtime request/profile 必须 exact match，evidence 与 eval report 显示实际 strategy；Backend/Runtime/Web/integration/Playwright、lexical control gate、真实 Postgres migration/evidence、native Qdrant 与 `all-minilm` provider smoke 均通过。不开放动态 Python plugin，也不执行 dense cutover。随后经产品选择，K8-R2 冻结为 `all-minilm` 对照 `qwen3-embedding:0.6b` 的单变量条件；先补 eval scope/parameter evidence，再执行比较。
- `2026-07-17`：`K8-R2` 已完成受控比较。runner 以同一 seven-format revision scope、`prompt_v2`、`llama3.2:latest`、`top_k=3`、rerank 关闭分别 materialize 两个隔离 dense collection；Qwen 为 `27/40`、grounded `0.7143`，优于 all-minilm 的 `23/40`、`0.6000`，检索/引用均为 `1.0`。两组拒答均为 `0.0`，所以 Qwen 只作为后续 dense 质量优化候选，不能切换业务 profile 或发布 dense snapshot。
- `2026-07-18`：`K8-R3` 已完成 fixture calibration。`evidence_min_score` 已成为 immutable retrieval config/Runtime/evidence/eval 字段；固定 Qwen score gap 为 must-answer 最低 `.5237`、must-refuse 最高 `.5020`，选择 `.51` 后最新 candidate `30/40`、grounded `.6429`、retrieval/citation/refusal `1.0` 并通过 prompt_v2 gate。该结论不批准业务 dense cutover；真实业务 snapshot 仍须经过 Q1 release 与 onboarding eval。
- `2026-07-18`：`K8-R4a` 已验证。管理员可在 `/knowledge` 为已发布 snapshot 创建审核过的 must-answer/must-refuse 真值集，冻结后由 Backend 以固定 snapshot、agent/retrieval config pair 执行 Runtime 并留存逐题 evidence。它不自动调参、发布配置或删除知识；真实业务内容仍待上传并实际运行质量门，passing run 才是测试知识逐个退役的前置证据。
- `2026-07-22`：新增 `A1` 澄清与回答处置规划。模糊内部问题不再靠 prompt 强行作答；设计冻结 pre/post 两阶段 scope/coverage gate、pending clarification/resolved query、legacy unknown、actual provider/fallback evidence 与 retrieval candidate/answer citation 分层。当前 `conv_047` 虽绑定 `llama3.2:latest`，运行中的 Runtime 未配置 primary LLM，实际走 heuristic fallback；真实 `fallback=false` llama3.2 gate 复验为 `29/40`、grounded `.6286`，故模型升级只能作为 Q1 候选，不能替代 A1 策略修正。
- `2026-07-30`：`A1-1` 已验证。`clarification/v1-dev.jsonl` 固定 `32` 条本地示例制度题（`10` grounded、`10` clarification、`6` insufficient、`6` partial）；runner 对显式处置/rank/coverage/resolution 评分，并记录 provider/fallback observation。mock `32/32` 仅证明数据和评分器，报告固定 `quality_eligible=false`，不能作为模型、检索或 Q1 发布证据。下一步是 `A1-2` shared contract/migration 设计，之后才允许 Runtime policy 实现。

### Phase 5: Capability Growth And Trial Release

- 范围：`D6`，以受权限约束的工具契约逐项扩展 Agent 能力，补 queue reliability、ownership、部署和回滚。
- 退出标准：每项新增能力都有 schema、权限、审计、失败边界与评测，产品可进入受控内部试用。

具体工作包、依赖、并行边界、迁移策略与下一动作以 [知识演进执行队列](single-agent-v1-next-dev-sequence.md) 为准。

## 7. 当前执行记录

### D0 全栈本地开发环境

- Owner：`p`
- 状态：`verified`
- 起始证据：Docker Postgres/Redis/MinIO/Qdrant 健康；Ollama 的 `llama3.2:latest` 已安装；Python、Node 和 Go 工具链可用。
- 配置策略：使用真实 Postgres、Redis、MinIO、Qdrant 与 Ollama，避免开发时悄悄退回内存或 deterministic model fallback。
- 当前地址：Web `http://127.0.0.1:3000`、Backend `http://127.0.0.1:8080`、Runtime `http://127.0.0.1:8000`。
- `2026-11-04`：Web 依赖统一切换为 pnpm `10.13.1`，`apps/web/pnpm-lock.yaml` 是唯一前端锁文件；`make web-install`、`make typecheck`、`make web-lint`、`make web-build` 均通过。真实全栈重启后 Web/Runtime/Backend readiness 均通过，聊天核心 Playwright 用例 `1 passed`。
- 验收证据：`RUNNING=1 make infra-check` 通过；三个应用 health endpoint 返回成功；管理员登录通过；新建 `llama3.2:latest` 配置版本后，真实 Runtime 回答“住宿费用超过800元时有什么要求？”并持久化 JSON、Markdown、TXT 三个来源；上传 `employee-handbook.md` 成功写入 `s3://knowledge-docs/...`，任务进入 `succeeded`；`WEB_BASE_URL=http://127.0.0.1:3000 make web-ui-smoke`、`make web-lint` 与 `make typecheck` 通过。
- 开发环境修复：Next 16 开发服务器已允许 `127.0.0.1` 作为 `allowedDevOrigins`，避免 HMR/客户端 hydration 被 loopback 来源拦截。
- `2026-07-22` 运行态审计：当前 Web/Backend/Runtime/Ollama 均监听，但 Runtime 进程未设置 `LOCAL_LLM_BASE_URL`/`LOCAL_LLM_ALLOW_FALLBACK=false`；因此当前页面只适合链路 smoke，不适合评价 llama3.2 回答质量。真实质量启动必须按 `docs/runbooks/local-model-eval.md` 显式连接 Ollama 并禁用 fallback。
- `2026-07-23` D0 provider/可观测性收口：删除产品 `HeuristicModelClient` 与 `CompositeModelClient`；模型 endpoint 缺失或调用失败时返回明确错误，不再生成替代答案。`runtime-rag-dev` 默认显式连接本机 Ollama/`llama3.2:latest`；Backend 与 Runtime 启动控制台分别报告脱敏的持久化依赖、active config、检索/embedding 与模型 provider，避免再以会话绑定模型或单一 `/healthz` 误判实际 provider。integration/e2e 改用显式 test-only OpenAI-compatible provider。
- `2026-07-24` [P0 生产 Fail-Closed 与真实性边界改造计划](single-agent-v1-production-fail-closed-hardening-plan.md)的 F0 已完成：`p` 已冻结产品/架构/错误 API、strict v2 Runtime 契约、execution attempt/服务认证设计和伪成功回归矩阵；没有修改服务代码、Postgres migration、active release 或部署配置。后续唯一实现起点是 F1 Backend，随后按 F2 Runtime、F3 摄取真实性、F4 profile、F5 readiness、F6 Web、F7 migration/release、F8 rollout 的无冲突波次执行。
- 下一步：进入 `K3` reliable delivery 与 revision-aware indexing；任何 RAG/Prompt/模型变更继续重跑 R5 gate。
