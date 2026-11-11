# packages/prompts

本目录用于管理可版本化的提示词与回答模板。

建议首批内容：

- 系统提示词
- 拒答模板
- 引用展示模板
- 模型差异化 prompt 变体记录

当前已落地：

- `runtime-chat-system-v1.txt`
- `runtime-chat-system-v8.txt`
- `runtime-a1-scope-policy-v1.txt`
- `runtime-chat-system-v9.txt`
- `runtime-a1-scope-policy-v2.txt`
- `runtime-chat-system-v10.txt`
- `runtime-a1-scope-policy-v3.txt`
- `runtime-chat-system-v11.txt`
- `runtime-a1-scope-policy-v4.txt`
- `runtime-chat-system-v12.txt`
- `runtime-a1-scope-policy-v5.txt`
- `runtime-refusal-v1.txt`

版本规则：

- 已使用的 prompt 文件不可原地改写；行为变化必须新增版本。
- `prompt_v1` 对应 `runtime-chat-system-v1.txt`。
- `prompt_v2` 对应 `runtime-chat-system-v2.txt`；它已连续两次通过严格 K7 质量门，可作为新配置版本启用。
- `prompt_v3` 对应 `runtime-chat-system-v3.txt`；它把事实回答的 citation marker 与范围明确但证据不足的 disposition marker 写成显式输出协议。它是 dense cutover 的候选，必须先通过非 Q1 remote probe，再以独立 immutable config 进入新的 Q1 experiment。
- `prompt_v4` 对应 `runtime-chat-system-v4.txt`；它保留 v3 的协议，并为政策例外题新增“默认规则 + 例外条件”完整性约束。它必须独立通过 remote probe 和 Q1，不能借用 v3 的报告。
- `prompt_v5` 对应 `runtime-chat-system-v5.txt`；它是 `qwen3:14b` 的新的 dense 候选，要求在生成前检查事实—来源映射，并把默认规则与例外作为完整结论回答。它必须先通过新的 non-Q1 remote probe，再创建独立 Q1 experiment；不能借用 v3/v4 或 `llama3.2:latest` 的报告。
- `prompt_v6` 对应 `runtime-chat-system-v6.txt`；它仅在显式 `ollama_native`/`think=false` deployment 中使用，并将 Qwen3 的 `/no_think` 指令写入 immutable prompt。它不得借用在 `openai_compatible` adapter 条件下失败的 `qexp_006`；必须用新 Agent Config、新 probe 与新 Q1 evidence 验证。
- `prompt_v7` 对应 `runtime-chat-system-v7.txt`；它保留 native Ollama 的 `/no_think` 条件，并把“例外问题必须先说明默认规则、再说明例外条件”的输出顺序写成可评测协议。它是对 `prompt_v6` probe 中 `dense_handbook_003` 完整性失败的独立候选，必须使用新 config、新 probe 和新 Q1 evidence。
- `prompt_v8` 配对 `answer_policy_v1` 与 `runtime-a1-scope-policy-v1.txt`。它是 A1-3 的未发布候选：scope policy 先决定澄清、pending resolution 或受限 retrieval，随后 `prompt_v8` 在候选证据上以严格 JSON 产出 answer disposition、coverage 与可解析的 citation marker。任一 JSON 或引用校验失败都 fail-closed 为 `answer_policy_invalid`；它不能替代 C3 router，也不能在没有 Q1 evidence 与人工审批时成为 active release。
- `prompt_v9` 只配对 `answer_policy_v2` 与 `runtime-a1-scope-policy-v2.txt`。v2 将注入式用户指令视为不可信文本；明确业务对象和所求事实字段先检索，再由证据决定回答或拒答，只有会改变检索目标的真实范围缺失才澄清。它保留 v8 的 JSON、citation、coverage 和无模型记忆边界，必须以独立 immutable config、probe 和 Q1 evidence 验证。
- `prompt_v10` 只配对 `answer_policy_v3` 与 `runtime-a1-scope-policy-v3.txt`。v3 保留 v9 的 final answer JSON、citation、coverage、无模型记忆和注入边界；scope policy 额外输出仅供 Runtime 验证的 `scope_assessment`，明确内部检索目标与会改变制度分支的未决维度。Runtime 只验证 JSON 的结构自洽，不按关键词或业务语义改写模型 action；任一缺字段、空值、重复、子集不一致或非法版本配对均以 `answer_policy_invalid` fail-closed。它是未发布候选，须使用独立 immutable config、pre-Q1 probe 和 Q1 evidence，不能复用 v1/v2 结果。
- `prompt_v11` 只配对 `answer_policy_v4` 与 `runtime-a1-scope-policy-v4.txt`。v4 将 scope policy 拆成 `fact_lookup`、`branch_dependent`、`unknown_target` 三种问题形状，由 Runtime 按冻结枚举解释为检索或澄清，并把形状与不可信指令标记写入 execution evidence。它是未发布候选，必须独立通过 pre-Q1 probe 和 Q1 evidence；不改变 v1/v2/v3 的历史合同，也不恢复 phrase router 或 heuristic fallback。
- `prompt_v12` 只配对 `answer_policy_v5` 与 `runtime-a1-scope-policy-v5.txt`。v5 保留 v4 的八键 scope shape 合同与严格必填校验，缩短 scope prompt 并提供完整 JSON 示例以降低边界题超时；它是未发布候选，必须使用独立 config、probe 和 Q1 evidence，不能复用 v4 结果。
