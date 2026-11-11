# apps/web

## 目的

本目录用于 `Next.js` 前端实现。

## 编码前约定

- 先以产品规格和契约为准，不在前端擅自发明接口字段。
- 所有 API 结构优先从 `packages/contracts/` 生成或同步。
- 首批页面聚焦聊天、登录、上传和来源查看，不扩展营销型页面。
- 面向用户的页面文案、按钮、空态、错误提示和交互反馈优先使用中文；只有接口名、技术字段、日志或代码标识保留英文。

## 当前命令

- 包管理器：`pnpm` `10.13.1`；依赖锁定文件为 `apps/web/pnpm-lock.yaml`
- 启动：`make web-dev`
- lint：`make lint`
- typecheck：`make typecheck`
- build：`make web-build`
- browser smoke：`WEB_BASE_URL=http://127.0.0.1:3000 make web-ui-smoke`

## 当前实现依据

- `docs/design-docs/single-agent-v1-web-ui-design.md`
- `docs/api/single-agent-v1-api-design.md`
- `packages/contracts/openapi.yaml`

## 当前状态

- 工作台采用角色专用壳层：普通用户与知识库管理员使用可收缩聊天侧栏，知识库管理员顶栏显示 `对话/知识库`；系统管理员只使用独立 `/config` 系统配置台，直达聊天或知识库会重定向回配置台，不显示会话侧栏或工作区切换。系统管理员的 Backend publish/retirement authority 暂保留，待治理审批区迁入配置台。
- `chat-first-shell` 与 `system-config-shell` 共同使用 `viewport-workspace-shell`：固定 `100dvh`（回退 `100vh`）并锁定根页面滚动；桌面各区内部滚动，窄屏聊天/管理内容各自单滚动。移动聊天侧栏和引用抽屉仍是 fixed overlay，不能被顶级壳裁剪。
- `/login` 是普通用户入口，`/admin/login` 是知识库管理员/系统管理员共用的 Backend session 登录入口；快捷凭据仅在本地开发入口按受众显示，生产构建必须隐藏。业务页面由 `proxy.ts` 做未登录、知识库和系统配置页面门禁，Backend 仍是角色与会话的权威来源
- 已有 `/chat`、`/chat/[conversation_id]`、`/knowledge`、`/config` 壳层路由
- `/knowledge` 已接真实请求层：浏览器请求 Next.js 同源 `/api/kb/files`，再由 route handler 转发到 Go Backend；详情右栏已支持 chunk inspector
- `/chat` 和 `/chat/[conversation_id]` 已接真实请求层：浏览器请求 Next.js 同源 `/api/chat/**`，再由 route handler 转发到 Go Backend；消息发送默认使用 SSE 骨架
- 聊天已采用本地 pending turn、SSE 生成态、自动跟随和保留草稿的输入交互；新会话先显示 Backend 的“新对话”，首个成功持久化问题后的标题必须以刷新返回值为准；侧栏支持 owner 的键盘行内重命名（`Enter` 保存、`Escape` 取消），不得嵌套 Link/按钮或在客户端推断 title source。唯一消息 viewport 负责历史滚动、自动跟随和圆形箭头“回到最新”操作；输入框从单行随内容增高至 `200px`，发送/停止共用右下角圆形操作位。宽屏阅读列和 composer 最宽 `760px`、窄屏保留 `20px` 正文边距。完成消息默认收敛为问题、回答和来源入口；`CitationFootprint` 继续按 Backend rune range 渲染、在跨行时逐行显示下划线，snapshot/freshness/K6 核验收进按需“回答信息”或来源抽屉；`knowledge_snapshot_unavailable` 保持 fail-closed，并展示管理员可执行的知识发布说明
- 聊天、知识库、系统配置页遇到 Backend `unauthenticated` 会清除 stale cookie 并以当前路径跳转登录；聊天额外在同标签临时恢复未发送草稿，知识库/配置页不保留未提交表单，也不把任何草稿写入服务端
- 未匹配路由始终显示主题化中文 404 页面，使用 `--ink`、`--surface-strong` 与 `--accent` 保证对比度；proxy 仅为已知工作台路由执行登录与角色门禁，未知地址不得被误重定向到登录页。
- `/config` 已接真实请求层：浏览器请求 Next.js 同源 `/api/agent/**`，再由 route handler 转发到 Go Backend；支持保存 agent / retrieval config
- 配置与知识管理详情使用按需弹窗，不再常驻右侧 inspector；主区入口打开 Base UI Dialog，弹窗由遮罩、关闭按钮和 `Escape` 关闭，详情内部独立滚动
- `/config` 的每个 Agent/retrieval 编辑项均有相邻中文帮助和默认展开的同题对比例，说明参数变化可能怎样影响表达、候选证据或拒答，而不会改变的事实边界，以及需要受控评测、重建索引或审批发布的情形；示例不是推荐参数或发布证据，保存仍只创建候选。
- `/config` 保存前会按 typed payload 比对当前版本：无语义变化时按钮不可用且不会创建候选；版本详情同时显示 Agent 与 retrieval 的完整历史，并以相邻版本的字段差异解释候选变更。历史由 `/api/agent/config/versions` 的 Postgres/Backend read model 提供，不从回答 evidence 推断。
- Web 外观已采用可注册主题系统：`verdant-habitat`（绿野共生）为默认生态主题，`quiet-paper` 为兼容基线；主题只保存在当前浏览器的 localStorage，通过 `html[data-theme]` 应用，不写 Backend、知识或 Agent 配置
- `U6a` 已接入 Tailwind v4 CSS-first 与 shadcn 本地源码组件层：主题 token 映射为 Tailwind semantic color，`app/_components/ui/` 拥有 Button/Input/Textarea/Select/Switch/Tooltip/AlertDialog。配置台、登录与主题选择已迁移；聊天和知识库仍待 `U6b` 复用该层逐页迁移。当前 shadcn registry source 使用 Base UI primitive，不增加业务状态或服务端权限路径。
- `K7` 已在 `/config` 接入 execution/delivery/eval evidence：管理员可按 conversation/snapshot/revision 筛选回答，并展开 snapshot manifest、配置版本和 citation provenance；prompt 版本只从已登记的 `prompt_v1/prompt_v2` 选择
- `Q1` 已在 `/config` 接入管理员 answer feedback、候选 experiment、报告对照、审批发布和 forward rollback；候选与发布状态由 Backend 决定，前端明确提示候选不影响线上、发布只影响后续新会话
- `K9-S0` 已要求 `Conversation.owner_id`；Web 不参与授权，直达不存在或无权访问的会话时跳到当前账号可访问会话并保留统一提示
- `K4-K6` 已接 document/revision lifecycle、回答 provenance、知识更新日历、snapshot 成员详情与独立历史核验 UI；核验 result 不会覆盖原回答正文
- `C1/U3` 已接回答内 citation anchor：Web 只按 Backend 持久化 rune range 渲染下划线，hover/focus 懒加载来源，点击打开可关闭的来源详情抽屉；无 anchor 的旧消息保留消息级来源入口
- `C2/C2-R1` 已区分知识回答与有限普通交流、明确的公共开放世界拒答：非知识回复不显示 snapshot、revalidation、citation 或额外知识状态标签；相关或混合问题仍按既有引用路径展示；首个 pending 文案保持中性。
- 同源 API proxy 会转发浏览器 cookie，Backend 是权限判断最终来源
- 已补 Playwright 浏览器级 smoke，覆盖登录、聊天发送、C2 非知识标签、SSE completed、citation 面板，以及宽/中/窄三种视口下的会话栏与引用检查器层级
- Next 16 开发配置已允许 `127.0.0.1` 为 `allowedDevOrigins`；这是本机 loopback/HMR 的开发兼容配置，生产请求来源不受此设置放宽

## 继续任务指南

如果新会话接手前端工作，默认按以下顺序继续：

1. 先细化跨页面错误态、loading skeleton 和空态
2. 再增强 Chat 错误态、重试与中止交互
3. 如调整 Chat 错误语义或引用结构，回归 Chat streaming UI

当前最适合直接开工的任务：

- `K9` 后续 service security、消息恢复和内部试用失败路径

联调注意事项：

- 默认 Web proxy 目标是 `http://127.0.0.1:8080`
- 如需覆盖 Backend 地址，启动 Next.js 前设置 `BACKEND_API_BASE_URL`
- 本地默认账号为系统管理员 `admin/admin123`、知识库管理员 `knowledge_admin/knowledge_admin123`、普通用户 `user/user123`，可通过 Backend 环境变量覆盖
- `make integration` 会覆盖 Web chat JSON/stream proxy smoke、Web knowledge proxy 上传 smoke 和 Web config proxy smoke

开始前先确认：

- `packages/contracts/openapi.yaml` 中聊天与知识库接口字段
- `docs/design-docs/single-agent-v1-web-ui-design.md` 中页面树、状态流和 citation 展示规则
- `make web-lint`、`make typecheck`、`make web-build` 当前可通过

## 后续需要补充

- K8 模型比较结果的横向对照视图
