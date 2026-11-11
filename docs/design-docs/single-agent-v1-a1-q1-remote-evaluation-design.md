# 单智能体 V1 A1-Q1 远程评测证据设计

## 文档控制

- 状态：`implemented; cfg_002 candidate stopped at pre-Q1 probe`
- 版本：`v1.0`
- 更新日期：`2026-08-04`
- 工作包：`A1-5-2`
- 依赖：[澄清与有依据回答处置设计](single-agent-v1-clarification-policy-design.md)、[质量运营与受控发布设计](single-agent-v1-quality-operations-release-design.md)、[评测 harness](../harness/single-agent-v1-harness.md)

## 1. 决策

- 变更：为 `v1-q1` 定义可审计的 `runtime-remote` baseline/candidate 实验条件、逐题执行证据和候选报告准入规则。
- 合同与边界：冻结条件文件是 runner 的唯一输入；Runtime 是实际执行事实的唯一来源；Backend 是 experiment/report/approval/release 的唯一写入方；浏览器不创建或推断质量证据。
- 写入路径：系统管理员先创建未发布 candidate config，再冻结 Backend experiment 和本地条件文件。runner 仅以 service token 调用 Runtime、写入原始 report，并将同一 report 依次登记、关联到 experiment。没有自动 approve 或 publish。
- 一致性：条件文件、dataset SHA-256、scope 和 experiment conditions SHA-256 必须完全一致。report 一旦登记保持 append-only；任何条件或数据修订均创建新文件、新 hash 和新 experiment。
- 失败行为：条件解析、请求/响应不匹配、缺失 provider、fallback、HTTP 错误、非法 A1 execution 事实或 gate 不通过都使该 run 不可关联为 candidate evidence。不得以澄清、模板、伪回答、伪引用或旧 report 代替。
- 拒绝的方案：不允许 runner 的零散 CLI 参数自行构成 Q1 条件；不把 active legacy baseline 标记为 A1 candidate quality eligible；不因 runtime response 未暴露实际检索参数而只相信请求回显；不让 Q1 测试报告直接发布配置。

## 2. 两类报告与发布边界

`baseline` 是当前 active release 的真实性对照。它必须是相同的远程 Runtime、scope、model、检索条件和无 fallback 的实际执行，但其 legacy prompt/policy 可以不满足 A1 policy contract。因此 baseline 报告只标记 `comparison_eligible=true`，不能单独也不能与 candidate 一起绕过 candidate gate。

`candidate` 固定为未发布的 `prompt_v8 + answer_policy_v1` config。candidate 报告必须满足相同的真实性要求，且每个 A1 样本都通过本设计第 4 节的处置感知 execution contract，才标记 `quality_eligible=true` 并可进入 candidate gate。Q1 approve 仍是系统管理员的显式状态转换；publish 仍需独立显式操作，且只影响新 conversation。

同一实验中 baseline 与 candidate 只能不同于 `config_version`、`system_prompt_version` 与 `answer_policy_version`。`model_id`、generation sampling、response style、retrieval config/version/strategy、embedding model、`top_k`、`rerank_enabled`、`evidence_min_score`、scope 和 dataset 均必须相同。违反该规则的 report 不可关联。

## 3. 冻结条件文件

条件文件采用 `a1_q1_remote_conditions_v1`，路径在 `data/eval-datasets/clarification/`，内容至少包括：

```json
{
  "schema_version": "a1_q1_remote_conditions_v1",
  "dataset": {"id": "v1-q1", "sha256": "sha256:..."},
  "scope": {"snapshot_id": "snap_004", "manifest_hash": "sha256:...", "revision_ids": ["..."], "resolved_at": "..."},
  "retrieval": {"config_version": "...", "strategy": "...", "embedding_model": "...", "top_k": 3, "rerank_enabled": false, "evidence_min_score": 0.51},
  "baseline": {"config_version": "cfg_001", "model_id": "qwen3:14b", "system_prompt_version": "prompt_v7", "answer_policy_version": null, "response_style": "concise", "temperature": 0, "top_p": 1, "max_output_tokens": 1024},
  "candidate": {"config_version": "new immutable config", "model_id": "qwen3:14b", "system_prompt_version": "prompt_v8", "answer_policy_version": "answer_policy_v1", "response_style": "concise", "temperature": 0, "top_p": 1, "max_output_tokens": 1024},
  "router_policy_version": "conversation_router_v1",
  "candidate_gate_id": "a1-q1-candidate-v1"
}
```

runner 从该文件派生 canonical JSON SHA-256，拒绝同时传入相冲突的 Q1 CLI 条件。baseline 必须固定 `prompt_v7` 且 candidate 必须为不同于 baseline 的新 immutable config。report 同时保存 immutable `conditions_sha256`、`evaluation_role`、原始 request conditions 和逐题 Runtime observation。条件文件和 candidate gate 可在 candidate config 创建之后、任何远程 run 之前写入；`v1-q1.jsonl` 本身绝不修改。

## 4. Runtime 逐题证据合同

Runtime response 的 `execution.retrieval` 在 `performed` 时必须记录实际采用的 `strategy`、`embedding_model`、collection、candidate count、`top_k`、`rerank_enabled` 和 `evidence_min_score`。前四项已有事实继续保持；后三项由 Runtime 从实际生效的 retrieval configuration 产生，不能由 runner 编造。runner 将所有 observed field 与冻结条件 exact-match。

candidate 的逐题最小执行矩阵如下：

| `answer_disposition` | policy | retrieval | generation | 其他强制事实 |
| --- | --- | --- | --- | --- |
| `grounded_answer` / `partial_answer` | `performed`，有非 test provider/model、且 policy model 等于 frozen candidate model | `performed`，并与条件完全匹配 | `performed`，有非 test provider/model | `fallback_used=false`、`knowledge_used=true`；scope/used rank/citation/anchor 合法 |
| `clarification_required` | `performed`，有非 test provider/model、且 policy model 等于 frozen candidate model | `not_run`（pre-retrieval）或 `performed`（post-retrieval） | `not_run` | `knowledge_used=false`；无业务事实、citation、anchor、used rank；不得把 transport/model failure 记作澄清 |
| `insufficient_evidence` | `performed`，有非 test provider/model、且 policy model 等于 frozen candidate model | `performed`，并与条件完全匹配 | `not_run` | `refusal=true`、`knowledge_used=false`、无用户 citation/anchor/used rank |

baseline 只验证 scope、无 fallback、实际 generation/retrieval provider 和可观察到的检索条件；它不伪造 A1 policy facts。baseline 因 legacy 协议不能达到 `quality_eligible` 时，报告仍可作对照，但 Backend 不得把它当作 candidate gate 的替代品。

## 5. Gate 与 Backend 关联

`a1-q1-candidate-v1` 只允许 `runtime-remote` candidate 报告，并校验 dataset/config/retrieval/conditions/generation config identity、`quality_eligible=true`、以下 A1 指标以及失败上限：

- disposition accuracy、clarification dimension accuracy、clarification resolution accuracy、resolved-query retrieval hit rate、citation/anchor fidelity 和 answer-point recall 使用明确的 minimum；
- unnecessary clarification rate 使用 maximum；
- partial coverage honesty 必须为 `1.0`；
- `runtime_contract_violation`、`answer_policy_protocol_failure`、重复澄清、internal marker leak、`unsupported_business_claim` 与 `forbidden_answer_point_violation_count` 上限为 `0`；后者是独立 metric，不能因为一个样本另有更早的 primary failure 而被 failure histogram 掩盖；
- 现有 prompt_v2、知识演进、C2/C3、onboarding 与浏览器回归继续作为 publish 前的外部门，不被该 gate 取代。

Backend 在关联报告时重新解析已登记 report：角色必须与关联角色一致；candidate 必须携带通过的 candidate gate、`quality_eligible=true` 和相同 conditions SHA；baseline 必须携带 `comparison_eligible=true`。这些是关联/approve 的服务层校验，不信任 Web 的显示状态。现有 service bearer 在 `K9 service security` 完成前仍是开发环境边界，必须在验收记录为剩余风险；它不能被描述为 production runner identity。

## 6. 实施与验收顺序

1. 扩展 Runtime/shared JSON/OpenAPI/Web-compatible execution observation，增加实际 retrieval 参数；运行 Runtime contract regression。
2. 扩展 eval runner：载入 conditions、传播 `answer_policy_version`、允许 `prompt_v8`、执行上表验证、输出 role/eligibility，并支持 A1 minimum/maximum gate。
3. 扩展 Backend experiment-report association 校验和真实 Postgres 测试；OpenAPI 对 config create 的“active”措辞同步为 immutable candidate creation。
4. 在确认 Backend 为 Postgres repository 后，读取 active config/history 与 active scope；仅创建一个未发布 candidate config，复读确认 active pointer 未变。
5. 以冻结 conditions 对 candidate 先做 2 条非 Q1 remote probe，再运行 baseline 和 candidate 的 80 条 report；逐条人工复核失败、失败上限和 provider evidence。
6. 只有所有自动 gate、既有回归和人工失败复核都通过时，才由管理员决定 approve；本工作包不执行 publish。

验证必须覆盖：条件 hash 篡改、baseline/candidate 参数漂移、`prompt_v8` 未传播、遗漏 A1 policy、合法 pre-retrieval clarification、post-retrieval clarification、insufficient evidence、假 provider、fallback、response retrieval 参数不一致、candidate gate 不通过、角色伪造和审批拒绝。

### 6.1 两题预检的非证据边界

两题预检使用 runner 的重复 `--probe-question-id` 参数。它只能与 `--mode runtime-remote`、`--a1-q1-conditions` 和 `--evaluation-role candidate` 一起使用；两个 id 必须唯一、非空，且在已完成 hash 校验的冻结 `v1-q1` 数据集中存在。runner 只在该校验后按指定顺序选择这两条记录，不创建、修改或替代数据集。

预检不能携带 `--quality-gate`，也不能携带任何 QualityExperiment report registration 参数。其本地 JSON report 必须记录 `evaluation_purpose: pre_q1_probe`，并将 `comparison_eligible`、`quality_eligible` 及对应 provider evidence eligibility 固定为 `false`。该 marker 是持久的非证据边界：预检可以暴露真实 Runtime/provider/citation 失败，但不能被关联到 experiment、用于 candidate gate、approve 或 publish。
