# 单智能体 V1 聊天恢复与流式体验计划

## 目标

- 让本地开发栈在管理员上传并发布首个 revision 后形成可检索的 active snapshot。
- 保留 K5 的 fail-closed 保护：没有 active published snapshot 时不生成无证据回答。
- 将聊天升级为即时反馈、稳定自动跟随、可中止且不丢失草稿的流式对话体验。

## 已确认根因

- `2026-07-15` 本地 `see_agent` 数据库没有 `knowledge_snapshots` 记录，`knowledge_active_snapshot.snapshot_id` 为 `NULL`。
- 预览 Backend 使用 Postgres，但以 `K3_DELIVERY_ENABLED=false` 启动；即使上传新 revision，也不会进入 Runtime index/`ready`，因此无法显式发布为 snapshot。
- K5 对此返回 retryable `knowledge_snapshot_unavailable`，且不写 user/assistant exchange；这是预期的数据完整性保护，不是模型或 SSE 故障。

## 设计决策

- 开发栈由 `backend-rag-dev` 统一提供 Postgres、Redis、Runtime 与 K3 delivery 环境变量；它不改变默认 `backend-dev` 的轻量 smoke 行为。
- 初始语料通过管理员生命周期 API 上传、等待 `ready` 后显式 publish；不直接写入 snapshot 表，也不允许空 snapshot。
- Web 的 pending turn 只属于浏览器本地状态。`message.started` 是持久化消息的唯一进入点；失败前未收到 started 时，本地回合标为失败、可重试但不冒充历史记录。
- 当前只允许一个 active generation。输入框在生成期间可编辑草稿，但第二次发送被禁用，避免引入未定义的并发会话写入语义。

## 验收

- [ ] `backend-rag-dev` 启动后，上传 Markdown revision 能被 Runtime index 为 `ready`，并可通过 publish 产生 active snapshot。
- [ ] 使用 published snapshot 的流式聊天返回 `message.started`、多个 `message.delta` 和 `message.completed`，答案含 scope/citation provenance。
- [ ] 无 active snapshot 时，Web 立即显示本地失败回合和明确的管理员动作说明，且 Backend 不写 exchange。
- [ ] 前端在首字节前显示生成占位，delta 时自动跟随；上滚后出现回到底部入口；停止生成不清空正在编辑的下一条草稿。
- [ ] `Enter` 发送、`Shift+Enter` 换行、失败重试和窄屏布局均通过浏览器测试。

## 执行

- [x] 补齐本地 RAG 启动命令与最小开发语料 bootstrap。
- [x] 建立并验证当前开发库的首个 published snapshot。
- [x] 重构 Web 流式 pending turn、滚动与输入交互。
- [x] 补齐 Backend/Runtime、浏览器和全链路验收，更新运行文档。

## 验收记录（`2026-07-15`）

- `make runtime-rag-dev`、`BACKEND_ADDR=:8081 make backend-rag-dev` 与 `BACKEND_API_BASE_URL=http://127.0.0.1:8081 make bootstrap-rag-knowledge` 已形成真实本地 RAG 链路；bootstrap 重复执行返回既有 `snap_001`。
- `snap_001` 为 published，active pointer 指向它，包含一个 Runtime index 成功的 published revision。真实 SSE 问答收到了 `message.started`、多个 `message.delta` 和 `message.completed`。
- 新增浏览器流式测试覆盖首个流事件前的 pending turn、草稿保留、`Enter` 发送、`Shift+Enter` 换行和完成态；空 snapshot mock 覆盖本地失败回合与可重试语义。
- `npm run lint`、`npm run typecheck`、`npm run build`、针对性 Playwright `6/6` 与 `make integration` 的 `41/41` 均通过。
- 验收生成的会话、消息、引用和核验记录已清除；开发语料及 `snap_001` 保留。

## 登录恢复补充（`2026-07-15`）

- 目标：Backend 会话失效时不把用户停留在聊天错误态；保留当前输入，完成登录后回到同一会话路由。
- 状态边界：仅 `ApiError(code=unauthenticated)` 触发恢复；`sessionStorage` 以当前 `/chat` 路径作为 key，内容在恢复后立即删除，不进入 Backend 或 conversation persistence。
- 验收：失效消息请求跳转登录页且包含原 `next`；登录后返回相同 `/chat/[conversation_id]` 并恢复 textarea 内容；正常 `403`、RAG scope 和 runtime 错误不触发登录跳转。
- 结果：新增 `chat-auth-recovery` Playwright 用例覆盖失效请求、logout、同路径回跳和草稿恢复；针对性浏览器测试 `4/4` 与全链路 `41/41` 通过。
