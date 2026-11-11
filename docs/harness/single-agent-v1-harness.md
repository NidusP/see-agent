# 单智能体 V1 Harness

## 文档控制

- 状态：用于实现启动的草案
- 版本：`v1.3`
- 目的：在开始代码级优化之前，先定义评测与实验循环

## 1. 为什么需要这份文档

这个项目不依赖纯直觉式迭代。凡是涉及检索、提示词和模型选择的质量工作，都必须通过一个可重复的 harness 来推进，并具备：

- 有代表性的任务
- 固定数据集
- 明确评分标准
- 可记录的实验变体

## 2. Harness 范围

该 harness 覆盖：

- 检索质量
- 有依据回答的质量
- 引用质量
- 证据不足时的拒答行为
- 请求范围不足时的澄清行为，以及“范围明确但证据只覆盖部分要求”的诚实表达
- 在基线稳定后进行模型间对比

该 harness 初期不覆盖：

- 面向公网安全策略的基准测试
- 微调指标
- 多轮自治式工具调用

## 3. 核心原则

Harness 是这个单智能体项目的控制系统。它决定一次变更究竟是改进、回退还是噪声。

## 4. 评测生命周期

1. 定义任务与失败分类。
2. 从真实问题模式构建种子数据集。
3. 冻结基线检索与 prompt 配置。
4. 对每次变更运行离线评测。
5. 结合 trace 检查失败。
6. 只提升那些在度量上确实改进的变更。

## 5. 任务定义

初始评测套件应包含：

- 来自单一来源的事实查找
- 同一文件内多个 chunk 的答案综合
- 跨文件答案综合
- 政策或流程说明
- 含糊表述或同义词检索
- 基于 `xlsx` 的表格查找与跨 sheet 综合
- 基于 `pptx` 的 slide 内容提炼与政策提取
- 基于 `pdf` 的页码定位与表格解析问答
- 证据不足问题
- 跨多个业务事项、角色或版本的宽泛总结请求（应澄清）
- 范围明确但要求数量/比较范围超过证据覆盖的请求（应部分回答并说明缺口）
- OCR 回退触发时的页面文本问答

## 6. 数据集设计

### 6.1 种子数据集规模

起步建议：

- 首轮使用 `30-50` 个高价值问题
- 当基线功能跑通后扩展到 `100+`

### 6.2 每条样本字段

每个评测样本应存储：

- `question_id`
- `question`
- `task_type`
- `expected_source_files`
- `expected_source_locators`
- 在已知时记录 `expected_chunks`
- `must_answer` 或 `must_refuse`
- `scoring_notes`

对于澄清与覆盖边界样本，额外记录：

- `expected_disposition`：`grounded_answer`、`clarification_required`、`insufficient_evidence` 或 `partial_answer`
- `required_scope_dimensions`：至少要由问题或既有上下文给出的事项、角色、时间、组织实体、排序标准等维度
- `expected_missing_dimensions`：当应澄清时必须指出的最小缺失维度
- `forbidden_answer_points`：澄清时不得臆造或提前断言的业务事实
- `conversation_turns`：多轮样本的 original query、assistant clarification 与用户 follow-up
- `expected_clarification_resolution`：`resolved`、`abandoned` 或 `needs_more`
- `expected_resolved_query`：补齐范围后实际用于 retrieval 的问题；不得覆盖原始用户文本
- `expected_used_citation_ranks`：最终答案实际使用的 citation，而不是全部 retrieval candidates

### 6.3 A1-1 开发集冻结契约

`data/eval-datasets/clarification/v1-dev.jsonl` 是 A1 的开发评测集。它只用于验证题目字段、评分器和候选策略的方向，**不是** Q1 发布门槛，也不得据此发布 prompt、模型或检索配置。发布前必须另建至少 `80` 条的冻结集。

该文件的 32 条题目只引用仓库中的无敏感本地示例制度：`差旅与费用管理制度.md`、`请假与远程办公制度.md`、`信息安全与访问管理规范.md`。题目描述的是这些示例文件的文本事实，不代表真实组织制度。

除通用字段外，每条 A1 样本使用 `task_type=answer_disposition`，并必须包含：

- `expected_disposition`：`grounded_answer`、`clarification_required`、`insufficient_evidence` 或 `partial_answer`。
- `required_scope_dimensions`：要得到业务结论必须明确的范围维度。
- `expected_missing_dimensions`：仅在应澄清时填写；它是最小缺失维度，不是泛泛的“请说具体”。
- `forbidden_answer_points`：澄清或证据不足时不得输出的业务事实；评分器检查这些文本不进入回答。
- `expected_used_citation_ranks`：开发 mock 中的稳定 answer citation rank；普通回答与部分回答必须非空，澄清与证据不足必须为空。真实运行同时按 source file/locator 复核，不能只凭 rank 判定正确。
- `expected_coverage`：仅在 `partial_answer` 填写 `{requested_count, supported_count, uncovered_description}`，并要求 `0 < supported_count < requested_count`。
- 多轮样本还使用既有 `conversation_turns` 保存当前提问之前的历史，当前用户 follow-up 放在 `question`；`expected_clarification_resolution` 只允许 `resolved`、`abandoned`、`needs_more`，而 `expected_resolved_query` 只允许 `resolved`。

32 条题目按 `10 + 10 + 6 + 6` 划分：

| ID 范围 | 预期处置 | 内容范围 |
| --- | --- | --- |
| `a1_dev_001`–`010` | `grounded_answer` | 报销期限/逾期要求/审批层级/住宿例外、请假期限与紧急请假、安全事件上报与权限调整；其中包含已补齐范围的 follow-up。 |
| `a1_dev_011`–`020` | `clarification_required` | 事项、角色、时间版本、组织地点或排序口径缺失；包含同一澄清后仍未补齐的 `needs_more`，以及用户另起安全事件问题的 `abandoned`。 |
| `a1_dev_021`–`026` | `insufficient_evidence` | 年假天数、无发票例外、远程办公天数、生产数据导出审批人、安全事件处罚和特定岗位福利等示例制度未覆盖的问题。 |
| `a1_dev_027`–`032` | `partial_answer` | 用户明确要求的数量或覆盖面超过示例制度能直接支持的部分；必须报告本次可验证数和未覆盖要求，不能把 `top_k` 当成知识库总数。 |

离线 runner 是此文件格式、派生评分和报告的唯一 writer；它不写入 Runtime、Backend、Postgres、Qdrant 或 Q1。Runtime 返回的实际字段在 A1-2 才会成为 API/持久化契约；A1-1 只能把缺失、非法或不匹配字段报告为质量失败，不能在 runner 中补造它们。

### 6.4 A1-1 评分与 provider 证据

每条 A1 样本都产出一个 `a1_evaluation`：期望/观察处置、缺失维度、覆盖、已使用 citation rank、禁止业务事实命中和澄清 resolution。它只读取 Runtime response，不根据回答文本猜测 `used_citation_ranks`、coverage 或 resolved query。

每条样本同时记录 `provider_evidence`：`fallback_used`、generation/retrieval 是否实际执行、provider、model id 和观察完整性。汇总报告列出 provider 集合、完整观察数、fallback 数和 `quality_eligible`。`mock` 只记录 `quality_eligible=false` 的合成事实；`runtime-local` 也只是开发证据；只有 `runtime-remote` 的真实、完整、`fallback_used=false` 观察才可在未来作为 Q1 候选输入。

失败只给一个主因，按以下优先顺序分类：运行/协议错误、处置错误、澄清维度或 resolution 错误、禁止业务事实、coverage honesty、citation mismatch、证据不足错误、answer point miss。这样同一条题不会被多个错误标签淹没，修复方向也保持明确。

对于 C3 会话路由样本，额外记录：

- `task_type=router_context_resolution`
- `runtime_intent=context_routing_required`
- `conversation_turns`：Backend 会发送给 Runtime 的有界历史，必须包含不可变 `message_id`
- `expected_context_resolution`：期望的 `intent`、`route`、`kind`，以及可选 `basis_message_ids`、`resolved_query_contains`
- `expected_router_policy_version=conversation_router_v1`：Q1/report 条件绑定的 Backend code-owned router policy；它来自 execution evidence/report 条件，不由 Runtime 模型输出

### 6.4.1 A1-3 候选 policy 实验约束

`prompt_v8 + answer_policy_v1` 是未发布候选。每次实验必须固定同一 dataset hash、knowledge scope、retrieval profile、generation model、scope-policy model 和 response configuration；报告单列 `policy_provider`、`policy_model_id`、`policy_call_count`、generation provider/model、fallback 与各阶段延迟。不得把 C3 router 调用、scope policy 调用或 answer generation 混为一个“LLM 已执行”字段。

开发阶段至少运行以下确定性 fixture，才可进入真实模型 probe：

- pre-retrieval clarification：无 retrieval/generation，只有一次 performed policy，零业务事实、citation、anchor 和 used rank。
- no-pending standalone retrieve：独立且范围充分的问题必须返回 `action=retrieve`、`clarification=null`、`pending_action=null`、`resolved_query=null`；不得把当前问题重述为 resolved query。
- pending resolved：scope policy 生成受控 query，retrieval 实际使用该 query，最终 resolution 与 request ID/state version 一致。
- pending abandoned 与 needs_more：独立问题关闭旧 pending；未补齐范围更新同一 pending；两次 needs_more 后由 Backend 写 expired。
- post-retrieval clarification：retrieval candidate 只出现在 admin evidence，不能进入用户 citation/anchor；policy call count 为 2，answer generation 为 not_run。
- partial answer：coverage 严格小于 requested count，正文每个业务结论只有 candidate/used rank 对应的 anchor。
- malformed scope/answer JSON、泄漏 JSON/marker、未知 rank、缺 provider/model、fallback 或空正文：JSON 和 SSE 均为 `answer_policy_invalid`，不产生 delta/completed 成功消息。

真实模型 probe 仍不是 Q1 release evidence；只有 A1-5 在至少 80 条冻结发布集上记录相同条件、人工复核失败样本并经 Q1 approval 后，候选才可影响新 conversation。

### 6.4.2 A1-5 Q1 冻结发布集契约

`data/eval-datasets/clarification/v1-q1.jsonl` 是 A1-5/Q1 的冻结发布集。它与 `v1-dev.jsonl` 分离：开发集可以用于调整题目和评分器；发布集一经记录 SHA-256、开始 baseline/candidate run 或附着到 QualityExperiment，便不得原地修改。发现题目错误时必须创建新版本文件和新的实验条件，旧报告继续指向旧 hash。

发布集固定为 `80` 条 `answer_disposition` 样本，其中范围充分、不应澄清的 `grounded_answer` 不少于 `40` 条；其余样本同时覆盖 `clarification_required`、`insufficient_evidence` 与 `partial_answer`。每条发布样本必须追加：

- `boundary_tags`：受控的边界维度。允许值为 `threshold_exact`、`threshold_adjacent`、`precondition`、`exception`、`negative_condition`、`sequence`、`time_clock`、`time_business_day`、`role_separation`、`scope_missing`、`scope_paired_resolution`、`topic_abandonment`、`needs_more`、`evidence_absent`、`count_partial`、`revision_scope`、`prompt_injection`、`ambiguity`。标签不是模型输入，也不参与用户回答；它只用于冻结构成、报告切片和人工复核。
- `evaluation_pair_id`：仅当样本是“范围不清 → 用户补齐 / 仍不充分 / 改问新主题”的配对链路时填写。相同 pair 至少有两个样本，并且不得把 follow-up 的 `resolved_query` 反写为原问题。

构成约束：发布集至少有 `8` 条精确阈值、`8` 条阈值相邻值、`18` 条前置条件、`12` 条例外、`8` 条否定条件、`10` 条多轮范围恢复/放弃/继续澄清、`8` 条证据缺口、`8` 条部分覆盖；三份示例制度都必须出现在有依据题中。阈值题必须同时覆盖 `2000/10000/800` 与 `3` 天、`5` 工作日、`1` 工作日、`2` 小时、`30` 分钟等边界，不能只用明显大于或小于的数字。例外题必须区分“计划”与“突发”、被批准与未批准、已批准业务理由与未知审批人、事实条件与制度未覆盖的结果。

每条 `expected_answer_points` 只写原文直接支持的最小主张，并用 `forbidden_answer_points` 约束最容易被错误推出的反面结论，例如“正好 10,000 元需要财务负责人”“超标住宿自动可报销”“没有审批也可远程办公”“示例制度规定了处罚”。不得以常识、相邻规则、候选片段数、假设历史版本或注入指令补全制度。

发布集的 mock 验证只证明数据和评分器的自洽，标记 `quality_eligible=false`。它不生成发布结论；冻结后仍需对 active `prompt_v7` 和候选 `prompt_v8 + answer_policy_v1` 在相同 scope/conditions 上运行 `runtime-remote`，并人工复核每个失败样本。`prompt_v7` 是 comparison baseline，不因缺失 A1 policy facts 被伪装为 candidate quality eligible；只有候选能满足完整 A1 execution contract 并通过 candidate gate。

### 6.5 数据集来源

优先使用：

- 真实用户问题
- 管理员整理的政策与流程问题
- 历史支持问题
- 测试过程中发现的边界案例

## 7. 失败分类体系

每个失败样本都应归类为一个主因：

- parse failure
- chunking failure
- retrieval miss
- rerank miss
- prompt assembly issue
- generation hallucination
- citation mismatch
- refusal policy failure
- clarification policy failure
- coverage honesty failure
- clarification resolution failure
- answer policy protocol failure
- generation provider mismatch

这个分类体系是强制的。没有它，团队就无法判断下一步该改解析、检索、提示词还是模型。

## 8. 指标

### 8.1 检索指标

- Recall@K
- 预期来源文件命中率
- 预期支持 chunk 命中率

### 8.2 回答指标

- grounded correctness
- 基于现有证据的完整性
- refusal correctness
- 回答格式合规性
- clarification correctness：应澄清样本是否在断言业务事实前提出最小、可执行的澄清问题
- unnecessary clarification rate：范围已足够的样本被无谓追问的比例
- coverage honesty：部分覆盖时是否如实说明已覆盖和未覆盖部分，而非凑数或过度概括
- unsupported claim rate：回答中无法映射到本次 answer citation/anchor 的业务事实声明比例；与 answer-point miss 分开统计，分别表示“无依据新增”和“应该回答却遗漏”
- claim-level grounded precision：逐项事实是否由对应 citation 内容直接支持，不能用“存在任意一个引用”替代整段 groundedness
- clarification target accuracy：是否询问真正会改变答案的最小缺失维度，而不是泛化地要求“请说具体一点”
- clarification resolution success：用户补齐范围后是否形成正确 `resolved_query`、停止重复澄清并给出应有回答
- clarification abandonment correctness：用户另起新问题时是否放弃旧 pending clarification，而非错误拼接

### 8.3 引用指标

- 是否存在引用
- 引用准确性
- 引用相关性
- evidence-anchor coverage：有 citation 的 normal answer 中，至少一个结论带有效 `citation_anchors` 的比例
- evidence-anchor fidelity：每个锚点只引用本次 response 的 citation rank，且锚点 rune 范围能够无损定位回最终回答正文

### 8.4 运行指标

- 回答时延
- 检索时延
- scope policy、answer generation 与首个可见 token 的 p50/p95 时延
- policy parser error、内部 marker 泄漏、连续澄清次数和未解决 clarification 比例
- actual generation provider、fallback used 与 answer policy version
- 索引成功率

### 8.5 澄清评测规模与发布门槛

- `32` 条 clarification slice 只作为开发种子，用于 schema、runner 和失败分类，不单独承担 `≤5%` 无谓澄清率的发布结论。
- 发布 gate 至少包含 `80` 条冻结样本，其中范围明确、不应澄清的样本不少于 `40` 条；模糊/补齐问题应采用配对设计，避免数据集只测试单轮措辞。
- 发布集必须覆盖：短但明确的问题、事项/角色/时间/组织/排序缺失、跨 revision/文档冲突、明确但无证据、部分覆盖、错别字、提示注入、用户另起问题和连续澄清上限。
- 所有 `clarification_required` 样本确定性检查：无业务事实、无用户可见 citation/anchor、`knowledge_used=false`、内部 marker 不可见。
- 所有 resolved-turn 样本检查 `resolved_query`、检索命中、answer disposition、coverage 与实际 answer citations；只检查最终措辞不足以证明澄清链路正确。
- `grounded_answer`/`partial_answer` 必须把业务事实拆成 claim 检查：无 anchor 的事实、anchor 内容不支持的事实和跨 citation 拼接出的新结论均计入 unsupported claim；遗漏 expected point 单独计入 completeness，二者不得合并为“grounded 分数”。
- C3 router slice 使用 `data/eval-datasets/fullstack/c3-r2-router-quality.jsonl` 作为开发 fixture；报告必须单列 `router_resolution_accuracy`，并把 router 失败归为 `router_quality_mismatch`，不得混入 retrieval/refusal 分母。

### 8.6 真实模型身份门槛

- 质量 run 报告必须记录 `model_id`、`generation_provider`、`fallback_used=false`、`answer_policy_version` 和各阶段时延。
- 只写 `model_id=llama3.2:latest` 不能证明模型被调用；Runtime 没有 generation provider 时该样本必须记为 runtime error，不能产出可读替代答案。
- 缺失 provider observation、历史报告发生过 fallback，或平均时延/输出形态与声明 provider 明显矛盾时，runner 必须将报告标记为不可用于 Q1 approval，而不是继续用同一个 gate 判为模型质量通过。

### 8.7 全栈远程发布评测

`runtime-local` 仅用于进程内回归，不是 fullstack/production 发布证据。任何要进入 Q1 approval 的本地全栈报告必须使用 `runtime-remote`：runner 通过 Runtime 的真实 HTTP endpoint、携带 service bearer token，并对每条 response 校验 `execution.policy/retrieval/generation` 与 `fallback_used=false`。candidate 的 `grounded_answer`/`partial_answer` 必须实际 policy、scoped retrieval 和 generation；`clarification_required` 必须实际 policy 且可以是 pre-retrieval（retrieval/generation 均 `not_run`）或 post-retrieval（retrieval `performed`、generation `not_run`）；`insufficient_evidence` 必须实际 policy 和 scoped retrieval、generation `not_run`。所有实际调用均须有非 test provider/model。缺少 scope、认证失败、空 provider/model、`test_*` provider、fallback、非法处置矩阵或任何 HTTP 失败都会使 candidate 样本成为 runtime error，并使 gate 失败。

远程 run 必须显式传入冻结 `knowledge_scope` 文件（snapshot、manifest、revision allow-list）和 `a1_q1_remote_conditions_v1` 条件文件。runner 只读取这些文件，不创建 fixture corpus、不向 Qdrant 写入样例内容；报告同时保存 canonical conditions SHA-256、scope、请求条件、实际 execution observation、数据集 SHA-256、配置版本和 gate。Runtime 在 retrieval `performed` 时必须回报实际 strategy、embedding model、collection、candidate count、`top_k`、`rerank_enabled` 与 `evidence_min_score`；runner 对冻结条件 exact-match。Q1 baseline/candidate 必须使用相同 scope、dataset hash、generation/retrieval conditions，只有 prompt/policy 可以不同；不能用本地 TestClient、fixture scope 或手工 JSON 报告替代。
- 生成模型比较与 embedding 模型比较分开。当前 3.2B 生成模型必须在相同 snapshot、retrieval、prompt/policy、dataset 和 grader 下与至少一个更强的本地 instruct candidate 对比；不得用检索提升替代生成/澄清能力结论。
- 生成模型发布比较至少同时报告 disposition accuracy、answer-point recall、unsupported claim rate、claim-level grounded precision、citation/anchor fidelity、拒答正确率和延迟；没有 claim-level 指标时，不得下结论称某模型“幻觉更少”。

### 8.7 F0 真实性与失败处置门槛

质量 harness 不能把基础设施故障评分为“合理拒答”“澄清成功”或模型质量。每个 run 先验证 strict execution facts，再进入回答评分：

- `grounded_answer` / `partial_answer`：必须有 `fallback_used=false`、实际 provider observation、完整 request scope、实际使用 citation ranks 和可验证 anchors；任一缺失记为 `runtime_contract_violation`，不进入 grounded 分母。
- `clarification_required`：可以没有 retrieval/generation，但必须是合法 policy 结果，且无业务事实、citation/anchor；不得用 Runtime 超时、缺 provider 或解析错误伪装澄清。
- `insufficient_evidence`：必须证明 scoped retrieval 已执行；候选不可被显示或评分为引用。
- `controlled_response`：必须记录 retrieval/generation 均 `not_run`；固定问候/开放世界拒答只验证产品文案与不调用知识，不计为模型回答。
- Runtime/数据库/Qdrant/对象存储/Redis/认证/写回失败、无 scope、空 answer、非法 rank/hash/anchor、零 chunk 摄取均单列为 execution failure。任何产生 completed message、citation、ready revision 或可审批质量报告的情况都是 P0 blocker。
- 失败路径用显式 test provider 与受控 fault injection 验证；测试报告必须标记 `test_provider=true`，不能作为 Q1 quality approval 依据。
- 若真实模型遗漏正常回答必需的 citation marker，Runtime 必须以 execution failure 拒绝；不得从全部 retrieval candidates、相邻句子、字符串相似度或 Web 展示层补造 citation/anchor。任何拟新增的 prompt version 先做非 Q1 `runtime-remote` probe：至少一条 must-answer 要有实际 anchor/used rank，至少一条 must-refuse 要在 scoped retrieval 后以零 citation/anchor 的 `insufficient_evidence` 返回。A1-Q1 使用冻结 conditions 的两题预检时，runner 必须以恰好两个 `--probe-question-id` 选择已校验冻结集的既有记录，并在本地 report 写入 `evaluation_purpose: pre_q1_probe` 与 eligibility=false；预检不得携带 gate 或 QualityExperiment report registration 参数，也不能作为 Q1 approval evidence。

`A1-5-3` 的 `cfg_003` 候选必须分两次运行四题 pre-Q1 probe，不能把一次通过的 evidence-absent 题推广为 scope policy 质量：第一组固定 `a1_q1_069/a1_q1_062`，分别覆盖注入式与普通的明确审批人但无证据；第二组固定 `a1_q1_060/a1_q1_043`，区分例外条件无证据与真正范围缺失。前三题必须 scoped retrieval 后 `insufficient_evidence`，最后一题必须 policy-only `clarification_required`；新 conditions 固定引用只适用于 `prompt_v9` 的 immutable `a1-q1-candidate-v2` gate，但 probe 继续禁止加载该 gate。所有报告继续为 local `pre_q1_probe`、eligibility=false，任一题失败即不得创建 Q1 experiment 或完整报告。

`A1-5-4` 不能将 v2 的 `1/2` failure 视为“多写一句 prompt”即可修复。`cfg_004` 的 v3 scope-policy output 必须附带内部 `retrieval_target` 与 `unresolved_branch_dimensions` self-assessment；Runtime 只验证 action、clarification 与该字段的结构一致，不按词语改写模型 action。真实 candidate pre-Q1 固定四组八题：`069/062`、`060/067` 必须 retrieval 后 `insufficient_evidence`；`043/044/045` 必须 policy-only clarification；`059` 必须 retrieval 后 `insufficient_evidence`。任一失败、fallback、结构错误、伪引用或 provider failure 均停止候选；所有报告仍是 local `pre_q1_probe`、eligibility=false，不能创建 experiment/report/approval/publish。

## 9. 评分策略

第一版 harness 应采用混合评分模型：

- 能确定的地方用确定性检查
- grounded correctness 用 rubric 评分
- 每轮对少量失败样本做人审

### 9.1 确定性检查

适用场景：

- 是否包含来源
- 在 must-refuse 样本上是否正确拒答
- 输出 schema 或格式约束是否满足

### 9.2 Rubric 评分

适用场景：

- 是否与证据事实一致
- 是否遗漏关键约束
- 是否超出证据范围过度断言

## 10. 实验矩阵

第一批受控实验一次只变化一个维度：

1. chunk size
2. overlap
3. dense-only 与 hybrid retrieval 的对比
4. reranker 开或关
5. top_k 与 rerank_top_n
6. prompt 变体
7. 模型后端

不要在检索、reranking 和 prompt 模板同时变化时做模型对比。

## 11. 基线发布门槛

在比较 `Qwen` 和 `DeepSeek` 之前，基线必须满足：

- 摄取链路端到端可用
- 检索命中率足够稳定，能够支撑可信比较
- 比较运行前，prompt 与引用策略已冻结
- 问题集同时包含可回答样本和必须拒答样本
- `txt`、`md`、`json`、`pdf`、`docx`、`xlsx`、`pptx` 每种格式至少有一个摄取样例和一个可回答评测样本

## 12. 模型对比协议

### 12.1 目的

在相同产品条件下比较 `Qwen` 与 `DeepSeek`。

### 12.2 固定变量

保持不变：

- 知识语料
- parser version
- chunking rules
- embedding model
- reranker model
- retrieval parameters
- prompt template
- grading rubric

### 12.3 变量

唯一变化项：

- 回答生成模型后端

### 12.4 对比输出

采集：

- grounded correctness
- citation accuracy
- refusal quality
- latency
- operator preference notes

### 12.5 当前本机候选与资源边界

- 环境：`24GB` 内存、约 `700GB` 可用磁盘；模型 artifact 可共存，但 Runtime 一次只加载一个 generation model。
- Qwen3 artifact 大小来自 Ollama 当前量化 tag；“建议内存”是为模型运行时开销、KV cache、Ollama、Postgres、Qdrant、Redis 与 Web/Backend/Runtime 保留余量的部署判断，不是模型官方最低要求。任何“收益”都必须经本 harness 的同条件 Q1 证实，不能由参数量直接推定。

| Qwen generation model | Ollama artifact 约值 | 建议运行内存 | 预期收益 | 在本项目的决策 |
| --- | ---: | ---: | --- | --- |
| `qwen3:4b` | `2.5GB` | `12GB+` | 延迟低、成本低，适合 UI/SSE/错误路径 smoke | 不适合主 Q1：与当前 3.2B 相比缺少足够容量来稳定遵守多条件、citation/disposition 协议；只能作开发回归模型。 |
| `qwen3:8b` | `5.2GB` | `16GB+` | 比 4B 有更好的中文理解、总结和一般指令遵循，适合低成本内部原型 | 可作低并发候选或快速开发基线，但本次已因 3.2B 协议不稳定而升级模型；14B 是更有意义的首轮质量门选择。 |
| `qwen3:14b` | `9.3GB` | `24GB+` | 预期提高复杂规则的默认/例外完整性、引用协议遵循与跨项目通用文本能力，同时保留系统与上下文余量 | **主候选**：先完成 provider smoke、non-Q1 probe 与新 Q1 baseline/candidate。只有全部 gate 通过才可发布。 |
| `qwen3:30b` | `19GB` | `48GB+`（`64GB+` 更适合长上下文或并发） | 相比 14B 更有机会改善复杂多条件推理、跨段综合和长文档回答完整性 | 不在当前 24GB 机器常驻：会挤压 KV cache 与全栈服务，导致延迟/稳定性风险；可作为未来升级硬件后的生产增强候选。 |
| `qwen3:32b` | `20GB` | `48GB+`（`64GB+` 更适合长上下文或并发） | 与 30B 档位类似，进一步提高复杂任务余量 | 同样不适合当前机器；不可因为磁盘有余量而忽略运行内存。 |
| `qwen3:235b` | `142GB` | `256GB+` 或多 GPU | 最大的本地 Qwen3 能力档 | 超出本机和常规单机私有部署范围，不进入本项目路线。 |

- Qwen3 是 hybrid-thinking 系列。用于用户可见的 RAG 回答时，模型接入与 Q1 必须固定 non-thinking/instruct 行为，避免思考内容、延迟和协议 marker 混入最终正文。
- 对照候选：Ollama `deepseek-r1:14b`（DeepSeek-R1-Distill-Qwen-14B，约 `9.0GB`）。它是 reasoning-oriented 对照，必须在完全相同的 snapshot、dataset、retrieval、prompt/policy、sampling 与 grader 下运行；不得将思考内容、延迟或未解析协议标记混入用户答案。
- 决策顺序：先验证 Qwen 是否通过完整 quality gate；只有 Qwen 通过或有明确失败分类后，才下载/运行 DeepSeek 对照。任何模型下载、启用或 Q1 release 都必须产生独立不可变配置与报告。

## 13. 微调决策门槛

只有在以下条件全部满足时，才开始微调：

- 检索召回已达到可接受水平
- reranking 已稳定
- prompt 变体已经测试
- 剩余缺陷主要集中在风格、格式或领域措辞

如果主导错误类型仍然是 retrieval miss，那么微调就不是正确的下一步动作。

## 14. 复审节奏

- 每次重要检索变更后
- 每次 prompt 模板变更后
- 每次生产模型切换前
- 每次考虑微调前

## 15. Harness 交付物

该 harness 最终必须产出：

- 一个种子数据集
- 一份文件格式覆盖矩阵
- 一个可重复运行的 eval runner
- 一套 grader rubric
- 一份失败日志
- 一份基线报告
- 一份模型对比报告

## 16. V1 退出标准

当满足以下条件时，harness 视为 V1-ready：

- 至少存在一份基线报告
- 至少存在一次受控模型对比
- 失败类型可以追溯到具体子系统
- 团队可以基于可量化证据说明生产模型选择理由

## 17. 知识演进评测

知识更新能力必须在固定 fixture 上独立评测，不能只复用静态问答集。第一批 fixture 至少覆盖：

- 新 revision 发布后，新问题只使用新的 published snapshot。
- 旧回答的 citation revision 被替换、禁用或删除后，系统标记为 `possibly_stale`，但不篡改历史文本。
- 核验请求以 latest snapshot 生成独立结果，并明确引用新 revision。
- indexing 失败、重复投递、乱序回包、future effective date 和向量清理失败都不得改变 active snapshot。
- disable/delete 后，Runtime 即使仍可见旧 Qdrant point 也不能从 snapshot scope 外返回 citation。

报告必须记录 snapshot id、manifest hash、citation revision、revalidation trigger 与 freshness 结果。任何检索、Prompt 或模型变更除重跑 R5 gate 外，也必须通过该知识演进 fixture。

## 18. K7 配置闭环复验记录

- `2026-07-15` 使用 `llama3.2:latest`、`temperature=0.2`、`max_output_tokens=1024`、fallback disabled 连续运行两次严格 R5。
- 两次结果一致：`26/40`，retrieval recall `0.9714`、citation accuracy `0.9714`、refusal correctness `1.0`、unexpected runtime error `0`；grounded correctness `0.5429` 低于 `0.55` 门槛。
- 最新 run 为 `runtime-local-20260715T084340Z`，报告位于 `data/eval-datasets/reports/runtime/latest.json` 与 `latest.md`。
- K7 新增参数未出现在该 eval payload 的实验变量中，因此“由某个新增参数直接导致退化”目前只是未经证实的假设。下一步必须从 failure report 做样本级归因；不得降低 gate 或覆盖旧通过报告来宣称通过。

K7 随后保留 `prompt_v1` 并新增 `prompt_v2`，让 eval payload/report/gate 显式记录 system prompt version。v2 在固定模型、temperature、max output、dataset、retrieval 与 grader 下连续两次通过：

- `30/40`，grounded `0.6714`；
- `28/40`，grounded `0.5857`；
- 两次 retrieval/citation `0.9714`、refusal `1.0`、unexpected runtime error `0`。

当前 K7 gate 为 `data/eval-datasets/baseline/k7-llama3.2-prompt-v2-quality-gate.json`，最新报告为 `data/eval-datasets/reports/runtime/prompt-v2/latest.json`。旧 R3 gate 与 prompt_v1 报告保留用于历史比较。

## 19. C2 有界会话意图评测

C2 不能只以“打招呼是否有回复”作为验收；固定 fixture 必须同时保护知识优先边界和 K5/K7 证据真实性：

- `你好`、`谢谢`、`再见`、`你能做什么`：命中有限会话意图，不执行 retrieval/generation，不附带 snapshot、citation 或 anchor。
- `你好，报销多久提交`、`谢谢，请问年假如何申请`：混合输入必须回退 `knowledge_query`，按 K5 snapshot scope 检索并生成回答。
- `讲个笑话`：返回范围提示，`refusal=true`，不伪造知识来源。
- 未识别、模糊或包含业务实体的输入：默认 `knowledge_query`，禁止分类器扩大闲聊范围。
- 没有 active snapshot 时：有限会话意图仍可完成；知识问题继续保持 `knowledge_snapshot_unavailable` 失败语义。
- 管理员 evidence 必须能逐条核对 `intent`、`retrieval_performed`、`generation_performed`、`knowledge_used` 与实际 citation/snapshot 一致。

Runtime、路由词表或 Prompt 发生变化时，除原有 R5 gate 和知识演进 fixture 外，还必须重跑以上 C2 fixture。允许增加高置信度短语，但不得用扩大正则或生成模型分类破坏“混合/未知输入默认 RAG”的保守边界。

## 20. 来源对齐

本 harness 遵循 OpenAI 公开强调的“评测优先迭代、数据集策展、trace 检查与可度量提升标准”。详见 `docs/references/openai-source-notes.md`。

## 21. K8-R1 Native Dense Retrieval 实验协议

K8-R1 只比较 lexical baseline 与 dense retrieval implementation；dataset、parser/chunk/index version、snapshot scope、prompt_v2、generation model、grader、`top_k` 和 rerank 开关必须冻结。报告额外记录：

- `retrieval_mode`、embedding provider/base URL 标识、embedding model、dimension、Qdrant collection；
- retrieval config version、snapshot id/manifest、revision ids；
- dense query latency、provider embedding latency、Qdrant query latency；
- failure 分类中的 `embedding_provider_failure`、`collection_schema_mismatch`、`native_query_failure`、`scope_filter_failure`。

R1 的功能门槛不是主观“回答更像人”，而是：真实向量写入与原生 filtered query 已发生；跨 scope revision 永不进入 citation；provider/collection/config 不兼容时 fail-closed；同一固定条件下重跑原有 R5 prompt_v2 gate 与知识演进/C1/C2 fixture。只有这些前提稳定后，才允许在 R2 比较模型或在后续 K8 增加 sparse/hybrid/provider rerank。

`2026-07-16` R1 已验证：Ollama `all-minilm:latest` 的实际 output 为 `384` 维，真实 provider + Docker Qdrant smoke 通过 ingest/reload/revision-scoped citation provenance。最终 lexical R5 gate 为 `30/40`、grounded `0.6571`、retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`。R2 必须在保持这些冻结条件的前提下新增且只新增第二个 embedding model。

## 22. K8-R2 第二 embedding model 受控比较冻结

状态：`frozen_before_execution`（`2026-07-17`）。本实验只比较 native dense 的 embedding 模型，不与已有 Qwen/DeepSeek 生成模型比较混为同一报告。

| 条件 | 已冻结值 |
| --- | --- |
| 对照 / 实验模型 | Ollama artifact `all-minilm:latest` / `qwen3-embedding:0.6b`；Runtime request 的 `EMBEDDING_MODEL` 分别为 `all-minilm` / `qwen3-embedding:0.6b`。 |
| 检索 profile | `RUNTIME_RETRIEVAL_STRATEGY=native_dense_v1`；每次只启动一个 exact-match Runtime profile。 |
| 语料、数据集与 scope | `data/eval-datasets/baseline/v1-seed.jsonl`（SHA-256 `38047c0958a78fb4035fe1d654859509ae7362e17c420f7c4e8f01c04e9f99ea`）的全部 40 条；相同七格式 fixture/revision 集合。开始评分前，必须把同一 immutable revision 清单及其 manifest hash 写入每个 run 的 `knowledge_scope`，不得使用仅名为 `snapshot_active` 的占位值。 |
| 生成与评分 | `llama3.2:latest`、`prompt_v2`、temperature `0.2`、max output `1024`、既有 grader/rubric 与 must-refuse 样本不变。 |
| 检索参数 | `top_k=3`、`rerank_enabled=false`；不引入 sparse、hybrid、provider reranker、chunking 或 prompt 变体。 |
| Qdrant / provider | 同一本机 Ollama OpenAI-compatible endpoint；每个模型使用独立临时 collection 并以实际 probe 的正整数维度创建。实际 dimension、collection、provider model id、revision manifest 与 profile 必须写入报告。 |

执行前门槛：先安装并 probe `qwen3-embedding:0.6b`，记录其实际 provider id 与 dimension；扩展 eval runner 以显式发送/记录 `knowledge_scope`、`top_k` 和 `rerank_enabled`，并为两个模型重建同一 immutable revision 集合的独立 projection。若 profile/request、provider/model、dimension、collection schema 或 scope 不匹配，该 run 记为环境失败，不能计入质量分数。

`2026-07-17` 预检完成：本机 Ollama 已安装 `qwen3-embedding:0.6b`；固定文本的 OpenAI-compatible `/v1/embeddings` probe 返回 `1024` 维，且以该 id、`EMBEDDING_DIMENSIONS=1024` 运行 `make runtime-native-dense-smoke` 通过真实 embedding、临时 collection、ingest/reload、revision scope 和 citation provenance。此证据只证明候选 provider 可用，不构成模型质量比较结果。

每个有效 run 都必须通过对应 provider smoke、Runtime tests、知识演进/C1/C2 fixture 与固定 lexical `prompt_v2` R5 control gate；报告同时保留 retrieval/citation/refusal/grounded/latency 与 failure 分类。比较只决定后续是否值得提出 dense cutover 候选，不能自动创建业务 retrieval config、发布 snapshot、改写历史 provenance 或移除 lexical compatibility。

### 22.1 实验投影与 evidence write path

- Owner：`scripts/eval/run_baseline.py` 是 K8-R2 实验 projection 的唯一写入者。它为七格式 sample corpus 生成确定性的 experiment `document_id`、`revision_id`、checksum 和 revision manifest，再通过既有 Runtime ingestion pipeline 写入**当前模型专属的 Qdrant collection**。
- Scope：每次 runner materialization 都生成相同的 `snapshot_id`、revision allow-list 与只基于 immutable source identity 的 experiment manifest hash；embedding model 不参与该 experiment manifest 的计算，避免把向量表示差异伪装成不同知识快照。每个 chat request 必须携带该完整 `knowledge_scope`。
- 参数：K8-R2 CLI 必须显式传入 `top_k=3` 与 `rerank_enabled=false`，并把它们同时写入 Runtime request 和 JSON/Markdown report。普通 legacy eval 不因本切片被强制改写。
- 隔离：`all-minilm` 与 `qwen3-embedding:0.6b` 各运行在单独 Runtime process/profile 与 collection；不得重用另一模型 collection，不得借用真实 Postgres snapshot，也不得向业务 collection 写入 experiment revision。
- 失败：任一 fixture ingestion、revision manifest、scope/profile/model match 或 Qdrant schema 校验失败，runner 返回环境失败且不生成可比较的质量结论；已经存在的 experiment collection 可被下一次相同模型 run 以 revision replace 安全重建。
- 验证：unit/integration tests 证明 scope、`top_k`、rerank 和报告 evidence 完整；每个模型完成 provider smoke、真实 scoped runtime eval 与既有 regression gate 后才可进入指标比较。

### 22.2 首次比较结果

`2026-07-17` 已完成同一 scope、`prompt_v2`、`llama3.2:latest`、`top_k=3`、rerank 关闭的 40 条运行。两份报告的 snapshot id、manifest hash、七个 revision id、generation config 与 retrieval strategy 完全相同；唯一变量是 embedding model 与其隔离 collection。

| 指标 | `all-minilm` | `qwen3-embedding:0.6b` |
| --- | ---: | ---: |
| 通过 / 失败 | `23 / 17` | `27 / 13` |
| retrieval recall / citation accuracy | `1.0000 / 1.0000` | `1.0000 / 1.0000` |
| grounded correctness | `0.6000` | `0.7143` |
| refusal correctness | `0.0000` | `0.0000` |
| 平均延迟 | `4186.207ms` | `4402.398ms` |

结论：Qwen 比对照多通过四条、grounded correctness 提升 `0.1143`，代价是约 `216ms` 的平均延迟；在本固定 fixture 中选为后续 dense 优化的候选。两组的五条 must-refuse 都失败，因此本结论**不**批准业务 dense cutover、retrieval config 创建或 snapshot publish；下一切片必须在不改变模型、scope、grader、`top_k` 与 rerank 的条件下修复 dense refusal 行为，并重跑本比较。

## 23. 管理反馈到质量发布的评测协议

管理员 feedback 是候选样本来源，不是自动真值。只有经审核、带原 message execution evidence、knowledge scope/manifest、config pair 与 citation identity 的反馈，才可附着到 experiment；它不得直接修改 `v1-seed.jsonl`、Runtime 环境变量或 active config。

每个可比较 experiment 冻结并登记：dataset id/SHA-256、knowledge snapshot/manifest、baseline/candidate config pair、完整 generation/retrieval profile、runner version、conditions SHA-256、acceptance gate 与 linked feedback ids。报告登记必须 exact-match 这些条件；不完整或 mismatch 的 report 是环境/流程失败，不是质量结论。

对于 `should_refuse`/`wrong_evidence`，报告必须保留每样本 score、refusal、citation 与阈值/profile 事实，以支持 calibration；对于 `knowledge_gap`/`outdated_knowledge`，报告只可作为知识 revision remediation 的输入，不能替代检索参数实验。只有 candidate report 通过冻结 gate、管理员明确审批后，才可发布新 config pair；发布后仍需保留 baseline、报告、理由和 rollback target。

## 24. K8-R3 Dense 证据边界校准协议

`evidence_min_score` 是 K8-R3 的唯一实验变量；它是“最佳证据低于此值时 Runtime 必须拒答”的 immutable retrieval config 字段，不是通用模型分数或可凭感觉调整的 UI 数字。协议、参数解释和新知识切换前置条件见 [K8-R3 设计](../design-docs/single-agent-v1-k8-dense-refusal-calibration-design.md)。

每个 run 必须保留每样本的 top score、threshold decision、refusal、citation 与 failure type，以分别观察 must-answer/must-refuse 分布。固定 Qwen、scope、generation、`top_k=3`、rerank=false 后，候选 threshold 只有在五条 must-refuse 全部通过、must-answer retrieval/citation/grounded 不回归且 prompt_v2/知识演进/C1/C2 仍通过时，才能进入 Q1 approval；否则保持 lexical active release。

`2026-07-18`：`0.15` 对照的 score distribution 给出 must-answer 最低 `0.5237`、must-refuse 最高 `0.5020`；仅改变为 `0.51` 后两次 candidate 均 `5/5` 正确拒答且没有 must-answer 误拒。最新 candidate `30/40`、grounded `0.6429`、retrieval/citation/refusal `1.0`、runtime error `0`，通过 `k7-llama3.2-prompt-v2-local-v1` gate。该证据冻结为 fixture calibration，不构成实际业务 knowledge snapshot 的 Q1 发布授权。
