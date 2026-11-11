# 单智能体 V1 K6 时效、更新日历与历史核验设计

## 文档控制

- 状态：`active`
- 版本：`v1.0`
- 更新日期：`2026-07-15`
- 工作包：`K6`
- 上位设计：[知识演进与回答时效设计](single-agent-v1-knowledge-evolution-design.md)、[知识演进契约](single-agent-v1-knowledge-evolution-contract.md) 与 [K5 快照与 provenance 设计](single-agent-v1-k5-snapshot-provenance-design.md)

## 1. 目标与非目标

K6 让系统能够回答两个不同的问题：

1. 某条历史回答当时依据的知识是否已被后续可见知识影响。
2. 使用当前 published snapshot 重新核验后，旧回答是仍有效、需要更新，还是证据不足。

K6 不修改历史 assistant message、其 citation 或其原始 snapshot。核验始终新增独立 result。K6 也不在本阶段解决多租户 resource ownership；当前单工作区角色门禁仍生效，完整资源归属属于 K9，不能把 K6 的 admin-only 日历误称为租户隔离。

## 2. 事实源与状态

PostgreSQL 是 update event、扫描、时效状态、核验 run 和 result 的唯一权威。Runtime、Qdrant、MinIO 不能直接改变这些记录。

```text
knowledge_update_event (append-only)
  -> freshness_scan: queued -> running -> completed
                               \-> retry -> running
                               \-> failed

answer_revalidation_run: queued -> running -> completed
                                          \-> failed
  -> answer_revalidation_result (append-only, at most one successful result per run)
```

- `knowledge_freshness_scans.event_id` 唯一：同一 event 的重放不会重复标记或重复计数。
- worker 以短 lease 认领 `queued/retry/running-expired` 记录；进程中断只留下可恢复工作，不会丢失事件。
- `answer_revalidation_runs` 以 `(requester_id, idempotency_key)` 幂等。相同 key 不同原 message 或 reason 返回 `409 idempotency_key_reused`。
- 核验成功把原 assistant message 与 `message_knowledge_provenance` 标为 `revalidated`，但原文本、snapshot、manifest 与 citation 不变。后续命中的 update event 可以再次标为 `possibly_stale`。

## 3. 哪些知识事件会影响回答

只有 `published`、`disabled`、`deleted` 是 K6 的可见性事件，必须创建或恢复 freshness scan。`uploaded`、`replaced`、`indexed`、`reindexed` 不会直接标记历史回答，因为它们尚未改变 active snapshot 的可见内容。

扫描候选使用历史 citation 的 document/revision 交集，而不是按时间粗暴标记：

| event | 候选 assistant message |
| --- | --- |
| `published(document D, revision R)` | 引用 `D` 且 revision 不为 `R` 的回答 |
| `disabled(document D)` | 引用 `D` 的回答 |
| `deleted(document D)` | 引用 `D` 的回答 |

只更新命中回答的 `freshness_status`。无 document/revision citation 的 legacy answer 保留 `unknown`，不被猜测为过期。scan 保存 `candidate_count`、`marked_count`、attempt、error 与完成时间，供管理员识别部分失败和恢复情况。

## 4. 历史核验写入路径

`POST /api/chat/conversations/{conversation_id}/messages/{message_id}/revalidate`：

1. 验证目标是已完成 assistant message，且拥有 K5 snapshot provenance；否则返回 `409 revalidation_invalid`。
2. 读取原 answer、原 snapshot、原 citation revision ids，以及该 answer 已命中的 update event ids。
3. 一次性解析当前 active published snapshot scope。没有当前 scope 返回 `409 knowledge_snapshot_unavailable`，不会创建 run。
4. 用 `Idempotency-Key` 创建 durable `queued` run，冻结请求者、reason、原 evidence、latest snapshot/manifest 和相关 events；返回 `202`。
5. worker 认领 run，使用 `answer_mode=revalidate` 调 Runtime。它只传递最小上下文：原 answer/evidence、相关 event ids 和最新 scope；不会传入无关会话历史。
6. Backend 验证 Runtime identity、snapshot echo 与 citations 均属于最新 scope，随后写入独立 result 及 citations，完成 run，并把原回答更新为 `revalidated`。

Runtime 的 revalidation prompt 要求以受控 verdict 产生结果：`still_valid`、`needs_update` 或 `insufficient_evidence`。Runtime 去除控制标记再返回用户可读正文；无 citation、refusal、无法解析 verdict 或 scope 错误统一为 `insufficient_evidence` 或 run failure，绝不能把旧 answer 当成最新 answer 返回。

## 5. 持久化对象

K6 追加 migration，不重写 K2-K5 表：

- `knowledge_freshness_scans`：event id、状态、attempt、lease、候选/标记计数、error 与时间戳。
- `answer_revalidation_runs` 新增请求者、idempotency、reason、latest snapshot/manifest、冻结的关联 event ids、lease、error 和 outcome。
- `answer_revalidation_results`：run id、独立 answer content、latest snapshot/manifest、outcome、citation JSON、完成时间。其内容和 citation 不会覆盖原 message。

结果 citation 采用结构化 JSONB，序列化形态复用 K5 `Citation` 契约；字段包括 document、revision、chunk hash、locator、rank、score 和 snapshot。该 JSON 是 run 的不可变结果证据，不作为 active retrieval 的来源。

## 6. API 与权限

- `GET /api/kb/updates`：admin-only，按时间倒序返回 update event、scan 状态、计数、错误与 snapshot 关联。
- `GET /api/kb/snapshots/{snapshot_id}`：admin-only，返回 immutable manifest 与 members。
- `POST .../revalidate`：已登录用户可发起；当前单工作区实现保留 K9 resource ownership 缺口。必须带 `Idempotency-Key`。
- `GET .../revalidations/{revalidation_id}`：已登录用户读取同一 conversation 的 run/result；当前资源归属缺口与上项相同。

稳定错误码：`revalidation_invalid`、`revalidation_not_found`、`knowledge_snapshot_unavailable`、`snapshot_scope_violation`、`idempotency_key_reused`。

## 7. Web 呈现

知识库页面显示更新日历，包含 event 类型、document/revision、active snapshot、扫描状态、受影响回答数量及失败原因。聊天页同时保留原 answer/provenance，并提供核验动作与独立的最新核验结果；结果必须明确显示最新 snapshot、引用、时间和 verdict，不能伪装为历史消息被改写。

## 8. 验收矩阵

1. replacement publish 仅标记引用同一 document 的旧 revision 回答；无交集回答保持 `current`。
2. disable/delete 标记所有引用目标 document 的回答；重复 event scan 不重复计数。
3. worker 中断后的 lease 到期可恢复；scan/runtime 失败可诊断并可重试。
4. manual retry 使用相同 idempotency key 返回同一 run；不同请求返回 `409`。
5. 核验 result 独立于原 message；Runtime scope/citation 违反时不写 result 且原 answer 不变。
6. admin calendar/snapshot 通过，普通用户被拒绝；浏览器展示 `possibly_stale`、running/failed result 与完成 result。
