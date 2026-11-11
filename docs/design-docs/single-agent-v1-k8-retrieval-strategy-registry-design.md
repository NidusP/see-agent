# 单智能体 V1 K8 受控检索策略注册表设计

## 文档控制

- 状态：`verified`
- 版本：`v1.2`
- 更新日期：`2026-07-17`
- 工作包：`K8-R2a`
- 依赖：`K5` snapshot scope、`K6` revalidation、`K7` retrieval config/evidence、`K8-R1` native dense

## 1. 决策

检索实现采用**受控的内建策略注册表**，而不是允许管理员上传或动态加载任意 Python 插件。

- 管理员在 retrieval config 中选择已注册的 `retrieval_strategy`；该选择随 `retrieval_config_version` 固化。
- Backend 只接受共享契约列出的 strategy id；Runtime 只构造本地代码中注册的实现。
- Runtime deployment 一次只启用一个 active strategy profile。知识问题的 request strategy 必须与该 profile 精确相等；不相等即 fail-closed，不能降级到另一种策略。
- 初始注册表只有 `lexical_compat_v1` 与 `native_dense_v1`。前者仅为历史会话、受控回滚和过渡兼容保留；后者复用 K8-R1 的 native dense/Qdrant 实现。

这是一种可扩展的“插件式边界”：未来 hybrid、sparse、rerank 可以以新的受审计 strategy id 和内建 factory 加入，而不是把不受信任代码带入生产 Runtime。

## 2. 领域契约与版本边界

### 2.1 Retrieval config

`RetrievalConfig` 新增不可变字段：

| 字段 | 示例 | 语义 |
| --- | --- | --- |
| `retrieval_strategy` | `native_dense_v1` | 本次会话应使用的已注册检索实现与行为版本。 |
| `embedding_model` | `all-minilm` | 该策略使用的 embedding provider model；lexical compatibility 仍保留既有标识。 |
| `top_k` / `rerank_enabled` | `6` / `true` | 该策略已支持的检索参数。 |

Postgres 的 retrieval config 继续是权威版本记录；conversation 只固定 `retrieval_config_version`。执行证据通过读取该 immutable config 记录 strategy，不能从当前 Runtime 环境推断或改写历史事实。

### 2.2 Backend 到 Runtime

`ChatRuntimeRequest.response_overrides` 新增 `retrieval_strategy`。Backend 在 normal chat 和 K6 revalidation 中从 conversation 固定的 config 写入该值，并和 `embedding_model`、`top_k`、`rerank_enabled` 一起发送。

Runtime 在任何索引、embedding 或 Qdrant 调用前验证：

1. knowledge query 必须携带 `retrieval_strategy`；
2. 值必须等于 active Runtime strategy profile；
3. native dense 的 `embedding_model` 仍必须等于 active embedding adapter；
4. 任一失败返回既有 retryable `retrieval_unavailable`，不写成功回答或 citation。

受控普通交流不调用 retrieval，因此不需要策略匹配；它仍不能伪造知识 evidence。

### 2.3 注册表与部署配置

Runtime 新增 `RUNTIME_RETRIEVAL_STRATEGY`，值为注册 strategy id。为兼容已部署环境，旧 `RUNTIME_RETRIEVAL_MODE=lexical|native_dense` 在本切片继续可读，分别映射为 `lexical_compat_v1|native_dense_v1`。

- 两者同时设置且映射结果不同：启动失败。
- 未设置新变量时维持旧 mode 默认 `lexical`，即 `lexical_compat_v1`。
- 未注册 strategy、缺 native dense 必填配置、collection schema 或 embedding dimension 不兼容：启动失败。
- strategy factory 负责构造对应 embedding adapter 和 vector store；RetrievalPipeline 继续只依赖通用 adapter/store 协议。

因此「策略」不会成为按请求动态 import 的插件系统。一个新增策略至少需要：共享 id、Runtime factory、部署配置说明、配置 API allow-list、harness 条件、scope/provenance/失败测试和评审。

## 3. 数据流与切换

```text
管理员保存已注册 retrieval strategy
  -> Backend 创建 immutable retrieval config version
  -> 新 conversation 固定该 version
  -> Backend request override(strategy/model/top_k/rerank)
  -> Runtime 校验 active strategy profile
  -> registered strategy 执行 scoped retrieval
  -> Backend 原子写入 answer/citation/execution evidence
```

`native_dense_v1` 切换不是一次配置保存即可完成：先部署对应 dense Runtime profile，再以相同 embedding model 重新索引 immutable revision、验证并发布 snapshot，最后创建 matching retrieval config，让**新会话**绑定它。已有 conversation 继续使用旧 config；如果其 profile 不再部署，问题会明确失败而不是用不同策略假装复现。历史回答和 provenance 永不回填。

## 4. 失败、回滚与安全边界

- 任何未知 strategy、profile/request 不匹配、模型不匹配或 provider/Qdrant 失败均 fail-closed；禁止 lexical fallback。
- Postgres strategy/config version 是权威事实；Qdrant 仍只是 immutable revision 的可重建投影，K5 revision allow-list 和 payload provenance validation 不变。
- 回滚是重新部署旧的受控 Runtime profile，并让需要继续对话的**新** conversation 使用对应 config；不修改已写 config/message/evidence。
- 管理员无权执行任意脚本、上传 wheel/zip、指定模块路径或覆盖 factory。动态代码加载、远程 plugin registry、租户自定义 retrieval code 不在 V1 范围。

## 5. 管理体验与证据

配置页面使用选择框展示当前内建 strategy；禁止自由文本输入。保存后明确提示只影响新会话。execution evidence 的 retrieval JSON 必须显示 `retrieval_strategy`，使管理员能回答一条回答在何时使用了哪个策略、embedding model、snapshot 和 config version。

## 6. 验收

- Postgres migration 为既有 retrieval config 安全回填 `lexical_compat_v1`，不改写历史 answer/evidence。
- API/Web 只能创建两个已注册 strategy；未知值得到 validation error。
- Backend normal chat 和 revalidation 均透传 strategy；execution evidence JSON 包含 strategy。
- Runtime 覆盖：旧 mode 映射、新变量、冲突配置、未知 strategy、native dense 配置缺失和 request/profile 不匹配；后者不得触发 embedding/vector-store search。
- K5 scope、K6 revalidation、C1 citation anchor、C2 controlled intent 及 K7 evidence 回归保持。
- 每次策略实现或模型配置变化都重跑 Runtime tests、native Qdrant/provider smoke、知识演进 fixture 与固定 R5 gate；模型比较报告记录 strategy id 和 deployment profile。

## 7. 非目标

- 本切片不选择第二 embedding model，也不改变 R5 dataset/grader/prompt；这些仍属于 K8-R2 模型比较。
- 不实现 sparse/hybrid fusion、provider reranker 或多 profile 单进程复用。
- 不执行实际业务 snapshot 的 dense cutover，也不删除 lexical 兼容代码或 collection。

## 8. 实现与验收记录

- `2026-07-17`：共享 JSON/OpenAPI、Postgres migration `011_retrieval_strategy_registry`、Backend config/chat/revalidation/evidence、Runtime factory registry/profile check、Web strategy select 和 eval report 均已实现。既有 config 在 migration 中回填 `lexical_compat_v1`；历史 answer/evidence 不更新。
- 已通过：Backend full test、Runtime `91 passed`、eval strategy contract `11 passed`、Web lint/typecheck/build、全栈 integration/e2e `45 passed`、独立生产构建 Web 的 Playwright 管理证据 smoke，以及 fixed `prompt_v2` control gate（`31/40`、grounded `0.7357`、retrieval/citation `0.9714`、refusal `1.0`、unexpected runtime error `0`）。该 gate 的 report 明确为 `lexical_compat_v1`，不构成 dense 模型选型结果。
- `2026-07-17` 环境验收：Docker Desktop 恢复后，`make postgres-k7-smoke` 通过真实 migration `011` 与 K7 evidence transaction；`make runtime-native-qdrant-smoke` 通过 native collection、filtered query/delete；使用 Ollama `all-minilm`（`384` 维）的 `make runtime-native-dense-smoke` 通过真实 embedding、ingest/reload、revision scope 与 citation provenance。验收中发现 dense smoke 仍沿用旧 request 形状而被 strategy exact-match 正确拒绝；已将该 smoke 固定为显式 `native_dense_v1` profile 和 request override 后复跑三项均通过。K8-R2a 转为 `verified`；不代表 dense business cutover 或第二 embedding model 的质量结论。
