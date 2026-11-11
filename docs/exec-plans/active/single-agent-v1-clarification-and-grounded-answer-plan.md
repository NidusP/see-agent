# A1 澄清、完整性与引用可信度交付计划

## 文档控制

- 状态：`active`
- 更新日期：`2026-08-03`
- Owner：`p`
- 权威设计：[澄清与有依据回答处置设计](../../design-docs/single-agent-v1-clarification-policy-design.md)
- 关联质量闭环：[质量运营与受控发布设计](../../design-docs/single-agent-v1-quality-operations-release-design.md)

## 目标

将“检索到了若干相近片段”与“已充分理解用户要问什么、能够完成请求”分开处理。优先解决四类高频质量风险：

1. 范围不明时强行总结、比较或凑数量。
2. 范围已明但证据不足时混入相近事项。
3. 只覆盖部分要求却声称给出完整答案。
4. 检索候选数被误呈现为回答引用数。

## 不变约束

- `C2` 的 Backend `IntentRouter` 只做高置信有限交流/开放世界快捷路由；未知和内部业务问题仍进入 `knowledge_query`，不扩张为通用相关性判定。
- `C3` 会话上下文解析在 retrieval 前处理有界追问、话题切换和受控确认；它的 `resolved_query` 是 A1 pending clarification query recovery 的基础，但不能替代 A1 的 scope/coverage 处置。
- K5 snapshot、K6 freshness、C1 anchor 与 Q1 受控发布仍是唯一证据/发布边界。
- 不直接修改 `prompt_v2` 或线上 active release；任何模型/prompt 策略进入候选 AgentConfig，并经固定数据集、报告和人工审批发布给新会话。
- 不回填、重写或重新解释历史消息和 citation。
- `model_id` 不等于实际生成 provider。任何质量结论必须记录 `answer_policy_version`、实际 `generation_provider` 和 `fallback_used=false`；generation provider 不可用时 fail-closed。

## 分阶段工作包

### A1-0：先收口当前引用展示语义

范围：Web 在存在 C1 anchors 的回答中，仅把 anchor 引用 rank 并集计为“已引用来源”，并过滤详情抽屉；无 anchor 的 legacy 消息维持现有消息级来源入口。

验收：`conv_047` 类回答正文只锚定 rank 2/3 时，入口显示 2 条已引用来源；不改 Runtime、Postgres 或历史 evidence。

退出条件：新增 Web component/E2E 覆盖锚点子集、无 anchor 兼容、source 加载失败和主题可读性。

#### 2026-07-30 执行计划

- 用户可见结果：回答正文只对 rank `2/3` 有证据锚点、而该消息总共有 3 个 retrieval citation 时，入口显示“查看 2 条已引用来源”，点击后抽屉只显示 rank `2/3`；不是把“检索候选”说成“回答引用”。
- 实现边界：只修改 `apps/web/app/_components/chat-workspace.tsx` 和其 Playwright 覆盖。复用 Backend 已持久化的 `citation_anchors`，经现有 rune 边界与非重叠校验后取 `citation_ranks` 去重并集；不新增 API 字段、不读取 execution evidence、不修改 `citations_count` 的历史含义，也不让浏览器推断新的 citation。
- 无锚点或锚点均无效时：维持现有“查看 N 条来源”入口和全量来源抽屉，保证 legacy 兼容。锚点重复 rank 必须只计一次；无效/重叠 anchor 继续按现有防御性规则降级，不能扩大显示范围。
- 交互：消息级入口将该 rank 并集传入已有 `citationAnchorFocus` 状态，复用抽屉已存在的按 rank 过滤逻辑；hover/focus 的单个 anchor、键盘和来源加载失败语义保持不变。
- 验收：新增一个 3 条 source / anchors 仅覆盖 `2/3` 的浏览器用例，验证入口计数、抽屉过滤、重复 rank 去重；保留无 anchor、单个 anchor、来源加载失败及窄屏抽屉回归。运行 `make web-lint`、`make typecheck`、目标 Playwright，随后在真实聊天页面检查一个带 anchor 的成功回答和一个无 anchor 的兼容回答。
- 非目标：不实施 A1-1 的数据集或 provider evidence，不新增回答处置、pending clarification、模型调用、契约或迁移；这些工作在 A1-0 验收后才开始。

#### 2026-07-30 交付证据

- Web 只从通过既有 rune/非重叠防御校验的 `citation_anchors` 取 rank 去重并集；有有效 anchor 时入口显示“查看 N 条已引用来源”，并把该并集送入已有 `citationAnchorFocus`，抽屉只显示这些 rank。无有效 anchor 仍显示“查看 N 条来源”与全量抽屉；未新增或推断 citation。
- `make web-lint`、`make typecheck` 与 `npm run ui-smoke -- e2e/chat-provenance-ui.spec.ts` 通过（`5 passed`）：覆盖 rank `2/3` 子集、重复 rank 去重、单 anchor、无 anchor 旧消息、来源加载失败和窄屏抽屉。
- 真实 `fullstack_dev` 浏览器：已有差旅报销期限回答显示“查看 1 条已引用来源”，抽屉只显示 rank `1` 的真实 snapshot/revision provenance；关闭 Runtime 后，业务提问显示“Runtime 暂不可用，本次消息没有被伪装成成功回复。”和“重试上一条”，没有完成回答或来源；随后恢复 Runtime，`make fullstack-smoke` 通过。
- 边界：仅变更 Web 展示与 Playwright 覆盖；未改 Runtime、Backend、Postgres、OpenAPI、execution evidence 或历史消息。

### A1-1：冻结行为真值与评测集

范围：在 `data/eval-datasets/` 新建版本化 clarification slice，并扩展 runner/report schema。每例标记 expected disposition、缺失范围维度、禁止断言、需要的 answer points 与 citation ranks。

开发种子集合先使用 32 条，验证 schema、runner 和失败分类：

| 类别 | 数量 | 关键断言 |
| --- | ---: | --- |
| 范围明确、可回答 | 10 | 不应无谓追问；结论有引用 |
| 事项/对象/版本/组织/排序不明 | 10 | 先问最小澄清；无业务事实 |
| 范围明确、证据不足 | 6 | 拒绝补全；不显示候选为引用 |
| 范围明确、仅部分覆盖 | 6 | 如实报告数量/覆盖缺口；已答事实有引用 |

验收：dataset schema validation、稳定 hash、mock runner 和 runtime-local report 都能记录 disposition / clarification / coverage；每个失败有主因分类（clarification policy、coverage honesty、citation mismatch 等）。

发布 gate 不能直接使用 32 条开发种子。发布前扩展到至少 80 条，其中“范围明确、不应澄清”不少于 40 条，才能有意义地约束 `≤5%` 的无谓澄清率。数据集必须包含成对样本：模糊问题、同一问题补齐范围、澄清后的短回复、用户放弃旧澄清并另起问题；同时覆盖短但明确的问题、事项/角色/版本/组织/排序缺失、跨 revision 冲突、错别字、提示注入和连续澄清上限。

每条多轮样本额外记录 original query、clarification question、follow-up、expected resolution (`resolved|abandoned|needs_more`)、expected resolved query、expected disposition、禁止事实和期望引用。确定性评分必须验证澄清回复无业务事实/citation、resolved turn 实际使用恢复后的 query，以及 parser/internal marker 不泄漏。

#### 2026-07-30 A1-1 实施边界与内容

- 唯一写入范围：`data/eval-datasets/clarification/v1-dev.jsonl`、`scripts/eval/run_baseline.py`、其定向测试、`data/eval-datasets/README.md` 与本计划/harness。离线 runner 是题库校验、派生评分和报告的唯一 writer；不得改 Runtime、Backend、Web、OpenAPI、Postgres、Qdrant、active AgentConfig 或 Q1 release。
- 数据真值：32 条全部以三份无敏感 local-dev 示例制度为依据，不声称是客户或生产制度。每条明确给出预期处置、范围维度、禁止业务事实、需回答要点、来源 file/locator 和开发 rank；多轮题的历史与当前 follow-up 分开保存，禁止让 `resolved_query` 覆盖原问题。
- 题目配额：`grounded_answer=10`、`clarification_required=10`、`insufficient_evidence=6`、`partial_answer=6`。澄清题至少覆盖事项、角色/对象、时间/版本、组织/地点和排序口径；多轮题至少覆盖 `resolved`、`abandoned`、`needs_more` 三种结局。
- 评分：每条只输出一个主失败原因。报告同时输出各处置命中率、范围维度命中、无谓澄清率、部分覆盖诚实性、citation rank/source 对齐和禁止业务事实命中；这些开发分数不构成发布 gate。
- provider 证据：逐题保存 Runtime 原始 execution observation 的 provider/model/fallback/generation/retrieval 事实；mock 明确标为合成且 `quality_eligible=false`，不允许将 mock/runtime-local 报告当成真实模型比较或 Q1 发布证据。
- 回滚：仅新增版本化数据与离线评分字段；删除该开发集或停止调用它不会改变任何用户对话、知识、模型、检索或发布状态。

#### 2026-07-30 A1-1 交付证据

- `data/eval-datasets/clarification/v1-dev.jsonl` 已冻结 `32` 条 A1 开发题：`grounded_answer=10`、`clarification_required=10`、`insufficient_evidence=6`、`partial_answer=6`。题目只引用三份 local-dev 示例制度，不构成真实组织制度或 Q1 发布集。
- runner 现在对 `answer_disposition` 强制校验范围维度、澄清缺失维度、禁止事实、显式 `used_citation_ranks`、部分覆盖、以及多轮 `resolved|abandoned|needs_more`。它从 Runtime response 读取这些字段，字段缺失或非法即评分失败，绝不从答案文本补造 rank、coverage 或 resolved query。
- 每条样本写入 `a1_evaluation` 和 `provider_evidence`；一条题只输出一个按优先级确定的主失败原因，同时保留观测到的禁止事实等诊断。mock 被固定标为合成执行与 `quality_eligible=false`；只有未来 `runtime-remote` 的完整、无 fallback observation 才可能成为 Q1 输入。
- 验证：`./.venv/bin/python -m pytest tests/integration/test_eval_a1_clarification.py tests/integration/test_eval_generation_config.py -q` 为 `29 passed`；mock runner 运行 `32/32`、`failed=0`，dataset SHA-256 为 `sha256:2c749029def9c6e77fecb00c7fdc7f941cd251b28f653b4da05e8421b527e01d`，不产生服务或数据写入。
- 边界复核：本切片未修改 Runtime、Backend、Web、OpenAPI、Postgres、Qdrant、active prompt/AgentConfig、execution evidence 写入或 Q1 release。下一步唯一动作是 A1-2，先冻结 pending clarification 和 answer-policy 的跨服务 API/持久化契约，再开始 Runtime policy 实现。

### A1-2：冻结跨服务契约与存储迁移

范围：先更新 OpenAPI、Runtime schema、Go domain、Web type、Postgres migration 和 K7 evidence read model；实现 `answer_disposition`、`answer_policy_version`、`clarification`、`clarification_resolution`、`coverage`、`used_citation_ranks` 与 provider observation 的不变量。冻结 retrieval candidate 与 answer citation 的分离持久化边界，并由 Backend/Postgres 维护 pending clarification 生命周期。

`2026-08-02` 实施状态：`A1-2` 已完成，但尚未启用 `A1-3` 的模型策略。共享 Runtime JSON/Pydantic、OpenAPI、Go domain、Postgres migration `024`、K7 evidence read model 和 Web transport type 已同步；Postgres 是 pending clarification 的唯一 writer，并通过 ID/state version 条件写入将状态转换与 message/evidence/citation/candidate 持久化为一个事务。普通用户 JSON/SSE 不透传 resolution、resolved query 或 retrieval candidates；管理员 evidence 可读取它们。没有修改 active prompt/quality release，也没有恢复 phrase router、heuristic fallback、伪回答或伪引用。

交付验证：Runtime 定向 contract `11 passed`、`make runtime-test` 为 `140 passed`；Backend `go test ./...` 通过；隔离 Postgres `TestPostgresA1ClarificationLifecycleAndEvidence` 覆盖创建、resolved、abandoned、两轮 `needs_more -> expired`、stale state rollback、candidate/evidence 和 policy config persistence；Web `pnpm typecheck`、`pnpm lint` 通过；四份 JSON schema/fixture 与 OpenAPI YAML parse 通过。

关键验收：

- Runtime/Backend 不允许 `clarification_required` 携带用户可见 citations/anchors 或 `knowledge_used=true`。
- `partial_answer` 必须有 coverage 字段，且其业务句子与 anchor rank 一致。
- 同一 conversation 最多一个 pending clarification；`pending → resolved|abandoned|expired` 在 message/evidence transaction 中强一致写入。`resolved_query` 不覆盖原始用户消息。
- `generation_performed`、`policy_evaluation_performed`、`answer_generation_performed`、`generation_provider` 与 `fallback_used` 不得互相矛盾。
- Runtime 返回 `used_citation_ranks`；Backend 将 retrieval candidates 保留在管理员 evidence，将 answer citations 作为用户来源。Web 不从文本相似度猜测。
- JSON/SSE、memory/Postgres 和 admin evidence 返回同一处置事实。
- 迁移向前兼容，历史消息保持 `legacy_unknown`/空值；仅可从无歧义旧 evidence 只读推导，不回填。

### A1-3：Runtime policy 与版本化 prompt 候选

范围：实现 pre-retrieval scope gate、post-retrieval evidence/coverage gate、受限输出解析与 Runtime/Backend 双重验证。创建 immutable `prompt_v8 + answer_policy_v1` candidate，不修改 active `prompt_v7`；将“最小澄清、不得猜测、部分覆盖诚实表达、每个事实需要 citation marker”写入版本化策略。

执行规则：pre-retrieval gate 先根据当前问题和 Backend 权威 pending context 判断明显缺失范围；范围已解析后生成 `resolved_query` 再检索；post-retrieval gate 处理跨事项/版本冲突与 coverage。模型内部标记必须在任何 delta 前剥离并通过 parser。解析失败返回 `answer_policy_invalid`，不得伪装为澄清；最多连续澄清两次，之后要求用户重述完整问题。

实现选择已冻结为两次结构化调用：scope policy 一次；只有 retrieval 有候选时再执行 answer policy/generation 一次。scope policy 的 provider schema 只收紧上下文已知的 null 字段，不能替代 parser 或 request-aware validation。policy/parser 版本随 AgentConfig、execution evidence 和 eval conditions 固化。

验收：Runtime JSON/SSE 单测、stream marker 边界、resolved query retrieval、独立新问题 abandon、clarification 上限、provider/fallback observation、Backend transaction rollback、真实连续会话和 C1/K5/K6 regression 全通过。

`2026-08-02` 设计冻结：只实现 immutable `prompt_v8 + answer_policy_v1` 候选，不修改 active release。对 C3 knowledge route，Runtime 先运行严格 scope-policy JSON，再在 immutable scoped retrieval 后运行受限 answer-policy/generation JSON；JSON、citation marker、coverage/candidate/anchor 与 pending resolution 任一不合法均在第一条 delta 前以 `answer_policy_invalid` fail-closed。C3 controlled reply 不运行 A1 policy，也不消费已有 pending clarification。execution evidence 追加 policy provider/model/call count，以区分 scope decision、post-retrieval policy 和实际知识答案生成。详细数据流、状态边界与非目标见设计文档第 4.8 节。

`2026-08-02` 实施与验收：Runtime 已实现上述 candidate，且 `ollama_native` scope call 使用同一 Pydantic 契约派生的 JSON Schema；无 pending 时 schema 强制 `pending_action=null`、`resolved_query=null`，parser 与 Backend 复核不放宽。`make runtime-test` 为 `154 passed`，Backend `go test ./...`、Web `pnpm typecheck`/`pnpm lint`、JSON/OpenAPI parse 与 fullstack readiness smoke 均通过。真实 `qwen3:14b` probe 在固定 `snap_004` scope 下先将不合法 JSON 以 `answer_policy_invalid` fail-closed，随后 JSON 与 SSE 成功返回 grounded answer、两个 validated anchors、`policy_call_count=2` 和 `fallback_used=false`。当前 active config 仍为 `cfg_001 / qwen3:14b / prompt_v7`；没有创建或发布 candidate AgentConfig。

`2026-08-03` 浏览器验收：Codex In-app Browser 登录普通用户后，业务问题成功显示 citation anchor、“查看 1 条已引用来源”与对应 `snap_004` provenance 抽屉。Runtime 停止时，页面显示明确失败状态、未完成消息和“重试上一条”，没有成功回答或来源；Runtime 恢复后，点击页面重试返回带引用的答案。该项验证当前 active `prompt_v7` 的用户可见路径；候选仍只以 Runtime JSON/SSE probe 验证，未被发布为 active config。

### A1-4：用户与管理员体验

范围：聊天卡片以“需要补充范围”“当前知识不足”“已覆盖部分”取代模糊的普通回答状态；澄清时无来源入口，部分回答只展示实际引用来源。管理员 evidence/质量反馈扩展 `should_clarify`、`unnecessary_clarification`、`incomplete_coverage`，并复用 `wrong_evidence`；这些枚举须同步 OpenAPI、Backend check/migration 和 Web。

`2026-08-03` 契约冻结：普通用户只读取 completed message 的 `answer_disposition`/`coverage` 和实际 answer citations；legacy message 保持兼容。`clarification_required` 与 `insufficient_evidence` 不显示来源、知识版本或核验入口，且 Web 不请求 source API；`partial_answer` 显示结构化 coverage 并只显示实际引用。管理员 evidence 分开显示 retrieval 是否发生、`knowledge_used`、最终处置、澄清/不足/覆盖事实与候选诊断。新 feedback 均为 append-only：`should_clarify` 仅针对非澄清的 A1 知识处置，`unnecessary_clarification` 仅针对澄清，`incomplete_coverage` 仅针对完整/部分有依据回答；三者要求 comment、禁止 citation rank。`wrong_evidence` 保持现有证据错误职责。Backend 是角色、target disposition、comment/rank 和 migration 约束的唯一权威。

实现顺序：先更新 OpenAPI/domain 与 Postgres `answer_feedback.kind` 的追加式约束，再让 service/repository 对新 kind 读取 immutable execution evidence 并拒绝不相容 target；随后接 Web message-card/source gating、管理员 evidence detail 与 feedback 控件过滤。不得修改历史消息、citation、provenance、candidate、prompt 或 active release。

验收：普通用户在同一会话补充“差旅报销”后得到有 citation 的回答；管理员可区分 retrieval 发生、知识被用于事实回答、以及澄清/拒答原因；不可通过 URL 或前端状态绕过 Backend 权威。

追加验收：Backend/数据库正反例覆盖新 kind 与原有 `wrong_evidence`；SSE completed 与刷新 detail 的 A1 卡片一致；浏览器覆盖澄清、知识不足、部分覆盖、真实来源和来源失败/窄屏抽屉，且 active `prompt_v7` 不被伪装为未发布 `prompt_v8`。

`2026-08-03` 实施与验收：`A1-4` 已完成。OpenAPI、Go domain 和 Postgres migration `025` 追加三个 feedback kind；Backend 从 immutable execution evidence 读取 `intent`/`answer_disposition`，对新 kind 强制 completed knowledge target、匹配处置、非空 comment 与空 citation ranks，原有 `wrong_evidence` 语义不变。普通用户卡片对 `clarification_required`、`insufficient_evidence`、`partial_answer` 分别显示“需要补充范围”“当前知识不足”“已覆盖部分”；前两者不显示也不请求来源、知识版本或核验，部分回答显示 N/M coverage 且只保留真实引用。管理员 evidence 同时显示 retrieval、知识是否用于事实回答、policy/generation 执行、处置事实和候选诊断；feedback 选择器只提供与目标处置相容的新 kind。

验证记录：`go test ./...` 通过；真实 Postgres `TestPostgresQualityOperationsReleaseLifecycle` 覆盖 migration `025`、允许/拒绝新 feedback kind，且通过；OpenAPI YAML parse、`pnpm typecheck`、`pnpm lint`、`pnpm build` 全部通过。定向 Playwright `a1-answer-disposition-ui`、`q1-quality-operations-ui`、`chat-provenance-ui`、`bounded-conversation-intent-ui` 为 `8 passed`，覆盖三种 disposition、source API gating、coverage、管理员语义反馈、来源失败、legacy anchor 与窄屏抽屉。fullstack readiness smoke 通过。真实普通用户浏览器在 Runtime 恢复后新建会话询问“差旅报销提交期限是什么？”，先见 SSE 生成态，后得到“10 个自然日内”答案与 anchor；来源抽屉展示 `snap_004`、文档、revision、段落和原文片段。此前 Runtime 停止时的失败卡片和页面重试路径仍作为 active `prompt_v7` 的失败路径证据；候选 `prompt_v8 + answer_policy_v1` 继续未发布。

### A1-5：Q1 实验、发布与持续治理

范围：运行当前 active `prompt_v7` 与未发布候选 `prompt_v8 + answer_policy_v1` 的同条件实验，冻结 snapshot、dataset hash、retrieval profile、实际 generation provider、fallback 状态和 grader；将真实质量反馈经过审核后按 Q1 进入后续 dataset revision。除 prompt/policy 对照外，再以同一条件比较当前 3.2B 生成模型与至少一个更强的本地 instruct candidate，不能把 embedding 模型对比当作生成模型结论。

`2026-08-03` A1-5-1 题库冻结设计：先创建独立的 `clarification/v1-q1.jsonl`，不修改 32 条开发集，也不运行或发布候选。该发布集固定为 80 条，至少 40 条范围充分的 `grounded_answer`，其余覆盖最小澄清、明确证据不足和部分覆盖。每题用 `boundary_tags` 标出精确阈值、相邻阈值、前置条件、例外、否定条件、顺序、时钟/工作日、角色分离、范围缺失、多轮恢复/放弃/继续、证据缺口、计数部分覆盖、revision scope、提示注入或歧义；多轮成对样本用 `evaluation_pair_id` 绑定。题目只引用三份无敏感 local-dev 制度，禁止臆造历史版本、未写明审批人、处罚、额度或例外。

执行顺序：`(1)` 在 runner 中将上述 tags/pair 作为仅评测元数据校验；`(2)` 写入 80 条带 source/locator、最小 answer points 与反向断言的冻结题，并用测试验证总数、处置配额、标签覆盖、配对完整性与 hash 稳定；`(3)` 运行 mock 报告验证 schema/评分器，但将其固定为 `quality_eligible=false`；`(4)` 先按 [A1-Q1 remote evaluation design](../../design-docs/single-agent-v1-a1-q1-remote-evaluation-design.md) 冻结 conditions/runner/Runtime/Backend report guardrail，再创建未发布 candidate config、冻结 `snap_004` scope 和 candidate gate，才启动两个 `runtime-remote` run。题库阶段不改 Runtime、Backend、Web、知识或 active release。

题库验收：精确 `2000/10000/800` 金额和 `3` 天、`5` 工作日、`1` 工作日、`2` 小时、`30` 分钟时间值均有“恰好/相邻”题；计划/突发、批准/未批准、超标/非自动报销、审批提交/系统执行、已批准业务理由/未指定审批人等条件均至少有正反例。每一项应澄清题只要求会改变结论的最小缺失条件；每一项无证据题明确范围但不允许把相邻制度当答案；每一项部分覆盖题实际支持数严格小于用户要求数。

`2026-08-03` A1-5-1 实施与验收：已写入 `data/eval-datasets/clarification/v1-q1.jsonl`，固定 SHA-256 为 `sha256:5aef680a679e79afe087f969cdd1bc02814231064d70ce98317aa97ceae11373`。构成为 `42 grounded_answer / 16 clarification_required / 12 insufficient_evidence / 10 partial_answer`，有 `8` 个完整多轮 pair（`16` 条样本），包含 resolved、abandoned 和 needs_more。runner 新增 `boundary_tags`/`evaluation_pair_id` 的类型、空值、重复和未知 tag 校验，并在报告输出 tag 计数与配对样本数；这两个字段只在 harness 内使用。定向题库与原 A1 测试 `9 passed`，全量 `tests/integration/test_eval_*.py` 为 `51 passed`；mock 运行 `80/80`、`failed=0`，且 provider evidence 为 `quality_eligible=false`。没有运行真实模型、创建 Q1 experiment、修改 Runtime/Backend/Web、知识 snapshot 或 active release。

候选发布门槛：

- 所有冻结的“范围不明”关键样本在业务事实前进入 `clarification_required`；不得发生已知模糊问题的强行回答。
- clarification slice 的处置正确率不低于 `90%`，范围明确样本的无谓澄清率不高于 `5%`；样本量扩展前同时人工复核所有失败项。
- `partial_answer` 样本的覆盖诚实性为 `100%`；`grounded_answer`/`partial_answer` 的 citation/anchor 契约与既有质量门均不退化。
- 冻结集不得出现高严重度 unsupported business claim；报告分别给出 answer-point recall 与 unsupported claim rate，不能把遗漏和幻觉合并为一个 grounded 总分。
- 澄清维度准确率、resolved-turn 回答成功率与 resolved-query 检索命中率分别达到冻结 gate；重复澄清、policy parser error 和内部 marker 泄漏均为 `0`。
- 质量 run 报告必须证明实际 provider 且 `fallback_used=false`；历史 fallback 或缺失 provider evidence 的报告只能作为 smoke，不能 approve/publish。
- 记录 policy/answer 两阶段 p50/p95 与首片延迟；若新增调用造成不可接受回退，必须在单阶段候选、规则触发或模型选择中重新权衡，而不是放宽正确性门槛。
- 既有 prompt_v2 strict gate、知识演进 fixture、C2-R1、onboarding 和浏览器回归通过；候选只经 Q1 approval/publish 影响新会话。

## 验收与节奏

推荐顺序为 `A1-0 → A1-1 → A1-2 → A1-3 → A1-4 → A1-5`。`A1-0` 是低风险的体验纠偏，可以独立先做；`A1-1` 是所有 Prompt/Runtime 变更的前置门槛；`A1-2` 与 `A1-3` 必须在 API/迁移评审后顺序执行，避免 Web 先猜状态。

每个工作包完成时均需记录：变更的 config/dataset hash、运行服务版本、自动化和真实会话证据、失败归因、未解决风险，以及下一步唯一动作。任何质量报告只说明候选在冻结条件下的表现，不能替代发布审批。

## 当前状态与下一动作

- 当前状态：`A1-0` 至 `A1-4` 已验证，`A1-5-1` 的 80 条冻结题库、评测元数据和 mock 合同验证已完成；A1 v3 candidate 的 Runtime、Backend、评测 runner 与版本配对校验已实现，但线上 active config 仍未启用 scope/coverage policy。`2026-07-23` 已先收口 D0 Runtime provider 边界：删除产品 `HeuristicModelClient`/`CompositeModelClient`，模型缺失或失败时 fail-closed；不修改 active quality release。
- 已观察证据：`conv_047` 绑定 `cfg_004 / llama3.2:latest / prompt_v2 / temperature=0.2`，但当时 Runtime 进程未配置 `LOCAL_LLM_BASE_URL`，因此旧实现使用 `HeuristicModelClient` fallback。该历史回答不是 llama3.2 的直接质量样本；新实现不再允许同类隐式替代回答。
- 模型证据：本机 `llama3.2:latest` 为 `3.2B / Q4_K_M / 2.0 GB`，项目 `local-model-smoke` 在 `fallback=false` 下通过。`2026-07-22` 重新显式设置 Ollama provider、`LOCAL_LLM_ALLOW_FALLBACK=false` 后运行 40 题 prompt_v2 gate：报告 `runtime-local-20260722T131133349524Z` 为 `29/40`、grounded `0.6286`、citation/retrieval `0.9714`、refusal `1.0`、平均 `1211 ms`，通过现有最低回归门但仍有 `11` 个 answer-point miss、其中 `6` 个同时为 retrieval miss。它足以做开发 smoke/基础 RAG，但不能证明适合复杂澄清、跨文档综合和严格完整性判断；后续 `1.63 ms` 报告是 heuristic 特征，不能覆盖真实模型结论。
- `2026-08-04` A1-5-2 执行结果：guardrail 与两题预检入口均已实现并复验。Postgres 仍指向 `qrel_bootstrap / cfg_001 / rcfg_001`；唯一未发布候选为 `cfg_002 / prompt_v8 / answer_policy_v1`。冻结文件 `clarification/v1-q1-remote-conditions.json` 的 canonical hash 为 `sha256:1e87db98f498601482e2a438e0f2b2e825ed8b0dba2c476075cc1bbbfccec8e3`，固定 `snap_004`、`rcfg_001` 和 `v1-q1`。真实 Runtime 两题预检报告 `runtime-remote-20260804T123022285090Z` 仅在本地保存为 `pre_q1_probe`，不能登记为 Q1 证据：`a1_q1_010` 的阈值/例外有依据回答通过；`a1_q1_069`（制度未覆盖且带提示注入）被实际 policy 错判为 `clarification_required`，而冻结期望为 scoped retrieval 后的 `insufficient_evidence`。结果 `1/2`，failure type 为 `answer_policy_protocol_failure`；没有 QualityExperiment、Backend eval report、approve、publish 或 active release 变更。
- `2026-08-04` A1-5-3 实施与停止：`prompt_v9 + answer_policy_v2 + scope-policy-v2`、Backend/Runtime 双重配对校验、v2 conditions/gate 和脱敏 strict-observation summary 均已实现。Backend/Runtime 全量、eval `74` 项、Web lint/typecheck、JSON/OpenAPI parse 和 restarted fullstack readiness 均通过；`cfg_003` 仅经管理员 API 创建为 immutable candidate。API 与 Postgres 双读仍为 `qrel_bootstrap / cfg_001 / rcfg_001`。第 A 组真实报告 `runtime-remote-20260804T131824593762Z`（conditions SHA-256 `sha256:b265956085c7c708359b48760adb898aacdad9e10ae357fab40e847eab620176`）为 `1/2`：普通的 `a1_q1_062` 检索后 `insufficient_evidence` 通过，但注入式 `a1_q1_069` 仍 pre-retrieval `clarification_required`。失败报告保存无 assistant 正文的 policy/retrieval/generation 摘要；它不是 Q1 evidence。
- `2026-08-04` A1-5-4 设计冻结并完成实现：已在 [scope-assessment-v3 correction design](../../design-docs/single-agent-v1-a1-policy-v3-scope-assessment-design.md) 冻结并实现 `prompt_v10 + answer_policy_v3 + scope-policy-v3`。它要求模型对 action 同时给出内部 `retrieval_target` 与 `unresolved_branch_dimensions`；Runtime 只验证结构自洽，不能用关键词或业务短语推翻模型动作。预检扩大为 `069/062`、`060/067`、`043/044`、`045/059` 四组八题，分别保护注入/无证据和真实 policy branch 缺失。Runtime 全量测试为 `190 passed`；Backend 全量 Go tests、stack smoke、Web 定向 UI tests、JSON/OpenAPI 校验均已复验通过。最近补齐了测试 provider 的 C3 结构化路由响应和 Backend 对 `knowledge_snapshot_unavailable` 的错误保留；这些只修复测试/错误记录边界，不恢复生产 phrase router、heuristic fallback、模板回答或伪引用。
- 当前状态补充：自动化回归和 fullstack readiness 已通过。Web 配置 API 集成测试在共享开发数据库中创建了测试配置 `cfg_004`（`qwen-local-dev-config-smoke / prompt_v1`）和测试检索配置 `rcfg_002`；active release 仍是 `qrel_bootstrap / cfg_001 / rcfg_001`。该测试记录保留，不删除、不覆盖，也不作为 A1 质量候选。
- `2026-08-06` A1-5-4 实际验收与停止：候选为 `cfg_005 / qwen3:14b / prompt_v10 / answer_policy_v3`，条件哈希为 `sha256:840a6a8014b3db4cae066b4b707f5c9b29edf9f7cbb62cfa6bf8757fe6f14968`。A 组首次报告 `runtime-remote-20260806T015132827116Z` 为 `1/2`，`a1_q1_069` 为 provider timeout；Runtime 清理后复核报告 `runtime-remote-20260806T015705619222Z` 仍为 `1/2`，同一注入式无证据题稳定产生 pre-retrieval `clarification_required`，普通等价题 `a1_q1_062` 正确为 scoped retrieval 后的 `insufficient_evidence`。因此 `cfg_005` 停止，B-D、完整 80 条报告、Q1 experiment、approval、publish 和候选浏览器验收均未执行；active release 仍为 `cfg_001 / rcfg_001`。两份报告均为 `pre_q1_probe`、`quality_eligible=false` 的失败证据。
- `2026-08-06` A1-5-5 实施与停止：`prompt_v11 + answer_policy_v4`、`scope_shape` parser/validator、八键严格 JSON Schema、Backend/Runtime/API 配对和 `cfg_006` 已完成。v4 第一份 A 组报告 `runtime-remote-20260806T022008867670Z` 为 `0/2`，模型省略 scope 字段并被 `answer_policy_invalid` 拒绝；收紧八键必填后独立复核报告 `runtime-remote-20260806T022857625011Z` 为 `1/2`：`a1_q1_062` scoped retrieval 后 `insufficient_evidence` 通过，`a1_q1_069` scope stage 超时。两份均为 `pre_q1_probe`、`quality_eligible=false`，不运行 B-D、Q1 experiment、approval、publish 或候选浏览器验收；active 仍为 `cfg_001 / rcfg_001`。
- 当前唯一下一动作：按 [A1-5-6 compact scope design](../../design-docs/single-agent-v1-a1-policy-v5-compact-scope-design.md) 新增 `prompt_v12 + answer_policy_v5`，只缩短 scope prompt 并提供完整八键示例；保留 v4 parser 语义与严格 fail-closed。自动化验证通过后创建新候选 `cfg_007`，只跑 A 组 `069/062`。禁止修改 v4 evidence、恢复 phrase router、关键词分类、heuristic fallback、模板回答或伪引用；不得修改、删除或发布 `cfg_002/cfg_003/cfg_005/cfg_006`。
- `2026-08-06` A1-5-6 实施与停止：`prompt_v12 + answer_policy_v5`、v4 严格 scope parser 复用、跨服务配对、v5 conditions/gate 和 `cfg_007` 已完成。候选 A 组报告 `runtime-remote-20260806T024138747995Z` 为 `0/2`，`a1_q1_069` 与 `a1_q1_062` 均为 `model_generation_timeout`，没有 Runtime strict execution observation。该报告为 `pre_q1_probe`、`quality_eligible=false`；不运行 B-D、Q1 experiment、approval、publish 或浏览器候选验收。active 仍为 `cfg_001 / rcfg_001`。下一步不再添加 A1 prompt candidate，先单独处理本地 Qwen provider 的稳定性与延迟证据；禁止用延长超时、重复失败样本、手写 JSON、测试 provider、phrase router 或 heuristic fallback 伪造通过。
- `2026-08-06` provider stability probe 设计冻结：新增 [本地模型服务稳定性探针设计](../../harness/single-agent-v1-provider-stability-probe-design.md)。探针只检查真实模型服务的可访问性、连续短文本生成、结构化 JSON 生成、首事件延迟和完整返回延迟；不调用 RAG、不运行 A1 题库、不写产品数据、不创建实验或发布配置。探针报告固定为 `evaluation_purpose=provider_stability_probe`、`quality_eligible=false`、`fallback_used=false`，任一真实调用失败即停止并保留失败证据。
- `2026-08-06` provider stability probe 实施与真实验收：新增 `make provider-stability-probe`、脱敏 JSON 报告和 MockTransport 自动化测试。真实报告 `provider-stability-20260806T030918424901Z` 使用 `ollama_native / qwen3:14b / timeout=90s`，三次短文本、三次结构化 JSON 与一次 SSE 均成功，`fallback_used=false`、`quality_eligible=false`。首条短文本为 57.874 秒，后两条为 2.738/2.499 秒；JSON 为 8.059/5.010/5.532 秒；SSE 首事件/完整返回为 1.830/2.797 秒。该结果证明 provider 当时可用，但第一条调用接近 Backend 默认 60 秒 deadline，不能解释为候选质量通过，也不能据此延长超时或重跑已停止的 `cfg_007`。下一动作保持为：在不创建新 A1 prompt candidate 的前提下，先冻结“长上下文 structured scope 调用”与 Backend deadline 的独立运行证据契约，区分模型冷启动、长 prompt 计算与跨服务 deadline；随后再决定是否需要基础设施资源调整或新的模型候选。
- `2026-08-06` provider stability slice 验收：Runtime `203 passed`，Backend `go test ./...` 通过，Web lint/typecheck 通过；合同版本断言、测试 Runtime 环境隔离、探针测试和浏览器包装测试已修正。完整 `make integration` 最终为 `111 passed`，包含 Web production build、跨服务 API/SSE、来源展示和浏览器引用成功路径。未执行 fullstack production readiness：当前终端缺少 `SEE_AGENT_RUNTIME_SERVICE_TOKEN`（Runtime 与 Backend 间的私有服务认证密钥），不能伪造该密钥；该环境项仍是后续真实全栈验收前置条件。active release 仍未变化。
