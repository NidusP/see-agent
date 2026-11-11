# 单智能体 V1 Eval Baseline 计划

## 文档控制

- 状态：用于实现启动的草案
- 版本：`v1.0`
- 目标：把 eval 数据集、报告格式和执行节奏补充到可直接启动基线实验的程度

## 当前实现状态

- `baseline/v1-seed.jsonl` 已沉淀首批 `40` 条样本。
- 七类格式 `txt/md/json/pdf/docx/xlsx/pptx` 各 `5` 条，must-refuse 样本 `5` 条。
- `make eval-baseline` 已接入 phase-1 deterministic mock runner。
- `T8` 已扩展 runtime baseline runner：同一脚本支持 `mock` 与 `runtime-local` 两种模式，后者会调用 Runtime `/v1/chat/run` 的真实检索 / 回答 / citations 链路。
- `K8-R1` runner 会在每份 report 中记录 retrieval mode、embedding endpoint/model/dimension 和 Qdrant collection；`--embedding-model`（或 `EVAL_EMBEDDING_MODEL`）会同时作为 Runtime retrieval-config override，避免 native dense 评测脱离实际配置。
- `K8-R2a` 起，报告还必须记录 `retrieval_strategy` 与 active Runtime profile。模型或检索对比只在 strategy/profile、dataset、grader、prompt、top_k/rerank 条件明确时成立；不允许把 profile 不匹配的失败混入质量分数。
- `2026-07-17` 的 K8-R2a fixed control gate 使用 `lexical_compat_v1`，得到 `31/40`、grounded `0.7357`、retrieval/citation `0.9714`、refusal `1.0`、unexpected runtime error `0`。它用于验证策略契约没有回归，不可作为 `native_dense_v1` 或第二 embedding model 的比较结论。
- `2026-07-17`：K8-R2a 的真实 Postgres、native Qdrant 和 `all-minilm` provider smoke 均已通过。经产品选择，R2 冻结为 `all-minilm` 对照 `qwen3-embedding:0.6b` 的单变量 dense embedding 比较。执行前必须补齐 runner 的显式 scope/`top_k`/rerank evidence；冻结条件以 `single-agent-v1-harness.md` 第 22 节为准。
- `2026-07-17`：K8-R2 runner 已 materialize 同一七格式 revision scope 到两个隔离 collection，并记录完整 scope/参数 evidence。`qwen3-embedding:0.6b` 为 `27/40`、grounded `0.7143`，优于 `all-minilm` 的 `23/40`、grounded `0.6000`；retrieval/citation 均为 `1.0`，但两组 refusal 均为 `0.0`。Qwen 仅作为下一轮 dense refusal/format 质量优化候选，不是业务 cutover 批准。
- 当前 runtime-local baseline 已可生成报告：`40` 条样本，`25` 通过，`15` 失败；报告位于 ignored `data/eval-datasets/reports/runtime/latest.*`。
- `Batch 1` 已新增 fallback failure triage 记录：`docs/harness/single-agent-v1-eval-failure-triage.md`。该记录不代表真实模型质量，只用于后续检索 / 引用优化排序。

## 1. 设计目标

本计划要解决三件事：

- 首批评测题如何组织
- 基线报告应输出哪些字段
- 在功能尚未完全实现前，如何分阶段把 eval runner 跑起来

## 2. 数据集组织

建议在 `data/eval-datasets/` 下采用以下组织方式：

```text
data/eval-datasets/
  seed-template.jsonl
  baseline/
    v1-seed.jsonl
  reports/
    README.md
    latest.json
    latest.md
```

### 2.1 `seed-template.jsonl`

- 作为字段模板和示例题集合
- 允许后续会话继续追加样本

### 2.2 `baseline/v1-seed.jsonl`

- 当首批 `30-50` 条题目稳定后，从模板沉淀成首个基线集
- 当前首批基线集已达到 `40` 条样本

### 2.3 `reports/`

- 存放基线运行产物说明
- `latest.json` 和 `latest.md` 为本地生成物，不作为稳定事实源提交
- 报告结构由 `scripts/eval/run_baseline.py` 输出并随 runner 演进

## 3. 首批题目配额建议

建议首轮 `30-50` 题按以下比例组织：

- `txt`: 4-5
- `md`: 4-5
- `json`: 4-5
- `pdf`: 4-5
- `docx`: 4-5
- `xlsx`: 4-5
- `pptx`: 4-5
- `must_refuse`: 4-6

这样可以同时保证：

- 每种格式不止 1 题
- 仍有足够拒答题防止模型只会回答不会拒绝

## 4. 题型建议

首批题目应至少覆盖：

- 单一来源事实查找
- 同一文件内多块综合
- 跨文件综合
- 政策 / 流程回答
- 引用定位检查
- 证据不足拒答

### 4.1 格式专项题

- `pdf`
  - 页码定位
  - 表格或条目问答
- `docx`
  - 标题路径定位
  - 列表或段落规则提取
- `xlsx`
  - sheet + range 组合定位
  - 指标或表格数值查找
- `pptx`
  - slide 标题与正文提炼
  - 里程碑 / owner / outcome 类问答

## 5. 样本字段规范

每条样本至少包含：

- `question_id`
- `question`
- `task_type`
- `expected_source_files`
- `expected_source_locators`
- `must_answer`
- `scoring_notes`

建议尽快补齐的可选字段：

- `expected_chunks`
- `expected_answer_points`
- `format_family`
- `difficulty`

## 6. 基线报告格式

建议 `make eval-baseline` 最终产出一个 JSON 报告和一个 Markdown 摘要。

### 6.1 JSON 报告最小字段

- `run_id`
- `dataset_id`
- `model_id`
- `config_version`
- `retrieval_config_version`
- `retrieval_strategy`
- `generation_config`
  - `temperature`
  - `max_output_tokens`
- `started_at`
- `completed_at`
- `metrics`
- `failures`

### 6.2 `metrics` 最小字段

- `retrieval_recall_at_k`
- `expected_file_hit_rate`
- `grounded_correctness`
- `citation_accuracy`
- `refusal_correctness`
- `average_latency_ms`

### 6.3 `failures` 最小字段

每个失败样本至少应记录：

- `question_id`
- `failure_type`
- `observed_answer`
- `observed_citations`
- `notes`

## 7. 分阶段执行策略

### A1 澄清与回答处置扩展

- 现有 `v1-seed` 的 40 条题主要覆盖事实查找、格式与拒答，不能单独衡量澄清、follow-up query recovery 或 partial coverage。
- A1 先建立 32 条开发 clarification slice；runner schema 支持 `expected_disposition`、多轮 turns、clarification resolution、resolved query、coverage、used citation ranks、answer policy version 和实际 generation provider/fallback observation。
- C3-R2 增加 `data/eval-datasets/fullstack/c3-r2-router-quality.jsonl` 开发 slice；runner 支持 `router_context_resolution`、`runtime_intent=context_routing_required`、有界 `conversation_turns`、`expected_context_resolution`、`expected_router_policy_version=conversation_router_v1`、`router_resolution_accuracy` 和 `router_quality_mismatch`。Q1 报告通过 `--router-policy-version conversation_router_v1` 把 Backend code-owned router policy 固定为实验条件。
- 发布门另行扩展到至少 80 条冻结样本，其中明确且不应澄清的样本不少于 40 条，并采用“模糊问题 → 补齐范围 → 有依据回答”配对。
- fallback 或缺少实际 provider evidence 的 runtime-local report 只能验证链路，不得登记为真实生成模型质量或进入 Q1 approval。

### 阶段 1：字段校验

目标：

- 确保数据集样本字段完整

此阶段不要求真实模型调用。

当前状态：

- 已完成。
- 入口：`make eval-baseline`

### 阶段 2：Mock Runner

目标：

- 跑通 runner、报告输出和失败分类框架

可以先用固定假响应代替真实模型。

当前状态：

- 已完成 phase-1 deterministic mock runner。
- 当前所有样本在 mock runner 下预期通过，后续真实 runtime runner 才产生模型或检索失败。

### 阶段 3：本地模型基线

目标：

- 在固定样例数据上跑第一轮真实结果

要求：

- runtime 摄取与回答基线已可用
- 至少 1 条每种格式的可回答题已具备

当前 `T8` 实现约定：

- `mock` 模式继续作为 phase-1 稳定字段校验入口，命令为 `make eval-baseline`。
- `runtime-local` 模式通过 FastAPI TestClient 调用 Runtime app 的 `/v1/chat/run`，命令为 `make eval-runtime-baseline`。
- 未配置 `LOCAL_LLM_BASE_URL` 时 Runtime 返回 generation provider unavailable，runtime-local 样本记为错误；配置 OpenAI-compatible 本地模型端点后，同一 runner 记录真实本地模型结果。确定性 integration 使用显式 test-only provider，不进入质量报告。
- runtime 报告允许出现失败样本；命令成功表示 runner 和报告产物生成成功，不表示模型质量已达标。
- 当前 runtime-local 指标：
  - `retrieval_recall_at_k = 0.8571`
  - `expected_file_hit_rate = 0.8571`
  - `grounded_correctness = 0.6214`
  - `citation_accuracy = 0.6`
  - `refusal_correctness = 0.6`
- 失败分类至少包含：
  - `retrieval_miss`
  - `citation_mismatch`
  - `answer_point_miss`
  - `refusal_policy_failure`
  - `unexpected_runtime_error`

### 阶段 4：受控对比

目标：

- 比较 `Qwen` 与 `DeepSeek`

约束：

- retrieval、prompt、dataset、grader 保持不变

### 阶段 3.1：Runtime Quality Gate

在真实模型基线稳定后，`make eval-runtime-gate` 使用版本化 JSON gate 运行同一 `runtime-local` 数据集。默认 gate 为 `data/eval-datasets/baseline/r3-llama3.2-quality-gate.json`，仅适用于：

- `model_id=llama3.2:latest`
- `temperature=0.2`
- `max_output_tokens=1024`

本轮最低条件：

- `passed_count >= 26`
- `retrieval_recall_at_k >= 0.95`
- `citation_accuracy >= 0.95`
- `refusal_correctness = 1.0`
- `grounded_correctness >= 0.55`
- `unexpected_runtime_error = 0`

Runner 必须先写入 JSON/Markdown report，再根据 gate 以零或非零退出；报告需包含 gate id、状态、实际/期望 generation config 和每个失败原因。平均延迟仅记录，不作为本地硬件相关的阻断阈值。模型、模式或 generation config 不匹配时 gate 必须失败，不能把不同实验条件的分数混入基线。

`2026-07-12` 的关闭 fallback 复验已通过：`27/40`、`retrieval_recall_at_k=0.9714`、`citation_accuracy=0.9714`、`refusal_correctness=1.0`、`grounded_correctness=0.5786`、`unexpected_runtime_error=0`。后续任何 Runtime/RAG/Prompt 改动都必须在相同模型与 generation config 下重跑该 gate；失败报告继续作为格式专项优化的输入。

## 8. 当前推荐动作

在当前阶段，最务实的推进方式是：

1. 保持 `baseline/v1-seed.jsonl` 作为 phase-1 固定数据集
2. 每次 Runtime/RAG/Prompt 改动后，在相同 Ollama 条件下运行 `make eval-runtime-gate`
3. 使用失败明细定位 PDF/XLSX/PPTX 的证据提取、chunking、retrieval 或答案生成问题
4. 只有在固定 strategy/profile、数据集、grader 与 gate 条件不变时，才对模型或检索策略进行受控比较

## 9. 与其他文档的关系

- `single-agent-v1-harness.md`
  - 定义评测原则和指标
- `single-agent-v1-integration-test-plan.md`
  - 定义系统链路打通测试
- 本文档
  - 定义 eval 数据与报告落地方式
