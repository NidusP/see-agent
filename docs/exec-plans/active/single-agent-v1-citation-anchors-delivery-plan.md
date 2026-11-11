# C1 回答内证据锚点交付计划

## 文档控制

- 状态：`verified`
- 版本：`v1.0`
- 更新日期：`2026-07-15`
- Owner：`p`
- 权威设计：[C1 回答内证据锚点设计](../../design-docs/single-agent-v1-citation-anchors-design.md)

## 目标

- 用户可在回答中 hover、focus 或点击被证据支持的结论，查看这段结论对应的来源。
- 每个可见锚点都可由持久化 message、citation、snapshot 和 revision 追溯。

## 执行

- [x] 冻结 `citation_anchors` 契约、owner、失败语义和兼容边界。
- [x] 更新 JSON Schema、OpenAPI fixture、Python/Go/TypeScript DTO。
  - 依赖：本计划与 C1 设计。
  - 验证：schema parse、Runtime contract tests、Go compile、Web typecheck。
- [x] 实现 Runtime marker prompt、stream parser 与 deterministic fallback anchors。
  - 依赖：共享 DTO 冻结。
  - 验证：Unicode、跨 token、多 rank、无 marker、非法 rank 单测。
- [x] 实现 Backend `006` migration、anchor 校验、同事务写入和读取。
  - 依赖：Runtime response DTO。
  - 验证：Postgres rollback/restart/legacy 兼容与 HTTP stream/non-stream 回归。
- [x] 实现 Web 下划线、hover/focus 来源卡和可固定详情。
  - 依赖：message response 已提供 anchors。
  - 验证：普通用户、来源失败、键盘、桌面/移动端 E2E。
- [x] 跑质量门、全栈回归并更新执行证据。

## 约束

- `packages/contracts/**`、Postgres migration 和 K5 provenance write path 只有 `p` 修改，不能并行编辑。
- 未实现 anchors 的历史消息必须保持可读；不得回填，也不得把消息级 citation 伪装为精确锚点。
- `C1` 不改变 snapshot scope、freshness scan、revalidation state machine 或管理员角色权限。

## 当前状态

- 已完成：`citation_anchors` JSON/OpenAPI/Python/Go/TypeScript DTO、Runtime marker parser、deterministic fallback、`006_citation_anchors` migration、同事务写入/重启读取、Web 下划线与 hover/focus/点击详情。
- 验证：`make runtime-test` 为 `68 passed`；`make backend-test` 通过；隔离 K5/Postgres migration + reopen smoke 通过；Web lint/typecheck/build 通过；Playwright citation `3 passed`、完整 UI `14 passed`、真实 RAG stream anchor smoke 通过；`make integration` 为 `41 passed`；`make eval-runtime-gate` 以 `40` 条 runtime-local 样本、`9` 条失败通过既有 R5 门。
- 后续：聊天壳层重做时，将现有 citation inspector 复用为可关闭的桌面/移动端详情抽屉，不改 C1 数据契约。
