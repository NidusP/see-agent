# 单智能体 V1 连续任务续接指南

## 文档控制

- 状态：`active / K9-R2_safe_retirement_verified`
- 更新日期：`2026-07-18`
- 目的：在长任务需要跨会话续接时提供唯一持久化 checkpoint，避免依赖易丢失的聊天摘要

## 1. 当前项目状态

- 已验证：`K1-K7/C1/C2/U3/K9-S0/K8-R1/K8-R2a`，包含知识 revision/snapshot、可靠 delivery、lifecycle、回答 provenance、时效核验、引用锚点、管理 evidence、配置闭环、有限会话意图、会话归属隔离、native dense 基础能力和受控策略注册闭环。
- 当前质量证据：`K8-R2a` 的 fixed lexical control gate 为 `31/40`、grounded `0.7357`、retrieval/citation `0.9714`、refusal `1.0`、unexpected runtime error `0`；其 report 显式记录 `retrieval_strategy=lexical_compat_v1`，不代表 dense 质量选型。
- 当前交付判断：功能完整的内部 MVP；尚未达到企业生产标准。
- 当前最高风险：Qwen fixture 的 `.51` evidence boundary 已通过校准，但其 collection/snapshot 不是业务知识，不能替代真实业务 onboarding、Q1 release 或 dense cutover；现有 deployment 仍是过渡词法检索。
- 当前后续主线：获得真实业务知识后，完成 successor snapshot、产品内 onboarding eval，并由系统管理员按两段式门禁安全退役测试知识；仅当同时切换 dense profile/embedding/index/boundary 时才进入 Q1 release。dense-only cutover、lexical retirement 和 K9 生产加固仍是后续独立工作。

## 2. 最近已验证

- `GOCACHE=/tmp/see-agent-go-build make backend-test`：通过；新 admin retrieval strategy allow-list、normal chat strategy propagation 与 K6 revalidation strategy assertion 编译并回归。
- `make runtime-test`：`91 passed`，覆盖 Runtime registry 的 explicit/legacy/unknown/conflict 配置和 request/profile mismatch 在索引/embedding 前 fail-closed。
- `./.venv/bin/python -m pytest tests/integration/test_eval_generation_config.py`：`11 passed`，确认 eval request/report 都带 strategy。
- `make web-lint`、`make typecheck`、`make web-build`：通过；`WEB_BASE_URL=http://127.0.0.1:3000 pnpm exec playwright test e2e/k7-admin-evidence-ui.spec.ts --workers=1`：`1 passed`。
- `GOCACHE=/tmp/see-agent-go-build make integration`：`45 passed`。
- `make eval-runtime-k7-gate`：通过，报告在 `data/eval-datasets/reports/runtime/prompt-v2/latest.json`。
- `make postgres-k7-smoke`、`make runtime-native-qdrant-smoke` 与使用 `EMBEDDING_BASE_URL=http://127.0.0.1:11434/v1 EMBEDDING_MODEL=all-minilm EMBEDDING_DIMENSIONS=384` 的 `make runtime-native-dense-smoke` 均通过。前两项验证真实 Postgres 和 native Qdrant；第三项验证真实 Ollama embedding、ingest/reload、scope 和 citation provenance。dense smoke 已补显式 `native_dense_v1` request/profile，防止旧 request 形状被 fail-closed 后误判为 provider 故障。
- `qwen3-embedding:0.6b` 已安装；OpenAI-compatible `/v1/embeddings` probe 返回 `1024` 维。使用 `EMBEDDING_BASE_URL=http://127.0.0.1:11434/v1 EMBEDDING_MODEL=qwen3-embedding:0.6b EMBEDDING_DIMENSIONS=1024` 的 `make runtime-native-dense-smoke` 通过。
- `K8-R2`：同一 immutable seven-format revision scope、`prompt_v2`、`llama3.2:latest`、`top_k=3`、rerank 关闭下，all-minilm 为 `23/40`、grounded `0.6000`，Qwen 为 `27/40`、`0.7143`；两组 retrieval/citation `1.0`、refusal `0.0`。Qwen 是后续质量优化候选，不是 cutover 批准。
- `make runtime-test`：`91 passed`；eval strategy contract：`15 passed`；`make eval-runtime-k7-gate` 通过；提升权限后 `GOCACHE=/tmp/see-agent-go-build make integration`：`49 passed`。

## 3. 新会话默认阅读顺序

1. `README.md`
2. `AGENTS.md`
3. `docs/exec-plans/active/single-agent-v1-product-delivery-plan.md`
4. `docs/exec-plans/active/single-agent-v1-next-dev-sequence.md`
5. `docs/exec-plans/active/single-agent-v1-production-hardening-plan.md`
6. `docs/design-docs/single-agent-v1-k8-retrieval-strategy-registry-design.md`
7. 本文件的“当前 checkpoint”

## 4. 自动续接触发与协议

- 只在多次上下文压缩已造成信息风险，或完成一个可独立验收的里程碑时触发；正常透明压缩不触发换会话。
- 触发前必须更新第 6 节 checkpoint，并停止在稳定状态；不得遗留未完成 migration、写操作或未知结果的命令。
- 新会话必须先检查工作区、服务健康和 checkpoint；不得仅凭摘要重复或覆盖已完成工作。
- 新会话成功创建并获得完整 checkpoint 后，旧会话才可归档。

## 5. 重要约束

- 前端页面交互优先中文。
- 每批结束统一验收和汇报，不再每完成单项就停下来问下一项。
- 改 API、状态机、eval 指标口径或 retrieval pipeline 前，先更新 docs。
- 本仓库不使用 Git 工作流；不要用 destructive git 命令。
- PostgreSQL 是 conversation ownership、knowledge lifecycle 与 provenance 的权威；Web cookie 不是授权权威。

## 6. 当前 checkpoint

- 状态：`active / K8-R4_business_knowledge_onboarding_pending`
- 原始目标：把 K8 原生检索升级为可扩展但受控的策略边界，并让管理员、Runtime 与 K7 evidence 对“回答实际使用的检索方案”达成可追溯闭环；不破坏 K5/K6/C1 历史 provenance。用户追加目标：把知识更新、质量纠正、受控实验、校准审批与发布/回滚融合进管理员产品流程。
- 已作决策：采用本地内建 registry，而非任意 Python plugin。当前仅 `lexical_compat_v1`、`native_dense_v1`；一个 Runtime process 只启用一个 active profile，knowledge request 的 strategy 必须 exact match。未知/冲突/mismatch、embedding/model/provider/Qdrant 失败均 fail-closed，禁止 lexical fallback。
- 已完成实现：
  - `retrieval_configs` migration `011_retrieval_strategy_registry` 回填历史 config 为 `lexical_compat_v1`；Postgres 继续是权威，历史 answer/evidence 不回填。
  - Backend `RetrievalConfig`、API allow-list、chat/K6 revalidation `response_overrides`、K7 execution evidence 全部携带 `retrieval_strategy`。
  - Runtime `app/retrieval_strategies.py` registry 兼容旧 `RUNTIME_RETRIEVAL_MODE`，新增 `RUNTIME_RETRIEVAL_STRATEGY`；pipeline 在 index/embed 前做 profile match。
  - Web 配置页为 strategy select；eval request/report/Markdown 均写 strategy；contracts/fixtures/tests 已同步。
- 环境验收与修复：真实 Postgres migration/evidence、native Qdrant transport 和 Ollama `all-minilm` provider smoke 都已通过；`scripts/ops/runtime_native_dense_smoke.py` 现在强制 `RUNTIME_RETRIEVAL_STRATEGY=native_dense_v1` 并发送 matching request override。
- K8-R2 已冻结并执行：仅比较 `all-minilm:latest`（provider id `all-minilm`）与 `qwen3-embedding:0.6b`；固定 40 条 baseline dataset、`prompt_v2`、`llama3.2:latest`、temperature `0.2`、max output `1024`、`top_k=3`、rerank 关闭、native dense profile 与现有 grader。runner 只写入每模型专属 Qdrant collection，并生成相同的 experiment scope；Qwen `27/40` / grounded `0.7143` 胜过 all-minilm `23/40` / `0.6000`，但二者 refusal 均为 `0.0`。详情以 harness 第 22 节为准。
- 关键变更范围：`packages/contracts/{openapi.yaml,chat-runtime-request.schema.json,fixtures/**}`；`services/backend-go/internal/{domain,repository,service,httpapi,server}`；`services/ai-runtime-py/app/{main.py,retrieval_strategies.py,api/schemas.py,pipelines/retrieval_pipeline.py}`；`apps/web/app/_components/config-workspace.tsx` 与 `_lib/config-api.ts`；`scripts/eval/run_baseline.py`；对应 Go/Python/Playwright/integration tests；K8-R2a design、harness、delivery plan、next sequence、parallel claims、README/AGENTS。
- 外部状态与服务：本任务启动的临时内存 Backend `:8080` 与 Web `:3000` 已停止；当前不能假定产品服务运行。Docker Desktop 的 Postgres/Redis/MinIO/Qdrant 已在本次验收中启动且通过 smoke，Ollama 已监听并安装 `all-minilm:latest`、`qwen3-embedding:0.6b`、`llama3.2:latest`；Qwen probe 的实际维度为 `1024`。后续会话仍应先检查健康状态。migration `013` 已由真实 Postgres smoke 覆盖。没有 Git 操作、没有真实 dense cutover、没有 lexical 删除。
- 已新增并验证 Q1：[质量运营与受控发布设计](../../design-docs/single-agent-v1-quality-operations-release-design.md)。Postgres `012` 以 Backend 为唯一 writer 持久化 append-only feedback、冻结 experiment、release pointer/event 与 forward rollback；candidate config 不再因创建时间自动 active。runner 仅 opt-in 登记 raw report 后 attach exact-match experiment report；`/config` 已提供 feedback、报告对照、审批发布与回滚状态。isolated Postgres lifecycle、Backend full、Runtime `91`、eval `18`、Web lint/typecheck/build、管理员 Playwright、full integration `52`、真实 Postgres K7 smoke 与固定 `prompt_v2` runtime-local gate 已通过。
- 已冻结 K8-R3：[Dense 证据边界校准与知识库切换设计](../../design-docs/single-agent-v1-k8-dense-refusal-calibration-design.md)，说明了每项参数如何影响“有依据回答/拒答”、`evidence_min_score` 的 immutable contract、单变量实验、Q1 发布门与测试知识退役顺序。
- K8-R3 已实现并完成 fixture calibration：migration `013`、OpenAPI/DTO、Runtime fail-closed override、execution evidence、Web 中文学习说明、eval score/decision/report archive 已闭环；固定 Qwen 的 must-answer score 最低 `.5237`、must-refuse 最高 `.5020`，`.51` 两次均 `5/5` 正确拒答且没有 must-answer 误拒。最新 candidate `30/40`、grounded `.6429`、retrieval/citation/refusal `1.0`，通过 `k7-llama3.2-prompt-v2-local-v1`；同日 lexical control gate 也为 `31/40`、grounded `.7357` 并通过。
- 剩余边界：上述 snapshot/collection 是七格式 eval fixture，不能作为业务 active release；未创建真实业务 dense snapshot、未执行 Q1 business approval/publish，未删除产品测试 document，也未移除 lexical。`.51` 不能复制给不同模型、index/chunk、reranker 或业务知识分布。
- K8-R4a 已实现并验证：migration `014`–`016`、Postgres-authoritative onboarding dataset/case/run/result、admin API、lease worker、固定 scope/config pair、逐题 evidence 和 `/knowledge` 中文工作台均已闭环。isolated Postgres lifecycle、scoped Runtime worker smoke、Go service/repository/httpapi/server 和 Web lint/typecheck/build 均通过。
- 本次最终回归：`make backend-test` 全通过；`ONBOARDING_TEST_DATABASE_URL=... go test ./internal/repository -run TestPostgresOnboardingDatasetLifecycle -count=1 -v` 与 `./internal/service -run TestPostgresOnboardingWorkerRunsFrozenScope -count=1 -v` 均通过；`make integration` 收集 `53` 项并以退出码 `0` 通过；OpenAPI 通过 YAML 解析。
- 实环境 schema：`make postgres-k7-smoke` 于 `2026-07-18` 通过；启动路径已将 migration `014`–`016` 应用于本地开发 Postgres，未导入、修改或删除任何知识内容。
- `K9-R1` 已验证：`user` / `knowledge_admin` / `system_admin` 的 Backend authority 与 Web 工作区已分离；知识库管理员上传/索引和运行 onboarding，系统管理员确认 revision publish 并从产品执行候选参数/Q1 发布。legacy `admin` session 兼容为系统管理员，K9-S0 legacy conversation subject 保持 `user_admin`。
- `K9-R2` 已验证：系统管理员可查看旧 document 的 retirement readiness；Backend/Postgres 在 transition transaction 内要求 old+new current snapshot passing run 才可 disable、new-only current snapshot passing run 才可 delete。知识库管理员不能访问 readiness/disable/delete；Web 只显示 Backend blocker，不能绕过。
- `U4` 已验证：Web build-time theme registry、`ThemeProvider` 和登录/工作台 picker 已落地。默认 `verdant-habitat` 生态主题以 CSS token 与非交互装饰实现，`quiet-paper` 证明可切换；只保存在浏览器 localStorage，不改 Agent/Backend。focused Playwright `2 passed`、Web build 与 integration `54 passed`。
- 本次验证：isolated Postgres retirement lifecycle、`make backend-test`、Web lint/typecheck/build、OpenAPI YAML parse、`make integration`（`54 passed`）均通过。
- 唯一下一动作：由知识库管理员上传真实业务知识并等待 ready；系统管理员确认发布包含旧+新的 successor snapshot；再由知识库管理员在 `/knowledge` 创建审核过的 onboarding truth、冻结并运行质量门。只有系统管理员在 readiness 显示 `ready_to_disable` 后才可停用旧测试 document；停用后需对 new-only snapshot 再跑 passing run，显示 `ready_to_delete` 才可删除。若发生验收失败或需要改变 retrieval/profile/index/boundary，则由系统管理员在 `/config` 创建候选并走 Q1；保持 lexical release 时不需要 Q1。禁止把模型生成内容当真值、先清空 active knowledge 或执行未经审批的 dense cutover/lexical retirement。
