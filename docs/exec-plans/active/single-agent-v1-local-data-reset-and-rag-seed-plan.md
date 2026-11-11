# 单智能体 V1 本地数据重置与语义 RAG 种子计划

## 文档控制

- 状态：`in_progress`
- 日期：`2026-07-28`
- 触发：清除开发阶段遗留的 lexical/fixture 数据，以 Qwen native dense 建立新的可追溯本地 RAG 基线。
- 关联：[fail-closed execution 设计](../../design-docs/single-agent-v1-fail-closed-execution-design.md)、[K4 生命周期 API 设计](../../design-docs/single-agent-v1-k4-lifecycle-api-design.md)、[本地 dense cutover 计划](single-agent-v1-local-dense-cutover-plan.md)。

## 1. 目标与边界

本次仅重置 `see-agent` 本机 Docker Compose 开发栈的可再生持久数据：PostgreSQL、Redis、MinIO 和 Qdrant 的 compose named volumes。它会删除旧会话、账号 session、知识文档/revision/snapshot、摄取任务、质量实验/发布记录、对象和向量；不会修改源码、Git 工作区、`.env`、仓库外 secret、Ollama 模型或其他 Docker project/容器。

旧数据包含开发期间的 lexical fixture、Llama 关联 evidence 与历史对话，不可作为 native dense 产品质量的证明。清除后不得由 Backend seed 再次自动写入 `employee-handbook.txt` 或任何知识 chunk；新库在管理员按 lifecycle 发布前必须保持无 active snapshot，并让聊天 fail-closed。迁移 `022` 同时移除没有任何历史引用的 `rcfg_lexical_v2`：它不改写已有 release、conversation、experiment 或 report，因而保留存量审计；新库则不会让历史 lexical bootstrap 参与 active release。

## 2. 新语料与预期能力

种子语料放在 `data/local-dev-knowledge/v1/`，为无敏感信息的 Markdown 制度示例：

| 文档 | 覆盖主题 | 代表性验证问题 |
| --- | --- | --- |
| `差旅与费用管理制度.md` | 10 日报销时限、金额审批、住宿例外 | “差旅结束后多久提交报销？超过 10,000 元谁审批？” |
| `信息安全与访问管理规范.md` | 事件上报、账号与访问控制 | “疑似账号泄露应在多久内上报？” |
| `请假与远程办公制度.md` | 请假提前申请、远程办公约定 | “连续请假三天需要提前多久申请？” |

这些文档仅用于本地开发和 onboarding，不代表真实企业制度。它们将通过管理员 `upload -> K3 indexing -> ready -> explicit publish` 写入，而不是直接插入 Postgres/MinIO/Qdrant。每次 publish 产生不可变 snapshot；最后一个 active snapshot 必须包含三份 revision，并使用 `native_dense_v1`、`qwen3-embedding:0.6b`。

## 3. 执行序列

1. 停止 Backend/Runtime，确认没有应用进程继续写入。
2. 以本仓库 compose 文件执行 `down -v`，再检查项目 volume 已不存在；不得使用宿主机范围的 Docker prune。
3. 启动 compose 依赖，运行 fullstack preflight，启动 Runtime 与 Backend；记录 readiness 与启动诊断中的 Postgres、Redis、MinIO、Qdrant、Qwen generation/embedding provider。
4. 运行 `make bootstrap-rag-knowledge`。该命令读取三份版本化 Markdown 文档，对每份执行 upload、等待 ready、explicit publish；失败立即退出，绝不伪造 ready/snapshot/citation。
5. 以普通用户新建会话验证三类明确问题各有真实回答与 citation；再验证一个非知识问题受控拒答且没有知识 citation。读取 active snapshot 与 Runtime diagnostics，确认均为 Qwen native dense。

## 4. 验收与失败处理

- 清除后的首次 Backend 初始化只 seed 最小 Agent/retrieval config，`knowledge_files`、documents、revisions、snapshots 和 chunks 均为空。
- 未运行种子前，知识聊天返回 `knowledge_snapshot_unavailable`，不能回落至全量检索、旧 fixture 或无引用生成。
- 三份语料每份均产生 `ready` revision 与可查询 ingestion/delivery evidence；最终 snapshot 有三个 document members。
- 每个代表性问题的 citation filename/revision 必须属于新语料；没有 citation、错误 scope、模型/embedding/profile 不匹配或 Runtime 故障一律视为失败，不做兜底。
- 若任一导入失败，保留失败 lifecycle evidence，修复依赖或文档后用新的 idempotency key 重试；不得手工补数据到任一存储。
- K3 relay 使用 `XADD -> authoritative streamed` 的 crash-safe 顺序。消费者若在两者之间读到 `pending/retry`，必须短暂重试且绝不 ACK；仍未完成状态转移时保留 Stream pending entry 以供 reclaim。该规则防止“revision 永远 queued、但消息已被确认”的静默丢任务。

## 5. 完成记录

- `2026-07-28`：仅删除并重新创建 `compose_postgres-data`、`compose_redis-data`、`compose_minio-data`、`compose_qdrant-data`；未触碰仓库、secret、Ollama 模型或其他 Docker project。最终空库检查为 `knowledge_files/documents/revisions/snapshots/conversations = 0`。
- 已移除 `PostgresStore.seedDefaults` 自动写入的 `employee-handbook` 文件、task 和 chunk。新库只保留 `cfg_001/qwen3:14b/prompt_v7` 与 `rcfg_001/native_dense_v1/qwen3-embedding:0.6b`；migration `022` 只删除无引用的 lexical bootstrap config，不改变历史引用。
- 修复 K3 `XADD -> streamed` 状态转换竞态，并以 `TestRevisionDeliveryWorkerDoesNotAckBeforeRelayMarksStreamed` 回归覆盖：消费者在 outbox 仍为 `pending/retry` 时不再确认消息，避免 revision 被永久遗留在 `queued`。
- 三份初始语料均经管理员 upload、Runtime indexing、system-admin explicit publish；为改善组合查询覆盖，差旅制度已产生一个 replacement revision，最终 active snapshot 为 `snap_004`，含三个 document members。Postgres 状态为 `3` 个 published revision、`1` 个 superseded revision；Qdrant `see_agent_dense_qwen_chunks_v1` 有 `44` 个 1024 维 Cosine points。
- 最终 Runtime diagnostics：`retrieval=native_dense_v1`、`embedding=qwen3-embedding:0.6b`、`generation=ollama_native`；不存在 lexical 或 heuristic fallback。
- 端到端验收：差旅组合规则正确回答“10 个自然日 + 直属负责人/部门负责人/财务负责人三者”；账号泄露正确回答“30 分钟内通过安全事件渠道上报”；连续请假三天正确回答“提前 5 个工作日”；均带 `snap_004` citation。杭州天气问题返回 `finish_reason=refusal`、`knowledge_used=false`、无 citation。
- 回归：`make backend-test` 全通过；`make runtime-test` 为 `117 passed`；`node --check scripts/ops/bootstrap_rag_knowledge.mjs` 通过。
- 已知运行风险：本机 Ollama/Qwen 首次或间歇请求曾返回 `503 runtime_unavailable`，同一真实请求重试后成功。系统没有生成或引用 fallback；后续应将模型 warm-up/readiness 与超时指标纳入部署可靠性工作包。
