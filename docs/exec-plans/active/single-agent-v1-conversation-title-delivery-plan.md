# 会话标题交付计划

## Goal

- 用户可从“新对话”开始提问，首个成功持久化的问题自动成为简洁标题，并可随时手动重命名。
- 范围：Go Backend、Memory/Postgres repository、OpenAPI/Next proxy、聊天侧栏与回归测试。
- 非范围：AI 摘要、历史批量改名、删除/分组、Runtime 改动。

## 已冻结约束

- 权威设计：[会话标题设计](../../design-docs/single-agent-v1-conversation-title-design.md)。
- `PATCH /api/chat/conversations/{conversation_id}` 是唯一手动标题写入 API；owner-filtered 404 语义与既有聊天资源一致。
- 自动标题只能随首个成功 exchange 在 repository transaction 内写入；Postgres 是持久化权威，MemoryStore 保持等价开发语义。
- `title_source` 仅为内部 persistence state；迁移保留所有历史标题为 `legacy`。

## Execution

- [x] 契约与设计冻结
  - Owner：p
  - Evidence：产品规格、Backend/Web/API/OpenAPI 与本计划已同步。
- [x] Backend 与持久化实现
  - Owner：小弟；p 已复核
  - Write scope：`services/backend-go/**`（含下一条 append-only migration 与测试）
  - Requirements：标题规范化、source state、自动命名同 transaction、owner-filtered PATCH、Memory/Postgres parity。
  - Verification：`make backend-test`、`make postgres-k9-ownership-smoke` 均通过；覆盖 migration `017`、legacy、默认标题、首问自动命名、手动改名与 ownership。
- [x] Web 与代理实现
  - Owner：小弟；p 已复核
  - Write scope：`apps/web/**`、相关 Web E2E
  - Dependency：以冻结的 OpenAPI 为准；不得改 contracts/docs/migration。
  - Requirements：PATCH proxy/client、无 Link 嵌套的侧栏行内编辑、成功/失败/键盘状态、标题刷新同步。
  - Verification：`make web-lint`、`make typecheck`、`make web-build` 通过；`conversation-title-ui.spec.ts` 退出码 `0`。
- [x] 集成验收与文档收口
  - Owner：p
  - Verification：OpenAPI YAML parse、完整 Backend test、Postgres migration/ownership smoke、Web lint/typecheck/build、目标 Playwright、真实 RAG Backend API 默认/重命名/422 路径均通过。

## Acceptance

1. 新建空会话显示“新对话”，没有英文计数标题。
2. 首个成功持久化的问题只命名一次，第二个问题不会覆盖。
3. 手动重命名成功后持久保存，自动命名和重试不能覆盖它。
4. 无效标题返回 422；非 owner 与不存在会话的 PATCH 都是 404。
5. 迁移不更改历史标题，失败的 exchange 不留下新的自动标题。
6. Web 在桌面与移动侧栏可键盘编辑，并在 API 失败时保留用户输入。

## Active Status

- Current state：verified。
- Blockers：无。
- Last verified：新会话 `POST` 实测返回“新对话”；`PATCH` 实测返回规范化后的标题；空白标题实测 `422`。Backend 已按 `make backend-rag-dev` 重启并保持运行。
- Next action：后续如需 AI 摘要标题，另立契约，不得改变本切片的确定性首问命名。
