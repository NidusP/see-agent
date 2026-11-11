# 单智能体 V1 知识演进执行队列

## 文档控制

- 状态：`active`
- 版本：`v1.6`
- 更新日期：`2026-07-30`
- 目的：把“管理员持续上传知识、可靠发布 RAG、历史回答可解释且可核验”的目标拆成唯一可执行的工作队列。
- 上位事实源：[产品交付计划](single-agent-v1-product-delivery-plan.md)；状态机和一致性语义以 [知识演进设计](../../design-docs/single-agent-v1-knowledge-evolution-design.md) 为准。

## 1. 目标与边界

### 目标

- 管理员可以新增、替换、禁用和删除逻辑文档；每次内容变更产生不可变 revision。
- 只有完整索引的 revision 才能进入已发布 snapshot；失败、重复或乱序任务不能影响当前可回答知识。
- 每条助手回答记录实际 snapshot、revision 和 citation；后续知识更新可标记潜在过期并用最新知识独立核验。

### 本轮不做

- 多租户、细粒度 RBAC、外部 SaaS、微调、Kafka、Temporal、LangChain/LlamaIndex 和多智能体编排。
- 自动重写或覆盖历史回答。核验始终产生新的结果记录。
- 因“清理旧向量”失败而推迟 disable/delete 的可见性变更。

## 2. 已冻结的执行规则

1. PostgreSQL 是 document、revision、snapshot、update event、outbox 和回答 provenance 的唯一业务写入权威；MinIO、Qdrant、Redis 都是派生或投递系统。
2. 普通新问题在每条用户消息开始时读取最新 `published` snapshot。配置版本仍按 conversation 固定，知识 snapshot 不再永久固定在 conversation。
3. Runtime 只能检索请求指定 snapshot 的成员 revision；不能因 Qdrant、Redis 或对象存储暂时落后而静默回退到其他 snapshot。
4. snapshot 发布由 Backend 的单一事务写入路径串行控制：验证所有成员 `ready`，写 manifest/event/outbox，再原子切换 active pointer。
5. shared contract、Postgres migration、状态转换和 active pointer 由一个 owner 串行修改。没有冻结 API 契约前，不并行改 Backend、Runtime 和 Web 的共享字段。

## 3. 工作包总览

| 工作包 | 状态 | 依赖 | 产出 | 退出证据 |
| --- | --- | --- | --- | --- |
| `K1` 契约与详细设计收口 | `verified` | `KV0` | revision/snapshot/event/provenance DTO、状态转换、旧 API 映射 | Runtime `41 passed`、Backend test、OpenAPI YAML parse 与 integration `41 passed`；未引入业务实现。 |
| `K2` 权威数据与迁移 | `verified` | `K1` | versioned migration、repository write path、outbox、旧数据迁移策略 | `make postgres-k2-smoke`、开发库升级 smoke、Backend/Runtime/integration 回归通过；后续 K3-K5 作为独立工作包消费其权威写路径。 |
| `K3` 可靠摄取与索引投影 | `verified` | `K2` | Redis Streams consumer group、retry/DLQ、revision-aware Runtime/Qdrant 写入 | 重复、乱序、失败和 pending recovery 不发布错误 snapshot。 |
| `K4` 管理员知识生命周期 | `verified` | `K2`、`K3` | revision upload/publish/disable/delete API 和管理界面 | 管理员能完成一次新增、替换、禁用与删除，不影响历史审计。 |
| `K5` 回答 provenance 与快照检索 | `verified` | `K3`、`K4` | per-message snapshot/revision citations、snapshot scoped retrieval | 新问题只引用 active snapshot；旧回答能解释知识时点。 |
| `K6` 时效、日历与核验 | `verified` | `K5` | update calendar、stale detector、revalidate API/UI | 更新后能标记受影响回答并生成独立核验结果。 |
| `C1` 回答内证据锚点 | `verified` | `K5` | citation anchor 契约、持久化、Runtime marker parser、Web hover/focus/固定详情 | 有效锚点精确关联到本次 citation；旧消息和缺锚点回答不伪造精确依据。 |
| `U3` 聊天优先壳层与引用抽屉 | `verified` | `C1` | 可收缩会话侧栏、管理员顶栏工作区切换、可关闭 citation detail 抽屉 | 普通用户不见管理入口；回答阅读列不被常驻检查器挤压；锚点 hover 预览和点击详情均可键盘、桌面和移动端使用。 |
| `K7` 管理证据与配置闭环 | `verified` | `K4`、`K6` | revision/snapshot/calendar/log/eval 管理视图；配置真实生效 | 管理员可回答“何时发生什么、影响哪些回答、使用什么配置”。 |
| `C2` 有界会话意图路由 | `verified` | `K5`、`K7`、`U3` | Backend 保守意图路由、Runtime 受控回复、Postgres execution facts、Web 标签 | 问候可正常回复；混合/未知输入仍走 RAG；越界闲聊被引导；不伪造知识证据。 |
| `C3-R2` 统一会话上下文路由 | `verified` | `C2`、`K5`、`K7`、`A1` 设计 | 模型主导的 bounded-history intent/route resolution、`rag`/controlled/clarification execution、resolved query/evidence；删除 C3-R1 phrase confirmation；`conversation_router_v1` evidence、router fixture、真实 SSE/browser 成功与失败路径均已验收 | 业务问题必经 scoped RAG；非业务仅受控短回复；新话题不污染；不确定时澄清；不伪造引用或 direct business answer。 |
| `A1` 澄清、回答完整性与引用可信度 | `planned` | `C1`、`C2`、`K5`、`K7`、`Q1` | scope/coverage disposition、pending clarification/resolved query、provider evidence、retrieval candidate/answer citation 分层 | 模糊问题先澄清；补齐后正确检索；部分回答不凑数；真实 provider 可证明并经 Q1 发布。 |
| `K9-S0` 会话归属与越权隔离 | `verified` | `K7`、`C2` | conversation owner、legacy backfill、owner-filtered chat API 与 Web 恢复提示 | 两个 subject 的 list/detail/message/source/revalidation 隔离；admin evidence 仍可全局审计。 |
| `K8` 原生检索质量与模型比较 | `in_progress` | `K5`、`K6` | `K8-R1` native dense 已验证；`K8-R2a` 先闭合受控策略注册与配置/evidence；后续 R2 模型比较、sparse/hybrid、rerank 与七格式质量收口 | R5 gate 和知识演进 fixture 持续通过；策略/profile 与模型比较报告可追溯且不混入 R1 功能验收。 |
| `K9` 可靠性与内部试用 | `in_progress` | `K7`、`K8` | security、回滚、部署、队列恢复与能力契约 | K9-S0 已验证；其余门槛通过后进入内部试用。 |

## 4. 详细执行顺序

### K1: 契约与详细设计收口

- Owner：`p`。此包开始前不分配共享 schema 修改。
- 范围：更新 Backend/Runtime 详细设计、`packages/contracts/`、API 示例、知识演进 fixture 和旧 API 兼容矩阵。
- 决策：将现有 `KnowledgeFile` 明确降为 legacy/upload facade；新增 `KnowledgeDocument`、`KnowledgeRevision`、`KnowledgeSnapshot`、`KnowledgeUpdateEvent`、`OutboxMessage`、answer provenance/revalidation DTO。
- 关键兼容性：旧 `/api/kb/files` 可在过渡期继续读取和上传，但内部映射为 document/revision；不允许新写入继续以可变 `file_id` 覆盖历史知识。
- 验收：枚举非法状态迁移、空 snapshot、future effective revision、重复 delivery、未授权资源和 Runtime 无 snapshot 的预期错误；为每条建立测试/fixture 名称。

### K2: 权威数据、迁移与发布事务

- Owner：`p`。`services/backend-go/internal/domain/**`、`repository/**`、migration 和 shared contract 同一时段只有一个写入者。
- 范围：引入 versioned Postgres migration，创建 document/revision/chunk manifest/snapshot/member/event/outbox/message provenance/revalidation 表与索引；repository 实现单一发布事务。
- 迁移策略：现有成功 file 先创建可审计的 `legacy_unpublished` revision，必须经过一次受控 reindex 才进入新 snapshot；绝不删除旧对象、消息或 citation。失败迁移保留旧 active snapshot，并输出可恢复错误。
- 回滚：迁移只追加；发布前写入失败回滚 transaction；已发布 snapshot 永远可读。不得用 schema reset 作为恢复手段。
- 验收：空库、含现有 file/conversation/citation 的开发库、重复 migration、并发 publication、成员非 ready、manifest hash 不一致均有 repository 测试和真实 Postgres smoke。

### K3: 可靠摄取与 revision-aware 索引

- 前置：`K2` 迁移和 ingestion event contract 已冻结。
- Backend 范围：Postgres transactional outbox、Redis Streams consumer group、ack/pending recovery、有限重试、DLQ 和可观测 delivery 状态。Redis 不拥有 revision 状态。
- Runtime 范围：接收 revision id、content hash、parse/index/embedding version 和 snapshot-independent payload；Qdrant point 写入幂等，按 revision 隔离并建立 `revision_id`、`document_id`、visibility 等 payload index。
- 禁止：当前 List `RPUSH/BLPOP` 不能与新 Streams 同时消费同一种 production event；切换必须有明确开关和 drain/reconcile 步骤。
- 验收：worker 重启、重复事件、索引失败、乱序回调、Qdrant 清理失败和 Redis pending recovery 均不改变 active snapshot；通过 Runtime、Backend、Redis、Qdrant 和真实对象存储 smoke。

### K4: 管理员知识生命周期

- 前置：`K3` 完成 revision ready 回传。
- Backend/API：新增 revision 上传、替换、显式发布、disable、delete、snapshot manifest 与 update list；所有读写要求 admin 权限、resource ownership 和 idempotency key。
- Web：知识库从“文件列表”过渡为“逻辑文档 -> revision -> 发布状态”；显示 indexing/ready/published/failed、manifest 与破坏性操作确认。
- 验收：一次 DOCX/Markdown 新增、一份替换、一次 disable 和一次 delete 的端到端流程；新 snapshot 检索不到被禁用 revision，旧回答来源仍可查看。

### K5: 回答 provenance 与快照约束检索

- 前置：`K4` 已能稳定发布 active snapshot。
- Backend：每个新用户消息解析 active snapshot 并传给 Runtime；assistant message/citation 事务记录 snapshot/revision/chunk hash/locator/score；sources API 返回 freshness 元数据。
- Runtime：原生 Qdrant 查询带 snapshot member/revision filter；无指定 snapshot 或 filter 无法建立时返回可重试错误，不使用全量或旧 snapshot 检索。
- Web：引用详情显示知识版本与回答时点，不修改历史答案文本。
- 验收：新消息在 snapshot A/B 切换后使用 B；复放旧回答仍显示 A；故意遗留的 Qdrant 旧 point 不能越过 scope 进入答案。

### K6: 回答时效、更新日历与核验

- 前置：`K5` provenance 完整；详细状态机、写入边界与接口以 [K6 时效、更新日历与历史核验设计](../../design-docs/single-agent-v1-k6-freshness-revalidation-design.md) 为准。
- Backend：发布 update event 后以 citation revision 交集找出候选回答，标记 `possibly_stale`；实现 update calendar 与 revalidation run 的权威记录。
- Runtime：核验请求只接收旧回答、旧 citation 和相关 update 的最小上下文，以 latest snapshot 重新检索/生成。
- Web：管理员查看日历；用户可在旧回答上发起核验并并列查看当时答案和最新核验结果。
- 验收：replacement/disable/delete/future effective date 触发正确结果；权限、conversation ownership、重复核验、Runtime 失败与部分 stale scan 都有回归测试。

### K7: 管理证据与配置闭环

- 范围：revision/snapshot/calendar、retrieval/generation/eval logs 的管理员查询；prompt/style/`top_p`/retrieval 参数真实传入 Runtime 并记录版本。
- 验收：管理员可按事件追溯答案的知识、配置和评测；配置改动不改变既有 config provenance，也不污染新消息的 latest knowledge snapshot 选择。
- `2026-07-15` 执行切片：先按 [K7 设计](../../design-docs/single-agent-v1-k7-admin-evidence-config-design.md) 冻结 Postgres execution evidence、eval report registration 与 admin-only 读取契约；将 agent/retrieval 的实际参数接入 Runtime，并在不改写 K5/K6/C1 历史记录的条件下完成最小 UI/API 验收。
- `2026-07-15` 功能结果：`007/008` migration、回答同事务 execution evidence、execution/delivery/eval 管理 API、评测报告幂等登记、配置真实应用与 Web 查询详情均已落地；未知 prompt 在配置保存边界被拒绝。功能测试与真实 Postgres/浏览器验收通过；当时严格 R5 连续两次为 grounded `0.5429 < 0.55`，K7 暂留 `in_progress`，随后由下一条质量收口解除。
- `2026-07-15` 质量收口：保留 `prompt_v1` 历史内容并新增 `prompt_v2`，eval payload/report/gate 显式校验 prompt version；v2 连续两次严格 gate 为 `30/40, grounded=0.6714` 与 `28/40, grounded=0.5857`，其他核心指标未退化。K7 转为 `verified`。

### C2: 有界会话意图路由

- 范围：用 Backend 内确定性 exact allowlist 识别有限问候/致谢/告别/能力说明/越界闲聊；混合、业务和未知输入默认 `knowledge_query`。
- 证据：Postgres `009` 持久化 intent 与 retrieval/generation/knowledge facts；Runtime 受控路径不调用 retrieval/model；Web 显示“未使用知识库”且不提供 snapshot/revalidation/citation。
- `2026-07-16`：Backend、Runtime `77 passed`、真实 Postgres、integration/e2e `42 passed`、production Playwright `17 passed`、真实 JSON/SSE 三路径和 prompt_v2 gate 均通过；C2 转为 `verified`。

### A1: 澄清、回答完整性与引用可信度

- 权威设计：[澄清与有依据回答处置设计](../../design-docs/single-agent-v1-clarification-policy-design.md)；执行切片与门槛见 [A1 交付计划](single-agent-v1-clarification-and-grounded-answer-plan.md)。
- 顺序：`A1-0` anchor 子集来源显示修正与 `A1-1` 开发评测集/provider evidence 均已验证；下一步做 `A1-2` contract/migration，Runtime policy、Web/admin 与 Q1 发布依次为 `A1-3` 到 `A1-5`。
- 约束：不扩张 C2 intent router；不改写 prompt_v2、历史消息或 active release；pending clarification 由 Backend/Postgres 强一致维护，Runtime 只提出 disposition/resolution；fallback 报告不得冒充真实模型质量。
- 模型结论：当前 3.2B llama 可做基础开发 smoke，但真实 `fallback=false` 40 题只有 `29/40`、grounded `0.6286`，尚未覆盖复杂澄清能力。A1-5 必须与至少一个更强本地 instruct candidate 做同条件 Q1 对比。

### K8: 原生检索质量与模型比较

- 范围：替换 Python payload 全扫描为真实 embedding、Qdrant dense+sparse/hybrid query、metadata filter 和 reranker；修复 PDF/XLSX/PPTX 的剩余质量失败；按冻结 harness 比较至少两个本地模型。
- 验收：每次 Runtime/RAG/Prompt/模型改动都通过 R5 gate 和知识演进 fixture；报告保留模型、embedding、reranker、snapshot manifest 与失败分类。
- `2026-07-16 K8-R1`：先按 [Native Dense Retrieval 设计](../../design-docs/single-agent-v1-k8-native-retrieval-design.md) 实施 OpenAI-compatible embedding、独立 dense collection、Qdrant `/points/query` 和 server-side revision filter；sparse/hybrid、provider rerank 与模型比较保持后续单变量实验。
- `2026-07-16 K8-R1 验收`：Runtime 已写入真实 dense vector、调用 Qdrant `/points/query` 并在服务端过滤 revision/file/source format；dense revision retry 也使用 filtered count/delete，不 scroll payload。provider、collection、维度与 Backend retrieval `embedding_model` 任一不匹配均 fail-closed。Backend 已把 immutable retrieval config 的 `embedding_model` 发送给 Runtime，eval report 已记录 native retrieval 配置。Ollama `all-minilm:latest` 已实测 `384` 维；真实 provider + Docker Qdrant smoke 完成 ingest/reload/scoped citation provenance，Runtime `89 passed`、词法与 native Qdrant transport smoke、最终 prompt_v2 R5 gate（`30/40`、grounded `0.6571`、retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`）以及全栈 integration/e2e 通过。`K8-R1` 转为 `verified`，现有 deployment 仍不切换 dense。
- `2026-07-17 K8-R2a`：已验证 [受控检索策略注册表设计](../../design-docs/single-agent-v1-k8-retrieval-strategy-registry-design.md)。本切片新增 immutable `retrieval_strategy`、Runtime profile/request exact match、策略 version evidence、eval metadata 与管理选择框；只注册 lexical compatibility 和 native dense，禁止动态 Python plugin。Backend full、Runtime `91 passed`、Web build、integration/e2e `45 passed`、Playwright 管理证据 smoke、fixed prompt_v2 lexical control gate、真实 Postgres migration/evidence、native Qdrant transport 与 Ollama `all-minilm` provider smoke 均通过。dense smoke 同步为显式 native strategy request，防止旧 request 形状绕过或误报。实际业务 dense cutover、lexical 删除、第二 embedding model 与 hybrid/rerank 仍不在本切片。

### K9: 可靠性与内部试用

- 范围：会话/资源 ownership 授权、默认凭据替换、完整迁移/备份/回滚 runbook、DLQ 运维、部署检查和受权限约束的工具能力契约。
- 验收：从空环境部署、升级、失败恢复、回滚和试用账号权限均有可重复验证；所有内部试用总门槛通过。
- `2026-07-16 K9-S0`：conversation `owner_id`、migration `010` legacy backfill、owner-filtered chat API、admin evidence 独立边界和 Web 越权链接恢复已验证；Backend full/race、isolated/开发 Postgres、integration `42 passed` 与 production Playwright `18 passed` 通过。K9 仍为 `in_progress`，不代表 service auth、部署、恢复和试用门槛已完成。
- `2026-07-18 K9-R1`：角色分离已验证，详见 [角色分离设计](../../design-docs/single-agent-v1-role-separation-design.md)。`user`、`knowledge_admin`、`system_admin` 已按 Backend route matrix 与 Web 工作区落实；legacy `admin` session 仅兼容为系统管理员。知识库管理员上传/索引和 onboarding，系统管理员确认 revision publish，并从产品执行参数候选/Q1。Backend full、Web lint/typecheck/build、integration `54 passed` 通过。
- `2026-07-18 K9-R2`：旧知识安全退役已验证，详见 [退役与检索策略发布设计](../../design-docs/single-agent-v1-k9-retirement-and-retrieval-release-design.md)。系统管理员可在产品查看 readiness，并且只有旧+新 current snapshot 的 passing onboarding 后才可 disable；disable 后还需要 new-only current snapshot 的 passing onboarding 才可 delete。服务端 transaction 是最终门禁，`knowledge_admin` 不能绕过。当前 active release 仍为 lexical；native dense 只可由系统管理员创建候选并经真实业务 Q1 发布。

## 5. 并行与认领规则

- `K1`、`K2` 必须串行。任何人不得同时编辑 migration、shared contract、Backend repository 或 active snapshot 写路径。
- `K3` contract 冻结后，可以并行：Backend 负责 outbox/Streams，Runtime 负责 revision-aware ingestion/Qdrant；各自不得改 shared DTO。
- `K4` API 冻结后，可以并行：Backend API 与 Web 展示；所有 endpoint contract 改动仍由 Backend owner 汇总。
- `K5` 和 `K6` 的 Backend/Runtime 主链串行；API 冻结后 Web 可并行。质量实验只能在 snapshot contract 稳定后启动。
- 开始新包前，在 `single-agent-v1-parallel-claims.md` 认领写入范围、依赖、验证命令和交接点。`p` 负责 integration、架构验收和最终状态更新。

## 6. 当前状态与下一动作

- 当前状态：`K7/C1/U3/C2/C3-R2/K9-S0/K8-R1/K8-R2a/K8-R3-calibration/K8-R4a/K9-R1/K9-R2/A1-0/A1-1 verified`，`A1-2 planned`；Qwen 的 fixture dense evidence/refusal 边界已校准为 `0.51`，但尚未达到业务 dense cutover 或真实业务知识发布门槛。会话归属已由 Postgres `owner_id` 权威控制，legacy conversation 隔离给 `user_admin`；管理员的全局能力只保留在 evidence API。
- 已验证基线：全栈开发环境、真实 Ollama 问答、上传/reindex、Qdrant/MinIO/Postgres/Redis 联调及 prompt_v2 严格质量门已通过。
- 当前风险：`KnowledgeFile` 仍是 legacy facade；现有 deployment 仍在 lexical profile，K8-R1 dense collection 必须以独立 embedding 模型重新索引、匹配 retrieval config 并发布新 snapshot 后才可切换；旧测试知识不可在无 new knowledge/onboarding proof 时清空。后续仍有生产默认凭据替换、部署/恢复、消息幂等和内部试用门槛。
- 当前动作：用户优先级完成 `A1-0 → A1-1`；A1-1 已建立 clarification 开发基线与实际 provider/fallback evidence 报告字段。下一步是 A1-2 shared contract/migration 设计；`K8-R4` 真实业务 onboarding 仍保持待外部知识输入状态，不与 A1 共享 contract/migration writer；期间不得改写 K5-K7/C1 历史 provenance，也不得让 fallback 报告或 dense 请求冒充已发布质量结论。

### K8-R3: Dense 证据边界校准与知识库切换准备

- 前置：`K8-R2` 的 Qwen candidate 与 `Q1` quality release 已验证；详细契约以 [K8-R3 设计](../../design-docs/single-agent-v1-k8-dense-refusal-calibration-design.md) 为准。
- 范围：将 `evidence_min_score` 从 Runtime `.15` 内部默认值提升为 immutable retrieval config / Runtime override / execution evidence / eval report；在固定 Qwen 条件下完成 score distribution 与 threshold sweep；为管理员配置页增加参数学习说明和 candidate/release evidence。
- 非范围：不更换模型、scope、prompt、top_k、rerank、chunking，不删除测试知识，也不执行 dense cutover。
- 发布门槛：must-refuse `5/5`、must-answer retrieval/citation/grounded 无回归、prompt_v2 与知识演进/C1/C2 gate 通过、报告条件 exact-match、Q1 approval 完成。
- 后续 K8-R4（仅在 R3 发布门槛通过后）：上传新业务知识并发布包含旧+新的 snapshot，完成 onboarding eval 后逐个 disable/delete 测试 document；不得在无 ready 新 snapshot 时清空知识库。

`2026-07-18` 校准结果：`evidence_min_score=0.51` 通过 fixed-Qwen fixture gate（最新 `30/40`、grounded `0.6429`、retrieval/citation/refusal `1.0`）。R3 的 calibration acceptance 已完成；真实业务 release/approval 仍属于 K8-R4，不能用 fixture snapshot 代替。

### K8-R4a: 产品内知识上线验收

- 前置：K4-K7/Q1/K8-R3 已验证；详细契约以 [K8-R4a 设计](../../design-docs/single-agent-v1-k8-r4-onboarding-quality-gate-design.md) 为准。
- 范围：管理员审核 must-answer/must-refuse onboarding dataset、冻结 snapshot/manifest/config pair、Backend scoped evaluation worker、逐题 citation/refusal evidence 与 Web gate。内容更新不自动改参数；检索配置变更仍依赖 Q1。
- 非范围：模型自动生成真值、浏览器直接执行 Runtime、自动删除 document、dense cutover。
- 发布门槛：reviewed case truth、冻结 identity、所有 case passing/no runtime error、existing knowledge/provenance/lexical gates 通过。只有 passing run 才可作为 K4 测试知识退役的前置证据。
- `2026-07-18`：已验证。migration `014`–`016`、Backend admin API/lease worker、fixed snapshot/config pair、逐题 result、`/knowledge` 工作台已实现；isolated Postgres lifecycle 和 scoped Runtime worker smoke、Go service/repository/httpapi/server、Web lint/typecheck/build 均通过。下一动作不再是开发本能力，而是管理员上传真实业务知识后实际执行此质量门。

### Q1: 质量运营反馈、实验与受控发布

- 前置：`K7` execution/eval evidence、`K8-R2a` strategy registry 和 `K9-S0` ownership 已验证；设计以 [质量运营与受控发布设计](../../design-docs/single-agent-v1-quality-operations-release-design.md) 为准。
- 范围：Postgres append-only feedback、受控 experiment conditions/report binding、候选 config 与 active release pointer 隔离、admin approval/publish/rollback，以及 `/config` 的 feedback/report/release UI。
- 写入边界：Postgres 是 feedback/experiment/release 的唯一权威；Backend service 是唯一业务写入者；runner 只写匹配 experiment 的 report 投影；Runtime/Web 不得直接写或自动变更 active release。
- 验收：candidate 永不因创建而成为 active；feedback 不改写 message/citation/provenance；报告条件 mismatch/未通过 gate/非法状态/并发 publish 都 fail-closed；publish 与 rollback 只改变后续 conversation 的 config pair；K8 lexical control gate 与知识演进 fixture 无回归。

## 7. K1 验证记录

- `make runtime-test`：`41 passed`，含新增知识演进 contract/fixture 测试。
- `make backend-test`：通过。
- `make integration`：`41 passed`，含 Web production build。
- 所有 JSON contract 通过 `python -m json.tool`；OpenAPI 通过 YAML 解析。
- 文档检查：产品规格、SDD、Backend/Runtime 详细设计、契约设计和执行队列已统一为“配置按 conversation 固定，知识按每条新消息选择 latest published snapshot”。

## 8. K2 验证记录

- `make backend-test`：通过。
- `make postgres-k2-smoke`：在随机隔离 PostgreSQL schema 中验证 K2 的 `001/002` migration、重复启动的 legacy 回填、ready publish、非 ready/future/manifest 拒绝、publish rollback hook 与并发 publication；K3 后追加的 `003` delivery migration 由 K3 smoke 单独覆盖。
- `make postgres-repository-smoke`：已有开发库已实际升级，重启后 conversation/message/citation/file 仍可读。
- 开发库检查：migration ledger 为 `001:legacy_core,002:knowledge_evolution`；三条成功 legacy file 各有一条 `legacy_unpublished` revision；active snapshot pointer 仍为 `NULL`。
- `make runtime-test`：`41 passed`；`make integration`：`41 passed`，含 Web production build。

## 9. K3 验证记录

- Runtime revision 投影：`make runtime-test` 为 `47 passed`；`make runtime-qdrant-smoke` 与 `make minio-ingestion-smoke` 通过。revision 的 point/chunk id、manifest、document/revision/visibility payload index 和 failure response 均已覆盖。
- Backend delivery：`go test ./...`、`make postgres-k2-smoke`、隔离 Postgres delivery lifecycle 和 Redis Streams PEL/DLQ smoke 均通过。
- 启动接线：在 `K3_DELIVERY_ENABLED=true`、隔离 Postgres schema 与 Redis Stream 下，Server 自动 relay outbox、调用 Runtime、校验 immutable identity、将 revision 写为 `ready` 和 delivery 写为 `delivered`；active snapshot pointer 保持不变。
- 全栈回归：`make integration` 为 `41 passed`。Legacy List ingestion 与 MinIO smoke 仍通过，K3 默认关闭且不消费 legacy event。

## 10. K4 验证记录

- API 与状态机：管理员 document/revision create、replace、list/detail、ready-only publish、disable、soft delete 与 Postgres idempotency ledger 已落地。disable 遇到 in-flight revision 返回 `document_has_inflight_revision`，最后一个 active document disable 清空 pointer 而不创建空 snapshot。
- 权限与兼容：Server HTTP smoke 覆盖 `401/403`、缺失/重复/冲突 idempotency key、非 ready publish、replacement、in-flight disable、delete conflict 和 Postgres 下 legacy `/api/kb/files` compatibility mapping。
- Web：同源 proxy、document/revision/delivery 状态、上传/替换/publish/disable/delete、错误态及 destructive confirm 已落地；lifecycle Playwright 通过。
- `p` 验收：K4 isolated Postgres、Server HTTP、Backend tests、Web lint/typecheck/build 与全栈 `make integration` 均通过（`41 passed`）。

## 11. K5 验证记录

- Backend：每条 lifecycle Postgres chat request 只读一次 active snapshot；no-active、scope/response mismatch 与错误 document/revision provenance 都拒绝且不写成功 exchange。assistant/citation/message provenance 在单 transaction 中持久化，历史 source 保持原 snapshot。
- Runtime：InMemory/Qdrant 都接收 revision allow-list，Qdrant filter/payload 错误不会退回全量查询；orphan point、scope A/B 与 citation metadata 均有测试。
- Web：message/citation panel 显示 snapshot、manifest、freshness、document/revision/chunk hash；legacy 数据明确为 unknown，scope failure 复用发送失败重试。
- `p` 验收：K5 isolated Postgres、Server HTTP、Backend full test、Runtime `53 passed`、Qdrant smoke、Web lint/typecheck、production Playwright `2 passed` 与全栈 `make integration` `41 passed` 均通过。

## 12. K6 验证记录

- Backend：`005` append-only migration、Postgres lease/retry/failed freshness scan、calendar/immutable snapshot API、idempotent revalidation run/result 与 background worker 已落地；只有 `published/disabled/deleted` 事件按 citation 交集标记历史回答。
- Runtime：revalidate mode 强制 latest scope、最小历史上下文和受控 `still_valid/needs_update/insufficient_evidence` verdict；控制 marker 不进入正文，拒答/无证据收敛为 `insufficient_evidence`。
- Web：管理员可查看日历、scan 计数/失败和 snapshot 成员；聊天页保留原 answer/provenance，独立显示核验状态、最新 result、verdict 与 citation。
- `p` 验收：isolated Postgres repository + Server K6 smoke、Backend full test、Runtime `64 passed`、Qdrant smoke、Web lint/typecheck/build、production Playwright `4 passed` 与全栈 `make integration` `41 passed` 均通过。

## 13. K7 验证记录

- Backend：Postgres migrations `001-008`、execution evidence 与消息/citation/anchor 原子写入、execution/delivery/eval admin API、评测报告语义幂等登记与冲突、prompt registry 保存边界均有回归测试；`make postgres-k7-smoke` 与 `go test ./...` 通过。
- Runtime：prompt/style/sampling/top-k/rerank 参数进入实际 pipeline；全量 `71 passed`，包含知识演进 fixture 与 prompt version fail-closed。
- Web/API：lint、typecheck、production build 通过；全栈 Python integration/e2e `42 passed`；生产 Playwright `16 passed`，其中 K7 覆盖 prompt registry、revision filter、answer detail、manifest、config、citation、delivery 与 eval evidence。
- 真实服务：管理员登录、创建并回答 conversation 后可读取 `snap_001`、两条 citations、`cfg_003` 与 `rcfg_001` execution evidence；未知 prompt 保存返回 `422` 且 active config 不变。
- 质量收口：没有改写 `prompt_v1`、grader 或阈值；新增 `prompt_v2` 与独立 `k7-llama3.2-prompt-v2-local-v1` gate。两次真实 Ollama 结果均通过，分别为 `30/40, grounded=0.6714` 与 `28/40, grounded=0.5857`，retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`。
- 真实配置：本地 active Agent Config 已创建 `cfg_004 / prompt_v2`，新 conversation 绑定该版本；execution evidence 读取到同一 prompt/config version。最新 v2 eval report 已通过管理员 API 幂等登记。
