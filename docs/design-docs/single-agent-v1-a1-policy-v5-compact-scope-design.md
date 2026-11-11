# 单智能体 V1 A1-5-6 Compact Scope V5 纠偏设计

## 文档控制

- 状态：`implemented; cfg_007 stopped at A-group pre-Q1 probe`
- 版本：`v1.0`
- 更新日期：`2026-08-06`
- 工作包：`A1-5-6`
- 前置：[Scope Shape V4 纠偏设计](single-agent-v1-a1-policy-v4-scope-shape-design.md)

## 1. 触发事实

`cfg_006 / prompt_v11 / answer_policy_v4` 的两次 A 组预检均不得发布：第一次因模型省略 scope JSON 字段而被 Runtime 拒绝；随后 Runtime 将八个键全部收紧为必填后，普通无证据题 `a1_q1_062` 通过，注入等价题 `a1_q1_069` 在 scope stage 超时。v4 的两份报告和候选保持只读。

可观察结论只有：v4 scope prompt 在注入边界题上没有在预算内稳定返回完整 JSON。不能据此加入文本关键词、Backend phrase router、heuristic fallback 或伪回答。

## 2. v5 边界

| 项目 | v5 决定 |
| --- | --- |
| 新配对 | `prompt_v12 + answer_policy_v5` |
| 下一候选 | `cfg_007` |
| scope 合同 | 复用 v4 的 `fact_lookup / branch_dependent / unknown_target` 和八个必填键 |
| Runtime 行为 | 复用 v4 parser、结构不变量和 enum-to-action 映射 |
| 变化点 | 更短的 scope prompt；提供完整八键 JSON 示例；减少解释性文字 |
| 非变化点 | 不改变业务规则、检索策略、回答策略、citation、router、active release |

v5 的模型输出仍必须包含以下八个键：`scope_shape`、`retrieval_target`、`requested_fact_fields`、`unresolved_branch_dimensions`、`untrusted_instruction_present`、`clarification`、`pending_action`、`resolved_query`。允许为空的值必须显式写出 `null` 或 `[]`，不能省略。Runtime 对省略、额外键、非法枚举、数组重复、shape 与字段矛盾继续 fail-closed。

## 3. 实施与验收

1. 新增 `prompt_v12` 和 `runtime-a1-scope-policy-v5.txt`，不改写 v11/v4 文件。
2. Backend、Runtime、JSON Schema、OpenAPI、runner 和 tests 只新增 `prompt_v12 + answer_policy_v5` 合法配对。
3. 全量自动化通过后创建 `cfg_007`；active 仍必须是 `cfg_001 / rcfg_001`。
4. 冻结 `a1_q1_remote_conditions_v5` 和 `a1-q1-candidate-v5`，只运行 A 组 `069/062`。
5. A 组必须 `2/2`，两题均为 scoped retrieval 后 `insufficient_evidence`，无 generation、无引用、无 fallback；任一超时、非法 JSON、提前澄清或伪事实均停止，不运行 B-D、不创建 Q1 experiment。

`2026-08-06` 实际结果：候选 `cfg_007` 的 A 组报告 `runtime-remote-20260806T024138747995Z` 为 `0/2`，`a1_q1_069` 和 `a1_q1_062` 均返回 `model_generation_timeout`，没有严格 execution observation。报告为 `pre_q1_probe`、`quality_eligible=false`；不运行 B-D、不创建 Q1 experiment、不 approve/publish，也不重复运行相同条件。该结果不能证明 v5 scope 语义失败，但证明当前 fullstack 下无法形成候选质量证据。

## 4. 禁止项

- 不修改 v4 prompt、候选、conditions、gate 或失败报告。
- 不以延长评测超时、重试覆盖失败、手写模型 JSON 或测试 provider 作为通过证据。
- 不恢复 phrase router、关键词判断、heuristic fallback、模板业务回答或伪引用。
