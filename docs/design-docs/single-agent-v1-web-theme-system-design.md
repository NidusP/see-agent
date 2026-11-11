# 单智能体 V1 Web 可插拔主题系统设计

## 文档控制

- 状态：`verified`
- 更新日期：`2026-07-18`
- 工作包：`U4`
- 上位设计：[Web UI 设计](single-agent-v1-web-ui-design.md)、[产品规格](../product-specs/single-agent-v1-spec.md)

## 1. 目标与边界

为 Web 工作台提供可扩展的主题注册系统，并交付首个 `verdant-habitat`（绿野共生）主题。主题应把动植物与环境保护的价值传达为安静、可长期阅读的工作环境，而不是营销页或影响业务语义的插画层。

本工作包不改变知识、聊天、citation、配置、角色、服务端 API 或 Q1 发布状态；主题不是系统管理员的 Agent 参数，也不进入 execution evidence。

## 2. 主题契约与所有权

`apps/web/app/_lib/themes.ts` 是主题注册表的唯一入口。每个主题声明稳定 `id`、中文名称、简短说明、颜色 token 组与可选装饰标识；新增主题只需注册一个定义及其 scoped CSS，不修改各业务页面。

`ThemeProvider` 是唯一写入者：

- 初始值为 `verdant-habitat`；
- 选择保存在当前浏览器 `localStorage` 的 `see-agent:theme`；
- provider 在 `<html data-theme>` 写入已验证的 id；未知/损坏值 fail-safe 回退到默认主题；
- 任意已登录角色及登录页均可选择，因为这是个人可逆的显示偏好，不是知识库或系统配置授权。

不写 Postgres、不共享给其他浏览器、不通过 cookie 影响鉴权。若将来需要组织级主题，应另行定义 server-owned policy 与 audit contract，不能复用本地偏好作为权威配置。

## 3. 视觉与可访问性

`verdant-habitat` 使用深松针绿、苔藓、纸白与土壤棕 token；页面背景使用低不透明度的叶片/水滴抽象层，侧栏放置非交互的叶芽与授粉者标记。装饰绝不覆盖文字、按钮或来源证据。

- 所有业务组件继续只引用语义 CSS variable；不得把主题色散写进聊天、知识或配置组件。
- 主题选择器有文字名称、当前状态和可访问 label；键盘可操作。
- `prefers-reduced-motion: reduce` 时所有主题动画停用；图案为静态且无闪烁。
- 对比度、焦点样式、危险/成功语义颜色不因主题降低；主题不传达任何业务状态。

## 4. 用户流

1. 用户进入登录页或任意工作台。
2. 在“界面主题”选择器选择一个已注册主题。
3. provider 立即写入 `data-theme` 和本地偏好；当前页面无刷新切换。
4. 后续在同一浏览器打开登录、聊天、知识库、配置页均保持该主题。

## 5. 验收与失败语义

- `verdant-habitat` 默认展示，且至少覆盖登录、聊天、知识库和配置壳层。
- 注册表返回的主题可被选择、持久化与恢复；无效 storage 值回退默认主题，不抛出异常。
- 主题选择不产生 Backend 请求，不改变 session/role/route，也不改变任何业务 API payload。
- Web lint、typecheck、production build 与现有 integration gate 通过；新增单元测试覆盖注册表 fallback 与存储读写。

## 6. 扩展与回滚

新增主题是前端构建期插件：增加一个 registry entry 与其 scoped token/decoration CSS 即可。删除或重命名已发布 id 时，旧 localStorage 值按 fallback 处理。首版不接第三方运行时插件，避免把未审核 CSS/脚本引入私有部署工作台。

## 7. 实现与验证记录

- 已实现 `themes.ts` build-time registry、`ThemeProvider` 的 `data-theme` 单一写入路径，以及登录页/工作台均可用的 picker。当前包括默认 `verdant-habitat` 与兼容基线 `quiet-paper`。
- `verdant-habitat` 使用松针、苔藓、纸白、叶脉、植物与授粉者装饰；所有装饰均为 CSS 层且 `pointer-events: none`，不会遮挡交互。
- 验证：Web lint、typecheck、production build、focused Playwright theme smoke（2 passed）和 `make integration`（54 passed）通过。
