# tests/e2e

本目录用于 Web 到后端的端到端验证。

当前设计依据：

- `docs/harness/single-agent-v1-integration-test-plan.md`

当前已落地：

- `test_web_shell_smoke.py`
  - 启动 `Next.js` production server
  - 冒烟覆盖未登录跳转、普通用户导航隐藏、`/chat`、`/chat/[conversation_id]`、管理员 `/knowledge`、管理员 `/config`
- `test_web_knowledge_proxy.py`
  - 启动 runtime、backend 与 Web
  - API proxy 聊天用例同时启动显式 test-only OpenAI-compatible provider，不使用产品 fallback
  - 覆盖 Web 登录后 cookie proxy、chat proxy、knowledge proxy、config proxy update 和普通用户访问管理 API `403`
  - 调用 `apps/web` 的 Playwright smoke，覆盖真实浏览器登录、聊天发送、SSE completed 和 citation 面板

相关命令：

- 全量：`make integration`
- 仅浏览器 UI smoke：先启动 runtime/backend/web，再运行 `WEB_BASE_URL=http://127.0.0.1:3000 make web-ui-smoke`

首批优先覆盖：

- 登录
- 发起会话
- 提问并查看引用
- 上传文件并观察状态
- 查看文件详情
