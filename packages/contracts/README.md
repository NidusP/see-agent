# packages/contracts

本目录用于集中维护跨服务契约。

当前已落地：

- `openapi.yaml`
  - 对外产品 API 契约
- `chat-runtime-request.schema.json`
  - 后端发往 AI runtime 的聊天执行请求
- `chat-runtime-response.schema.json`
  - AI runtime 返回给后端的聊天执行结果，包含消息级 citations、可选的 `citation_anchors`，以及 A1 的 versioned answer disposition、clarification、coverage、retrieval candidates 和 execution observation
- `chat-runtime-strict-request.schema.json` / `chat-runtime-strict-response.schema.json`
  - F0 冻结的生产真实性 v2 目标契约：scope、execution observation、provider、answer disposition 和 citation provenance 不可缺省。F1/F2 完成前，现行 schema 仍是迁移兼容输入；严格 schema 已是后续实现的唯一设计依据。
- `ingestion-task.schema.json`
  - 后端发往 AI runtime 的摄取任务载荷
- `ingestion-task-result.schema.json`
  - AI runtime 回传给后端的摄取任务状态结果
- `source-locator.schema.json`
  - 统一来源定位结构
- `knowledge-evolution.schema.json`
  - 不可变 revision、published snapshot、更新事件、回答时效与核验结论的共享枚举

当前仍建议后续补充：

- 统一错误码清单
- 流式聊天事件协议
- 共享类型自动生成策略
- K2 后基于 fixture 的 JSON Schema conformance runner
