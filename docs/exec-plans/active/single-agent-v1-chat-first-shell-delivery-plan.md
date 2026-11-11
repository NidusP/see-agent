# U3 聊天优先壳层与引用抽屉交付计划

## 文档控制

- 状态：`verified`
- 版本：`v1.1`
- 更新日期：`2026-07-15`
- Owner：`p`
- 权威设计：[Web UI 设计](../../design-docs/single-agent-v1-web-ui-design.md)、[C1 回答内证据锚点设计](../../design-docs/single-agent-v1-citation-anchors-design.md)

## 目标

- 用可收缩的会话侧栏建立聊天优先的主界面，让回答与输入成为主阅读流。
- 只向管理员显示位于顶栏产品名位置的工作区切换；普通用户只获得聊天入口。
- 将 C1 已有的 citation detail 复用为按需、可关闭的详情抽屉，保留 hover/focus 轻量预览。

## 范围与约束

- 只变更 `apps/web`、浏览器测试和 Markdown 记录；不修改 API、`citation_anchors`、RAG、snapshot、provenance 或 RBAC 契约。
- 侧栏收缩状态使用当前浏览器本地存储，不上传服务端。窄屏以临时遮罩抽屉呈现会话列表。
- anchor hover/focus 可以预取并更新预览；只有 click、`Enter` 或 `Space` 打开固定 citation detail 抽屉。
- 关闭 citation drawer 或按 `Escape` 不改变当前会话、消息滚动位置和 composer 草稿。

## 执行与验收

- [x] 用顶部 workspace menu 与聊天侧栏替换旧全局 rail；管理员可切换聊天、知识库、配置，普通用户不看到管理入口。
- [x] 实现桌面收缩与窄屏遮罩侧栏，确保会话列表不会挤入正文自然流。
- [x] 将 citation detail 包装为右侧/底部可关闭抽屉，并区分预览焦点和固定详情焦点。
- [x] 更新 Playwright：角色可见性、收缩/移动侧栏、click-to-open drawer、hover preview、`Escape` 和抽屉关闭。
- [x] 运行 `make web-lint`、`make typecheck`、`make web-build`、目标 Playwright 与完整 UI smoke；在本地开发环境人工确认桌面和移动视图。

## 验收记录

- `npm run lint`、`npm run typecheck`、`npm run build` 均通过。
- `WEB_BASE_URL=http://127.0.0.1:3101 npm run ui-smoke -- --reporter=line`：`15 passed`。
- 用生产 Next 预览在 `1440x900` 和 `390x844` 检查：桌面侧栏收缩后正文获得完整宽度；移动侧栏为遮罩抽屉；citation detail 从右侧或底部出现且不会改变 composer 草稿。

## 2026-07-15 壳层交互回归收口

- 复现：桌面侧栏进入 `is-sidebar-collapsed` 后，CSS 同时隐藏 `.chat-sidebar-collapse`，导致用户没有展开入口；收缩态的新建按钮也因文字 `display:none` 失去 accessible name。
- 复现：管理员顶栏 workspace trigger 写死为 `智能问答`，在 `/knowledge` 和 `/config` 不能表达当前模块。
- 真实右侧浏览器宽度为 `619px`，进一步复现移动端语义冲突：桌面 collapsed 偏好让抽屉内按钮显示“展开”，但其 click handler 实际只关闭抽屉。
- 冻结行为：`智能问答` 只作为侧栏产品品牌；顶栏按路由显示 `对话/知识库/配置`。桌面收起后必须保留唯一的展开按钮和可识别的新建按钮；再次展开恢复会话列表。移动端使用顶栏菜单按钮打开临时抽屉，抽屉内使用独立“关闭会话侧栏”按钮，桌面收缩按钮隐藏且 collapsed 偏好不改变移动端文案或动作。
- 必须新增生产 Playwright 回归：桌面连续收起/展开、三个路由的模块名、收缩偏好跨管理路由可恢复，以及移动端打开/关闭。
- 验收结果：`npm run lint`、`npm run typecheck`、`npm run build` 通过；目标 workspace Playwright `3 passed`，完整生产 UI smoke `16 passed`。右侧真实浏览器 `619px` 下验证 `对话` 模块名、顶栏打开、独立“关闭会话侧栏”、关闭后重开和无 console error；桌面恢复由 `1440px` Playwright 覆盖。

## 非目标

- 不新增会话搜索、语音、附件或引用原文定位。
- 不重做知识库和配置业务表单，只让它们进入同一顶栏工作区模型。
- 不改变历史消息、citation、revalidation 或权限数据。
