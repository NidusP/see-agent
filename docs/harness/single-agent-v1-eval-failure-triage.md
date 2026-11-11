# 单智能体 V1 Eval Failure Triage

## 文档控制

- 状态：活动记录
- 版本：`v1.0`
- 更新日期：`2026-07-12`
- 适用范围：`Batch 1` fallback runtime-local baseline

## 1. 本轮结论

本轮环境未配置 `LOCAL_LLM_BASE_URL`，因此 `make eval-runtime-baseline` 使用 Runtime deterministic fallback 路径。该结果不代表真实本地模型质量，只用于定位当前检索、引用、拒答和报告链路的基线问题。

运行结果：

- 命令：`make eval-runtime-baseline`
- 数据集：`data/eval-datasets/baseline/v1-seed.jsonl`
- 样本数：`40`
- 通过：`25`
- 失败：`15`
- `retrieval_recall_at_k`: `0.8571`
- `expected_file_hit_rate`: `0.8571`
- `grounded_correctness`: `0.6214`
- `citation_accuracy`: `0.6`
- `refusal_correctness`: `0.6`

## 2. 失败类型分布

事件级失败计数：

- `citation_mismatch`: `19`
- `answer_point_miss`: `13`
- `retrieval_miss`: `7`
- `refusal_policy_failure`: `2`

按格式归因：

| format | failure summary |
| --- | --- |
| `docx` | `retrieval_miss=5`、`citation_mismatch=8`、`answer_point_miss=5` |
| `json` | `citation_mismatch=5` |
| `md` | `answer_point_miss=1` |
| `must_refuse` | `refusal_policy_failure=2` |
| `pdf` | `answer_point_miss=5` |
| `pptx` | `retrieval_miss=1`、`answer_point_miss=1` |
| `xlsx` | `retrieval_miss=1`、`citation_mismatch=6`、`answer_point_miss=1` |

按题型归因：

| task type | failure summary |
| --- | --- |
| `single_source_fact_lookup` | `answer_point_miss=9`、`citation_mismatch=11`、`retrieval_miss=4` |
| `table_lookup` | `retrieval_miss=1`、`citation_mismatch=5`、`answer_point_miss=1` |
| `policy_flow_answer` | `retrieval_miss=2`、`citation_mismatch=2`、`answer_point_miss=2` |
| `same_file_multi_block` | `citation_mismatch=1` |
| `locator_check` | `answer_point_miss=1` |
| `insufficient_evidence` | `refusal_policy_failure=2` |

## 3. 优先级判断

下一轮优化建议顺序：

1. `citation_mismatch`：优先检查 citation grader 与 locator 规范是否过严或不一致，尤其是 `json/xlsx/docx`。
2. `docx retrieval_miss`：检查 docx parser 的 heading_path、block label 和 chunk 文本是否足以被当前 lexical scorer 命中。
3. `pdf answer_point_miss`：检查 fallback answer composer 是否只拼接了相邻或高分片段，导致答案点遗漏。
4. `must_refuse`：检查拒答策略是否在检索到弱相关片段时过度回答。

## 4. Batch 2 输入

`R3` 检索与引用质量优化默认从以下动作开始：

- 输出每个失败样本的 expected locator 与 observed citation 对比表。
- 对 `json/xlsx` citation mismatch 做一次 grader 规则复核，避免把等价 locator 判成失败。
- 对 `docx` 样本检查 chunk inspector 中的 heading_path 与文本预览。
- 对拒答题增加“弱证据不回答”的阈值或 prompt 约束实验。

真实模型端点可用后，必须重新运行：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 make eval-runtime-baseline
```

并把真实模型报告与本 fallback triage 分开记录。

## 5. 2026-07-09 真实 Ollama 试跑记录

本机 Ollama 已可访问，但当前只发现 `llama3.2:latest`：

- `GET http://127.0.0.1:11434/v1/models`
- `GET http://127.0.0.1:11434/api/tags`

本轮真实模型命令：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
LOCAL_LLM_API_KEY=ollama \
LOCAL_LLM_ALLOW_FALLBACK=false \
.venv/bin/python scripts/eval/run_baseline.py \
  --mode runtime-local \
  --dataset data/eval-datasets/baseline/v1-seed.jsonl \
  --report-dir data/eval-datasets/reports/runtime \
  --model-id llama3.2:latest
```

结果：

- 样本数：`40`
- 通过：`3`
- 失败：`37`
- `retrieval_recall_at_k`: `0.0571`
- `expected_file_hit_rate`: `0.0571`
- `grounded_correctness`: `0.0`
- `citation_accuracy`: `0.0571`
- `refusal_correctness`: `0.6`
- `average_latency_ms`: `9070.396`

失败类型：

- `unexpected_runtime_error`: `30`
- `citation_mismatch`: `10`
- `retrieval_miss`: `7`
- `answer_point_miss`: `7`

后续单条复现确认 `unexpected_runtime_error` 主要来自 `LOCAL_LLM_TIMEOUT_SECONDS=30` 下的 `httpx.ReadTimeout`，不是 RAG 检索本身的失败。因此这次真实模型试跑只证明 OpenAI-compatible Ollama 链路可被调用，不应覆盖 fallback baseline 对检索、引用和拒答质量的判断。

下一步：

- 为真实模型 eval 显式配置更大的 `LOCAL_LLM_TIMEOUT_SECONDS` 和较小 `max_output_tokens`。
- 改进 eval runner，把 runtime exception message 写入报告 failure notes，避免 `unexpected_runtime_error` 丢失根因。
- 如目标是中文企业知识问答基线，优先准备更适配中文的本地模型 tag，再重跑真实模型报告。

## 6. 2026-07-12 本机连接复核

本机直接向 Ollama 的 OpenAI-compatible endpoint 发出最小请求时，`llama3.2:latest` 正常返回 `READY`。但 Runtime 进程中同一类请求曾出现 `ReadTimeout` 和 `502`，调用栈显示 `httpx` 选择了系统 HTTP proxy transport；这与 Qdrant/object-store 已明确禁用 loopback proxy 的经验一致。

结论与动作：

- 此问题属于 Runtime 到本机模型的连接边界，不可归类为模型质量或 RAG 检索失败。
- Model adapter 必须对 `localhost`、`127.0.0.1`、`::1` 禁用环境代理，对远程 OpenAI-compatible endpoint 保留现有代理语义。
- 修复后用 `LOCAL_LLM_ALLOW_FALLBACK=false make local-model-smoke` 验证指定 model id 与一次真实生成，再以 `LOCAL_LLM_TIMEOUT_SECONDS=90` 重跑完整 eval。

## 7. 2026-07-12 修复后真实模型基线

已完成以下严格验收：

- `make local-model-smoke`：`llama3.2:latest` 出现在 `/v1/models`，并通过 Runtime 的 `OpenAICompatibleModelClient` 取得非空生成；命令显式设定 `LOCAL_LLM_ALLOW_FALLBACK=false`。
- 新启动 Backend 的 Agent Config 与新会话快照均为 `llama3.2:latest`；Backend -> Runtime 的中文 RAG 问答返回 `10个自然日` 与 citation。
- 真实评测命令：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
LOCAL_LLM_API_KEY=ollama \
LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest \
LOCAL_LLM_TIMEOUT_SECONDS=90 \
LOCAL_LLM_ALLOW_FALLBACK=false \
make eval-runtime-baseline
```

结果：

- 模型：`llama3.2:latest`
- 样本：`40`；通过：`24`；失败：`16`
- `retrieval_recall_at_k=0.8571`
- `expected_file_hit_rate=0.8571`
- `grounded_correctness=0.5643`
- `citation_accuracy=0.6`
- `refusal_correctness=0.6`
- `average_latency_ms=5353.408`
- `unexpected_runtime_error=0`；原有代理导致的超时/502 已消除。

结论：`R1` 本地模型接入与基线已完成。剩余失败仍集中在 `answer_point_miss`、`citation_mismatch`、`retrieval_miss` 和弱证据拒答，按路线图进入 `R3`，不能因模型已接通而视为质量达标。

## 8. R3 第一轮实验契约

### 8.1 目标与基线

本轮以第 7 节 `llama3.2:latest` 真实基线为唯一对照：`retrieval_recall_at_k=0.8571`、`citation_accuracy=0.6`、`refusal_correctness=0.6`，且 `unexpected_runtime_error=0`。

第一轮只处理已定位、低风险的三类失败：

1. 中文问题检索英文 DOCX 时缺少业务术语桥接，导致低于 `min_score` 的误拒答。
2. `员工`、`公司`、`样例` 等泛词命中标题或无关块，导致 must-refuse 问题被送入模型。
3. XLSX 的评测期望是单元格、Runtime citation 是包含该单元格的 sheet region，当前评分器把可审计的上位区域误判为 mismatch。

### 8.2 冻结的实现边界

- Runtime 只在 lexical query 表征中加入固定、可审查的中文业务术语到英文术语别名；仅当 `source_formats` 明确为 `docx` 或问题明确含 `Word` / `DOCX` 时启用，原始问题、chunk 文本和返回的 `source_locator` 均不改写。
- 词法 tokenizer 排除有限的泛化 CJK 词，避免它们单独构成检索证据；不得降低明确业务术语、数字、表格字段或英文 token 的权重。
- 检索可扩大候选池以判断证据质量，但只有非 `heading` chunk 能进入回答 context；仅标题命中必须走 retrieval refusal，不能把标题送入模型生成路径。
- Eval 对 XLSX 仅在同一 `sheet_name` 且 citation 的矩形范围覆盖期望单元格时承认 locator 等价；不同 sheet、相邻但不覆盖、或无法解析的范围必须继续失败。
- Eval 对 JSON 的 `?id=...` selector 只在同一 expected source 文件内解析到唯一数组下标后，才允许与 citation 的 descendant `json_path` 等价；不得忽略 selector、跨文件匹配或通过模糊字符串匹配放宽评分。DOCX heading 与 paragraph 的更细等价关系本轮仅记录。
- 不修改模型、prompt、公共 API、知识文件、生产 citation 结构或全局 `min_score`。

### 8.3 验收与回滚

必跑：`make runtime-test`、`make eval-baseline`、`make integration`、使用 `LOCAL_LLM_ALLOW_FALLBACK=false` 的真实 `make eval-runtime-baseline`。

本轮通过条件：

- `unexpected_runtime_error` 保持为 `0`。
- `retrieval_recall_at_k` 不低于 `0.8571`，且 DOCX must-answer 不再全部因低置信度拒答。
- `citation_accuracy` 高于 `0.6`，并由 XLSX 同 sheet/range 与 JSON 同文件/id-selector 回归测试证明没有把错误 sheet、错误 policy、跨文件或不相交区域放行。
- `refusal_correctness` 高于 `0.6`，并保留至少一个正常必须回答的中文问题作为反例。

若真实 eval 任一核心指标退化、或泛词过滤导致已有 txt/md/json 正确命中退化，回滚对应 alias/stopword 规则；若只有评分器异常，回滚 locator equivalence，不改变 Runtime retrieval。

## 9. 2026-07-12 R3 第一轮结果

已完成：DOCX scope 内的中文/英文 lexical aliases、泛 CJK 词过滤、heading-only evidence refusal、XLSX cell/region 与 JSON id-selector 的严格 locator 等价评分。所有 locator 匹配都要求同一 citation 同时命中期望 filename 与结构定位；错误 sheet、错误 JSON policy、错误文件、跨 citation 拼接和不完整 range 均有回归测试。

验证：

- `make runtime-test`：`36 passed`。
- `tests/integration/test_eval_locator_grading.py`：`11 passed`。
- `make eval-baseline`：`40` 样本、`0` 失败。
- `make integration`：`28 passed`。
- 真实模型命令保持 `LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest`、`LOCAL_LLM_TIMEOUT_SECONDS=90`、`LOCAL_LLM_ALLOW_FALLBACK=false`。

真实 `llama3.2:latest` 结果与第 7 节基线对比：

| 指标 | R1 基线 | R3 第一轮 | 结论 |
| --- | ---: | ---: | --- |
| `retrieval_recall_at_k` | `0.8571` | `0.9714` | 提升；DOCX 不再低置信度拒答 |
| `citation_accuracy` | `0.6` | `0.9714` | 提升；XLSX/JSON 等价定位按结构严格恢复 |
| `refusal_correctness` | `0.6` | `1.0` | 提升；两条泛词假证据已拒答 |
| `unexpected_runtime_error` | `0` | `0` | 保持 |
| `grounded_correctness` | `0.5643` | `0.4857` | 退化，未通过整体质量验收 |
| `passed_count` | `24/40` | `24/40` | 未提升 |

结论：R3 第一轮的检索、引用和拒答子目标通过，但 R3 整体仍为 `in_progress`。DOCX 五题已取得正确 chunk 与 citation，仍被 `answer_point_miss` 计为失败，说明下一个瓶颈是 `llama3.2:latest` 对英文证据的跨语言生成/术语保持，而不是检索。后续不得通过放宽 answer grader 掩盖这一问题；应先验证 Agent Config 的 temperature/max-output 是否真正传入 Runtime，再用受控 prompt 约束或更适配中文的模型比较改善生成质量。

## 10. R3 第二轮：生成参数链路实验契约

当前发现：Agent Config 已在 Backend 版本化保存 `temperature` 与 `max_output_tokens`，但现有 chat dispatcher 没有把它们写进 Runtime 的既有 `response_overrides` 字段。因此上一轮真实模型结果混入了 Ollama serving 默认生成参数，不能作为 Admin Config 质量调优的可靠实验。

本轮边界：

- 不扩展公共 API 或 Runtime request schema；`response_overrides` 已是冻结契约。
- Backend 只从 conversation 绑定的 `config_version` 读取 immutable Agent Config，绝不读取当前 active config 作为替代。
- 找不到 config snapshot 时，拒绝 Runtime 调用与消息落库；不得 fallback 或静默参数替换。
- Eval runner 必须显式传入并在报告中记录 generation 参数。默认实验条件与新 repository 的 Agent Config 一致：`temperature=0.2`、`max_output_tokens=1024`；可以通过命令行或环境变量覆盖。
- `system_prompt_version` 暂不接动态 prompt registry，本轮不把它计为已生效。

验收：

- Backend 单测分别覆盖普通与 SSE request 都含会话版本的 `temperature/max_output_tokens`，以及缺失版本不调用 Runtime、不写消息。
- Eval 单测覆盖默认/显式 generation 参数进入 `/v1/chat/run` payload 与报告。
- `make backend-test`、`make runtime-test`、`make integration` 通过；再用关闭 fallback 的真实模型 eval 对比 R3 第一轮，报告必须显示实际 generation 参数。
- 不要求通过数必然提升；若 `grounded_correctness` 未改善，下一步是受控 prompt 或中文能力更强的模型比较，不修改 answer grader。

## 11. 2026-07-12 R3 第二轮结果与收口

Backend 已按 conversation 的 immutable `config_version` 读取 Agent Config，并将 `temperature/max_output_tokens` 传入普通与 SSE Runtime request。缺失 snapshot 会在调用 Runtime 和写入 exchange 前返回 `runtime_unavailable`；不会退回 current config 或 fallback。Eval runner 也已显式传递并报告同一组 generation 参数。

验证：

- `make backend-test`：通过。
- `make runtime-test`：`36 passed`。
- 两组 eval 回归：`18 passed`。
- `make integration`：`35 passed`。
- 真实 eval 条件：`llama3.2:latest`、`temperature=0.2`、`max_output_tokens=1024`、`LOCAL_LLM_ALLOW_FALLBACK=false`。

| 指标 | R3 第一轮 | R3 第二轮 | 相对 R1 基线 |
| --- | ---: | ---: | --- |
| 通过数 | `24/40` | `27/40` | 提升 `3` 条 |
| `retrieval_recall_at_k` | `0.9714` | `0.9714` | 高于 `0.8571` |
| `citation_accuracy` | `0.9714` | `0.9714` | 高于 `0.6` |
| `refusal_correctness` | `1.0` | `1.0` | 高于 `0.6` |
| `grounded_correctness` | `0.4857` | `0.5714` | 高于 `0.5643` |
| `unexpected_runtime_error` | `0` | `0` | 保持 `0` |

结论：`R3` 完成。检索、结构化引用、弱证据拒答和已保存的生成参数都已得到真实模型证据支持。剩余 `13` 个失败样本集中在 PDF/XLSX/PPTX 的 answer-point 与 `q_xlsx_001` / `q_pptx_005` 的 retrieval；它们是下一阶段的质量债，不应通过修改答案评分器掩盖。下一工作包为 `R5`：把本轮指标冻结为显式 quality gate，再决定 PDF/XLSX/PPTX 的下一轮检索或模型改进优先级。

## 12. 2026-07-12 R5 质量门复验

`R5` 将本轮真实模型条件冻结为 `data/eval-datasets/baseline/r3-llama3.2-quality-gate.json`。`make eval-runtime-gate` 只接受 `runtime-local`、`llama3.2:latest`、`temperature=0.2`、`max_output_tokens=1024`；runner 会先写 JSON/Markdown 报告，再按 gate 结果返回零或非零状态。这样模型、模式或 generation config 不匹配不会被误当成同一基线。

验收命令在关闭 fallback 的 Ollama 端点上通过：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
LOCAL_LLM_API_KEY=ollama \
LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest \
LOCAL_LLM_TIMEOUT_SECONDS=90 \
LOCAL_LLM_ALLOW_FALLBACK=false \
EVAL_TEMPERATURE=0.2 \
EVAL_MAX_OUTPUT_TOKENS=1024 \
make eval-runtime-gate
```

| Gate 条件 | 要求 | 本次结果 |
| --- | ---: | ---: |
| 通过数 | `>=26/40` | `27/40` |
| `retrieval_recall_at_k` | `>=0.95` | `0.9714` |
| `citation_accuracy` | `>=0.95` | `0.9714` |
| `refusal_correctness` | `=1.0` | `1.0` |
| `grounded_correctness` | `>=0.55` | `0.5786` |
| `unexpected_runtime_error` | `=0` | `0` |

结论：`r3-llama3.2-local-v1` 已通过。`13` 个失败仍全部以 `answer_point_miss`、`retrieval_miss` 与 `citation_mismatch` 的明细保留在本地报告中；失败计数按样本-失败类型统计，因此会大于失败样本数。后续优先修复 PDF/XLSX/PPTX 的证据与答案提取，不放宽 grader 或门槛。

## 13. 2026-07-15 K7 后 R5 回归归因

K7 配置实际应用完成后，以第 12 节完全相同的模型、temperature、max output 与 fallback-disabled 条件连续复验两次；两次均为 `26/40`，仅 grounded correctness `0.5429 < 0.55` 未过。最新 run 为 `runtime-local-20260715T084340Z`。

失败分布：

- `14` 个失败问题，产生 `answer_point_miss=14`、`retrieval_miss=6`、`citation_mismatch=2`。
- 格式集中在 DOCX `5` 题、PDF `5` 题、PPTX `2` 题、XLSX `2` 题；TXT、MD、JSON 与 must-refuse 全部通过。
- DOCX 五题已有正确 source/citation，中文回答也保留了主要事实，但当前 deterministic grader 用英文 expected point 做 token overlap，`CNY 800`、`3 business days`、direct manager/HR、same approval thread、finance leadership written exception 等跨语言术语未达到命中阈值。这延续了第 9 节已记录的生成/术语保持问题，不能把它重新归类为 retrieval miss。
- 六个 retrieval miss 仍是既有 PDF/XLSX/PPTX 质量债；retrieval recall 与 citation accuracy 都保持 `0.9714`，拒答与 runtime error 未退化。

K7 直接影响审计配置的证据检查：eval runner 的 request 仍只发送 `temperature=0.2` 与 `max_output_tokens=1024`；`response_style`、`top_p`、`top_k`、`rerank_enabled` 均未设置，`system_prompt_version` 走原有 bundled `prompt_v1` 默认值。因此当前没有证据把这次 `0.0357` grounded 波动归因于某个 K7 新参数。模型输出变动与基线余量过窄是待验证假设，不作为降低 gate 的理由。

下一步保持 answer grader 与 R5 阈值不动，在 K8 受控实验中二选一验证：让生成显式保留证据中的关键英文术语并附中文解释，或比较更适合中文问答的模型。任何方案都先冻结实验变量，再重跑 R5 与知识演进 fixture。

## 14. K7 Prompt V2 单变量实验契约

本轮先验证成本最低且可回滚的 prompt 方案，不切换模型、不修改 retrieval、不修改 dataset/grader/threshold：

- 对照组：`prompt_v1`；实验组：新增不可变 `prompt_v2`。
- 唯一语义差异：当问题与证据语言不同时，v2 仍以用户语言直接回答，但在 citation marker 前逐字附一个包含答案的完整证据短句；表格则附目标行的字段和值。不得翻译、改写或复制无关上下文。
- 固定变量：`llama3.2:latest`、temperature `0.2`、max output `1024`、相同 dataset、retrieval config、知识快照、评分器和 R5 阈值。`top_p`、response style、top-k、rerank 均保持未设置，避免混入第二变量。
- 版本边界：不改写 `prompt_v1`；eval payload/report/gate 显式包含 `system_prompt_version`；新 gate 使用新 id，旧 R3 gate 文件保留。
- 通过条件：严格 R5 所有检查通过，知识演进 fixture 通过，且 retrieval/citation/refusal/runtime error 不退化。若失败，保留实验报告但不把 v2 设为默认，不修改 gate。

实验结果：

- 弱提示探针只保留零散英文术语，DOCX `0/5`，被否决；v2 随后收紧为逐字附一个完整证据短句或目标表格行。
- 完整 40 题第一次：`30/40`，grounded `0.6714`；第二次：`28/40`，grounded `0.5857`。两次均通过 `k7-llama3.2-prompt-v2-local-v1`。
- 两次 retrieval recall/citation accuracy 均为 `0.9714`，refusal correctness `1.0`，unexpected runtime error `0`，证明改进没有通过牺牲检索、引用或拒答换取。
- 结论：保留并登记 `prompt_v2`，`prompt_v1` 不变；K7 quality blocker 解除。剩余 PDF/XLSX/PPTX retrieval/answer debt 进入 K8。
