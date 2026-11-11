# 本地全栈语义检索切换计划

## 目标

将本地 `fullstack_dev` 的**新会话**从 `lexical_compat_v1` 切换为 `native_dense_v1`，使用已校准的 Ollama `qwen3-embedding:0.6b`（`1024` 维）、generation `qwen3:14b` 和独立 Qdrant collection。切换验证通过后，退役 `llama3.2:latest` 与产品请求中的 lexical strategy；历史 lexical conversation、snapshot、citation、experiment/report 与 quality release 只作为只读审计事实保留。

## 边界与决策

- Runtime 是唯一的 dense projection writer；Backend/Postgres 是 revision、snapshot、retrieval config、Q1 experiment/release 的唯一权威。
- 新 collection 为 `see_agent_dense_qwen_chunks_v1`，绝不复用现有词法 `see_agent_chunks`（size=1）。
- 不改写已发布 revision；对现有本地 handbook 通过 reindex 创建新的 immutable revision，并发布包含它的新 snapshot。
- Runtime profile、retrieval config、embedding model 必须 exact match：`native_dense_v1` / `qwen3-embedding:0.6b` / `1024`。不匹配、embedding/Qdrant 故障或 scope 错误均 fail-closed，绝不回退 lexical。
- generation config 也必须 exact match：新 Q1 pair 使用 `qwen3:14b` 与新的不可变 prompt version；历史 `llama3.2:latest` 报告只能审计，绝不可作为 Qwen quality evidence。
- 本次是本地 fullstack cutover，不构成 production-wide 质量批准；生产仍需要真实业务 onboarding、至少 80 条冻结发布集与独立 staging 观察期。
- 「删除 lexical」不删除历史记录、Q1 report 或 Qdrant 的旧派生 collection；它的含义是移除 Runtime/Backend/Web 的 lexical normal-request factory、配置选项与默认值。旧 conversation 的新知识请求因此明确失败（不允许通过 dense 假装重放），历史消息仍可阅读。

## 执行与验收

1. 更新 fullstack 默认环境与 preflight：native dense 强制要求全部 `EMBEDDING_*`、独立 collection，缺少任一项拒绝启动。
2. 用真实 Ollama + Qdrant 跑 native dense provider smoke，确认模型维度、collection schema 与 server-side revision filter。
3. 重启 Runtime 为 dense profile；通过管理 lifecycle 对当前 local handbook reindex，等待 ready revision，再发布 successor snapshot。
4. 为新 snapshot 创建已审核 onboarding cases：至少三条 must-answer 和三条 must-refuse；保持 draft，不能在 lexical active release 与 dense Runtime profile 不一致时冻结或运行。
5. 创建语义检索 Q1 activation experiment；固定 snapshot、dataset hash、完整 generation/retrieval 条件。此 local activation 的 baseline/candidate 可以是同一 immutable dense config 的独立真实运行（它验证发布链路而非伪造 lexical 对比）；只有 candidate gate 通过后才审批并 forward publish dense release。
6. dense release 成为 active pair 后，冻结并运行第 4 步 onboarding dataset；它才会将正确的 dense config/retrieval pair 固化进 run evidence。所有 case 必须通过且记录 citation/provenance/execution evidence。
7. 用新 conversation 验证 native dense 成功回答与 citation anchor；旧 lexical conversation 验证 profile mismatch fail-closed。停止 embedding/Runtime 验证没有 lexical fallback，恢复后重新 ready。
8. 仅在第 1–7 步全部通过后执行 lexical retirement：删除 lexical profile/factory 与可创建的 lexical config，更新空环境 bootstrap 默认值为 Qwen dense，并用旧 conversation 的 fail-closed 断言和全栈成功链路回归验证。不得清理 Postgres 中的历史 config/release/evidence，也不得改写既有 citation。
9. 最后确认没有 active config、Runtime 环境、启动命令或 smoke 默认值引用 `llama3.2:latest`，执行一次 Qwen fullstack restart/chat smoke 后，再 `ollama rm llama3.2:latest` 并验证 Ollama 列表中不存在旧模型。

## 回滚

在 lexical retirement 前，不删除 dense collection 或新 revision。若 dense gate、运行或观察失败，停止在 Qwen candidate，保持当前 lexical release；不删除 `llama3.2:latest`。lexical retirement 后回滚只能是发布先前已验证的 Qwen dense pair 或关闭知识聊天 capability，不能重新启用已删除的 lexical factory。

## 当前状态

- 状态：`local_dense_cutover_verified`
- 前置：K8-R1/R2/R3、K8-R4a、Q1、F1-F8 local fullstack 已验证。
- `2026-07-27` 已创建的 `qexp_004` baseline remote report 以 `4` 条 `unexpected_runtime_error` 失败并保留。原因不是模型或检索质量：eval runner 接受缺少 `knowledge_scope.resolved_at` 的 scope file，Runtime strict schema 正确以 `422` 拒绝每个请求。该 report 不能删除、改写或作为通过证据；已绑定 baseline 的 frozen `qexp_004` 不得重用。
- 修复顺序：先使 remote scope loader 对 `snapshot_id`、`manifest_hash`、`revision_ids`、`resolved_at` 执行与 Runtime 相同的 fail-fast 校验，并以回归测试覆盖缺字段；修订本地 `snap_002` scope file。随后创建 successor Q1 experiment，使用相同 immutable dataset/snapshot/config/conditions，重新执行两次真实 remote report。只有 successor candidate gate 通过，才可 publish dense release。
- `2026-07-27` 的 successor `qexp_005` 已冻结并以相同 immutable scope/config/conditions 完成 baseline remote run；报告 `runtime-remote-20260727T052024085663Z` 以 `4` 条 `unexpected_runtime_error` 失败并保留。Runtime 已完成真实 scoped native-dense retrieval 且调用真实 `llama3.2:latest`，但模型输出没有任何可解析 `[[cite:<rank>]]` marker；strict response contract 因此以 `503 model_answer_contains_no_valid_citation_anchors` 拒绝。该失败不得转换为回答、拒答、citation 或 Q1 passing evidence，也不得重用 `qexp_005`。
- `2026-07-27` 的非 Q1 remote probe `runtime-remote-20260727T052959257481Z` 固定 `prompt_v3`，验证了真实 marker/anchor、无证据 disposition、零伪造 citation 和 `fallback_used=false`：`4/4` sample 行为通过、retrieval/citation/refusal 均为 `1.0`。但 `dense_handbook_003` 只陈述“财务主管书面批准时可以”，遗漏相同证据中的默认规则“缺少发票默认不予报销”；grounded correctness 为 `.8333`，低于 gate 的 `1.0`。该 probe 不能作为 Q1 approval evidence，`prompt_v3` 不得改写。
- `2026-07-27` 的 `prompt_v4` probe `runtime-remote-20260727T053241261994Z` 失败：`3/4`，`dense_handbook_003` 再次没有有效 marker，产生 `503`；直接向同一 `llama3.2:latest` 发送最小重现时，它还把已有的“默认不予报销，除非财务主管书面批准”误判为 `insufficient_evidence`。这是本地 3.2B 生成模型无法稳定遵循受限回答协议的 evidence，不是可由 Runtime 自动补 citation 的错误。
- 已执行安全恢复：Runtime 已切回 Backend 当前 active release 的 `lexical_compat_v1 / see_agent_chunks`，Backend 与 Runtime `/readyz` 均返回 ready。dense collection、snapshot、candidate configs、`qexp_004/qexp_005` 和所有失败报告均保留，未发布 dense、未写入伪造回答或引用。
- 模型决策（`2026-07-27`）：本机为 `24GB` 内存、约 `700GB` 可用磁盘。首个 generation 主候选固定为 Ollama `qwen3:14b`（Q4 artifact 约 `9.3GB`）；它是本项目 dense/Q1 的唯一下一安装目标。对照候选为 `deepseek-r1:14b`（DeepSeek-R1-Distill-Qwen-14B，约 `9.0GB`），用于后续复杂规则推理与跨项目通用能力对比；两者可共存于磁盘，但全栈日常一次只加载一个。各 Qwen3 尺寸的内存边界、预期收益和排除理由以 [Harness §12.5](../../harness/single-agent-v1-harness.md) 为准；参数量或磁盘余量都不能替代 Q1 gate。
- `2026-07-27`：`qwen3:14b` 已完整下载并注册为本机 Ollama 模型（`9.3GB`）。实际 `/api/chat` provider smoke 在温度 `0`、`/no_think` 下返回“模型已就绪。”；首次加载约 `10.4s`、整次请求约 `25.9s`。这只证明本机模型可加载与响应，不构成 Q1 或 dense 发布证据。当前 active Agent Config 仍引用 `llama3.2:latest`，因此在 Qwen 通过 Q1 并 forward publish 新配置前不得删除旧模型，也不得用同名 alias 伪装替换。
- `2026-07-27` 当前权威运行事实：Backend active release `qrel_001` 指向 `cfg_004/llama3.2:latest + rcfg_lexical_v2/lexical_compat_v1`；Runtime 也实际运行 `lexical_compat_v1 / see_agent_chunks`。因此 lexical 仍存在且是 live normal-request path，并非已删除。`qexp_004/qexp_005` 都保持 evaluating/failed evidence，绝不可重用。
- `prompt_v5` 与其 non-Q1 remote probe gate `snap-002-qwen3-14b-dense-prompt-v5-probe` 已冻结。它固定 Qwen generation、现有 dense embedding/snapshot/dataset 和 strict `4/4` retrieval/citation/refusal/grounded 门槛；probe 只决定是否有资格创建新的 Q1，不会写入 Q1 report 或改变 active release。
- `2026-07-27`：Qwen `prompt_v5` non-Q1 probe `runtime-remote-20260727T130419341427Z` 通过 `4/4`。随后冻结的 `qexp_006` baseline 也通过 `4/4`，但 candidate `runtime-remote-20260727T131641156395Z` 为 `0/4`，所有样本返回严格 `503`，无可写入的 answer/citation；该 report 已登记且 experiment 为 `review_ready`，不得批准、发布、删除或重用。失败发生在 `openai_compatible` adapter 路径，不能被认定为 dense/retrieval 失败或通过降低 anchor 规则修复。
- 修复决策：新增显式 `ollama_native` adapter，原生 `/api/chat` 请求使用 `think=false`，thinking trace 绝不参与正文或 citation。该 adapter 是新的 deployment condition；为防止 v5/Q1 证据混淆，后续创建 `prompt_v6`、新的 Agent Config、new non-Q1 probe 和新的 Q1 experiment。不得重用 `qexp_006` 的任何 baseline/candidate report。
- `2026-07-27`：`ollama_native` adapter 已通过 Runtime/preflight 回归；live Runtime 诊断已显示 `provider=ollama_native thinking=disabled`，一条真实 scoped request 返回 `provider=ollama_native`、`fallback_used=false`、valid anchor/citation。新 Agent Config 为 `cfg_006/qwen3:14b/prompt_v6`；其 probe gate 是 `snap-002-qwen3-14b-dense-prompt-v6-native-probe`。两者都仍是 candidate，未改变 `qrel_001`。
- `2026-07-27`：`prompt_v6` native probe `runtime-remote-20260727T133111607009Z` 的功能判定为 `4/4`、retrieval/citation/refusal 均为 `1.0`，但严格 gate 失败：`dense_handbook_003` 只回答书面批准的例外，遗漏证据中“默认不予报销”，故 grounded correctness 为 `0.8333 < 1.0`。此 report 是不可通过证据，不能创建 Q1、批准、发布或降低阈值。
- 修复决策：新增 `prompt_v7`，仅强化例外问题的强制输出顺序（先默认规则，后例外条件）；新的候选 config 采用温度 `0` 以降低本地生成漂移。它必须先通过独立 native probe，再创建独立 Q1 experiment；不得修改或重用 `cfg_006` / v6 report。
- 下一动作：跑 `prompt_v7 + qwen3:14b + ollama_native/think=false` non-Q1 probe。只有 probe、两份新的 Q1 report、approval/publish 和全栈聊天均通过，才进行 lexical retirement 和 `llama3.2:latest` 删除。`deepseek-r1:14b` 只在 Qwen 主候选完成首轮结果后，按完全相同条件做独立模型对比。
- `2026-07-28`：`prompt_v7` probe 第二次真实运行 `runtime-remote-20260727T134304965909Z` 通过 `4/4` 且 strict metrics 均为 `1.0`；第一次 scoped-retrieval `503` report 保留为可靠性证据，未降低门槛。独立冻结实验 `qexp_007` 使用 `cfg_007/qwen3:14b/prompt_v7/temperature=0 + rcfg_002/native_dense_v1` 收集 baseline `runtime-remote-20260727T134546693103Z` 与 candidate `runtime-remote-20260727T134651846930Z`，均通过后批准并 forward publish 为 `qrel_002`。
- `2026-07-28`：lexical normal-request path 已退役：Runtime 仅允许 `native_dense_v1` profile，不再映射 `RUNTIME_RETRIEVAL_MODE` 或构造 lexical factory；Backend/Web 不可创建或选择 lexical config；历史 lexical config、conversation、report 与 citation 仅只读审计。direct Runtime historical lexical request 以 `503 retrieval_unavailable` fail-closed。fresh-install defaults、preflight、local smoke 均改为 Qwen native dense。
- `2026-07-28`：本机 `llama3.2:latest` 已通过 `ollama rm` 删除。删除后的 fullstack preflight、Runtime direct request 与 normal-user Backend chat `conv_052` 均验证 Qwen 回答、citation/provenance 完整且 `fallback_used=false`。首个 cold Qwen Backend request 曾返回 `503`，随后 Runtime 加载完成且重试成功；后续可靠性工作应将 warm-up/timeout 纳入 readiness，而不是恢复任何 fallback。
