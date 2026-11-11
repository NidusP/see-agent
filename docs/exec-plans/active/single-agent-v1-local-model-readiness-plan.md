# 单智能体 V1 本地模型 Warm-up 与调用预算计划

## 文档控制

- 状态：`completed`
- 日期：`2026-07-28`
- 触发：Qwen native-dense 本地全栈首次/间歇聊天在 Backend `20s` deadline 到期后返回通用 `503 runtime_unavailable`，而 Runtime 的 Ollama 预算为 `90s`。
- 关联：[Fail-Closed 执行设计](../../design-docs/single-agent-v1-fail-closed-execution-design.md)、[Runtime 摄取与检索设计](../../design-docs/single-agent-v1-runtime-ingestion-design.md)、[本地数据重置与 RAG 种子计划](single-agent-v1-local-data-reset-and-rag-seed-plan.md)。

## 1. 目标与非目标

目标是在 `fullstack_dev` 与 `production` 中，使“Runtime ready”表示已成功完成一次对配置 generation model 的真实短生成；并让 Backend 的调用预算不再比 Runtime/Ollama 的模型预算短。失败必须继续 fail-closed，同时在 execution attempt 中保留可诊断的失败类别。

不做模型回答 fallback、预生成用户答案、自动重试用户请求、修改已持久化的失败/成功消息，或把模型输出作为 readiness API 的业务内容。

## 2. 跨服务契约

| 边界 | Owner | 契约 |
| --- | --- | --- |
| Runtime startup → Ollama | AI Runtime | startup 时以配置 model 发起无用户可见的最小 generation probe；只接受非空响应。失败使应用启动失败，不能返回 ready。 |
| Runtime `/readyz` | AI Runtime | 保留 Qdrant、模型 endpoint、对象存储短探测；另要求本进程 warm-up 已成功。响应不包含探针文本、token 或 endpoint secret。 |
| Backend → Runtime chat | Backend | `BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS` 是 Runtime HTTP client 和 Service normal/stream/revalidation 调用的统一上限；默认 `60s`，必须为正整数。 |
| Runtime failure → Backend attempt | Runtime/Backend adapter | Backend 保留公共 `runtime_unavailable`，但 attempt 记录精确 `error_code/error_stage`：连接、Runtime HTTP、模型 provider、模型 generation timeout、Backend deadline。 |

预算规则：本地模板设 `Backend=60s`、`Runtime/Ollama=90s`。Backend 的预算必须小于等于 Runtime provider budget，避免 Backend 在模型仍正常执行时过早取消；部署方可按资源调整，但 preflight/启动校验拒绝无效值。

## 3. 失败语义与安全

- warm-up 是操作性 probe，不经过 answer pipeline，不读取知识库，不写 conversation/message/citation/attempt，也不向用户泄漏 probe 文本。
- warm-up 网络失败、模型超时、HTTP 非成功或空输出均阻止 Runtime ready；不会启用 heuristic、mock 或旧模型。
- 用户请求的 Backend deadline/Runtime provider timeout 都是 retryable `failed` attempt；不会写 completed assistant 或 citation。
- 用户 API 继续使用安全的通用 `runtime_unavailable` envelope，防止将内部 topology 暴露给普通用户；系统管理员 evidence 可读取细分 code/stage。

## 4. 实施与验收

1. Runtime：实现模型 probe、warm-up state、readiness/diagnostic 暴露和测试；默认 fullstack/production 必须 warm-up 成功。
2. Backend：实现显式 timeout 配置、统一注入 normal/stream/revalidation、typed runtime failure 到 attempt code/stage 的映射及测试。
3. 文档/模板：记录默认预算、启动日志和排障步骤。
4. 验收：Runtime 单测覆盖 warm-up 成功、失败和 readiness gate；Backend 单测覆盖 deadline/连接/Runtime error 的 attempt 分类；本机重启 Runtime 后确认 warm-up 日志、`readyz`、diagnostics 和一次真实聊天均通过。

## 5. 回滚与残余风险

这是 forward-only 行为改造，无数据迁移。回滚只能恢复旧二进制/配置；不得通过关闭 warm-up 或缩短 deadline 来伪称模型已准备。Warm-up 只能证明启动时的一次真实生成，不保证长时间空闲后的模型常驻内存；生产还应监控首 token 延迟、超时率和内存压力。

## 6. 进度

- 已完成：Runtime 对 `fullstack_dev`/`production` 使用 lifespan warm-up；成功状态进入 `/readyz` 与受保护 diagnostics，失败不会启动为 ready。probe 直接调用真实 provider，不读取检索、不写业务数据。
- 已完成：Backend 使用 `BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS`（默认 `60`）统一 normal、stream、revalidation 等 Runtime 调用预算；preflight 校验为 `5–600` 秒且不超过 `LOCAL_LLM_TIMEOUT_SECONDS`。typed Runtime 错误在 attempt 中分为 Backend deadline、transport、Runtime HTTP 与模型 provider 类别，公共响应仍为安全的 `runtime_unavailable`。
- 自动化验收（2026-07-28）：`make backend-test` 全通过；`make runtime-test` 为 `122 passed`；`scripts/ops/test_fullstack_preflight.sh` 与 `make fullstack-smoke` 通过。
- 本机验收（2026-07-28）：重启后的 Runtime `/readyz` 返回 `200`，诊断显示 `ollama_native` 且 `warmup=ready`，检索为 `native_dense_v1/qwen3-embedding:0.6b`。Backend `/healthz`、`/readyz` 均返回 `200`。真实、已认证的知识问答耗时 `28.66s`（超过旧 `20s` deadline）仍以 `succeeded` 完成，返回 `snap_004` 的 1 条 provenance citation；回答与当前已发布的“差旅与费用管理制度”原文（行程结束后 10 个自然日）一致。
- 残余风险与唯一下一动作：warm-up 仅证明启动时可生成；在真实部署中接入首 token 延迟、模型 timeout 率和资源压力监控，再根据运行证据调整 `60s/90s` 预算，不能以 fallback 掩盖故障。
