# tests/integration

本目录用于跨服务联调测试。

当前设计依据：

- `docs/harness/single-agent-v1-integration-test-plan.md`

当前已落地：

- `test_stack_smoke.py`
  - 启动 runtime 与 backend 进程
  - 覆盖 Backend 登录、`/api/auth/me` 和 `user/admin` 角色门禁
- 覆盖 runtime `preview/run`
- 聊天路径启动显式 test-only OpenAI-compatible provider；产品 Runtime 未配置真实模型时 fail-closed，不再依赖隐式 heuristic fallback
  - 覆盖 backend conversation、sources、upload、chunks、reindex 冒烟链路
  - 覆盖 `P4` 的 happy-path / failure-path 摄取状态回写

首批优先覆盖：

- 文件上传到摄取任务创建
- 摄取状态回写
- chunk 可见性
- 聊天请求到来源返回
- 删除文件后的检索不可见性
