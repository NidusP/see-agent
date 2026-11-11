# 单智能体 V1 生产加固交付计划

## 文档控制

- 状态：`active`
- 版本：`v1.1`
- 更新日期：`2026-07-16`
- Owner：`p`
- 上位事实源：[产品交付计划](single-agent-v1-product-delivery-plan.md) 与 [知识演进执行队列](single-agent-v1-next-dev-sequence.md)

## 1. 目标

把当前功能完整的内部 MVP 推进为可受控内部试用、随后可生产部署的单智能体知识问答产品。安全和数据隔离先于扩大用户范围；质量、部署与可靠性必须以可重复证据收口。

## 2. 执行顺序

| 工作包 | 状态 | 目标 | 退出证据 |
| --- | --- | --- | --- |
| `K9-S0` ownership/security minimum | `verified` | conversation owner、跨用户拒绝 | owner migration/backfill、401/403/404、跨用户 API/Web/Postgres 回归通过 |
| `K8-R1` native retrieval | `verified` | 真实 embedding、Qdrant native dense query、fail-closed provider boundary | all-minilm provider smoke、scope 隔离、R5 gate 与全栈回归通过 |
| `K8-R2` model comparison | `pending` | 至少两模型受控比较 | 固定 dataset/config、可追溯报告和选型结论 |
| `K9-S1` service security | `pending` | OIDC/SSO 边界、service auth、TLS/secure cookie、rate limit、fallback fail-closed | 安全负例、secret/config 检查、依赖漏洞门禁 |
| `K9-R1` chat reliability | `pending` | message idempotency、SSE 中断恢复、统一 timeout/cancel | 重复/超时/断线/重启测试 |
| `K9-O1` observability | `pending` | metrics、structured logs、trace、SLO/alert | dashboard/alert smoke 与故障定位演练 |
| `K9-D1` deploy/recovery | `pending` | application images、deployment manifests、backup/restore/rollback | 空环境部署、升级、恢复、回滚演练 |
| `K9-U1` trial closure | `pending` | 原件预览决策、反馈、搜索/归档、试用说明 | 内部试用 checklist 全部通过 |

## 3. K9-S0 范围

- conversation create 写入当前认证用户 `owner_id`。
- 普通用户仅能 list/read/write/revalidate 自己的 conversation。
- 管理员的全局审计能力只存在于 admin evidence API；聊天 API 不因 admin 角色自动绕过 owner。
- legacy conversation 统一回填给稳定的管理员 subject `user_admin`，作为管理员隔离区；不猜测或分配给普通用户。管理员因此可继续查看历史开发会话，但不会获得对其他用户新会话的隐式越权。
- Web 遇到统一的 `404 conversation_not_found` 时跳转到当前账号可访问的会话（无会话时返回聊天首页），并保留明确提示，不泄露资源是否存在的更多细节。

## 4. 非目标

- 本切片不引入多租户、组织树或细粒度 RBAC。
- 不同时实现 OIDC、服务间 mTLS、原生向量检索或部署清单。
- 不修改历史消息文本、citation 或 provenance。

## 5. 验收

- 同一用户的 create/list/detail/message/source/revalidation 全部成功。
- 另一普通用户对上述资源的访问全部失败，且不能通过 ID 枚举读取内容。
- admin evidence 仍能按 conversation 查询审计事实；admin chat API 不默认读取他人会话。
- migration 对空库、现有 conversation、重复启动均安全；Postgres 是 owner 权威。
- Backend full/race、isolated Postgres、integration/e2e、Web lint/typecheck/build 与 production Playwright 通过。

## 6. 回滚

- migration 只追加 `owner_id` 与索引，不删除历史数据。
- 代码回滚仍可忽略新列；禁止把 owner 校验改为 Web-only。
- legacy backfill 只允许分配给稳定管理员 subject `user_admin`，不得分配给普通账号或按会话内容猜测归属。

## 7. K9-S0 验收记录

- Backend：migration `010`、owner-filtered Memory/Postgres repository、session subject create/list/detail/JSON+SSE message/source/revalidation guard 已落地；Runtime request 使用 owner subject。
- 权限：跨 owner 的 detail/message/source/revalidation 统一为 `404 conversation_not_found`；admin chat 不 bypass，admin evidence 维持全局审计，普通用户 evidence 为 `403`。
- 数据：隔离 Postgres 验证 `001-010`、legacy backfill、NOT NULL、空 owner fail-closed；开发主库已实际升级，历史 message/citation/provenance 未改写。
- Web：Conversation contract 包含 `owner_id`；直达不可访问会话会跳至可访问会话并显示持久提示。
- 验证：Backend full 与 race、`make postgres-k9-ownership-smoke`、Web lint/typecheck/build、全栈 `42 passed`、production Playwright `18 passed`。
- 下一动作：冻结 `K8-R2` 的第二 embedding model 受控比较；保持 ownership、K5-K7/C1/C2 contract 不动。

## 8. K8-R1 验收记录

- 设计已冻结于 [K8 Native Dense Retrieval 设计](../../design-docs/single-agent-v1-k8-native-retrieval-design.md)：独立 dense collection、OpenAI-compatible embedding、Qdrant server-side query/filter，失败不回退 lexical。
- 已实现：Runtime 的实际 float vector upsert、`/points/query`、revision/file/source-format filter 与 schema 验证；Backend 将 retrieval config 的 `embedding_model` 传入 Runtime；eval report 记录 retrieval mode/model/dimension/collection。
- 已验证：Runtime `89 passed`、Backend full tests、词法 Qdrant compatibility smoke 与真实 native Qdrant transport smoke、最终 prompt_v2 R5 gate `30/40`（grounded `0.6571`，retrieval/citation `0.9714`，refusal `1.0`，runtime error `0`）、全栈 integration/e2e 与 production Web build 通过。
- 已关闭：Ollama `all-minilm:latest` 实测为 `384` 维，`make runtime-native-dense-smoke` 已通过真实 embedding、ingest/reload、revision scope 和 citation provenance；临时 collection 已清理。最终 R5 gate 为 `30/40`、grounded `0.6571`、retrieval/citation `0.9714`、refusal `1.0`、runtime error `0`。
- 下一动作：先冻结 `K8-R2` 的单变量模型比较（至少保留 `all-minilm` 基线并选择第二个 embedding model），再下载/配置第二模型和运行固定 dataset 比较；不得以 R1 功能验证替代质量选型。
