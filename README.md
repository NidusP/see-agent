# see-agent

`see-agent` 是一个文档优先的工作区，用于规划、初始化并逐步实现一个私有部署的单智能体 AI 产品。

当前阶段已完成三类初始化资产：

- AI 工程文档集合
- monorepo 骨架与 agent 调度脚手架
- Web、Go Backend、Python AI Runtime 的最小可运行骨架与跨服务 smoke 测试
- 管理员质量反馈、受控实验与审批发布/回滚闭环（已验证）

文档集合包括：

- 产品规格
- 系统设计文档
- 评测 harness
- 执行计划
- 资料来源说明

刚接触本项目时，可先阅读 [摄取、检索与回答职责导读](docs/design-docs/single-agent-v1-ingestion-chat-boundary-guide.md)：它解释管理员上传与用户提问两条链路，并明确 Go Backend、Python AI Runtime、PostgreSQL、MinIO、Redis、Qdrant 和 Ollama 的职责边界。

启动本地真实全栈、准备模型与服务凭据，以及排查常见问题，请参阅[本地联调栈启动说明](docs/runbooks/local-stack-bootstrap.md)。

现在也可以在仓库根目录直接运行 `make dev`：它会检查本地工具、Ollama 模型、Runtime service token、Docker 依赖和子项目依赖，按 Runtime → Backend → Web 顺序启动并等待就绪；失败会停止后续步骤并输出对应日志。示例知识不会自动导入，服务就绪后可自行执行 `make bootstrap-rag-knowledge`。

管理员如何把历史回答纠正沉淀为受控实验并经审批发布，见[质量运营与受控发布设计](docs/design-docs/single-agent-v1-quality-operations-release-design.md)。

生产链路如何禁止伪回答、伪引用与隐式降级，见 [Fail-Closed 执行设计](docs/design-docs/single-agent-v1-fail-closed-execution-design.md) 和 [P0 回归矩阵](docs/harness/single-agent-v1-fail-closed-regression-matrix.md)。该契约的 F0 已冻结；服务代码收口从 F1 开始。

这套文档结构遵循 OpenAI《Harness Engineering》强调的工作方式：将 `AGENTS.md` 作为导航图，将结构化的 `docs/` 目录作为产品、设计与执行意图的持久化记录。

## 目录结构

```text
see-agent/
  AGENTS.md
  README.md
  Makefile
  docs/
    product-specs/
      single-agent-v1-spec.md
    design-docs/
      single-agent-v1-sdd.md
      single-agent-v1-ingestion-chat-boundary-guide.md
      single-agent-v1-knowledge-evolution-design.md
      single-agent-v1-fail-closed-execution-design.md
      single-agent-v1-workspace-layout.md
      single-agent-v1-request-prompt-lifecycle.md
    harness/
      single-agent-v1-harness.md
      single-agent-v1-fail-closed-regression-matrix.md
    exec-plans/
      active/
        single-agent-v1-plan.md
        single-agent-v1-readiness-checklist.md
        single-agent-v1-parallel-workstreams.md
        single-agent-v1-parallel-claims.md
    references/
      openai-source-notes.md
  apps/
    web/
  services/
    backend-go/
    ai-runtime-py/
  packages/
    contracts/
    prompts/
    config-schema/
    shared-types/
  infra/
    compose/
    k8s/
  data/
    sample-docs/
    eval-datasets/
    fixtures/
  tests/
    integration/
    e2e/
  agent-registry/
    registry/
    runbooks/
    handoffs/
    task-templates/
```

## 预期构建方向

- 产品：单智能体 Web 问答
- 部署：私有化 / 自托管
- 知识：目标是支持 revision、snapshot、更新日历和回答时效核验的内部知识库
- 推理策略：先建立 RAG 基线
- 模型微调：延后到评测证明 RAG 局限后再考虑
- 模型对比：在基线 harness 稳定后比较 `Qwen` 与 `DeepSeek`

## 核心产品判断

- LLM 是可替换的大脑，Agent 的护城河是业务上下文工程。
- 模型决定上限的一部分，系统决定可靠性的大部分。
- 专家能力来自正确知识、上下文、工具、约束与评测的持续组合，而不是依赖单一模型记忆。

## 当前实现状态

- `apps/web/`：Next.js 聊天优先壳层已落地；已具备中文登录页、普通用户聊天门禁、管理员顶栏工作区切换与可收缩历史会话侧栏；`/chat` 已接同源 API proxy、即时 pending turn、SSE token 增量、有限普通交流标签、citations 与回答内 evidence anchors/按需详情抽屉；非知识回复明确显示“未使用知识库”；`/knowledge` 已接 lifecycle；`/config` 已接配置与 execution/delivery/eval evidence 追溯。
- Web 已提供 build-time 可插拔主题注册表：默认 `verdant-habitat` 以植物、授粉者与绿色共生理念塑造阅读环境，用户可在登录页或工作台切换个人本地主题；该偏好不影响知识、答案、角色或系统管理员参数。
- `services/backend-go/`：最小产品 API、登录与角色门禁、Postgres conversation ownership、可选 Redis/Postgres/MinIO、K3 Redis Streams、chat dispatcher/SSE，以及 K7 evidence 已落地；检索策略、embedding model、top_k/rerank 会随 immutable retrieval config 传入 Runtime 并进入 execution evidence。
- `services/ai-runtime-py/`：七类格式 parser、chat/stream、revision/Qdrant projection、模型 adapter、K8-R1 原生 dense retrieval 与 K8-R2a 受控策略注册表已落地；C2 controlled intent 使用固定产品回复，不调用 retrieval/model，并返回可审计 execution facts。
- `tests/integration/` 与 `tests/e2e/`：已具备可执行 smoke 测试，`make integration` 会构建 Web、启动 runtime/backend/web，并检查 API proxy 与浏览器级关键链路。
- `make provider-stability-probe`：对真实 generation provider（负责文本生成的模型服务）执行连续短文本、结构化 JSON 和 SSE（服务端事件流）探针，记录稳定性与延迟，不写产品状态，也不产生质量评测结论。
- `infra/compose/`：Postgres、Redis、MinIO、Qdrant 本地 compose 基线已存在。
- 根级 `make dev`：一键执行全栈开发启动检查、依赖准备、Runtime/Backend/Web 启动和 readiness smoke；应用日志写入 `.local/dev/logs/`，按 `Ctrl-C` 只停止本次启动的应用进程。
- `data/eval-datasets/`：首批 `40` 条 baseline 样本、phase-1 deterministic mock runner 与 runtime-local baseline runner 已落地。

当前主要缺口与注意事项：

- 新环境的 Agent Config 默认模型为 `qwen3:14b`，默认 retrieval 为 `native_dense_v1 + qwen3-embedding:0.6b`。`qrel_002` 已通过本机 Qwen strict Q1 baseline/candidate 与真实全栈聊天验证；`LOCAL_LLM_DEFAULT_MODEL_ID` 只影响首次初始化。历史 Llama/lexical evidence 保留为只读审计，不能重新成为产品执行路径。
- `R3/R5` 的 `prompt_v1` 曾以 `27/40`、grounded `0.5786` 建立基线，K7 收口时又连续两次降到 `26/40`、grounded `0.5429`。仓库没有降低门槛或改写 v1，而是新增不可变 `prompt_v2`；两次严格 K7 gate 分别达到 `30/40, grounded=0.6714` 与 `28/40, grounded=0.5857`，retrieval/citation `0.9714`、refusal `1.0`、runtime error `0` 均保持。最新 v2 报告位于 `data/eval-datasets/reports/runtime/prompt-v2/latest.json`。
- K8-R1/K8-R2a/K8-R3 calibration 已验证：Ollama `all-minilm:latest` 的实际 embedding 为 `384` 维；真实 provider + Docker Qdrant smoke 已覆盖 immutable revision 摄取、Runtime reload、revision-scoped query 和 citation provenance。K8-R2a 使 `lexical_compat_v1` 与 `native_dense_v1` 成为受控、版本化的 retrieval strategy：配置、Runtime profile 和回答 evidence 可互相核验；未知或不匹配策略 fail-closed，不加载任意代码。K8-R2 的受控比较中，Qwen `27/40` / grounded `0.7143` 优于 all-minilm `23/40` / `0.6000`，但二者 dense must-refuse 均失败；K8-R3 已把 `evidence_min_score` 固化为可审计参数，并在同一 Qwen fixture 以 `.51` 达到 refusal `1.0`、最新 `30/40` / grounded `.6429`、通过 prompt_v2 gate。该值只能作为相同 collection/scope 的校准证据；真实业务知识始终须 onboarding eval，而只有同时切换 dense/profile/index/boundary 时才须 Q1 release。
- Chat streaming/SSE 只接受 Runtime 原生 token stream；Runtime 未配置、不可用、契约无效或在终态前中断时，Backend/Web 显示明确失败并保留 execution attempt，不再用本地回答或分块伪装成功。
- Runtime 产品 profile 只允许 `RUNTIME_RETRIEVAL_STRATEGY=native_dense_v1`，它需要 `EMBEDDING_*` 和独立 Qdrant collection；已退役的 `RUNTIME_RETRIEVAL_MODE` 会拒绝启动。历史 lexical request 只能 fail-closed，不能回退或伪造回答。
- 当前产品发布为 `qrel_002`（Qwen native dense）。系统管理员不能创建 lexical config；任何后续 retrieval 变更仍须匹配 collection/index、业务 onboarding 与 Q1 审批发布。知识库管理员负责上传、索引和 onboarding；系统管理员确认 revision publish，并且只能在“旧+新”当前快照通过 onboarding 后停用旧文档、在不含旧文档的当前快照再次通过后删除。该门禁在 Postgres transaction 内复核，界面按钮不能绕过。
- `K1-K6` 已验证：不可变 document/revision/snapshot、可靠索引投影、管理员 lifecycle、snapshot-scoped chat/provenance，以及知识更新日历、possibly-stale 标记和独立历史核验均已具备。核验不会改写原回答；它使用最新 snapshot 生成带 verdict 和引用的新记录。
- `K7` 已验证：回答、snapshot manifest、revision citation、Agent/retrieval config、delivery 与 eval report 可由管理员查询；eval 是幂等登记，不是发布。`prompt_v1` 历史保持不变，`prompt_v2` 已通过质量门并可由新配置选择。
- `K9-S0` 已验证：conversation 由 Postgres `owner_id` 授权，legacy 会话隔离给 `user_admin`；普通用户和管理员的 chat API 都只能访问自己会话，管理员全局查询只保留在 evidence API。
- `C2` 已验证：`你好` 等有限交流正常响应；笑话/陪聊等开放闲聊被简短引导回知识问答；混合业务输入继续走 RAG。Postgres 和管理页会如实显示是否检索、生成和使用知识。
- Backend 默认仍走内存 repository、本地 ingestion queue 和内存 session；配置 `DATABASE_URL` 后可切到 Postgres repository，配置 `REDIS_URL` 后可切到 Redis-backed ingestion queue 与 Redis-backed session store。
- 本地真实全栈开发使用显式的 `fullstack_dev` 环境、`make fullstack-preflight`、`make runtime-fullstack-dev`、`make backend-fullstack-dev` 与 `make fullstack-smoke`。`SEE_AGENT_RUNTIME_SERVICE_TOKEN` 必须从仓库外的权限受限 secret 文件加载；模板中的变量为空，缺失、占位或过短值会拒绝启动。Runtime 在启动时会对实际 generation provider 做一轮不持久化的短生成 warm-up，只有成功后 `/readyz` 才会返回 ready。`BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS`（默认 60）必须不超过 `LOCAL_LLM_TIMEOUT_SECONDS`，避免 Backend 先超时而将仍在生成的模型误报为不可用。旧 `*-rag-dev` 命令已退役，不能再为产品路径补入默认 DSN、模型、账号或对象存储凭据；全新数据库不会自动写入知识，需以 `make bootstrap-rag-knowledge` 读取 `data/local-dev-knowledge/v1/` 的三份示例制度并走管理员 upload/index/publish 生命周期。

本地 MVP 默认登录账号：

- 系统管理员：`admin` / `admin123`
- 知识库管理员：`knowledge_admin` / `knowledge_admin123`
- 普通用户：`user` / `user123`

可通过 `SEE_AGENT_SYSTEM_ADMIN_USERNAME`、`SEE_AGENT_SYSTEM_ADMIN_PASSWORD`、`SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME`、`SEE_AGENT_KNOWLEDGE_ADMIN_PASSWORD`、`SEE_AGENT_USER_USERNAME`、`SEE_AGENT_USER_PASSWORD` 覆盖。旧 `SEE_AGENT_ADMIN_USERNAME/PASSWORD` 仍作为系统管理员兼容别名；新变量优先。

## 建议阅读顺序

1. `AGENTS.md`
2. `docs/exec-plans/active/single-agent-v1-remaining-roadmap.md`
3. `docs/product-specs/single-agent-v1-spec.md`
4. `docs/design-docs/single-agent-v1-sdd.md`
5. `docs/design-docs/single-agent-v1-workspace-layout.md`
6. `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
7. `docs/design-docs/single-agent-v1-request-prompt-lifecycle.md`
8. `docs/design-docs/single-agent-v1-web-ui-design.md`
9. `docs/design-docs/single-agent-v1-backend-design.md`
10. `docs/harness/single-agent-v1-harness.md`
11. `docs/harness/single-agent-v1-integration-test-plan.md`
11. `docs/harness/single-agent-v1-eval-baseline-plan.md`
12. `docs/exec-plans/active/single-agent-v1-readiness-checklist.md`
13. `docs/exec-plans/active/single-agent-v1-next-dev-sequence.md`
14. `docs/exec-plans/active/single-agent-v1-parallel-workstreams.md`
15. `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
16. `docs/exec-plans/active/single-agent-v1-plan.md`
