# U4 可插拔生态主题交付计划

## Goal

- 交付可注册、可选择、浏览器本地持久化的 Web 主题系统。
- 首个主题为 `verdant-habitat`，在不干扰 AI 问答和管理员工作流的前提下表达动植物与环保理念。

## Requirements And Acceptance

- 主题注册表、provider 与选择器具有单一写入路径。
- 所有角色与登录页可切换个人主题；不写服务端、不改变权限或运行时配置。
- 默认生态主题覆盖全局 shell 和登录页；语义 token 保证聊天、管理页继承主题。
- invalid storage fail-safe、reduced motion、键盘可操作、构建/测试回归均有证据。

## Execution

- [x] 冻结主题 contract 与视觉边界
- [x] 实现 registry/provider/selector
- [x] 接入 workspace 和 login 壳层，完成 verdant-habitat CSS
- [x] 追加测试、构建与 integration 验收

## Active Status

- 当前状态：`verified`
- 风险：避免把个人外观偏好误写入 Agent 配置或污染业务 CSS token。
- 最后验证：`pnpm lint`、`pnpm typecheck`、`pnpm build`、focused Playwright `2 passed`、`make integration`（`54 passed`）。
- 下一动作：如需新增主题，只登记 registry entry 和对应 scoped token CSS；如需组织级主题策略，另立 server-owned design。
