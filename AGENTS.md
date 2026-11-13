# see-agent

本仓库目前是一个文档优先的 AI 开发工作区，已经包含 monorepo 骨架、协作脚手架、最小 Web/Backend/Runtime 实现和跨服务 smoke 测试。

## 目标

使用“规格优先、设计文档驱动、评测驱动”的工作流，构建一个私有部署的单智能体 Web 问答产品。

## 工作规则

1. `AGENTS.md` 是导航地图，不是完整规格说明。
2. `docs/` 是产品意图、设计意图、评测意图与执行计划的系统记录。
3. 产品变更必须优先更新 `docs/product-specs/`。
4. 架构或 API 变更必须在实现前先更新 `docs/design-docs/`。
5. 模型、检索或提示词质量相关工作，必须在实验开始前先更新 `docs/harness/`。
6. 交付范围、构建顺序与验收门槛统一维护在 `docs/exec-plans/`。
7. 本仓库将 RAG 视为首选知识策略。微调是可选项，且要等到评测 harness 证明检索基线已经稳定后再考虑。
8. 面向用户的每一次进度、总结、计划、验收、问题解释或结果汇报，都必须解释其中的内部编号、缩写、版本号和技术术语：说明它是什么、用于什么、当前状态以及对用户的影响。不得只报类似 `A1`、`cfg_001`、`prompt_v7` 的简写来代替说明。项目文档、代码和执行记录可以使用简写，但汇报时必须同步给出清晰含义；后续重复汇报也必须保留解释，不能只依赖前文或“首次出现时解释”规则。

## 仓库地图

- `docs/runbooks/local-stack-bootstrap.md`
  - 本地真实全栈的依赖、模型、凭据、一键启动顺序和排障指南。
- `docs/product-specs/single-agent-v1-spec.md`
  - 首个单智能体版本的产品需求与行为契约。
- `docs/design-docs/single-agent-v1-sdd.md`
  - 系统设计文档，包含接口、数据边界、运行流和部署形态。
- `docs/design-docs/single-agent-v1-ingestion-chat-boundary-guide.md`
  - 面向学习与排障的端到端导读：管理员摄取、用户提问、服务职责、权威边界、失败处理和 lexical/dense 区别。
- `docs/design-docs/single-agent-v1-knowledge-evolution-design.md`
  - 知识 revision、snapshot、更新日历、回答时效和核验的权威架构设计。
- `docs/design-docs/single-agent-v1-knowledge-evolution-contract.md`
  - `K1` 冻结的领域对象、状态机、跨服务字段和 API 兼容契约；`K2-K6` 必须遵守。
- `docs/design-docs/single-agent-v1-knowledge-persistence-design.md`
  - `K2` 的 migration、权威表、发布事务、legacy reconciliation 和回滚设计。
- `docs/design-docs/single-agent-v1-k4-lifecycle-api-design.md`
  - `K4` 管理员 document/revision lifecycle 的 API、写入路径、idempotency 与 disable/delete 状态语义；K4 Backend/Web 必须遵守。
- `docs/design-docs/single-agent-v1-optional-prepublish-onboarding-design.md`
  - `K8-R4b` 可选的发布前知识验收：待发布快照、可直接发布、验收为推荐项；停用/删除安全门不变。已实现，交互以该设计为准。
- `docs/design-docs/single-agent-v1-k5-snapshot-provenance-design.md`
  - `K5` 每消息 snapshot scope、Runtime revision filter、citation provenance 与 legacy fallback 边界；K5 Backend/Runtime/Web 必须遵守。
- `docs/design-docs/single-agent-v1-citation-anchors-design.md`
  - `C1` 回答内 evidence anchor 的跨服务契约、marker 解析、事务持久化和 Web 交互；所有 citation anchor 改动必须遵守。
- `docs/design-docs/single-agent-v1-clarification-policy-design.md`
  - `A1` 知识问题的 scope/coverage 处置、pending clarification、resolved query、provider evidence 与引用分层设计。
- `docs/design-docs/single-agent-v1-a1-q1-remote-evaluation-design.md`
  - `A1-5-2` 的 remote baseline/candidate 条件冻结、执行证据、Q1 report 准入与人工审批边界；相关 runner、Runtime 或 Backend 改动必须遵守。
- `docs/design-docs/single-agent-v1-a1-policy-v2-correction-design.md`
  - `A1-5-3` 因 `cfg_002` 真实 pre-Q1 probe 失败而冻结的 `prompt_v9 + answer_policy_v2 + scope-policy-v2` 纠偏边界、候选配对、预检矩阵与不可发布约束。
- `docs/design-docs/single-agent-v1-a1-policy-v3-scope-assessment-design.md`
  - `A1-5-4` 因 `cfg_003` 注入边界预检仍失败而冻结的 `prompt_v10 + answer_policy_v3 + scope-assessment-v3` 内部 JSON 合同、八题预检矩阵与不可发布约束。
- `docs/design-docs/single-agent-v1-a1-policy-v4-scope-shape-design.md`
  - `A1-5-5` 因 `cfg_005` 复核仍把注入式无证据题判为澄清而冻结的 `prompt_v11 + answer_policy_v4 + scope-shape-v4` 合同、shape 到动作映射、八题预检矩阵与不可发布约束。
- `docs/design-docs/single-agent-v1-a1-policy-v5-compact-scope-design.md`
  - `A1-5-6` 因 `cfg_006` 注入边界 scope 超时而冻结的 `prompt_v12 + answer_policy_v5` 紧凑 JSON 输出纠偏；只允许新候选 `cfg_007`。
- `docs/design-docs/single-agent-v1-fail-closed-execution-design.md`
  - `F0` 生产 fail-closed 的 execution attempt、错误状态、服务认证、profile/readiness 与历史兼容设计；F1-F8 必须遵守。
- `docs/design-docs/single-agent-v1-k6-freshness-revalidation-design.md`
  - `K6` update calendar、可恢复 freshness scan、独立 historical revalidation result 与 Runtime verdict；K6 Backend/Runtime/Web 必须遵守。
- `docs/design-docs/single-agent-v1-workspace-layout.md`
  - 多技术栈 monorepo 目录结构与 agent 调度约定。
- `docs/harness/single-agent-v1-harness.md`
  - 评测 harness、数据集策略、评分规则、实验循环和模型对比协议。
- `docs/harness/single-agent-v1-provider-stability-probe-design.md`
  - 本地模型服务稳定性探针、延迟记录、失败边界和 A1 预检前置条件。
- `docs/exec-plans/active/single-agent-v1-plan.md`
  - 为期 `4-6` 周的执行计划，包含里程碑、负责人和退出标准。
- `docs/exec-plans/active/single-agent-v1-product-delivery-plan.md`
  - 当前产品交付事实源，定义可内部试用的终极目标、剩余缺口与总验收门槛。
- `docs/exec-plans/active/single-agent-v1-next-dev-sequence.md`
  - 当前唯一可执行的知识演进任务队列，定义 `K1-K9` 的依赖、写入边界、验收和下一动作。
- `docs/exec-plans/active/single-agent-v1-k3-k6-delivery-plan.md`
  - 当前 `K3-K6` 的实现分工、跨服务交接、故障语义和阶段验收计划；`p` 用它调度与验收 worker 交付。
- `docs/exec-plans/active/single-agent-v1-chat-first-shell-delivery-plan.md`
  - 当前 `U3` 聊天优先壳层、角色工作区切换和 C1 引用详情抽屉的范围与验收记录。
- `docs/exec-plans/active/single-agent-v1-chat-recovery-experience-plan.md`
  - 当前开发栈的 active snapshot 恢复与流式对话体验收口计划；用于处理空知识快照、K3 delivery 和聊天即时反馈。
- `docs/exec-plans/active/single-agent-v1-citation-anchors-delivery-plan.md`
  - `C1` 回答内 evidence anchor 的实现状态、跨服务写入边界和验收证据。
- `docs/exec-plans/active/single-agent-v1-clarification-and-grounded-answer-plan.md`
  - `A1` 澄清、部分覆盖、引用可信度和真实生成模型对比的当前交付队列与门槛。
- `docs/exec-plans/active/single-agent-v1-readiness-checklist.md`
  - 编码前冻结决策、实现前门槛与启动检查清单。
- `docs/exec-plans/active/single-agent-v1-one-click-dev-start-plan.md`
  - 根级 `make dev` 一键全栈启动、失败回收、日志和验收记录。
- `docs/references/openai-source-notes.md`
  - 用于推导本套文档的 OpenAI 官方资料映射说明。

## 预期工作流

1. 先在规格文档中确认产品范围。
2. 再在 SDD 中确认系统边界与接口。
3. 在 harness 文档中定义评测数据集、评分器和质量门槛。
4. 按照当前有效的执行计划推进。
5. 只有完成以上步骤后，才开始或扩展代码和基础设施。

## 当前状态

- 阶段：最小可运行骨架后的架构与联调收口
- 产品形态：支持登录、普通用户聊天、知识库管理员知识管理与系统管理员参数/发布管理的单智能体 Web 问答
- 运行方向：先做 RAG 基线，在 harness 稳定后再做模型对比
- 代码初始化：Web 登录、owner-filtered chat 与越权直达恢复、聊天 provenance、回答内 evidence anchor、C2 有界会话标签、知识 lifecycle/update calendar/revalidation、K7 管理证据查询与配置工作台、Q1 受控实验/审批发布与 forward rollback、K9-R1 `user/knowledge_admin/system_admin` 角色门禁、K9-R2 系统管理员两段式旧知识退役门禁，Runtime 原生 Chat SSE、受控非知识回复、snapshot-scoped retrieval、citation marker parser、K8-R1 原生 dense 与 K8-R2a 受控策略注册，Go Backend 保守 IntentRouter、Postgres authoritative K2-K7/C1/C2/K9-S0/Q1 write path、K3 Redis Streams，以及浏览器/integration smoke 均已建立
- 主要缺口：`K9-S0` conversation ownership、`K8-R1` native dense、`K8-R2a` 策略配置/evidence 与 `Q1` 质量运营闭环均已验证；`K8-R3` 已完成 fixed Qwen fixture 的 must-refuse calibration，但真实业务 dense cutover 仍待 onboarding/Q1。后续还有 sparse/hybrid/rerank，以及 `K9` service security、可靠性、部署恢复与内部试用。
- 本地启动：根目录 `make dev` 已提供一键全栈开发入口；它会检查工具链、Ollama 模型、Runtime service token、Docker 依赖和子项目依赖，按 Runtime、Backend、Web 顺序启动并等待 readiness，失败时停止后续阶段并显示日志尾部。

## 继续任务指南

新会话进入本仓库后，默认按以下顺序继续：

1. 先阅读 `README.md`
   - 需要启动本地服务时，参阅 `docs/runbooks/local-stack-bootstrap.md`。
2. 再阅读 `docs/exec-plans/active/single-agent-v1-product-delivery-plan.md`
3. 再阅读 `docs/design-docs/single-agent-v1-knowledge-evolution-design.md`
4. 为理解服务协作，再阅读 `docs/design-docs/single-agent-v1-ingestion-chat-boundary-guide.md`
5. 再阅读 `docs/exec-plans/active/single-agent-v1-next-dev-sequence.md`
6. 如果要并行认领任务，再阅读并更新 `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
7. 根据要接手的模块，阅读对应设计文档和目录下的局部 `AGENTS.md`
8. 历史路线图、旧工作流与归档交接文档仅用于追溯，不能覆盖当前交付计划和知识演进执行队列。

### 当前推荐优先级

如果没有用户额外指定方向，优先按以下顺序推进：

1. `D0-D4/K2-K7/C1/C2/U3/K9-S0/K8-R1/K8-R2a` 已验证；固定 Qwen K8-R2 条件并先收口 dense must-refuse，再考虑 sparse/hybrid/rerank
2. 保持 K3-K7/C1 历史、prompt version 与 provenance contract 不变；任何 K8 变更继续重跑 prompt_v2 gate 和知识演进 fixture
3. 任何 Runtime、RAG、Prompt 或模型改动必须重跑 gate 和知识演进 fixture；Chat 错误态、重试与中止首片已有实现，后续以浏览器失败路径验收为准

### 选择工作包的规则

- 一次只领取一个工作包
- 优先选择 `docs/exec-plans/active/single-agent-v1-next-dev-sequence.md` 中依赖已满足的 `K1` 到 `K9`；`P1-P10` 是历史工作流，只用于追溯
- 并行领取前，先更新 `docs/exec-plans/active/single-agent-v1-parallel-claims.md` 标记 owner、状态、写入范围和下一步动作
- 如果工作会改变 API、数据对象或状态机，先更新 `docs/` 再改代码
- 如果工作涉及检索质量、提示词或模型行为，先更新 `docs/harness/` 再做实验

### 模块入口

- 想做前端：
  - 先读 `docs/design-docs/single-agent-v1-web-ui-design.md`
  - 再读 `apps/web/AGENTS.md`
- 想做后端：
  - 先读 `docs/design-docs/single-agent-v1-backend-design.md`
  - 再读 `services/backend-go/AGENTS.md`
- 想做 AI Runtime：
  - 先读 `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
  - 再读 `services/ai-runtime-py/AGENTS.md`
- 想做测试与评测：
  - 先读 `docs/harness/single-agent-v1-integration-test-plan.md`
  - 再读 `docs/harness/single-agent-v1-eval-baseline-plan.md`

### 接手时的最低检查

开始编码前至少确认：

- 根级 `Makefile` 命令是否可用
- `packages/contracts/` 是否覆盖本次改动涉及的字段
- 相关模块设计文档是否已存在且足够支撑实现
- 改动完成后要同步更新对应 `README.md`、`AGENTS.md` 和执行文档
