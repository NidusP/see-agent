# 单智能体 V1 K8-R3 Dense 证据边界校准与知识库切换设计

## 文档控制

- 状态：`active / calibration_verified_business_release_pending`
- 更新日期：`2026-07-18`
- 工作包：`K8-R3`
- 依赖：`K8-R2` Qwen candidate、`Q1` 质量运营与受控发布、K4/K5/K6 知识生命周期

## 1. 用户目标

让管理员能理解每个参数如何帮助 Agent “基于知识准确回答或在无依据时拒答”，并在固定测试知识上完成可复现的 dense 校准。只有达到质量门槛后，才允许以不中断服务、不改写历史证据的方式退役产品内的测试知识并导入新的业务知识。

测试语料和评测数据集是长期质量基准，不等同于产品内可删除的测试文档；删除产品知识不会删除 `data/eval-datasets/**` 中的 fixture、报告或评测规则。

## 2. 准确回答与拒答的工作原理

```text
用户问题
  → 在当前 published knowledge snapshot 内检索 top_k 个 chunk
  → 仅当最佳证据分数 ≥ evidence_min_score 时，交给模型生成带 citation 的回答
  → 否则清空生成上下文，返回 refusal=true / retrieval_low_confidence
```

这不是让模型“凭感觉谦虚”。拒答由 Runtime 在生成前根据冻结的证据边界作出；模型只对通过边界的证据组织回答。`must-answer` 检验正常知识是否仍可检出，`must-refuse` 检验无关文本不会因表面语义相似而被冒充为证据。

## 3. 参数学习卡与所有权

| 参数 | 作用 | 不应误解为 | 当前/目标所有者 |
| --- | --- | --- | --- |
| `knowledge_snapshot` / manifest | 固定本次可检索的 revision 集合 | 模型记忆或全库搜索 | K4/K5 Postgres publication |
| `retrieval_strategy` | 选择经注册的检索实现 | 任意插件或自动 fallback | immutable retrieval config / Runtime profile |
| `embedding_model` / dimension | 把问题与 chunk 映射到同一向量空间 | 可与另一模型 collection 混用 | provider probe、Qdrant schema、retrieval config |
| `top_k` | 送入生成的最高排序证据条数 | 质量或置信度阈值 | immutable retrieval config |
| `rerank_enabled` | 是否应用已注册的二次排序 | 当前 dense 证据可靠性修复 | immutable retrieval config |
| `evidence_min_score` | 最佳检索证据低于它即拒答 | 跨模型通用的“0.5 就安全”规则 | **K8-R3 新增 immutable retrieval config** |
| `system_prompt_version` / style | 约束如何基于证据表达、引用、说明不足 | 替代检索或补齐缺失知识 | immutable agent config |
| `temperature` / `top_p` / output budget | 控制表达随机性与长度 | 提高知识真实性 | immutable agent config |

管理台必须为这些字段提供中文说明、当前生效值、候选/已发布状态与对应 experiment/report 链接。高级参数不能绕过 Q1 直接写 active release。

## 4. `evidence_min_score` 契约与边界

当前 Runtime `RetrievalPipeline` 的 `.15` 是未版本化内部默认值。K8-R3 将它提升为 `evidence_min_score`：正有限、显式传入 Runtime response override、写入 retrieval config、execution evidence 和 eval report。缺失、非法、request/profile 不一致时 fail-closed；不得用旧 `.15` 静默兜底 native dense request。

该分数只可在相同 embedding model、collection、chunk/index version、strategy、scope 和相同相似度定义下比较。切换 embedding model、chunking、reranker、Qdrant distance 或知识分布后，必须重新评测；不能复制 Qwen 的阈值给另一个模型。

K8-R3 仅改变这个变量：固定 `qwen3-embedding:0.6b`、1024 dimension、native dense profile、同一 K8-R2 scope、`prompt_v2`、`llama3.2:latest`、`top_k=3`、rerank=false、grader 与 40 条 dataset。不得同时引入 sparse/hybrid/rerank/prompt/model/chunk 变体。

## 5. 校准实验状态与决策

1. 从 Q1 创建 baseline/candidate experiment，冻结 dataset SHA、scope/manifest、config pair、candidate threshold 列表和 gate。
2. Runner 为每个样本记录 top score、是否命中 `evidence_min_score`、refusal、citation 与 failure class；先形成 must-answer/must-refuse score distribution。
3. 比较多个候选阈值。候选必须同时满足：5 条 must-refuse 全部 refusal-correct、must-answer 不出现检索/引用回归、原有 prompt_v2 gate、知识演进/C1/C2 fixture 与延迟上限通过。
4. 将唯一胜出的值固化为 candidate retrieval config；报告 exact-match 后由 Q1 approval/publish 只影响未来 conversation。

如果没有任何单阈值可同时满足门槛，实验结论是“单阈值不够”，而不是降低 refusal 标准或发布 Qwen。下一独立设计才可考察 score margin 或 reranker。

### 5.1 `2026-07-18` 固定 Qwen 校准证据

契约已实现为 Postgres migration `013`、Backend retrieval config/Runtime override/execution evidence、Web 参数学习卡和 eval report；native dense 缺失该字段会 fail-closed。runner 也会同时保存以 `run_id` 命名的报告和 `latest` 便利指针，避免后续比较覆盖证据。

在固定 seven-format scope、`qwen3-embedding:0.6b`（1024 dimension）、`prompt_v2`、`llama3.2:latest`、`top_k=3`、rerank=false 条件下，`0.15` 对照的 35 条 must-answer top score 最低为 `0.5237`，5 条 must-refuse 最高为 `0.5020`。选择位于空隙内的 `0.51` 后，两次 candidate 均为 must-refuse `5/5`、无 must-answer 误拒、retrieval/citation `1.0`；最新保留报告为 `30/40`、grounded `0.6429`、refusal `1.0`、runtime error `0`，并通过既有 `prompt_v2` quality gate（grounded 下限 `0.55`、passed 下限 `26`）。报告副本在 `data/eval-datasets/reports/runtime/k8-r3/qwen3-embedding-0.6b/threshold-0.51-verified/`。

此结论只验证**实验 fixture 的阈值校准**，不等于业务 dense cutover 或 Q1 business release：fixture collection/snapshot 不能成为产品 active release。实际业务知识上传始终要以其真实 snapshot 完成 onboarding eval；只有同时切换到 native dense profile、embedding/index 或证据边界时，才须以真实 collection/snapshot 创建 Q1 candidate/release。因此不得把 `0.51` 复制给不同 embedding、chunk/index、reranker 或业务知识分布而跳过评测。

## 6. 测试知识退役与新知识导入

允许删除的前提不是“某个数字看起来合理”，而是：新业务知识的 revision 已全部 `ready`，管理员已审阅新知识的最小 onboarding eval，并且 replacement 后的 active retrieval release 已通过其适用的质量门。若只替换知识、保持当前 lexical release，则沿用已发布 lexical config/gate，不要求 dense Q1；若同时切换 native dense profile、embedding/index、reranker 或 evidence boundary，则必须先通过第 5 节的 Q1 quality release。

安全顺序：

```text
上传新业务 document/revision
  → K3 投影 ready 且 manifest 校验
  → 发布包含旧+新文档的 successor snapshot
  → 用新 snapshot 执行 onboarding eval / 人工带 citation 验收
  → 逐个 disable 测试 document，发布只含新知识的 successor snapshot
  → 验证旧 document 不再进入 citation、freshness scan 完成
  → 再 delete 已 disable document（历史 message/provenance 保留）
```

禁止先删除最后一个 active document，否则 K5 会正确返回 `knowledge_snapshot_unavailable`，造成知识问答中断。删除是 lifecycle 的 soft-delete/retention 语义；历史回答、citation、eval fixture 和 Qdrant 派生清理由既有 K4/K6 策略处理，不可用数据库 reset 替代。

## 7. 验收与回滚

- 参数解释：Web 能看到每项参数的中文作用、风险、当前 release/experiment/evidence；未知或不完整配置不能发布。
- Runtime：boundary 只取 request 中 immutable `evidence_min_score`；低于边界拒答，高于边界才生成；缺失/非法值 fail-closed。
- 校准：Qwen baseline/candidate 条件、scope/hash、报告和 approval 完整；must-refuse 为 `1.0` 且既有质量门无回归。
- 新知识：七格式至少各一个 onboarding sample，new-only snapshot 的回答皆引用新 revision；测试 document 不在新 citation；旧回答仍可查看 source/provenance。
- 回滚：质量问题以 Q1 forward rollback 回到上一 release；知识问题可在删除前停止 disable，或发布含旧 revision 的 successor snapshot。不得改写旧 conversation。

## 8. 非目标

- 自动根据单次上传或单条反馈修改阈值；
- 删除评测 fixture/报告来提高分数；
- dense cutover 前移除 lexical compatibility；
- 在尚无 ready 新 snapshot 时清空产品知识库。
