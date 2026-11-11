# 单智能体 V1 质量运营与受控发布设计

## 文档控制

- 状态：`verified`
- 更新日期：`2026-08-03`
- 工作包：`Q1`（质量反馈、受控实验与发布）
- 上位设计：[K7 管理证据与配置闭环](single-agent-v1-k7-admin-evidence-config-design.md)、[K8 原生检索设计](single-agent-v1-k8-native-retrieval-design.md)

## 1. 目标、范围与非目标

把现有“知识 revision/snapshot → 回答 execution evidence → 离线 eval report”的证据链产品化为管理员可操作的闭环：记录纠正、将经审核的信号组织为受控实验、比较报告、人工审批、仅对新会话发布并可回滚。

本包覆盖管理员反馈、实验/报告/发布的权威记录与管理台。反馈绝不直接修改 Runtime 参数、知识 snapshot、历史回答、citation 或 provenance；Runtime 也绝不写这些记录。

不在本包范围内：自动在线调参、从浏览器启动任意模型任务、dense cutover、lexical retirement、微调、把未审核对话直接写入基线数据集。K8-R2 dense must-refuse 仍须先按冻结条件完成阈值校准。

## 2. 权威边界与读写路径

| 记录 | 权威与唯一写入者 | 读取者 | 不变量 |
| --- | --- | --- | --- |
| knowledge/revision/snapshot | 既有 Postgres lifecycle transaction | Backend/Runtime/Web | 不由质量工作包改写 |
| answer feedback | Postgres；Backend service | 管理员 UI、实验服务 | 永远锚定既有 assistant message/execution evidence；append-only |
| experiment conditions/reports/decision | Postgres；Backend service，runner 只能登记匹配 report | 管理员 UI | scope、dataset hash、版本/参数 hash 冻结后不可变 |
| active quality release | Postgres；Backend publish transaction | 创建 conversation 的 Backend | 一次只指向一个已发布 config pair；历史 conversation 不迁移 |
| Runtime observation | Runtime response | Backend evidence transaction | Runtime 不写 feedback/experiment/release |

新 conversation 在一个 Backend read transaction 中从 active quality release 取得不可变 agent/retrieval config pair；候选 config 永远不会因创建时间较新而自动被选中。旧 conversation 保持既有 binding。首次 migration 会把当前有效 pair 回填为 bootstrap published release，因此兼容现有运行。

## 3. 领域对象与状态机

### 3.1 AnswerFeedback

`feedback_id`、`message_id`、`conversation_id`、`actor_id`、`kind`、`comment`、可选 `corrected_answer`/`expected_citation_ranks`、`idempotency_key`、`created_at`。`kind` 限于 `correct`、`incorrect`、`should_refuse`、`missing_evidence`、`wrong_evidence`、`outdated_knowledge`、`knowledge_gap`、`should_clarify`、`unnecessary_clarification`、`incomplete_coverage`。

服务层在写入前验证：目标为已有 assistant message，存在 execution evidence；rank 属于该 message 的 citation；actor 为管理员。`outdated_knowledge`/`knowledge_gap` 只作为知识生命周期 remediation 信号，不能被拿来宣称检索参数已修复。

`A1-4` 的三种语义反馈还须读取该 immutable evidence 的 `intent` 和 `answer_disposition`：`should_clarify` 只能锚定 `grounded_answer`、`partial_answer` 或 `insufficient_evidence`；`unnecessary_clarification` 只能锚定 `clarification_required`；`incomplete_coverage` 只能锚定 `grounded_answer` 或 `partial_answer`。它们均要求非空 `comment`，且不接受 `expected_citation_ranks`。`wrong_evidence` 继续承载引用不支持事实的信号，不与 coverage 或澄清判断重叠。legacy、C2 controlled response、非知识或 failed message 不能写入上述三种新 kind。完整的用户/管理员可见边界见 [A1 澄清与有依据回答处置设计](single-agent-v1-clarification-policy-design.md)。

### 3.2 QualityExperiment

实验引用 baseline/candidate config pair、固定 knowledge snapshot/manifest、dataset id/SHA-256、runner version、条件 JSON/SHA-256、acceptance JSON、附着的 feedback、baseline/candidate reports 与审计 decision。状态为：

```text
draft → collecting_feedback → ready_for_eval → evaluating → review_ready → approved → published
                         ↘ cancelled / failed / rejected / superseded
```

只有 `draft|collecting_feedback` 可附着反馈；`freeze` 在 `ready_for_eval` 计算 conditions hash；runner report 只可在 `evaluating` 登记且必须 exact-match；`approve` 仅对 `review_ready` 且 gate verdict 已通过的 candidate 有效；`publish` 仅对 `approved` 有效。

### 3.3 QualityRelease

release 是 immutable 的 agent/retrieval config pair、来源 experiment、previous release、审批/发布 actor 与时间。`publish` 用同一 Postgres transaction 锁定 active pointer、验证 approval/report/conditions/gate、追加 release event 后切 pointer。rollback 不篡改旧 release，而是从已发布 release 前滚创建一条新的 rollback release。失败、冲突或重试绝不改变 pointer。

## 4. API 契约

所有以下接口均为 admin-only，变更接口要求 `Idempotency-Key`：

- `POST/GET /api/admin/quality/feedback`：写入或筛选 feedback。
- `POST/GET /api/admin/quality/experiments`，`GET /api/admin/quality/experiments/{id}`：创建与读取实验。
- `POST /api/admin/quality/experiments/{id}/feedback`、`/freeze`、`/approve`、`/publish`：按状态机操作。
- `POST /api/admin/quality/experiments/{id}/reports`：仅受控 runner 登记；report 的 dataset/scope/config/conditions identity 必须匹配 experiment。
- `GET /api/admin/quality/releases`、`POST /api/admin/quality/releases/{id}/rollback`：读取和前滚回退。

已存在的 `POST /api/eval/reports` 继续保存原始独立 eval evidence，但不能单独成为发布许可；废弃但未实现的裸 `/api/eval/run` 不得由 Web 暴露为可操作能力。

## 5. 失败、安全与可追溯性

- 任何不存在 message/evidence/rank、非管理员、重复 key 的不同内容、非法状态、报告条件不一致、未通过 gate 或并发发布均返回稳定 4xx/409，且不改变 active release。
- 配置候选、feedback、实验与 release 写入失败不阻塞聊天、知识上传或 Runtime。
- 历史 assistant message、K5 provenance、K6 result、C1 anchor、已有 config/eval report 永不被本包回填或更新。
- 每个可发布 candidate 必须指向固定 dataset hash、scope/manifest、完整 generation/retrieval profile、报告与审批理由；单条对话反馈既不是自动训练数据，也不是自动发布授权。

## 6. Web 体验

在现有 `/config` 管理工作台内扩展，不创建第二套管理壳层：回答证据详情提供“记录质量反馈”；质量运营区显示 feedback、实验状态、固定条件、报告对照、审批/发布/回滚时间线。文案以“候选”“等待评测/审批”为中心，明确保存候选不改变线上新会话，发布只影响之后创建的会话。

## 7. 实施顺序与验收

1. 冻结 product/OpenAPI/domain、migration 与 release pointer；先让候选隔离于线上。
2. 实现 append-only feedback 与 experiment/report condition validation；runner 只登记投影。
3. 实现 approval/publish/rollback transaction 和 conversation binding；再接 Web。
4. 用 Qwen dense refusal 校准作为首个实验，不在 gate 通过前发布。

验收至少覆盖 admin gate、idempotency、message/evidence/rank 约束、条件 hash mismatch、非法状态、failed publish、candidate 不影响新 conversation、旧 conversation 不变、publish/rollback 原子性、Web 反馈/报告对照与既有 K5-K8 gates。

`2026-07-17` 验收：Postgres `012_quality_operations_release` 已在 isolated real-Postgres lifecycle 中验证 feedback rank、candidate isolation、report identity mismatch、gate rejection、publish 与 forward rollback；Backend full、Runtime `91 passed`、eval contract `18 passed`、Web lint/typecheck/build、管理员 Playwright、full integration `52 passed`、真实 Postgres K7 smoke 与固定 `prompt_v2` runtime-local gate 均通过。Q1 完成不代表 dense quality 已获发布批准。
