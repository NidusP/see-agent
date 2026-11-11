# C2 有限对话意图路由交付计划

## 文档控制

- 状态：`active`
- 更新日期：`2026-07-22`
- Owner：`p`
- 权威设计：[有限对话意图路由设计](../../design-docs/single-agent-v1-bounded-conversation-intent-design.md)

## 交付目标

- 在 retrieval 前稳定识别高置信度问候/感谢/告别/能力咨询/越界闲聊。
- 知识问题和任何不确定输入继续使用 K5 snapshot-scoped RAG。
- 管理员和用户都能区分“使用知识的回答”与“受控普通交流”。

## 执行

- [x] 冻结 request/response/message/evidence contract 与 Postgres `009` migration。
- [x] 实现 Backend `IntentRouter` 与混合输入优先走知识的测试。
- [x] 实现 Runtime JSON/SSE 受控回复，证明未调用 retrieval/model。
- [x] 实现 Postgres 非知识回答原子写入和 K7 evidence 查询。
- [x] Web 隐藏非知识回答 snapshot，显示 intent 标签并使用中性 pending 文案。
- [x] 运行 Backend/Runtime/Web/full integration/Postgres/Playwright/真实全栈与严格 quality gate。
- [x] `C2-R1`：为明确公共开放世界问题增加 `out_of_scope` 受控拒答，统一问候/能力/范围提示中的 RAG 边界文案。
  - 依赖：`C2` 已有受控 write path、K5/K7 evidence；不改 snapshot、检索配置或发布指针。
  - 写入范围：产品规格、C2 设计、Runtime request schema/OpenAPI、Backend IntentRouter、Runtime 固定回复及回归测试。
  - 验收：天气/美国总统（含“忽略知识库”前缀）JSON/SSE 均为无检索拒答；`你好，报销多久提交` 仍走 RAG 并保留 citation。

## 完成条件

设计文档的六类意图矩阵全部具备自动化或真实全栈证据；K5/K6/C1 历史契约、prompt_v2 gate 与知识演进 fixture 均保持通过。

## 验收结果

- Backend 全量通过；Runtime `77 passed`；integration/e2e `42 passed`；production Playwright `17 passed`。
- 隔离 Postgres `009` migration 与 controlled execution evidence smoke 通过。
- 真实 JSON/SSE、管理 evidence 与 Web 标签均验收；服务保持在 Web `3101`、Backend `8081`、Runtime `8000`。
- 最新严格 gate：`29/40`、grounded `0.6286`，全部门槛通过。

## C2-R1 当前状态

- 当前状态：`verified`
- 交付：Backend 的确定性 `IntentRouter` 仅对冻结的天气/美国总统公共开放世界类别返回 `out_of_scope`；Runtime JSON/SSE 固定拒答；Web 复用受控非知识 UI。未知、混合和业务问题仍为 `knowledge_query`。
- 自动化证据：`GOCACHE=/tmp/see-agent-go-build make backend-test` 通过；`make runtime-test` 为 `94 passed`；`make web-lint`、`make typecheck` 通过；Playwright `e2e/bounded-conversation-intent-ui.spec.ts` 为 `1 passed`。
- 真实栈证据：`conv_046` 普通用户连续提问中，`你好` 为 `social_greeting` 且说明 RAG 边界；`今天天气如何`、`美国总统是谁` 均为 `out_of_scope/refusal`；`员工手册里报销提交期限是多少天？` 保持 `knowledge_query`、`knowledge_used=true` 且有 `3` 条 citation。Runtime 直接 JSON/SSE 均记录 `retrieval_performed=false`、`generation_performed=false`、`knowledge_used=false`、无 citation。
- 质量门：`make eval-runtime-k7-gate` 通过（`31/40`，retrieval/citation `0.9714`，refusal `1.0`，grounded `0.7357`）。
- 下一动作：在后续真实业务知识 onboarding/Q1 期间，按同一固定类别边界观察开放世界误命中；不要扩展为未经评测的通用“相关性分类”。
