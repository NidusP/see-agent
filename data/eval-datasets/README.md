# data/eval-datasets

本目录用于存放评测数据集。

首批目标：

- `30-50` 条种子问题
- 覆盖 `txt/md/json/pdf/docx/xlsx/pptx`
- 包含 must-answer 和 must-refuse 两类样本

当前已落地：

- `seed-template.jsonl`
  - 已覆盖 `txt/md/json/pdf/docx/xlsx/pptx` 和 1 条拒答样本
- `baseline/v1-seed.jsonl`
  - 首批 `40` 条 baseline 样本
  - 覆盖 `txt/md/json/pdf/docx/xlsx/pptx` 各 `5` 条，以及 `5` 条 must-refuse 样本
- `reports/`
  - `make eval-baseline` 的本地报告输出目录
- `fullstack/c3-r2-router-quality.jsonl`
  - C3-R2/A1/Q1 开发路由样本，覆盖 greeting、capability confirmation、standalone RAG、follow-up、topic shift 和 clarification；需用 `--router-policy-version conversation_router_v1` 将 Backend code-owned router policy 绑定到报告条件。
- `clarification/v1-dev.jsonl`
  - A1-1 的 `32` 条开发样本：`10` 条有依据回答、`10` 条需要澄清、`6` 条证据不足和 `6` 条部分覆盖。
  - 只引用三份无敏感本地示例制度；它不是 Q1 发布集，也不代表真实组织制度。
  - 数据集约定和评分字段见 `docs/harness/single-agent-v1-harness.md` 的 A1-1 章节。
- `clarification/v1-q1.jsonl`
  - A1-5/Q1 的独立 `80` 条冻结发布集：至少 `40` 条范围充分、不应澄清的题目，并覆盖阈值、前置条件、例外、否定条件、时间、角色分离、边界值、多轮恢复和证据不足。
  - 每条以 `boundary_tags` 记录评测切片；多轮配对以 `evaluation_pair_id` 绑定。标签只服务于评测和人工复核，不会传给 Runtime 或用户界面。
  - mock 报告只验证数据/评分器，始终 `quality_eligible=false`；Q1 发布证据必须来自同条件的 `runtime-remote` run。
- `clarification/a1-q1-remote-conditions.schema.json`
  - `A1-5-2/A1-5-4` conditions 文件的通用 schema。实际条件文件必须冻结 `v1-q1` hash、published scope、同一 retrieval boundary 和 `prompt_v7` baseline；`a1_q1_remote_conditions_v1` 只允许不同 config version 的 `prompt_v8 + answer_policy_v1` candidate，`v2` 只允许 `prompt_v9 + answer_policy_v2`，`v3` 只允许 `prompt_v10 + answer_policy_v3`。runner 对文件计算 canonical SHA-256。
- `clarification/v1-q1-remote-conditions.json`
  - `2026-08-04` 为真实远程评测冻结的条件：active `cfg_001` 是 baseline，未发布 `cfg_002` 是唯一 candidate；二者共享 `snap_004`、`rcfg_001` 和完整 `v1-q1` 数据集。该文件只在创建新实验时替换，任何修改都必须对应新的 conditions hash 与 experiment。
- `clarification/v1-q1-remote-conditions-v2.json`
  - `2026-08-04` A1-5-3 的独立条件：active `cfg_001` 继续为 baseline，未发布 `cfg_003 / prompt_v9 / answer_policy_v2` 是唯一 candidate；它保留同一 frozen scope/retrieval/dataset，且不能复用 v1 的 conditions hash、gate 或报告。第 A 组真实 pre-Q1 report 为 `1/2`，因此本文件和 candidate 只读保留，不能进入完整 Q1。
- `quality-gates/a1-q1-candidate-v1.json`
  - A1 candidate 专用 `runtime-remote` gate：当前 sampling 与 active `cfg_001` 一致（`qwen3:14b`、`temperature=0`、`top_p=1`、`concise`、`1024`），除 prompt/policy 外不允许改变实验变量。其总样本下限为 `72/80`，但部分覆盖诚实性、禁答事实、协议/marker/运行时契约错误保持独立严格门槛。
- `quality-gates/a1-q1-candidate-v2.json`
  - A1-5-3 的 immutable candidate gate；阈值与 v1 相同，但只允许 `qwen3:14b / prompt_v9 / answer_policy_v2`。它仅供四题 pre-Q1 probe 通过后的完整 candidate report 使用；probe 不能加载该 gate。

相关设计文档：

- `docs/harness/single-agent-v1-harness.md`
- `docs/harness/single-agent-v1-eval-baseline-plan.md`

当前命令：

- `make eval-baseline`
  - 校验 `baseline/v1-seed.jsonl`
  - 运行 phase-1 deterministic mock runner
  - 输出 `data/eval-datasets/reports/latest.json` 与 `latest.md`
- `./.venv/bin/python scripts/eval/run_baseline.py --mode mock --dataset data/eval-datasets/clarification/v1-dev.jsonl --report-dir /private/tmp/see-agent-a1-dev-report`
  - 校验并运行 A1-1 开发集；报告会标明合成 provider evidence 和 `quality_eligible=false`。
