# 单智能体 V1 Conversation Ownership 设计

## 文档控制

- 状态：`verified`
- 版本：`v1.0`
- 更新日期：`2026-07-16`
- 工作包：`K9-S0`
- 依赖：现有 session subject、Postgres migrations `001-009`、K5/K6/K7/C2 chat/evidence 契约

## 1. 用户可见目标

每个已登录用户只能看到和操作自己创建的 conversation。管理员继续拥有全局 execution/delivery/eval evidence 查询能力，但管理员角色不会在聊天 API 中自动读取普通用户的私有会话。

## 2. 权威与边界

- `conversations.owner_id` 是唯一 ownership 权威，值为认证系统的稳定 `UserID`，不是可变 username 或客户端 cookie。
- Backend session 是 subject 来源；Web 只做导航体验，不决定授权。
- owner 校验必须发生在返回 conversation/message、调用 Runtime、读取 citation/source 或创建/读取 revalidation 之前。
- Postgres/Memory repository 提供 owner-filtered list/read；后台 worker 的内部读路径与产品 API 分开使用。

## 3. API 契约

- Conversation response 新增必填 `owner_id`。
- create 自动从 session 写 owner；客户端不能提交或覆盖 owner。
- list 只返回 owner 的会话。
- detail/message/source/revalidation 对不存在、message-conversation 错配和非 owner 统一返回 `404 conversation_not_found`，避免枚举。
- admin evidence API 保持 admin-only 全局查询，不改变 K7 历史 evidence。

## 4. 数据迁移

- append-only migration `010 conversation_ownership`：新增 nullable `owner_id`，将所有 legacy conversation 回填为 `user_admin`，再设置 NOT NULL，并创建 `(owner_id, updated_at DESC)` 索引。
- `user_admin` 是稳定管理员 subject，用作 legacy 管理隔离区；不把历史会话分给普通用户，也不根据内容猜测。
- message、citation、provenance、revalidation 和 execution evidence 不改写。
- migration 必须可重复启动；代码回滚可以忽略新列，数据不丢失。

## 5. 服务写入与读取路径

```text
session token -> Backend UserID
  -> create conversation(owner_id=UserID)
  -> list/read owner filter
  -> message/source/revalidation owner guard
  -> existing K5/K6/C1/C2 write transaction
```

Runtime request 的 `user_id` 使用 owner subject，不再写固定 `user_dev`。stream prepare 完成 owner 校验后，completed write 复用冻结 request；owner 字段不可由 Runtime 改变。

## 6. 失败与竞态

- owner 不可变，本切片没有转移 ownership 的入口。
- 非 owner 请求不得调用 Runtime、不得创建 user/assistant message、不得返回 sources 或 revalidation 状态。
- owner 校验后 conversation 被删除时，现有 repository transaction 仍以 not found/rollback 结束；不产生部分回答。
- SSE started 事件只在 owner 校验成功后发送。

## 7. 验收矩阵

- `user_member` 创建的 conversation 仅出现在自己的 list；`user_admin` list 不包含它。
- owner 可 JSON/SSE 发送、读取 sources、创建并查询 revalidation。
- 另一用户对 detail/message/source/revalidation 均得到相同 404，Runtime 调用计数与消息数不变。
- legacy conversation 在 migration 后属于 `user_admin`，管理员可继续读取，普通用户不可见。
- admin evidence 仍可按普通用户 conversation id 查询 execution evidence。
- Memory 与 Postgres 路径、isolated migration、Backend full/race、integration、Web auth recovery 与 production Playwright 通过。

## 8. 验收记录

- migration `010` 已在随机隔离 schema 与现有开发库执行：legacy conversation 全部回填为 `user_admin`，`owner_id NOT NULL` 与 owner/update 索引生效，重复启动安全。
- Backend Memory/API 回归覆盖两个真实 subject 的 list/detail/JSON message/source/revalidation 隔离；越权写入不增加消息，也不调用回答链路。
- 真实开发库验证：`user_member` 新会话不进入 `user_admin` list；管理员跨账号 detail/message/source/revalidation 均返回 `404 conversation_not_found`；admin evidence 仍可读取该普通用户回答，普通用户访问 evidence 为 `403`。
- Web 对越权或已不存在的直达链接跳转到当前账号可访问的会话，并保留“会话不存在或当前账号无权访问”提示；production Playwright 已锁定该行为。
