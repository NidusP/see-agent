# apps/web

这里承载 `Next.js` Web 前端工作台。

预期首批职责：

- 聊天界面
- 登录页面
- 管理员上传界面
- 配置管理界面
- 回答引用与来源查看

当前已落地的工作台壳层：

- 普通用户登录页提供“管理员登录”入口；它会进入管理登录页并默认定位到知识库上传工作区，不在普通登录页展示管理员快捷凭据
- `/login`：面向终端用户的中文内部登录页；不展示管理员信息，本地开发时仅提供普通用户快捷凭据
- `/admin/login`：知识库管理员与系统管理员共用的受控管理登录入口；两者仍使用同一 Backend session API，本地开发时才显示管理员快捷凭据
- `/`：默认进入真实聊天工作台，自动选中后端会话或展示空态
- `/chat`：真实会话列表、空态、新建会话和自动建会话发送
- `/chat/[conversation_id]`：真实消息区、SSE、citation/provenance，以及不会覆盖原回答的独立历史核验结果
- 聊天在首个 SSE 事件前先显示本地 pending turn；会话标题与输入区固定，只有消息历史区滚动；完成消息默认只保留问题、回答和来源入口，snapshot/freshness/历史核验收进按需“回答信息”或来源抽屉；流式生成自动跟随，用户可继续编辑下一条草稿、按 `Enter` 发送、按 `Shift+Enter` 换行或停止当前生成
- composer 采用单一多行输入卡片，发送（或生成中的停止）按钮固定在输入框右下角；错误、重试和键盘提示在输入卡片下方，不与主发送操作竞争
- 工作台采用聊天优先布局：左侧会话历史可收缩，桌面收起后保留明确的展开入口；管理员从顶栏当前模块名切换对话、知识库和配置；窄屏使用独立的打开/关闭抽屉操作；引用详情仅在点击后以可关闭抽屉出现
- `/knowledge`：真实上传入口、知识文件列表、状态刷新、详情右栏、chunk inspector、reindex、更新日历与 snapshot 成员详情
- `/config`：真实 agent / retrieval config 读取、保存和 Agent 版本历史；管理员还可按 conversation/snapshot/revision 查询 execution evidence，并查看回答的 manifest、配置、citation、delivery 与 eval report，以及记录质量 feedback、比较候选 experiment、审批发布或前滚回退
- `/config`：系统管理员可在“知识发布审批”区查看已完成索引的 `ready` revision，并通过现有生命周期 API 确认发布 successor snapshot；知识库管理员仍只能上传、替换和观察投影状态
- 系统管理员的“配置详情”和知识库管理员的“知识库详情”均按需在详情弹窗中打开，主工作区不再被固定右栏压缩；弹窗支持遮罩/关闭按钮/`Escape` 关闭，回答证据、知识快照和文档版本入口会自动打开对应详情
- UI foundation：`/config`、登录、主题选择、退出入口、聊天和知识库的交互控件使用 Tailwind CSS 与仓库内 shadcn/ui 源码组件。聊天 composer、引用下划线和会话侧栏继续使用原有布局类，避免组件默认尺寸打乱阅读区。
- 视口工作区壳：`/chat`、`/knowledge` 与 `/config` 的顶级壳固定为 `100dvh`（回退 `100vh`）；根页面不滚动，桌面采用已声明的分区滚动，窄屏由聊天主区或管理内容区单独滚动，移动会话侧栏与引用抽屉仍以 fixed overlay 显示

注意：

- 未登录访问聊天会进入 `/login`；访问知识库或系统配置会进入 `/admin/login`。普通用户只显示聊天入口，管理员显示其获授权的工作区
- `/knowledge` 使用 `apps/web/app/_lib/knowledge-api.ts` 和 Next.js 同源 route handler 转发到 Go Backend
- `/chat` 和 `/chat/[conversation_id]` 使用 `apps/web/app/_lib/chat-api.ts` 和 Next.js 同源 route handler 转发到 Go Backend
- `/config` 使用 `apps/web/app/_lib/config-api.ts` 和 Next.js 同源 route handler 转发到 Go Backend
- prompt 版本使用已登记版本下拉框；K7 当前登记不可变 `prompt_v1` 与已通过质量门的 `prompt_v2`，未知版本仍由 Backend/Runtime fail-closed
- Conversation response 包含 Backend 权威 `owner_id`；Web 只负责导航体验，越权或不存在的直达链接会跳回可访问会话并显示统一提示
- 所有业务 API proxy 都会转发浏览器 cookie 到 Go Backend；权限最终由 Backend 校验
- 默认 Web proxy 目标为 `http://127.0.0.1:8080`，可用 `BACKEND_API_BASE_URL` 覆盖
- Next 开发模式允许 `127.0.0.1` 作为 `allowedDevOrigins`，保证浏览器或 Playwright 通过 loopback 访问时 HMR 与 client hydration 正常工作
- Chat 默认使用 SSE；配置 runtime 时 delta 来自 Runtime `/v1/chat/stream`，未配置 runtime 时 Backend 保留本地 fallback 分块，Web 仍只消费同一套 `message.started` / `message.delta` / `message.completed` 事件
- 缺少 active published snapshot 时，Web 保留本地失败回合并提示管理员完成上传、索引、发布，不把无来源回答伪装成成功
- 聊天工作台在宽屏保留可收缩会话栏与居中对话主区；窄屏侧栏为遮罩抽屉，引用详情从底部弹出，不压缩输入区
- 有 `citation_anchors` 的回答会直接给受支持结论加下划线；hover/focus 显示同次 citation 的来源摘要，点击筛选并打开来源详情抽屉，`Escape` 可关闭预览或抽屉
- Backend 返回 `unauthenticated` 时，聊天回到 `/login`，知识库和系统配置回到 `/admin/login`，并以当前路径作为 `next`；只有聊天会从当前标签页临时草稿恢复输入内容
- 核验 UI 通过同源 revalidate proxy 发起带 `Idempotency-Key` 的请求，并在独立区域轮询/刷新 run；历史 message 文本和 provenance 只读保留

本地默认账号：

- 系统管理员：`admin` / `admin123`
- 知识库管理员：`knowledge_admin` / `knowledge_admin123`
- 普通用户：`user` / `user123`

实现前请先对齐：

- `docs/product-specs/single-agent-v1-spec.md`
- `docs/design-docs/single-agent-v1-sdd.md`
- `docs/design-docs/single-agent-v1-workspace-layout.md`
- `docs/design-docs/single-agent-v1-web-ui-design.md`

当前命令：

- 启动：`make web-dev`
- 安装依赖：`make web-install`（使用 pnpm 与 `pnpm-lock.yaml`）
- 检查：`make web-lint`（底层执行 `pnpm run lint`）
- 类型检查：`make typecheck`（底层执行 `pnpm run typecheck`）
- 构建：`make web-build`（底层执行 `pnpm run build`）
- 浏览器 UI smoke：`WEB_BASE_URL=http://127.0.0.1:3000 make web-ui-smoke`
