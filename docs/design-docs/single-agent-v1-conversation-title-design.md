# 单智能体 V1 会话标题设计

## 文档控制

- 状态：已冻结，待实现
- 日期：2026-07-21
- 范围：会话默认标题、首问自动命名和用户手动重命名

## 决策

- 新会话由 Backend 以“新对话”创建，不再使用英文全局序号。
- Backend 不调用 Runtime 或模型生成标题。它从第一条成功持久化的用户消息生成确定性标题：`strings.Fields` 规范化空白、按 Unicode rune 截断至 28 个 rune；超出时保留 27 个 rune 并追加 `…`。
- 用户手动输入经相同空白规范化后必须为 1–48 个 Unicode rune；空白、过长和非法 UTF-8 以 `422 validation_error` 拒绝，绝不静默截断用户标题。
- 标题内部来源为 `system_default`、`first_user_message`、`user_edited`、`legacy`。它是 persistence state，不加入 Web/API 的 `Conversation` JSON。
- Postgres 迁移新增非空 `title_source`，既有记录设为 `legacy`；MemoryStore 同步支持该状态。迁移为只追加、无需回填标题，回滚采用向前修复。

## 写入边界与一致性

```text
Web new conversation -> POST /conversations -> Backend writes “新对话” (system_default)
first successful exchange -> repository transaction writes messages + derived title (first_user_message)
manual rename -> PATCH /conversations/{id} -> owner-filtered repository write (user_edited)
```

- 自动命名只能位于已有 exchange 的权威 repository transaction 中，不能由 Web、Runtime 或异步 worker 成为第二写入者。
- 所有会话读/写继续以 `owner_id` 为权威；不存在与非 owner 的 PATCH 都返回相同 `404 conversation_not_found`。
- PostgreSQL 对 conversation 行采用已有写锁串行化；MemoryStore 在其 mutex 内维持相同语义。首个成功 transaction 获得自动命名权；手动重命名将来源改为 `user_edited`，后续自动命名必须跳过。
- 自动命名并不要求 assistant 成功以外的额外异步步骤；若 exchange 未持久化，标题不变且下一次成功首问仍可命名。

## Web 交互

- 默认标题由服务端回包和列表刷新显示。消息流完成后既有 `loadConversations({ silent: true })` 负责读取标题；当前已选会话的内存 title 也必须同步为刷新结果或详情结果。
- 侧栏会话行在桌面展开态与移动抽屉提供“重命名会话”按钮。编辑时避免 Link 嵌套交互；保存用 `PATCH`，成功后以返回的 conversation 更新列表与选中会话，失败保留草稿和焦点。
- 收缩侧栏不增加无法发现的编辑控件；用户先展开侧栏再重命名。

## 非范围与拒绝方案

- 本切片不做 AI 摘要、标题批量回填、删除、会话分组或跨设备草稿同步。
- 不向客户端暴露 `title_source`，避免 UI 将内部写入策略当成权限或状态机。
- 不因标题写入改变 `updated_at` 之外的 conversation 配置快照，且不改写 message、citation、execution evidence 或知识 provenance。

## 验证

- Backend：空标题创建、首条成功消息、第二条不覆盖、手动改名、无效标题、非 owner 和并发/重试顺序。
- Postgres：迁移后 legacy 行可读；标题与消息同 transaction，失败不留下半更新。
- Web：创建后显示“新对话”；首轮完成后显示后端标题；重命名键盘保存/取消/错误；普通用户不能重命名他人会话。
