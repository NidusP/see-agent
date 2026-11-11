# 单智能体 V1 Web UI 设计

## 文档控制

- 状态：`active`
- 版本：`v1.2`
- 更新日期：`2026-08-03`
- 目标：为 `apps/web/` 提供可直接指导前端实现的页面、状态和组件设计

## 1. 设计目标

V1 Web 前端要解决的不是“做一个漂亮首页”，而是：

- 提供稳定的聊天体验
- 展示结构化引用
- 支持管理员上传知识并查看状态
- 支持管理员查看和调整配置版本
- 在不暴露运行时内部细节的前提下，清楚呈现系统状态

## 2. 范围

### 2.1 本文覆盖

- 页面树
- 布局结构
- 状态流
- 契约对象到前端 view model 的映射
- 引用卡片展示
- 上传工作流

### 2.2 本文不覆盖

- 用户管理、部门级和文档级权限
- 营销页面
- 多租户导航
- 微调管理界面
- 图像区域级证据交互

## 3. 页面树

建议首版页面结构：

```text
app/
  (public)/
    login/page.tsx
    admin/login/page.tsx
  (workspace)/
    chat/page.tsx
    chat/[conversation_id]/page.tsx
    knowledge/page.tsx
    knowledge/[file_id]/page.tsx
    config/page.tsx
```

### 3.1 `/login`

- 终端用户登录入口；不显示管理员角色、管理能力或管理员快捷凭据
- 成功后进入聊天工作台
- 登录失败展示中文错误提示
- 支持 `next` 参数，登录后回到原目标页面
- 本地开发时只可显示普通用户快捷凭据；生产构建不显示任何快捷凭据。

### 3.1.1 `/admin/login`

- 知识库管理员与系统管理员共用的受控管理入口；页面不要求用户在登录前选择角色，Backend 登录结果决定实际工作区和权限。
- 本地开发时可显示管理员测试凭据；生产构建不显示任何快捷凭据。
- `/knowledge`、`/config` 及其失效会话恢复都以此入口作为 `next` 登录目标；`/chat` 继续使用 `/login`。
- 两个入口共用同一 `/api/auth/login`、session cookie 和 Backend role gate。前端入口分离仅控制可见性，不能作为授权判断。
- 已进入工作台后，如任一受保护 API 返回 `unauthenticated`，Web 调用 logout 清除失效 cookie，并跳转与当前工作区匹配的登录入口；聊天页在同一标签恢复未发送草稿，知识库与配置页不保留未提交的管理表单。

### 3.2 `/chat`

- 会话列表 + 默认空态
- 新建会话入口

### 3.3 `/chat/[conversation_id]`

- 主聊天工作区
- 消息列表
- 输入区
- 引用查看区

### 3.4 `/knowledge`

- 文件上传入口
- 文件列表
- 状态筛选

### 3.5 `/knowledge/[file_id]`

- 文件详情
- 最新任务状态
- chunk / locator 检查视图

### 3.6 `/config`

- 当前 agent config
- 当前 retrieval config
- 版本列表
- 每张配置卡在字段旁展示简短影响说明，并提供默认展开的“同一问题下的回答变化示例”：Agent 卡覆盖风格、采样与长度；检索卡覆盖候选证据、拒答边界、策略与 rerank。示例必须明确其为说明性对比，不构成推荐参数或发布证据；每个示例均强调候选保存、固定 scope 评测、人工审批和发布仍是独立步骤。

## 4. 全局布局

工作台以聊天为第一视图：历史会话始终属于左侧栏，当前工作区由顶栏选择，引用详情只在用户需要时展开。这样阅读回答、继续写作和查看证据不会被常驻管理导航挤压。

所有已登录工作区的顶级壳使用同一 `viewport-workspace-shell` 视觉约束：优先 `100dvh`、回退 `100vh`、根文档 overflow 锁定和 `min-height: 0`。这是 CSS 布局职责，不建立新的 React state/provider。`chat-first-shell` 继续拥有聊天侧栏/抽屉和顶栏网格，`system-config-shell` 继续拥有配置顶栏与双栏网格；它们都不得把长内容泄漏为浏览器页面滚动。窄屏聊天壳本身允许 fixed 侧栏与 scrim 越界显示，但根文档和聊天 frame 仍锁定，主内容继续在内部滚动。

### 4.1 聊天侧栏

- 左侧栏承载产品标记、新建会话、会话列表、当前角色和退出入口；会话行只展示标题、更新时间和 active state。
- 新建会话初始显示“新对话”；第一条成功持久化的用户问题会由 Backend 自动命名，Web 在本轮完成后刷新会话列表，使用服务端返回的标题而不自行计算。会话行提供可访问的“重命名会话”操作；编辑态为单行输入、`Enter` 保存、`Escape` 取消，移动抽屉与桌面展开态均可使用。提交中或空白标题不可保存，失败时保留输入并展示就近中文错误。
- 桌面侧栏可收缩为稳定的紧凑宽度，收缩后保留可聚焦、带明确 accessible name 的展开与新建会话操作，不显示会话列表文字；收缩样式不得隐藏恢复按钮本身。
- 侧栏收缩偏好只保存在当前浏览器；不写入服务端，也不改变会话、权限或路由。
- 窄屏时侧栏变为带遮罩的临时抽屉，由顶栏的菜单按钮打开；抽屉内只显示语义明确的“关闭会话侧栏”按钮，不能复用桌面“收起/展开”按钮或其本地偏好。选择会话、切换路由、点击遮罩或按 `Escape` 后关闭。

### 4.2 顶栏工作区切换

- 产品品牌 `智能问答` 固定在会话侧栏；顶栏左侧显示当前功能模块名。普通用户只显示静态 `对话`，不显示知识库或配置入口。
- 知识库管理员以当前功能模块名作为工作区菜单触发器，在同一位置切换 `对话`、`知识库`；系统管理员还可切换 `配置`。触发器文字随当前路由同步，菜单当前项有 active state。这个菜单取代旧的全局导航 rail。
- `U5` 起系统管理员使用独立的单一“系统配置台”壳层，只显示 `/config`、主题和退出，不渲染会话侧栏或知识库导航；直达 `/chat`、`/knowledge` 由 Web 路由体验层送回 `/config`。这不改变 Backend 的 system-admin 安全授权；治理审批入口将在后续迁入配置台。
- 角色与退出操作放在顶栏右侧或侧栏底部，不在主阅读区重复展示。
- 前端不显示管理入口只是体验边界；Backend 仍是管理员路由和 API 权限的最终裁决者。

### 4.3 主工作区

- 聊天页使用单一、居中的消息阅读列和底部输入区；宽屏阅读列与 composer 的最大宽度为 `760px`，消息 viewport 在此之外仍保留至少 `32px` 横向内边距，正文不得贴到主工作区边缘。窄屏保持 `20px` 内边距并允许列宽填满可用区域。空会话将问题引导和输入区组织在同一视觉中心。
- 知识库与配置页复用同一顶栏与侧栏，但其文件详情、snapshot 和配置版本作为该路由的局部检查器，不显示会话历史以外的 mock 内容。
- 默认会话扫描只保留用户问题、助手正文和来源入口：已完成的用户消息不显示角色、时间或完成标签，已完成助手消息只标识“助手”。生成、失败、停止和待发送状态仍要直接显示。用户消息气泡采用紧凑内边距（桌面 `12px 16px`），避免短问题在右侧形成过大的留白。模型、snapshot、revision、freshness 和历史核验等审计信息只在按需的“回答信息”或来源详情抽屉出现，不挤占正文阅读空间。
- composer 是单个多行输入卡片：初始为单行，随文字行数自动增高至 `200px`，超过上限后仅 textarea 内滚动，不能让输入区挤占消息阅读区。主发送/停止操作固定在输入框右下角；发送使用圆形向上箭头，无内容时禁用，停止保持同一圆形操作位。正常空闲态不显示键盘提示；仅发送状态、失败原因和重试操作在输入卡片下方出现，不能和主要发送操作竞争视觉层级。

### 4.4 引用详情抽屉

- 回答内的锚点由 `CitationFootprint` 渲染：它只消费 Backend 持久化 rune range，不改变 citation/provenance 数据契约；其文字下划线必须逐行绘制，引用跨行时每一行都可见同样的证据足迹。hover/focus 只显示轻量来源预览，不自动占用阅读空间。
- 用户点击锚点或“查看来源”后打开可关闭的引用详情抽屉，抽屉按当前 message 或锚点 rank 过滤 citation，并保留 snapshot、revision 和 freshness provenance。
- 桌面抽屉从右侧覆盖主内容的辅助区域；移动端从底部弹出。关闭抽屉、切换锚点或按 `Escape` 不重置会话、滚动位置或输入草稿。

### 4.5 响应式行为

- 宽屏（`>= 901px`）：聊天使用 `可收缩会话侧栏 + 顶栏/主对话`；消息区独立滚动，引用详情为按需右侧抽屉。
- 窄屏（`<= 900px`）：主对话占满页面，侧栏为遮罩抽屉，引用详情为底部抽屉；两个抽屉都不参与正文自然流，避免压缩输入区。
- 所有宽度下聊天保持固定视口壳层：桌面消息区、会话列表及管理双栏在各自声明的区域滚动；窄屏聊天主区或管理内容区收敛为单一内部滚动区。浏览器根页面不得成为长会话或长表单的滚动容器。
- 聊天主工作区进一步划分为固定会话标题区、唯一可滚动的消息历史区和固定输入区。输入框不得以 sticky 方式覆盖消息；“回到最新”作为消息历史区上的浮层出现，自动跟随与用户上滑检测只监听消息历史区。该入口是居中的圆形向下箭头按钮，不显示文字；可访问名称和悬停提示保留“回到最新”。圆形图标操作是可执行按钮 `6px` 圆角规则的明确例外。
- 不以 viewport 宽度缩放字号；按钮、导航项和列表行使用稳定高度，避免状态变化造成布局跳动。
- 可执行按钮与文件选择控件使用 `6px` 圆角；只有状态、格式和版本等非交互标签保留胶囊形态，明确区分操作与信息。

### 4.6 主题偏好（U4）

- 界面主题是本浏览器的可逆显示偏好，所有角色均可在登录页和工作台切换；它不是系统管理员 Agent 配置，不写服务端、不进入审计 evidence。
- Web 通过主题注册表和 `html[data-theme]` 应用语义 CSS token。业务组件不得硬编码主题颜色；未知主题偏好回退默认主题。
- 首个 `verdant-habitat` 主题使用绿色生态、植物与授粉者意象，但装饰不能遮挡聊天、citation 或管理操作；遵从 `prefers-reduced-motion`。

实现记录（`2026-07-15`）：旧版四栏 `rail + conversations + main + inspector` 已作为历史实现完成验收。`U3` 已将其收敛为本节的聊天优先壳层，并复用 C1 的 citation detail 内容，不改变引用数据契约。生产 Playwright UI smoke 为 `15 passed`，桌面与移动实拍已验收。

交互回归记录（`2026-07-15`）：桌面收起态恢复按钮不再被 CSS 隐藏；新建会话图标保留 accessible name；移动抽屉使用独立关闭按钮，不复用桌面折叠语义；顶栏按路由显示 `对话/知识库/配置`。目标 Playwright `3 passed`、完整生产 UI `16 passed`，真实 `619px` 右侧浏览器完成开/关/重开验收且无 console error。

### 4.6 流式对话状态

- 提交后，Web 立即在本地渲染用户消息和 assistant 生成占位，不等待首个网络字节；占位先显示“正在检索知识”，收到 `message.started` 后替换为 Backend 持久化消息。
- `message.delta` 只追加 assistant 可见正文，并显示短暂的生成光标；`message.completed` 替换为持久化 answer、聚焦引用并恢复可发送状态。
- 发送期间用户仍可编辑下一条草稿，但不并发发送第二个请求。`Enter` 发送，`Shift+Enter` 换行；“停止生成”只中止当前请求，保留草稿与已显示内容。
- 消息区在用户停留底部时自动跟随生成；用户向上阅读历史后停止强制滚动，并显示圆形向下箭头的“回到最新”入口。
- `knowledge_snapshot_unavailable` 不显示为模糊的暂时故障：保留本地失败回合，明确说明“还没有已发布的知识版本”，并提示管理员完成上传、索引和发布。它不伪造已持久化的回答或来源。
- 会话正文避免展示 conversation id、模型配置版本和原始状态枚举；这些审计字段仅保留在检查器或管理界面。
- 当 Backend 返回 `unauthenticated` 时，Web 将当前输入草稿写入当前标签页的 `sessionStorage`，调用 logout 清除失效 cookie，并跳转 `/login?next=<当前聊天路径>`。登录成功后回到原路径，聊天工作区消费并删除该草稿；草稿不写入服务端、不进入消息历史，也不跨浏览器标签保留。
- 聊天侧栏会出现在知识库工作区；侧栏的会话读取若先发现失效会话，仍必须保留当前 `/knowledge` 返回路径，不得把知识库管理员错误送往 `/chat`。同一标签内只允许一次认证恢复，避免并行 API 失败覆盖首个 `next`。

## 5. 核心用户流

### 5.1 聊天流

1. 未登录用户访问工作台时跳转 `/login`
2. 用户登录后进入 `/chat`
3. 用户点击“新建会话”
4. 前端调用 `POST /api/chat/conversations`
5. 跳转到 `/chat/[conversation_id]`
6. 用户输入问题
7. 前端调用 `POST /api/chat/conversations/{conversation_id}/messages`
8. 消息区展示流式或非流式回答
9. 用户点击引用，按需打开详情抽屉展示 citation 列表

#### 5.1.1 T5 本地联调基线

`T5` 阶段 Web Chat 与 Knowledge 采用一致的同源 API proxy：

- 浏览器请求 Next.js 同源路径，例如 `/api/chat/conversations`、`/api/chat/conversations/{conversation_id}`、`/api/chat/conversations/{conversation_id}/messages` 和 `/api/chat/conversations/{conversation_id}/sources/{message_id}`。
- Next.js route handler 转发到 Go Backend，默认目标为 `http://127.0.0.1:8080`，可通过 `BACKEND_API_BASE_URL` 覆盖。
- Next.js route handler 必须转发浏览器 cookie，使 Backend 可以校验登录态。
- 首版已保留非流式 JSON 响应；`T12` 起 message send 默认使用 `stream=true` 的 SSE 骨架。
- 当 runtime chat 显式配置但不可达时，Backend 会返回 `503 runtime_unavailable`，Web 应展示发送失败状态而不是插入成功回答。

当前实现状态（`2026-07-02`）：

- 已新增 `app/_lib/chat-api.ts`，封装 conversation list/detail/create、message send 和 message sources。
- 已新增 `/api/chat/conversations`、`/api/chat/conversations/{conversation_id}`、`/api/chat/conversations/{conversation_id}/messages` 和 `/api/chat/conversations/{conversation_id}/sources/{message_id}` 的 Next.js route handlers。
- `/chat` 与 `/chat/{conversation_id}` 已使用 client-side `ChatWorkspaceProvider` 管理会话列表、选中会话、消息、citation focus、发送中状态和错误态。
- 未选会话时发送第一条消息会先创建会话，再发送消息。
- 已有 Web proxy e2e smoke 覆盖 create conversation、send message 和 fetch sources；UI 级 browser interaction smoke 留给后续增强。

#### 5.1.2 T12 Chat SSE 骨架

`T12` 的 Web 行为约定：

- `ChatWorkspaceProvider` 发送消息时优先调用 `streamMessage`，请求体带 `stream=true`，请求头带 `Accept: text/event-stream`。
- Next.js API proxy 对 `text/event-stream` 响应必须直接透传 `response.body`，避免把事件流缓冲成整段文本。
- UI 收到 `message.started` 后插入 user message 和 assistant streaming 占位消息。
- UI 收到 `message.delta` 后按 `message_id` 追加 assistant 内容。
- UI 收到 `message.completed` 后用最终 assistant message 覆盖 streaming 占位，并刷新会话列表和 citation focus。
- `message.delta` 只来自 Runtime `/v1/chat/stream`；未配置 Runtime、收到 `message.error` 或流在终态前中断时，Web 显示失败卡片与重试，不把它当作完成回答。

#### 5.1.3 U2 Chat 错误、重试与中止首片

`U2` 的第一片只改变 Web 交互，不要求 Backend 新增取消接口：

- 发送或流式读取失败时，消息输入区展示中文局部错误态，说明本次回答未成功完成。
- Web 记住最近一次失败的用户输入，并在错误态中提供“重试上一条”按钮；重试会把同一段用户文本重新发送到当前会话，未选会话时仍按现有规则先创建会话。
- 如果 SSE 已经收到 `message.started`，失败时保留已插入的 user message，并把仍处于 `streaming` 的 assistant message 标为 `failed` 与 `finish_reason=error`。
- 如果请求在 `message.started` 前失败，页面不伪造成功消息，但错误态仍可重试原始用户输入。
- 发送中提供“停止生成”按钮。Web 使用 `AbortController` 中止浏览器侧 fetch / stream reader；中止后把当前 streaming assistant message 标为 `failed`，并展示“已停止生成，可重试上一条。”。
- 首片不保证 Backend 或 Runtime 已停止远端推理；若服务端已写入部分消息，刷新会话后以后端持久状态为准。

#### 5.1.4 A1-4 回答处置卡片与来源边界

- completed assistant message 的 A1 状态只取 `answer_disposition` 与结构化 `coverage`；Web 不从正文、citation 数、retrieval candidate 或 snapshot 猜测“是否完整”“是否需要澄清”。没有该字段的历史消息保持原样。
- `clarification_required` 显示“需要补充范围”和已校验问题；`insufficient_evidence` 显示“当前知识不足”；`partial_answer` 显示“已覆盖部分”以及 `supported_count/requested_count` 和未覆盖描述。`grounded_answer` 保持正常回答，`controlled_response` 沿用 C2 非知识展示。
- 澄清、不足和受控回复没有 message source action、citation anchor preview、知识版本/freshness 或历史核验入口，也不得为了抽屉预加载调用 message sources。`partial_answer` 与 `grounded_answer` 只显示实际 answer citations；legacy message 只在已有 citation 时保留兼容入口。
- `/config` 的管理员 evidence 详情显示 disposition、policy/retrieval/generation/knowledge-used 事实、coverage、clarification/resolution 和受控 candidate metadata。候选不是 citation，普通聊天页面不可读取。
- 管理员质量反馈按所选 evidence 的实际处置提供合法类型；`should_clarify`、`unnecessary_clarification` 与 `incomplete_coverage` 要求文字说明且不收 citation rank。控件过滤仅改善交互，提交仍由 Backend role gate、idempotency 和 disposition 校验决定。

### 5.2 上传流

1. 管理员进入 `/knowledge`
2. 选择一个或多个文件
3. 前端调用 `POST /api/kb/files`
4. 文件进入列表，状态显示为 `queued` 或 `running`
5. 前端轮询或刷新列表
6. 状态变为 `succeeded` / `failed`
7. 点击文件进入详情页查看 chunk 和 locator

#### 5.2.1 P6 本地联调基线

为避免浏览器直接依赖 Go Backend 端口和 CORS 设置，`P6` 阶段 Web 采用同源 API proxy：

- 浏览器只请求 Next.js 同源路径，例如 `/api/kb/files`、`/api/kb/files/{file_id}` 和 `/api/kb/files/{file_id}/reindex`。
- Next.js route handler 再转发到 Go Backend，默认目标为 `http://127.0.0.1:8080`，也可通过 `BACKEND_API_BASE_URL` 覆盖。
- 未登录用户访问 `/knowledge` 时进入 `/login`；普通用户访问时重定向回 `/chat` 并由 Backend 对 API 返回 `403`。
- 知识库的 document、snapshot、更新日历或 onboarding API 返回 `unauthenticated` 时，清除失效会话并跳转 `/login?next=/knowledge`；不保留未提交上传、replacement 或 onboarding 表单。
- `/knowledge` 页面使用 client-side server state 管理列表、上传中状态、筛选条件和详情右栏。
- 当 Backend 不可达时，页面应保留可渲染的错误态和刷新入口，不回退到 mock 数据冒充真实状态。

#### 5.2.2 U1 Chunk Inspector

`U1` 将知识库详情右栏从任务摘要扩展为 chunk / locator 检查视图，不新增原始文件预览跳转：

- `/knowledge` 仍保持三栏工作台，不新增强制跳转流程。
- Web 通过 Next.js 同源 route handler 代理 `GET /api/kb/files/{file_id}/chunks`。
- 右栏展示：
  - 文件 metadata 与 latest task。
  - chunk 总数。
  - 每个 chunk 的 block type、token count、locator 摘要、文本预览和 chunk id。
- 该视图仅管理员可见；普通用户仍由前端路由守卫和 Backend role gate 双重拦截。
- Chunk inspector 用于排查 eval 中的 `retrieval_miss`、`citation_mismatch` 和 locator 生成问题，不负责展示原始文件正文或文件下载。

### 5.3 配置流

1. 管理员进入 `/config`
2. 查看当前 agent / retrieval config
3. 编辑并提交
4. 前端调用 `PUT /api/agent/config` 或 `PUT /api/agent/retrieval-config`
5. UI 提示“仅新会话使用新版本”

普通用户不显示 `/config` 导航，直接访问 `/config` 会被前端路由守卫送回聊天页；Backend 对 `/api/agent/**` 仍返回 `403`。

#### 5.3.1 T11 本地联调基线

`T11` 阶段把 `/config` 从 mock 数据切到 Backend 真实请求层：

- 浏览器只请求 Next.js 同源路径：`/api/agent/config`、`/api/agent/retrieval-config` 和 `/api/agent/config/versions`。
- Next.js route handler 转发到 Go Backend，默认目标仍为 `http://127.0.0.1:8080`，并转发浏览器 cookie。
- 页面加载时并行读取 active agent config、active retrieval config 和版本列表。
- 保存 agent config 时调用 `PUT /api/agent/config`，保存 retrieval config 时调用 `PUT /api/agent/retrieval-config`。
- 保存成功后刷新当前配置和版本历史，并保留“只影响新会话”的风险提示。
- 若配置、证据或质量运营 API 返回 `unauthenticated`，页面不得继续展示可能由过期角色 cookie 生成的系统管理员壳或错误表单；必须清除会话并跳转 `/login?next=/config`。
- 首版不做配置回滚、审批流、按环境发布或多模型目录管理。

## 6. 前端状态模型

建议首版状态拆成三层：

### 6.1 Server State

由接口驱动，适合使用数据请求层管理：

- conversation list
- conversation detail
- message sources
- knowledge file list
- knowledge file detail
- config

### 6.2 View State

仅与界面交互有关：

- 当前选中的 conversation
- 当前展开的 citation 面板
- 上传对话框是否打开
- 配置表单脏状态

### 6.3 Transient State

短生命周期状态：

- 消息发送中
- 上传中
- 流式回答拼接中
- 刷新中

## 7. View Model 映射

前端不应直接把 API 对象原封不动塞给组件，而应先转换成 view model。

### 7.1 ConversationListItem

来源：

- `GET /api/chat/conversations`

字段建议：

- `conversation_id`
- `title`
- `updated_at`
- `is_active`

### 7.2 ChatMessageView

来源：

- `GET /api/chat/conversations/{conversation_id}`
- `POST /api/chat/conversations/{conversation_id}/messages`

字段建议：

- `message_id`
- `role`
- `content`
- `status`
- `created_at`
- `finish_reason`
- `citations_count`

### 7.3 CitationCardView

来源：

- `GET /api/chat/conversations/{conversation_id}/sources/{message_id}`

字段建议：

- `file_id`
- `filename`
- `citation_label`
- `snippet`
- `score`
- `source_format`
- `locator_display`

其中 `locator_display` 由前端根据 `source_locator` 结构生成，例如：

- PDF：`Page 3`
- DOCX：`2. Expense Reimbursement / paragraph 1`
- XLSX：`Summary · A1:F5`
- PPTX：`Slide 2 · Rollout milestones`

### 7.4 KnowledgeFileRow

来源：

- `GET /api/kb/files`

字段建议：

- `file_id`
- `filename`
- `source_format`
- `status`
- `uploaded_at`
- `uploaded_by`

### 7.5 KnowledgeFileDetailView

来源：

- `GET /api/kb/files/{file_id}`
- `GET /api/kb/files/{file_id}/chunks`

字段建议：

- `file`
- `latest_task`
- `chunks`
- `task_error`

## 8. 组件边界建议

建议至少拆出以下组件：

- `ShellSidebar`
- `ConversationList`
- `MessageList`
- `MessageComposer`
- `CitationPanel`
- `CitationCard`
- `KnowledgeUploadDropzone`
- `KnowledgeFileTable`
- `KnowledgeFileStatusBadge`
- `ChunkInspector`
- `ConfigForm`

### 8.1 不建议过早抽象的部分

- 过度通用的表单引擎
- 过度通用的表格抽象
- 复杂插件式页面系统

V1 更适合先做稳定、清晰、可改的直接实现。

## 9. 聊天页设计

### 9.1 空态

在尚未选择会话时展示：

- 产品定位简述
- 新建会话按钮
- 示例问题占位

### 9.2 消息区

用户消息与 assistant 消息视觉区分：

- 用户消息为紧凑、右对齐气泡，完成后不重复显示时间或状态
- assistant 消息强调可读性和引用入口；完成态仅保留轻量“助手”标签，异常/流式才展示状态
- 已完成回答的 snapshot、freshness 和历史核验收进按需“回答信息”；来源详情抽屉仍保留完整 provenance

assistant 消息卡建议包含：

- 正文
- 生成状态
- 引用按钮

### 9.3 输入区

至少包含：

- 多行输入框
- 输入框右下角内嵌的发送按钮；生成时同位置替换为停止按钮
- 发送中禁用状态
- 输入框下方的键盘提示、失败原因与重试操作

V1 不需要：

- 工具选择器
- 附件直传到聊天框
- 多模态输入

## 10. 引用面板设计

引用不是附属品，而是回答可信度的核心 UI。

### 10.1 交互规则

- 点击 assistant 消息的引用按钮后打开引用详情抽屉
- 默认展示按 `rank` 排序的 citation 卡片
- 当前消息切换时，右栏内容同步切换

### 10.2 回答内证据锚点

- assistant 正文中的 `citation_anchors` 渲染为可聚焦的细下划线文本，不额外插入模型内部标记、citation rank 或技术 id。
- pointer hover 或键盘 focus 展示轻量来源卡：文件名、结构定位、片段摘要、snapshot 和 freshness；卡片只使用该锚点声明的 citation rank。
- 点击锚点会过滤并固定到引用详情抽屉；宽屏从右侧打开，窄屏从底部打开。关闭抽屉后仍保留当前阅读位置和输入草稿。
- `citation_anchors` 为空时，保留“查看来源”入口，但不把整段回答误渲染为精确引用。来源加载失败时显示局部“依据暂不可用”，不得阻塞阅读或发送。
- 锚点必须支持鼠标、触摸和键盘；`Escape` 关闭临时来源卡或已打开的详情抽屉，并保留触发锚点焦点。

### 10.3 CitationCard 最小内容

- 文件名
- 结构定位标签
- 证据片段
- 分数或相对排序

### 10.4 结构定位展示规则

- `pdf`
  - `Page {page_no}`
- `docx`
  - `heading_path.join(" / ")`
  - 没有 heading 时显示 `paragraph {n}`
- `xlsx`
  - `{sheet_name} · {cell_range}`
- `pptx`
  - `Slide {slide_no} · {slide_title}`
- `json`
  - `{json_path}`
- `txt` / `md`
  - `block_label` 或标题路径

## 11. 知识库页设计

### 11.1 上传区

支持：

- 点击上传
- drag-and-drop
- 多文件上传

上传前的前端检查：

- 扩展名 / MIME 粗校验
- 文件大小超限提示

### 11.2 文件表格

建议列：

- 文件名
- 格式
- 状态
- 上传人
- 上传时间
- 操作

操作首版包含：

- 查看详情
- 重新索引
- 删除

### 11.3 状态徽标

推荐视觉映射：

- `uploaded` / `queued`
  - 中性
- `running`
  - 进行中
- `succeeded`
  - 成功
- `failed`
  - 警示

## 12. 配置页设计

### 12.1 Agent Config

首版至少包含：

- `model_id`
- `system_prompt_version`
- `temperature`
- `max_output_tokens`

### 12.2 Retrieval Config

首版至少包含：

- `embedding_model`
- `top_k`
- `rerank_enabled`

### 12.3 风险提示

配置页必须显式提示：

- 变更只影响新会话
- 历史会话仍绑定旧快照

### 12.4 质量运营与发布

配置工作台复用既有回答 evidence、eval report 与配置区，不另起管理壳层。管理员可在某条 evidence 详情中记录“正确/错误/应拒答/证据问题/知识缺口”及说明、期望答案或已有 citation rank；页面必须展示该反馈锚定的历史 snapshot/config/citation，而不是当前 active 状态。

实验视图显示固定 dataset/scope/manifest、baseline 与 candidate、报告指标和状态时间线。候选配置明确标为“不会影响线上”；只有 report 条件匹配且管理员审批后显示发布操作。发布与回滚必须提示“仅影响后续新会话”，并显示目标 release/前一 release；不允许前端假设或代替 Backend 判断 gate、状态机或权限。

## 13. 加载、错误与空态

### 13.1 加载态

- 列表区域使用 skeleton
- 聊天消息流式期间保留正在生成状态

### 13.2 错误态

按两层处理：

- 页面级错误
- 局部动作错误

例如：

- 上传失败
- 发消息失败
- 获取 citations 失败
- 未匹配路由：无论是否已有登录态均显示中文 404 页面，使用当前主题的前景、表面与主色 token，提供回到聊天工作台的入口；路由守卫只处理已知工作台路由，不得将未知地址重定向到登录或依赖框架默认的浅色错误文案。
- 数据加载失败与空态必须分离。任何 execution、delivery、eval、citation 或管理 API 的 `401/403/404/5xx` 不能被转换为 `{ items: [] }`、`暂无数据` 或无来源的正常回答；保留错误卡片、重试动作和对应 request id。
- 聊天收到 JSON error 或 SSE `message.error` 时，将瞬时正文标为未完成并展示失败/重试卡片；不得显示来源入口、snapshot 或“回答完成”。只有 Backend 返回 `completed` message 后才能渲染 citations/anchors。
- `401` 或 session 无效时保留安全的当前路径并跳转 `/login?next=...`；`403`、`404`、`503` 分别使用无权限、资源不可用、依赖不可用状态，不相互伪装。

### 13.3 空态

- 无会话
- 无文件
- 无引用
- 无配置历史

都应有明确文案，而不是空白页面。

## 14. 实现顺序建议

前端实现建议按以下顺序推进：

1. 工作台壳层布局
2. 会话列表与聊天页空态
3. 消息区与发送表单
4. 引用面板
5. 知识库上传与文件列表
6. 文件详情与 chunk inspector
7. 配置页

## 15. 与当前代码骨架的关系

当前 [page.tsx](/Users/populus/workspace/agent/see-agent/apps/web/app/page.tsx) 只是实现前的引导页。

下一步应逐步把它演进为：

- 真正的工作台入口
- 可承载会话与导航的布局页
- 与契约字段对齐的数据驱动页面
- 中文优先的登录、导航、空态和错误提示
