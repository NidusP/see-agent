# C3 会话上下文解析交付计划

## 文档控制

- 状态：`verified`
- 日期：`2026-07-30`
- Owner：`p`（设计、实现、验收）；关联设计：[C3-R2 会话上下文路由设计](../../design-docs/single-agent-v1-conversation-context-resolution-design.md)。

## 目标

以模型主导的结构化语义路由替代 C3-R1 的 Backend phrase router。模型结合有界历史得出真实 intent、话题关系和 route；业务问题必须进入 scoped RAG，非业务交流使用受控短回复，歧义问题澄清。`controlled_confirmation` 被删除。

## 范围与非目标

- 范围：`context_routing_required` request、Runtime conversation router、route/intent/evidence contract、Backend scope deferral and validator、C2-R1 deletion、JSON/SSE/Web adapters、A1 fixtures 与真实 Qwen 验收。
- 非目标：长期记忆、工具、开放式闲聊、模型直出业务答案、自动发布候选策略、历史数据回填、LangChain/LangGraph 引入。

## 共享契约与写入边界

`p` 是 shared contract、migration 和全部实现的唯一 writer。Backend/Postgres 仍是 user message、assistant、execution evidence 和 clarification state 的唯一 writer；Runtime 只提出受约束的 resolution 并执行已验证 route。模型不能改变 owner、scope、snapshot、config 或写入路径。

## 执行步骤

- [x] C3-R2-0：产品与架构冻结。
  - Owner：`p`
  - 验收：本文、C3-R2 设计、产品规格及执行队列一致；明确删除 C3-R1 phrase routing。
- [x] C3-R2-1：shared contract 与 Backend boundary。
  - Owner：`p`
  - 范围：request `context_routing_required`、response `conversation_resolution`、删除 `controlled_confirmation`、scope deferral、strict invariants 与 legacy read compatibility。
  - 已验证：Backend 全量 Go 测试通过；`context_routing_required` 仅为 request internal marker，response/public Web enum 不含该值或 `controlled_confirmation`；无 scope 的受控 route 可持久化，strict profile 的 knowledge route 必须有 immutable scope。
- [x] C3-R2-2：Runtime router 与执行分支。
  - Owner：`p`
  - 范围：一次 JSON-mode model router、冻结 direct templates、RAG/clarification route、JSON/SSE evidence；移除 C3-R1 `_may_depend_on_context` 和 confirmation branch。
  - 已验证：Runtime `129 passed`；模型 router 只接受完整 JSON、不可恢复的 schema/basis 错误返回 `context_resolution_failed`，不回落到关键词或伪回答；direct route 仅用固定模板。
- [x] C3-R2-3：Web、persistence 和评测。
  - Owner：`p`
  - 已完成：Web enum/read model、Postgres `context_resolution` evidence payload、Backend-owned `router_policy_version=conversation_router_v1` stamping、A1/Q1 router fixture schema 与 `data/eval-datasets/fullstack/c3-r2-router-quality.jsonl`、非流式 actual-Qwen JSON acceptance。
  - 已验证：真实 Runtime SSE 返回完成事件；浏览器中的“你好”显示受控能力说明且无 citation；“那个怎么办？”显示要求用户直接描述事项的受控澄清提示；两条路径均由实际 `qwen3:14b` 生成路由并由 Backend 持久化。此前已验证的差旅问题及“超过期限呢？”继续使用 scoped RAG 和 citation。
  - 验收：真实 route/evidence/retrieval/citation facts 一致；无 `controlled_confirmation` 新记录；router policy 可由 Q1 run 精确绑定。

## 风险、回滚与验收门

- 每条普通消息新增一次 router 模型调用；以真实 Qwen 延迟和失败率建立质量门，不用关键词跳过。
- 模型可能把业务误判为 simple reply；direct route 只能输出冻结模板，因而不可能产生无证据业务事实。模型将业务正确判为 RAG 后，scope/retrieval/citation 仍是硬约束。
- 新 router policy 作为 Backend code-owned `conversation_router_v1` 记录在 execution evidence 的 `context_resolution.router_policy_version`，并可进入 Q1 candidate；不能直接覆盖既有 release。
- 回滚只能前滚到上一受支持 router policy；不得重新启用 C3-R1 phrase list、heuristic fallback 或全历史检索。
- 失败 attempt 的完整 router evidence 仍需独立 append-only contract；本包至少记录 `conversation_routing_failed` code/stage，不能伪称完成态 evidence。

## 当前状态

- C3-R1 已被删除，不再是产品或性能优化路径。
- 2026-07-28 本地 `fullstack_dev` 实测：`你好 → 真的吗？` 由 Qwen 路由为 `capability_help / controlled_reply`，不检索；`差旅报销提交期限是什么？` 走 scoped `native_dense_v1` 并返回一个 evidence anchor；随后 `超过期限呢？` 使用上一问题的 immutable message id 作为 `follow_up` basis，继续 scoped RAG 并返回一个引用。早期 Qwen 曾把 standalone `resolved_query` 和 follow-up basis 生成成非法值；已通过明确 JSON examples 促使模型输出合法值，错误期间均以 `context_resolution_failed` failed attempt 收口，未持久化回答。
- 2026-07-29 C3-R2-3 已补 `conversation_router_v1` execution evidence、A1/Q1 router fixture dataset 与 eval runner router scoring；同时修正 SSE completion 以 validated Runtime intent 而非 request marker 判断 scoped/controlled persistence。
- 2026-07-29 已完成真实 SSE 与浏览器成功/失败路径验收；C3-R2 转为 `verified`。router policy 未来如变更，必须作为 Q1 冻结条件评测和发布；不得恢复任何 phrase router。
- 下一动作：开始 A1-0 的引用来源计数/过滤体验修正，不在本工作包继续扩张会话路由。
