# 单智能体 V1 系统配置台交互设计

## 文档控制

- 状态：`implemented / scoped_verification_passed`
- 更新日期：`2026-07-19`
- 工作包：`U5`
- 上位设计：[角色分离](single-agent-v1-role-separation-design.md)、[Web UI](single-agent-v1-web-ui-design.md)、[K7 配置/evidence](single-agent-v1-k7-admin-evidence-config-design.md)

## 1. 目标与非目标

系统管理员的 Web 交互收敛为单一 `/config` 系统配置台：不再显示聊天会话侧栏、聊天入口或知识库工作区。配置字段旁必须显示简洁、可操作的中文说明，使管理员在编辑时理解参数对“有依据回答 / 拒答 / 版本发布”的影响。

非目标：本包不改变 Go Backend 的角色授权、Postgres 状态机或 Q1/K9-R2 安全门。系统管理员目前仍是 revision publish 与旧知识退役 API 的授权者；这些操作后续要迁入配置台的“治理审批”区，不能因此下放为知识库管理员权限或绕过现有门禁。

## 2. 路由与壳层契约

| 角色 | 可见 Web 工作区 | UI 直达其它工作区 |
| --- | --- | --- |
| `user` | 对话 | 保持现有门禁。 |
| `knowledge_admin` | 对话、知识库 | 保持现有门禁。 |
| `system_admin` | 仅系统配置台 | `/chat` 与 `/knowledge` 重定向至 `/config`。 |

系统配置台使用独立的窄顶栏：产品标识、`系统配置`、主题选择、角色标签和退出。它不创建 Chat provider、不读取会话列表，也不暴露知识库导航。配置详情以按需打开的详情弹窗呈现，默认不占主工作区宽度；弹窗支持明确的关闭按钮和 `Escape` 关闭。

配置台壳层固定为视口高度（优先 `100dvh`，回退 `100vh`），顶栏以外的空间全部交给配置布局。主编辑区占满可用宽度并在自身内部纵向滚动；详情弹窗打开后覆盖在主区之上，内容区独立纵向滚动，页面根节点不得随详情内容增长；窄屏弹窗改为接近全宽的底部/侧边面板，避免双重嵌套滚动。

这只是 Web interaction boundary，不能被误读为 API deny：Backend 的 system-admin publish/retirement 路由维持原状，直到治理审批 UI 被迁移完成。

## 3. 配置字段说明契约

每个可编辑 field 必须在 label 后紧贴 `.field-help`：

- 说明该参数直接改变什么；
- 说明它**不会**改变什么（例如模型/采样参数不会创造未上传知识）；
- 涉及策略、embedding、rerank、拒答边界时说明必须评测、发布或重新索引的前置条件。

不再把所有解释集中放在表单尾部。表单尾部只保留候选/审批/发布的全局风险说明。

## 4. 验收

- system_admin 登录后只看到 `/config` 壳层；直接访问 `/chat` 或 `/knowledge` 到达 `/config`。
- user/knowledge_admin 行为与 Backend API 权限不变。
- generation/retrieval 的每一个编辑 field 都有相邻中文 help；保存仍只创建候选，不自动发布。
- Web lint/typecheck/build、角色布局 Playwright 与 integration 回归通过。

## 4.1 U5.2 候选变更与版本差异契约

保存候选前，Web 必须将表单解析为与当前已加载版本相同的 typed payload 再比较。空白、数字字符串的等价写法（例如 `0.20` 与 `0.2`）不构成变更：保存按钮 disabled，并提示“未检测到配置变更”。提交 handler 也必须重复该检查，不能只依赖 disabled UI。

`GET /api/agent/config/versions` 扩展为一个只读、向后兼容的历史 read model：保留 `items`/`total` 的 Agent 版本字段，新增 `retrieval_items`/`retrieval_total`。Backend/Repository 是历史的唯一读取者；Web 不从 execution evidence 或当前 active release 推断历史值。

版本历史按各自创建顺序比较与紧邻前一版本的字段：

- Agent：`model_id`、`system_prompt_version`、`response_style`、`temperature`、`top_p`、`max_output_tokens`；
- Retrieval：`retrieval_strategy`、`embedding_model`、`top_k`、`rerank_enabled`、`evidence_min_score`。

首个版本明确显示为“初始版本”，不伪造不存在的前版本。历史页只解释候选版本的字段差异，不宣称它已通过评测或已经发布。

## 4.2 U6a shadcn/ui 与 Tailwind 组件层契约

`U6a` 在不改变任何 Backend API、RAG、角色、安全门或配置候选/发布状态机的前提下，为 Web 引入 Tailwind CSS 与 shadcn/ui 的源码型组件层。

- Tailwind 只负责布局、响应式断点和组件状态样式；当前 `verdant-habitat` / `quiet-paper` 主题仍是唯一的颜色语义权威。主题 token 映射到 shadcn 的 `background`、`foreground`、`primary`、`border`、`ring` 等变量，组件不得写死 Material 或无主题颜色。
- 滚动条也只消费主题注册表的 `scrollbar-track`、`scrollbar-thumb` 与 `scrollbar-thumb-hover` token；它不承载业务状态，且不得改变聊天、知识库、配置详情或抽屉的现有 overflow/滚动逻辑。
- shadcn 组件源码落在 Web 仓库中，由本项目维护；本次官方 registry 选择的 Base UI primitive 负责 Select、Switch、Dialog、Tooltip 等键盘、焦点与弹层语义。它与 Radix 一样是无样式、可访问的底层实现，不读取、写入或推断业务状态。
- `U6a` 必须先迁移 `/config` 的 Button、Input、Textarea、Select、Switch、Tooltip 与 confirmation Dialog；禁用、loading、表单 dirty 校验与候选/审批提示保持既有语义。原生 `<input>`/`textarea` 作为可访问的基础元素仍存在于 shadcn 的组件内部，不再由页面自行拼装样式。
- Select 菜单必须锚定其触发框：正常空间下紧贴下边缘展开；仅当视口空间不足时才翻转并紧贴上边缘。旧页面的容器级 `button` CSS 不得覆盖带 `data-slot` 的 shadcn/Base UI 控件。
- `/config` 必须使用固定视口壳层；桌面 `.app-main` / `.app-inspector` 的滚动仅限各自内容区，且二者共享相同的剩余高度。不得使用仅有 `min-height` 的网格让长侧栏把页面整体撑开。
- 本阶段不强制重写聊天和知识库页面；`U6b` 再使用同一组件 API 逐页迁移，避免影响 SSE、citation drawer、上传和 K4-K9 受控流程。

验收：新增组件层可 tree-shake；配置台不再直接渲染原生 `<button>`、`<select>` 或 checkbox；宽/窄布局、键盘焦点和 disabled 状态可用；Web lint/typecheck/build、配置台 Playwright 和现有 workspace role smoke 通过。

## 5. 后续

`U5.1` 将系统管理员的 revision publish、retirement readiness 与 disable/delete 审批，从知识库详情迁入 `/config` 的治理审批队列。该工作必须复用现有 Backend/Postgres gate，不能复制或放松规则。

### 5.1.2 详情弹窗

系统管理员的“配置详情”与知识库管理员的“知识库详情”均不再作为常驻右栏。主区提供明确的“配置详情”或“查看详情”入口，打开后使用带遮罩的详情弹窗；弹窗标题、关闭按钮、`aria-modal` 和 `Escape` 行为必须可见且可操作。原详情内容保持只读/原有操作语义，关闭后主区筛选、选中 revision 和回答证据状态保持不变。

### 5.1.1 知识发布审批入口

系统管理员登录 `/config` 后，主工作区顶部显示“知识发布审批”区。它读取现有知识 lifecycle API，列出 `active` 文档中状态为 `ready` 且不在当前 active snapshot 的 revision，展示文档名、文件名、revision、投影状态和更新时间。每项提供“确认发布”操作；确认后仍通过既有 `POST /api/kb/revisions/{revision_id}/publish` 与 `Idempotency-Key` 创建 successor snapshot，不能由 Web 直接写数据库或改变 revision 状态。

知识库管理员继续在 `/knowledge` 上传、替换和观察 delivery；其页面保留“等待系统管理员确认发布”提示，但不显示可执行发布按钮。系统管理员不需要访问 `/knowledge`，因为 `/knowledge` 对该角色仍重定向到 `/config`，发布审批必须在配置台完成。

加载失败、无待发布 revision、重复点击和 Backend 权限错误都必须给出可见状态；发布成功后刷新队列并显示新 snapshot id。该入口不包含 disable/delete，旧知识退役仍必须遵守现有 onboarding 和两段式门禁。

## 6. 实现与验证（2026-07-19）

- `WorkspaceShell` 对 `system_admin` 渲染独立配置壳层，且 `proxy.ts` 将 `/chat`、`/chat/**`、`/knowledge`、`/knowledge/**` 送回 `/config`；同源 `/api/**` 不受该体验层重定向影响。
- Agent 与 retrieval 的 11 个可编辑字段均增加相邻中文说明；说明覆盖生成模型、prompt、采样、策略、embedding、候选数、拒答边界和 rerank 的直接影响与安全前置条件。
- `pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；定向 Playwright `workspace-layout.spec.ts` 为 `4 passed`，覆盖 system-admin 直达重定向、无聊天侧栏/工作区切换、字段说明和其它两种角色入口。
- 完整 `pnpm ui-smoke` 在聊天真实模型断言处受本机 Runtime 未启动影响：Backend 返回 `Runtime chat is not configured for this backend process.`。这不影响已通过的壳层验证，恢复 Runtime 后仍须重跑完整 smoke。
- `U5.2` 已实现保存前的 typed change detection 与历史字段差异。`GET /api/agent/config/versions` 返回 Agent 的 `items`/`total` 以及 retrieval 的 `retrieval_items`/`retrieval_total`；UI 对每个版本显示相邻前版本的实际字段变化，首版标为初始版本。Backend HTTP/repository/service 测试通过，Web lint/typecheck/build 通过，配置历史与系统配置台 Playwright 为 `5 passed`。
- `U6a` 已验证：Tailwind v4 CSS-first、shadcn registry 与本地 `Button/Input/Textarea/Select/Switch/Tooltip/AlertDialog` source 已加入。现有生态主题 token 映射到 Tailwind semantic color，未增加第二套主题状态；`/config`、登录和主题选择已移除页面级原生 button/select/checkbox 拼装，发布与回退 confirmation 改为 AlertDialog。`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；配置、主题、质量运营与 workspace Playwright 共 `9 passed`。配置 Select 已修正为贴近触发框下边缘展开，且 K7 Playwright 用例覆盖其稳定后的几何位置。
- `U6b` 已把聊天和知识库页面的按钮、输入、多行输入、下拉、文件选择、确认框和提示接到同一组件层。聊天发送、引用锚点和知识上传/发布 API 不变；composer、会话侧栏和引用下划线继续使用原有布局类。配置台的浏览器 `confirm()` 发布确认仍留在 `U6a` 范围内，未在本切片改写。
- `2026-11-04`：补齐 `U5.1` 的 Web 治理入口。系统管理员 `/config` 的“知识发布审批”读取 active 文档的 `ready` revision，复用 `POST /api/kb/revisions/{revision_id}/publish` 和幂等键创建 successor snapshot；知识库管理员仍只看到“等待系统管理员确认发布”。定向角色/布局 Playwright、类型检查、lint 和生产构建通过。
- `2026-11-04`：配置详情与知识库详情由固定右栏改为按需 Base UI Dialog；主区恢复全宽，详情弹窗独立滚动并支持焦点捕获、遮罩、关闭按钮和 `Escape`。配置证据、知识快照、文档版本入口均已接入自动打开，相关 Playwright `13 passed`。
