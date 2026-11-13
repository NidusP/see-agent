# U6a shadcn/ui 与 Tailwind 配置台现代化计划

## 文档控制

- 状态：`verified / U6b_chat_knowledge_controls_migrated`
- 更新日期：`2026-07-20`
- Owner：`p`
- 权威设计：[Web UI 设计](../../design-docs/single-agent-v1-web-ui-design.md)、[系统配置台交互设计](../../design-docs/single-agent-v1-system-config-console-ui-design.md)

## 目标

为现有 Next.js 前端建立 Tailwind CSS + shadcn/ui 源码组件层，先消除系统配置台的浏览器原生 Button/Select/checkbox 视觉，同时保持生态主题、角色边界、候选配置安全语义和所有 API 不变。

## 范围与边界

- 新增 Tailwind v4 CSS-first 入口、shadcn registry 配置、`cn` helper 和本地拥有的 UI component source。
- 将现有主题 token 映射到 shadcn semantic token；不引入 MUI、不切换到 Material Design、不删除现有主题注册表。
- 本切片迁移 `/config` 表单、质量运营、版本记录和配置详情的可交互控件；原生 confirmation 改为可访问 Dialog。
- 不修改 Backend、OpenAPI、Postgres、Runtime、RAG、聊天 SSE、知识摄取或发布状态机。
- `U6b` 再迁移聊天和知识库；不得将这次 UI foundation 误标为全站控件迁移完成。

## 执行

- [x] 冻结 token ownership、组件边界和不变业务契约。
- [x] 安装 Tailwind/shadcn 依赖并生成源码组件层。
- [x] 映射生态主题 token，迁移配置台控件与确认交互。
- [x] 增加配置台 UI 回归，验证非原生 Select/Switch、disabled 和 dialog。
- [x] 跑 lint/typecheck/build、目标 Playwright 和 workspace role smoke。

## 验收与风险

- 配置候选仍仅由现有 `PUT` 和 Backend 决定；UI 组件不能绕过 dirty check、Q1 approval 或 release gate。
- 主题切换仍只写浏览器 localStorage；Tailwind 不持久化主题或配置。
- 最大风险是 Tailwind preflight 改变旧页面默认元素。通过保留现有 CSS、以组件作用域迁移、并回归 chat/knowledge role smoke 降低风险。
- 验证失败时可移除新增的组件使用并保留原有 CSS/API；不存在数据迁移或服务端回滚。

## U6a.1 遗留全局控件 CSS 审计（2026-07-19）

### 目标与边界

- 清除已不再命中的原生登录、配置台与主题 picker 选择器，并阻止余下的 legacy page CSS 覆盖任何带 `data-slot` 的 shadcn/Base UI 控件。
- 不在本切片迁移聊天和知识库原生控件；它们的视觉规则仍归 `U6b`，不能为了清理 CSS 而删除。
- 保留全局字体、disabled cursor 与 pointer cursor 基础语义；保留 app rail 的退出入口和旧页面专属 class 样式，因为它们有明确的当前调用点。

### 执行与验收

- [x] 审计 `globals.css` 的原生控件/通用 button selector 与 TSX 调用点。
- [x] 删除零调用点 selector，并为保留的 legacy selector 添加 `:not([data-slot])` 防护。
- [x] 回归配置、登录、主题、聊天与知识库的角色页面，确认 data-slot 控件不再受 legacy rule 命中。

## U6a.2 主题化滚动条（2026-07-19）

- 为现有主题注册表增加轨道、滑块与 hover token；全局滚动容器只消费这些 token，不改变其 `overflow`、尺寸或滚动逻辑。
- Firefox 使用标准 `scrollbar-color` / `scrollbar-width`；Chromium/WebKit 使用 `::-webkit-scrollbar` 家族。保留系统的触控滚动行为，不引入滚动动画或脚本。
- 验收：两套主题的滚动条 token 不同，桌面端滑块有足够点击面积、圆角与 hover 反馈；主题 picker 与主要角色页面回归通过。

已验证：`--scrollbar-track`、`--scrollbar-thumb` 与 `--scrollbar-thumb-hover` 已分别在绿野共生和静纸工作室主题登记；`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过，`theme-system-ui` 与 `workspace-layout` 为 `6 passed`，且测试断言主题切换后滑块 token 实际改变。

## U6a.3 视口约束的配置双栏（2026-07-19）

- `/config` 根壳层固定为 `100dvh`（带 `100vh` 回退）；顶栏外仅保留一个受约束的内容区域。
- 桌面端主编辑区与右侧详情区同高、各自滚动；窄屏时内容区单独滚动，不让浏览器页面或某一栏的内容高度决定另一栏高度。
- 验收：桌面端外壳与视口等高、两栏高度相等、布局本身不向页面泄漏纵向滚动；角色/路由/表单语义不变。

已验证：配置壳使用 `auto minmax(0, 1fr)` 网格行、桌面布局与双栏都受约束；根 `html/body` 在配置壳存在时锁定纵向 overflow，窄屏由内容布局单独滚动。`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；配置、主题与角色布局 Playwright `8 passed`，其中新增用例覆盖桌面等高、两栏内部 scroll 与移动端单滚动区。

## U6a.4 共享视口工作区壳（2026-07-20）

- 抽取 `viewport-workspace-shell` 公共 CSS 约束，供 `chat-first-shell` 与 `system-config-shell` 共同使用；它不创建 React 组件、状态或 API 边界。
- 顶级壳始终锁定 `100dvh`/`100vh` 与根页面 overflow；桌面保持已声明的分区滚动，窄屏聊天和知识库/管理内容收敛为各自的单一内部滚动区。
- 验收：普通用户聊天、知识库管理员的管理布局和系统配置台均与视口同高；根页面不滚动；桌面双栏等高、移动端没有双重滚动或遮挡侧栏/抽屉。

已验证：两个顶级 `<main>` 共同拥有 `viewport-workspace-shell`；桌面聊天、知识库与配置保留各自的内部滚动边界，窄屏聊天主区与管理内容区改为单一内部滚动。移动侧栏仍为 fixed 抽屉：为避免主题选择器覆盖其定位，移动断点以同等选择器优先级显式恢复 `position: fixed`。`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；配置、主题与角色布局 Playwright `9 passed`，新增用例覆盖聊天/知识库共享壳、桌面双栏和移动内部滚动。

## U6a.5 聊天消息视口（2026-07-20）

- 聊天主区拆分为固定会话上下文、唯一可滚动的消息历史与固定 composer；禁止长消息滑入 composer 背后。
- 自动跟随、上滑暂停和“回到最新”只以消息历史 viewport 为滚动来源；SSE、草稿、重试、中止和 citation 行为不变。
- 验收：桌面和窄屏下 composer 与会话标题不随历史滚动，消息 viewport 独立滚动，回到最新不遮挡输入控件。

已验证：`chat-first-main` 不再滚动，`chat-message-viewport` 是唯一消息滚动源，composer 作为 grid 固定底行；“回到最新”定位在消息区而非输入区。同步修复认证恢复中的零延迟 timer 竞争：草稿消费后立即写入 composer，避免登录 `router.refresh()` 取消恢复。`chat-auth-recovery`、`chat-stream-experience` 与工作区布局 Playwright `8 passed`。

## U6a.6 对话阅读层级与内嵌 Composer（2026-07-20）

### 目标与边界

- 将默认会话阅读收敛为经典 AI 聊天层级：阅读列始终有可见横向留白；完成消息只呈现用户问题、助手正文和来源入口；发送按钮位于 textarea 右下角。
- 生成、失败、停止、重试、键盘发送、citation anchor、来源详情、snapshot/freshness provenance 与 K6 历史核验能力必须保留。审计字段改为按需回答信息或来源详情，不能删除或改写历史 message/provenance。
- 只改 Web 的呈现结构、样式与回归；不修改聊天 API、SSE、Backend、Runtime、知识或检索语义。

### 验收

- [x] 桌面和窄屏消息内容不贴主工作区边缘；完成用户消息不显示冗余 metadata/状态，完成助手消息不显示“已完成”胶囊。
- [x] 发送/停止按钮在输入卡片内且保留可访问名称；`Enter`、`Shift+Enter`、禁用、停止和重试路径不回归。
- [x] 回答信息与引用详情仍能访问 snapshot/freshness/revalidation；citation anchor 与历史回答文本不变。
- [x] lint、typecheck、build 和聊天/工作区目标 Playwright 通过。

已验证：消息 viewport 横向 padding 增至桌面 `clamp(28px, 6vw, 112px)`、窄屏 `20px`，阅读列与 composer 统一为 `800px` 上限。完成用户消息只显示右对齐问题气泡；完成助手消息只保留“助手”、正文、按需“回答信息”和来源入口，非完成态继续展示明确状态。composer 用 `composer-input-shell` 承载 textarea，`发送消息` 与 `停止生成` 在同一右下角位置替换，失败重试与键盘提示位于卡片下方。`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；`chat-stream-experience`、`chat-auth-recovery`、`chat-provenance-ui`、`bounded-conversation-intent-ui`、`k6-calendar-revalidation-ui` 与 `workspace-layout` 为 `14 passed`，真实服务 `chat-ui-smoke` 为 `1 passed`。桌面本机视觉检查确认消息留白、内嵌按钮与来源抽屉入口；窄屏几何由 workspace Playwright 覆盖。

## 验收记录（2026-07-19）

- Tailwind v4 通过 `@tailwindcss/postcss` 接入；`components.json`、TypeScript `@/*` alias 与 `cn` helper 已建立。shadcn registry source 在 `app/_components/ui/`，此次 registry 选择 Base UI primitive；依赖与视觉源代码均留在本仓库的 Web 组件边界内。
- 已迁移 `/config` 全部页面级交互控件、登录表单/快捷登录、主题选择和退出入口。配置 dirty check、候选保存、evidence 查询、Q1 experiment/feedback/approval、发布/回退的 Backend 请求与状态机不变；后两项由 AlertDialog 显式确认。
- `pnpm lint && pnpm typecheck && pnpm build` 通过。Playwright：`config-version-history`、`k7-admin-evidence`、`q1-quality-operations`、`theme-system-ui` 与 `workspace-layout` 合计 `9 passed`。
- 选择器定位回归：组件默认在触发框下边缘 `0-3px` 内展开；空间不足时才由定位 primitive 翻转到上边缘。`k7-admin-evidence` 对此几何关系有稳定等待后的断言；与 `theme-system-ui` 合计 `3 passed`。
- `U6a.1` 审计已移除无调用点的 login 原生 input/button、配置卡 button、evidence filter input 与紧凑主题原生 select 规则。仍有调用点的聊天/知识库旧控件规则全部以 `:not([data-slot])` 与 shadcn/Base UI 隔离；全局仅保留字体、cursor 等无尺寸/定位副作用的基础语义。`pnpm lint`、`pnpm typecheck`、`pnpm build` 通过；登录、主题、系统配置、知识库生命周期与角色布局 Playwright `11 passed`。
- 残余工作：`U6b` 将聊天和知识库控件迁移到同一组件层；本切片不会声称全站原生元素已替换。该残余项已由下方 `U6b` 记录完成。

## U6b 聊天与知识库控件迁移（2026-11-13）

### 目标与边界

- 把 `/chat`、`/chat/[conversation_id]` 和 `/knowledge` 上仍由页面直接拼装的按钮、单行输入、多行输入、下拉、文件选择、确认框和提示，改接到 `U6a` 已有的 `app/_components/ui/` 组件。
- 不新增第二套组件库，不增加 Button/Input/Textarea/Select/Switch/Tooltip/AlertDialog 之外的 primitive。
- 不改变聊天发送、SSE、引用、知识上传、发布、停用或删除的请求与状态机。发布、停用、删除仍调用原来的 API；只是把浏览器 `confirm()` 换成 AlertDialog。
- 配置台、登录和主题选择保持 `U6a` 的实现。仅两处共享修正：AlertDialog 叠层提高到知识库详情弹窗之上；详情弹窗关闭按钮改用同一 Button，避免知识库详情里留下原生关闭按钮。

### 与已合并的可选发布前验收的衔接

- 可选发布前验收已进入 `main`（合并提交 `7cd6aa3`）。知识库详情保留准备发布、待发布快照、直接发布确认、发布后验收状态和补跑验收。
- 这些路径上的手工题目、未验收确认和补跑入口改用同一组件层。停用和删除仍用 AlertDialog，不再使用浏览器 `confirm()`。
- 配置台的准备发布和激活请求保持原有行为；未验收确认从原生勾选改为 Switch，文案和“必须先确认才能直接发布”的门槛不变。

### 验收

- [x] 聊天会话栏、composer、示例、重试、引用入口、引用下划线、来源抽屉和历史核验按钮使用组件层。
- [x] 知识库创建、筛选、验收集、replacement、发布/停用/删除确认和更新日历入口使用组件层。
- [x] composer、会话侧栏、引用下划线和抽屉遮罩保留原有布局类，避免组件默认高度打断阅读区。
- [x] `pnpm lint` 与 `pnpm typecheck` 通过。本地页面检查确认 composer 发送按钮仍在输入卡片内，知识库筛选值不再被标题的大写规则改写。
- 共享样式修正：表单标题选择器改为 `label > span`，避免下拉当前值继承大写。AlertDialog 叠层高于详情弹窗。
