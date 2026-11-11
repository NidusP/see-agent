# 单智能体 V1 K7 管理证据与配置闭环设计

## 文档控制

- 状态：`active`
- 版本：`v1.0`
- 更新日期：`2026-07-15`
- 工作包：`K7`
- 上位设计：[知识演进与回答时效设计](single-agent-v1-knowledge-evolution-design.md)

## 1. 目标与边界

K7 让管理员能从一个持久化读取模型回答“某次运行何时发生、用了哪些知识与配置、检索和生成结果为何、对应什么评测”。它不改变 K5 的每消息 snapshot scope、K6 的历史核验结果或 C1 的 anchor 范围；历史回答和 provenance 永不被 K7 改写。

首个可验收切片包含：

- 配置版本的完整运行时应用：`system_prompt_version`、`response_style`、`model_id`、`temperature`、`top_p`、`max_output_tokens`，以及 retrieval 的 `top_k` 和 `rerank_enabled` 进入 Runtime request；Runtime 用已知 prompt 版本、style 与采样参数构造实际调用，并将实际的 retrieval/generation 结果返回给 Backend。
- PostgreSQL 在同一事务内为每条 K5 scoped assistant message 写入不可变 execution evidence；其引用的 config/retrieval version、知识 scope、citations、usage、finish/refusal 状态和耗时均可读取。
- 管理员可按 message、conversation、snapshot 或 revision 查询该 evidence；calendar/manifest/delivery 继续使用其已有的 Postgres 权威表，通过同一管理页/API 组合展示。
- 评测 runner 通过 admin-only、幂等的 report registration API 将已完成的受控评测摘要和完整 JSON 登记到 Postgres，管理员可查询并将 run 的 config version 与 generation config 关联到运行证据。

本切片不实现 K8 的 dense/sparse embedding provider、production reranker 或模型比较调度。`embedding_model` 仍是 revision projection/评测 provenance；在 K8 前它不能宣称会热切换已建立的 embedding index。管理员界面必须明确此限制。

## 2. 权威、写路径与一致性

| 记录 | 权威 | 写入者 | 一致性 |
| --- | --- | --- | --- |
| conversation config/retrieval binding | PostgreSQL `conversations` | Backend 创建会话 | 创建后不可变 |
| message execution evidence | PostgreSQL `message_execution_evidence` | Backend 的 `AppendScopedRuntimeExchange` 事务 | 与 assistant message、K5 provenance、citations、C1 anchors 同事务提交 |
| lifecycle/calendar/manifest/delivery evidence | 现有 PostgreSQL knowledge 表与 outbox | 既有 K2-K6/K3 写路径 | K7 只读，不复制或重放 |
| eval report | PostgreSQL `eval_reports` | 受管理员鉴权的 eval registrar | `report_id` 幂等；完整 report JSON 以 JSONB 语义等价形式保存 |

Runtime 不写 Postgres。它只能返回一次 request 对应的 retrieval/generation observation。Backend 在运行结果与 request identity、snapshot scope、citation authority 都校验成功后，才写 execution evidence；Runtime 失败或 scope 校验失败不留下伪造的成功 evidence。

## 3. 配置语义

新 conversation 固定 active agent/retrieval version；后续配置更改不能改变该 conversation 或历史 message。每个正常回答仍在开始时选择 latest active knowledge snapshot，两个快照维度相互独立。

`system_prompt_version` 必须解析到 Runtime 内受版本控制的 prompt 文本；已登记版本由 API enum、Backend 保存边界和 Runtime registry 共同约束，未知版本必须 fail-closed，不能静默回退。`response_style` 作为受控 guidance 加入 system prompt；`temperature`、`top_p`、`max_output_tokens` 透传模型适配器。`top_k` 控制 retrieval evidence 截断；`rerank_enabled` 控制当前 lexical rerank stage。所有这些实际值连同 config version 写入 execution evidence。

K7 质量收口不得原地修改 `prompt_v1`，否则历史 execution evidence 中的 prompt version 失去可重放语义。新增 `prompt_v2` 作为单变量实验：保持检索、模型、temperature、top-p、输出预算与 grader 不变；跨语言回答先使用用户语言，再逐字附上证据中的一个完整短句或目标表格行，保留金额、期限、角色、审批条件和字段值，并继续使用同一 citation marker。只有严格 R5 与知识演进 fixture 通过后，`prompt_v2` 才能成为新配置可选版本；回滚是不选择 v2，已有 v1 conversation 不迁移。

`prompt_v2` 通过门禁后，全新 Memory/Postgres repository 的初始 Agent Config 使用 v2。已有 repository 的 seed、active config、conversation 和 execution evidence 不做 migration；管理员只能通过正常配置 API 新建 v2 config version。

## 4. 管理读取 API

- `GET /api/admin/evidence/messages/{message_id}`：一条回答的 execution evidence、知识 scope、config/retrieval snapshots、citations 与关联 calendar events。
- `GET /api/admin/evidence/executions`：按 `conversation_id`、`snapshot_id`、`revision_id` 筛选的 execution summary 列表，使用 cursor/limit 分页。
- `GET /api/admin/evidence/deliveries`：复用 K3 outbox delivery 的只读分页视图。
- `POST /api/eval/reports`、`GET /api/eval/reports`、`GET /api/eval/reports/{report_id}`：管理员登记及查询受控 eval evidence；`POST` 需要 `Idempotency-Key`。

所有 K7 API 都是 admin-only。缺失 evidence 返回稳定 `404`；无效 filter 返回 `400`；重复 report registration 返回原记录而不创建第二条。K7 不提供删除或更新 historical evidence 的 API。

## 5. 失败与回滚

- evidence insert 失败会回滚整个 scoped message 写入，避免出现“可见回答但不可审计”的新记录；用户收到可重试失败。
- report registration 失败不会影响聊天或 knowledge lifecycle；登记方可用相同 idempotency key 重试。
- K7 migration 只增加表、索引和读取路径。回滚采用前滚修复；已有 K5/K6/C1 表和历史回答不迁移、不重写。

## 6. 验收

- 新会话在更新配置后使用新 config/retrieval binding；旧会话仍发送原版本的所有运行时参数。
- Runtime 测试证明 prompt/style、`top_p`、`top_k` 与 rerank 开关影响相应 pipeline/adapter 调用。
- Postgres 集成测试证明 assistant message、K5 provenance、citations、C1 anchors 与 execution evidence 原子写入；注入 evidence 错误时没有部分 message。
- admin API 测试覆盖查询筛选、未登录/普通用户拒绝、无记录、非法 filter 与 eval 幂等登记。
- 浏览器验收能从一条回答进入管理证据详情，看到 knowledge snapshot、revision/citation、agent/retrieval config、generation/retrieval observation 和相关 eval report；历史回答内容保持不变。
- prompt 质量实验必须在 eval request/report 中显式记录 `system_prompt_version`；quality gate 校验该版本，不能把 v1/v2 结果混为同一基线。
