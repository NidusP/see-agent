# P0 生产 Fail-Closed 与真实性边界改造计划

## 文档控制

- 状态：`F0-F8 local fullstack hardening verified; production promotion remains a separately authorized rollout`
- 更新日期：`2026-07-24`
- Owner：`p`
- 用户目标：删除早期为打通流程而保留、但已经不符合生产要求的隐式兜底；任何端都不得伪造回答、引用、执行事实或成功状态，只能根据权威执行结果返回真实成功、受控处置或明确失败。
- 关联产品规格：[single-agent-v1-spec.md](../../product-specs/single-agent-v1-spec.md)
- 关联设计：
  - [Backend 设计](../../design-docs/single-agent-v1-backend-design.md)
  - [Runtime 与摄取设计](../../design-docs/single-agent-v1-runtime-ingestion-design.md)
  - [澄清与回答处置设计](../../design-docs/single-agent-v1-clarification-policy-design.md)
  - [知识 snapshot provenance 设计](../../design-docs/single-agent-v1-k5-snapshot-provenance-design.md)
  - [回答内引用锚点设计](../../design-docs/single-agent-v1-citation-anchors-design.md)
  - [Fail-closed execution 设计](../../design-docs/single-agent-v1-fail-closed-execution-design.md)
- 关联执行计划：
  - [产品交付计划](single-agent-v1-product-delivery-plan.md)
  - [A1 澄清、完整性与引用可信度计划](single-agent-v1-clarification-and-grounded-answer-plan.md)

commander 已原则性认可本计划的真实性边界与六项规则。F0 只允许冻结实现级契约和测试基线；在 F0 完成前，不改变 active quality release、Postgres migration 或聊天主写入路径，也不覆盖已经冻结的 K5/C1/Q1 历史契约。F0 中由 `p` 单独修改 shared contract；任何实现包均不得并行改动它。

## 1. 改造目标

生产链路必须满足以下真实性原则：

1. 没有真实模型执行，就没有模型回答。
2. 没有经过 snapshot scope 限定的真实检索，就没有知识回答。
3. 没有不可变 provenance，就没有用户可见引用。
4. 没有生成可索引 chunk，就没有摄取成功、revision ready 或发布资格。
5. 没有真实执行证据，就不补造 `intent`、provider、retrieval/generation flags、citation rank 或成功状态。
6. 依赖缺失、连接失败、超时、契约不匹配和写回失败必须以失败结束，不得转成可读的替代回答。
7. “需要澄清”“知识不足”“只能部分回答”和有限普通交流属于真实的产品处置结果，不属于基础设施 fallback。
8. 开发便利必须通过显式 test/smoke profile、依赖注入和固定 fixture 提供，不能藏在产品代码的默认分支中。

## 2. 成功、受控处置与失败的权威分类

### 2.1 可以返回 `completed` 的结果

| 类别 | HTTP/SSE 结果 | 是否调用检索/模型 | 用户引用 | 持久化要求 |
| --- | --- | --- | --- | --- |
| `grounded_answer` | `200` / `message.completed` | scoped retrieval + generation | 必须来自实际使用 citation；业务事实需要 anchor | assistant `completed`，写入 snapshot/provider/policy/citation evidence |
| `partial_answer` | `200` / `message.completed` | scoped retrieval + generation | 只展示实际支持已回答部分的 citation | assistant `completed`，必须记录 coverage 与未覆盖项 |
| `clarification_required` | `200` / `message.completed` | policy 可执行；不得提前生成业务事实 | 无 | assistant `completed`，记录最小缺失维度和 pending clarification |
| `insufficient_evidence` | `200` / `message.completed` | scoped retrieval 已发生；answer generation 可不发生 | 无用户可见引用；候选仅供管理员审计 | assistant `completed`，记录 refusal/disposition 和 retrieval evidence |
| `controlled_response` | `200` / `message.completed` | 不检索、不调用模型 | 无 | greeting/thanks/farewell/help/off-topic 等固定产品回复，execution facts 必须为 not-run |

`completed` 表示系统真实完成了声明的处置，不等同于“模型回答了业务问题”。

### 2.2 必须返回失败的结果

| 失败域 | 代表场景 | 对外结果 | 消息/引用状态 |
| --- | --- | --- | --- |
| `configuration_error` | 缺少生产必需环境变量、active config 与 Runtime profile 不匹配 | 启动失败或 `/readyz 503` | 不创建 completed assistant，不创建 citation |
| `dependency_unavailable` | PostgreSQL、Redis、Runtime、Qdrant、模型、对象存储不可达 | 请求前失败返回稳定 `503`；流开始后 `message.error` | assistant `failed`，无 citation |
| `runtime_contract_violation` | Runtime 缺少 execution facts、identity/scope/provenance 不一致、空回答、非法 rank/anchor | `502` 或流内 `message.error` | 回滚成功写路径，assistant `failed`，无 citation |
| `knowledge_snapshot_unavailable` | 没有 active snapshot、scope 不完整 | `409/503` 稳定错误 | 不调用生成，不创建 completed assistant |
| `no_indexable_content` | 空文件、扫描 PDF 无文本且 OCR 不可用、parser 产生零 chunk | ingestion task/revision `failed` | 不进入 ready，不更新 active snapshot |
| `authentication/authorization_error` | 未登录、错误角色、ownership 不匹配、内部服务未认证 | `401/403/404` | 不调用 Runtime，不泄漏资源存在性 |
| `state_writeback_failed` | 回答已生成但事务写入失败 | `500` 或流内 `message.error` | 不发送 `message.completed`；不保留部分 citation |
| `cancelled/timeout` | 用户停止、Backend/Runtime deadline | `message.error`/取消状态 | 不伪装 completed；保留可审计 attempt，不创建引用 |

### 2.3 明确禁止的行为

- 固定字符串替代模型回答。
- 固定文档、固定片段、固定 score 或固定 citation 替代真实检索结果。
- Backend 对 Runtime 缺失字段补造 retrieval/generation/knowledge-used 事实。
- 空回答替换成提示文字后保存为 `completed`。
- 把非法 citation rank 自动改为合法 rank，或自动生成看似真实的 citation label。
- 把 Runtime/Backend 请求失败转换成 Web 空列表、空知识库或“暂无数据”。
- 把扫描 PDF 的零 chunk 结果标为 ingestion succeeded。
- 在生产请求缺少 `knowledge_scope` 时退回全量或非 revision-scoped 检索。
- 在 dense/provider/collection 不兼容时退回 lexical、内存向量库或另一个模型。
- 仅凭配置中的 `model_id`、`embedding_model` 或健康端点宣称对应 provider 已实际执行。

## 3. 目标架构与权威边界

### 3.1 Backend 是产品执行状态的唯一权威写入者

每次聊天请求使用 `request_id` / idempotency key 创建一个权威 execution attempt：

```text
accepted -> running -> succeeded
                    -> failed
                    -> cancelled
```

- `accepted`：认证、ownership、conversation config 和请求格式已通过。
- `running`：已冻结 config、retrieval config、active snapshot scope，并开始 Runtime 调用。
- `succeeded`：Backend 已验证 Runtime identity、scope、execution facts、answer disposition、citation provenance 和 anchor，且消息/citation/evidence 已在同一事务提交。
- `failed`：依赖、契约、策略或写回失败；记录稳定错误码与阶段，不产生 completed assistant/citation。
- `cancelled`：用户停止或 deadline 取消；不得转成普通失败回答。

建议复用或扩展 K7 execution evidence 作为 attempt 事实源；如果现有表不能表达 started/failed/cancelled，则新增 append-only attempt/status event，而不是把错误文本塞进 assistant content。

### 3.2 Runtime 只拥有计算结果，不拥有产品成功语义

Runtime 负责：

- 校验请求 schema、策略、模型与 scope；
- 执行 policy、检索、生成和 citation marker 解析；
- 返回完整、不可猜测的 execution facts；
- 对 provider、retrieval、scope 或输出协议错误 fail-closed。

Backend 负责最终判定结果能否成为产品 `completed`，Runtime 不能直接写 Postgres 产品消息。

### 3.3 Citation 只有一个权威产生路径

1. Runtime scoped retrieval 产生候选及不可变 provenance。
2. answer policy/generation 明确返回实际使用的 citation ranks。
3. Runtime 解析并验证 marker/anchor。
4. Backend 再次验证 rank 唯一、正数、存在于本次 response，revision 属于请求 scope，content hash/manifest/identity 一致。
5. Postgres 在 assistant message、answer citations、anchors、execution evidence 的同一事务中写入。
6. Web 只渲染 Backend 返回的 answer citations；不得从 retrieval candidates、正文相似度或默认 fixture 推断来源。

任一步失败都不得落库为 completed answer。

### 3.4 产品与测试依赖必须物理分离

- 产品 app factory 不构造 heuristic/fake/fixture provider。
- 测试通过 constructor injection 或独立 test app factory 显式注入 deterministic provider、MemoryStore 和 InMemoryVectorStore。
- fixture provider 必须位于测试目录或带 test-only build/import boundary，生产入口无法引用。
- 产品 Runtime 不提供 `ALLOW_UNSCOPED=true`、`ALLOW_FAKE_MODEL=true` 等可被误带到生产的逃生开关。
- emergency degradation 只能“关闭聊天/摄取并返回明确失败”，不能恢复伪回答。

## 4. 运行 Profile 与依赖策略

建议新增显式 `SEE_AGENT_PROFILE`，不再根据某个环境变量是否为空自动猜运行形态。

| Profile | 用途 | 允许的 adapter | 缺失依赖行为 |
| --- | --- | --- | --- |
| `test` | 单元/集成 fixture | 依赖注入的 memory/local/test provider | 测试构造阶段失败；不得由生产命令选择 |
| `smoke` | 骨架/API 演示，不声明真实 RAG | 显式 memory/local；聊天知识能力默认禁用 | 返回 capability unavailable，不生成替代答案 |
| `fullstack_dev` | 与生产拓扑一致的本地开发 | Postgres、Redis、MinIO、Qdrant、真实 OpenAI-compatible LLM | 任一必需依赖缺失则启动失败或 not-ready |
| `production` | 私有部署 | 持久化依赖、服务认证、非默认 secrets、固定 Runtime profile | 配置缺失直接退出；运行中依赖失败则 fail-closed |

### 4.1 `fullstack_dev/production` 必需条件

Backend：

- `DATABASE_URL`
- `REDIS_URL`，且 session/ingestion/K3 均显式使用 Redis
- `AI_RUNTIME_BASE_URL`
- `OBJECT_STORE_ENDPOINT`、bucket 与非默认 credentials
- 三类登录凭据已覆盖；生产后续应切换到独立身份源
- K3 delivery 与 active snapshot 能力一致

Runtime：

- `QDRANT_URL`、固定 collection 和 strategy
- 真实 `LOCAL_LLM_BASE_URL`
- native dense 时要求 embedding endpoint/model/dimensions exact match
- 与 Backend 相同的对象存储 endpoint/credentials
- Backend -> Runtime 服务认证凭据
- 禁止 sample docs 自动 bootstrap

### 4.2 当前 immutable 配置迁移

当前发布 retrieval config 为 `lexical_compat_v1 / embedding_model=bge-m3`，而 Runtime 实际使用 `baseline-lexical-embedder`。不允许原地改写历史 config：

1. 先增加只读一致性审计，阻止新的错误组合。
2. 创建新的 lexical retrieval config，embedding identity 使用规范值 `baseline-lexical-embedder`。
3. 在固定 snapshot/provider 下重跑现有 gate。
4. 经 Q1 approval/publish 创建新的 active pair，只影响新会话。
5. 新 active pair 生效后开启 Runtime/Backend exact-match 强校验。
6. 历史 conversation/evidence 保留旧 config，不回填、不重写。

## 5. API、SSE 与持久化契约改造

### 5.1 统一错误 envelope

Backend/Web 同源 API 继续使用稳定 envelope：

```json
{
  "error": {
    "code": "runtime_contract_violation",
    "message": "当前无法安全完成回答，请稍后重试。",
    "retryable": true,
    "request_id": "req_...",
    "stage": "runtime_response_validation"
  }
}
```

- 用户消息必须安全、可行动，不暴露 DSN、API key、内部 host 或 prompt。
- 管理员 execution evidence 记录脱敏技术原因、dependency/provider、attempt 和阶段。
- 4xx/5xx 与业务处置不可混用：`insufficient_evidence` 是完成的知识策略结果，`runtime_unavailable` 是执行失败。

### 5.2 流式状态机

```text
request validation
  ├─ fail before headers -> JSON error, no SSE
  └─ accepted -> message.started
                  ├─ message.delta* -> message.completed
                  └─ message.error (terminal)
```

- `message.completed` 与 `message.error` 互斥且都只能出现一次。
- Runtime 在最终完整 response 通过校验前输出的 delta 只属于瞬时 UI，不是持久化事实。
- 最终验证或事务失败时发送 `message.error`，Web 丢弃未完成正文或明确标成失败草稿；不得保存为正常回答。
- 重试使用新的 attempt id，并通过 parent/retry relation 关联；不得覆盖原失败 evidence。

### 5.3 消息与引用持久化

候选方案采用“先记录 attempt，成功时原子写 answer”作为默认设计：

- 用户输入可在 accepted transaction 中持久化，便于恢复和重试。
- assistant placeholder 使用 `pending/streaming`，失败时转 `failed`，content 为空或仅由 Web 根据 error code 展示本地化提示。
- 只有成功事务可写 `assistant.status=completed`、业务正文、citations、anchors 和 knowledge-used facts。
- citation 表增加/保留约束：rank 正数、同 message 唯一、revision/snapshot/hash 非空、scope membership 可验证。
- 失败 attempt 的 citations 必须为零；数据库约束或 service invariant 应阻止部分提交。

若现有 schema 不适合可靠保存 placeholder，则第一阶段可以保持失败消息只存在于 attempt evidence，Web 根据 request error 渲染；不得为了 UI 方便写伪 assistant content。

## 6. 分阶段工作包

### F0：冻结生产失败契约与基线

范围：

- 将本计划经 commander 审阅后的决策同步到产品规格、Backend/Runtime/Web 设计、OpenAPI 和 harness。
- 建立 fallback inventory，列出每条生产/测试调用点、触发条件、替代行为和删除工作包。
- 为现存问题先写会失败的 regression tests，避免实现阶段遗漏。

必须覆盖：固定 chat answer/citation、空回答替换、execution facts 补造、无 scope 检索、sample docs bootstrap、零 chunk success、Web failure-to-empty、隐式 memory/local adapter、默认账号、单纯 health 误判。

退出标准：跨服务错误矩阵和 acceptance tests 评审通过；共享 schema 只有一个 writer。

### F1：Backend 聊天与 citation 路径 fail-closed

范围：

- 删除 MemoryStore/PostgresStore 的固定 `AppendExchange` 回答及固定 citation。
- 删除空回答替换、rank 自动修复和默认 citation label。
- 移除 `normalizeRuntimeExecutionFacts`；Runtime 缺字段直接 `runtime_contract_violation`。
- 在 service 层统一验证非空回答、disposition、facts、scope、provider、citations 和 anchors。
- 非流式、流式、onboarding、revalidation、quality run 使用同一 validator。
- Repository 只接收已验证的 success command，不再自行“修复”外部 response。
- 失败事务不写 completed answer/citation；补充 attempt 失败 evidence。

验证：Backend unit/service/handler/Postgres integration，覆盖 Runtime 未配置、超时、空 body、缺 intent、非法 rank、scope/hash 不匹配、写回失败和 SSE terminal exclusivity。

### F2：Runtime scope、provider 与检索严格化

范围：

- `knowledge_query/revalidate` 强制完整 `knowledge_scope`；删除 legacy unscoped retrieval。
- 删除产品启动时的 sample docs 自动 bootstrap；样例由测试显式 ingestion。
- 策略、embedding identity、dimensions、collection 和请求 config exact match。
- provider 缺失、模型不存在、响应为空/畸形、stream 中断均返回稳定错误。
- Runtime 内部 endpoint 增加服务认证；部署层只允许 Backend 网络访问。
- controlled response 保持不检索、不调用模型，并返回明确 not-run facts。

验证：Runtime schema/pipeline/API tests，直接无 scope 请求必须失败；跨 revision、错误 token、provider down、Qdrant down、模型缺失都不得产生 completed response/citation。

### F3：摄取真实性与 OCR 边界

范围：

- parser/chunker 输出零个有效 chunk 时返回 `no_indexable_content`。
- 将当前 `pdf_text_plus_ocr_fallback` 改为真实能力名称；OCR 未实现前不得声称 fallback 可用。
- `enable_ocr_fallback=true` 而 OCR provider 不可用时返回 `ocr_unavailable`，不能忽略配置。
- 正式 ingestion 成功要求：parse 成功、有效 chunk > 0、embedding 成功、向量写入成功、manifest 可验证。
- K3 仅在完整成功后推进 revision ready；失败/重试/重复投递不影响 active snapshot。
- 对空 TXT/MD、空 JSON、仅图片 PDF、空 sheet/slide、损坏文件建立 fixture。

验证：七格式单测、Runtime ingestion API、Redis/K3、Postgres revision state、Qdrant projection 和真实 MinIO smoke。

### F4：显式运行 Profile 与启动门禁

范围：

- 新增 `SEE_AGENT_PROFILE` 及 Backend/Runtime config validator。
- `fullstack_dev/production` 不再自动回退 MemoryStore、LocalQueue、MemorySession、LocalStore、InMemoryVectorStore 或默认 Runtime URL。
- `make backend-dev/runtime-dev` 改为明确的 smoke 命令或移除；`make dev` 执行依赖 preflight 后启动完整拓扑。
- test adapters 只能由测试入口显式注入。
- production 禁止默认账号/对象存储密钥；缺 secrets 直接启动失败。
- 对环境变量使用 unknown/冲突检测，避免拼错变量后静默使用默认值。

验证：配置矩阵测试和 clean-environment smoke；每个缺失/冲突配置均产生可预测退出码和脱敏日志。

### F5：Liveness、Readiness 与深度诊断

范围：

- `/healthz` 保持进程 liveness。
- 新增 `/readyz`：Backend 检查 Postgres、Redis、Runtime capability；Runtime 检查 Qdrant、实际 retrieval profile、generation provider、对象存储配置完整性。
- 增加受保护的 capability/diagnostic endpoint 或 CLI，报告实际 adapter、model/provider、collection、strategy、embedding identity 和 active config pair 的一致性，不输出 secrets。
- 提供完整 `make fullstack-preflight` 与 `make fullstack-smoke`；前者只读检查，后者使用隔离测试账号/数据执行真实模型、检索、引用、上传、索引和失败路径。
- readiness 依赖检查有短 timeout、缓存/抖动策略，避免健康检查本身压垮依赖。

验证：依次停止模型、Qdrant、Redis、Postgres、MinIO，确认 liveness/readiness/产品请求表现符合矩阵，且恢复后自动 ready。

### F6：Web 真实错误体验

范围：

- 删除 execution/delivery/eval 请求失败转空数组的逻辑；区分 loading、empty、unavailable、forbidden。
- chat 对 JSON error 和 SSE `message.error` 展示稳定失败卡片、重试/重新登录动作，不显示来源入口。
- 未认证统一跳转 `/login?next=...`；403、404、503 不混同。
- admin 页面明确显示当前 adapter/profile mismatch 和 not-ready dependency。
- Web 不根据 config、URL、文本或候选数推断模型、知识使用或引用。

验证：组件测试、Playwright happy/error paths、浏览器网络中断、Runtime down、session expiry、主题对比度和键盘操作。

### F7：现有配置、数据与文档迁移

范围：

- 创建规范 lexical retrieval config 并通过 Q1 forward release 修正 `bge-m3` 标签错配。
- 历史 fallback 消息、citation 和 eval report 不改写；在管理员 evidence 中标记 `legacy_fallback/invalid_for_quality`，不得进入新审批依据。
- 清理根 README、旧设计、runbook 和 harness 中“默认 fallback 可用于 integration/quality”的过期描述。
- integration/e2e 继续使用显式 test-only provider，但报告必须明确 `test_provider=true`，不可登记为质量报告。
- 更新部署与事故 runbook：依赖故障时关闭能力或返回 503，绝不重新启用 fake answer。

验证：历史读取兼容、new writes strict、Q1 forward release、文档链接和命令 smoke。

### F8：生产切换与观察期

范围：

1. 先部署 schema/validator 的 report-only 观测版本，但不得继续生成已知 fake answer。
2. 修正 active retrieval config 和部署 secrets/object store/network。
3. 在 staging/隔离全栈开启 strict profile，跑完整 gate。
4. production 先开启 Backend fail-closed，再开启 Runtime scope/provider strictness，避免旧 Backend 误解新错误。
5. 观察 failed attempts、contract violations、readiness flaps、zero-chunk rate 和 retry success。
6. 达标后删除 report-only 兼容代码与旧 fallback tests。

回滚原则：使用 forward fix 或关闭对应能力；不得以恢复伪回答、全量检索或默认引用作为回滚手段。

## 7. 执行依赖与推荐顺序

```text
F0 contract/tests
  ├─ F1 Backend fail-closed ─┐
  ├─ F2 Runtime strictness ──┼─ F5 readiness ─ F6 Web ─ F7 migration ─ F8 cutover
  └─ F3 ingestion truth ─────┘
                 F4 profiles ─┘
```

- `F0` 是所有实现的前置门槛。
- `F1` 先于 `F2` 上线，确保 Backend 能正确处理新的 Runtime 错误。
- `F3` 可在 F1/F2 contract 冻结后独立实现，但与 K3/Postgres 状态测试在集成点合并。
- `F4` 与 F1/F2 可并行开发，但 strict profile 最后统一开启。
- `F5/F6` 依赖稳定错误码与 execution attempt 状态。
- `F7` 的 active config forward release 必须在 strict exact-match 开启前完成。
- `A1` 的澄清/coverage 实现可继续准备数据集，但 A1-2/A1-3 的产品实现应复用本计划的 attempt、facts、citation 和错误契约。

## 8. 验收场景矩阵

上线前至少证明以下场景：

| 场景 | 期望 |
| --- | --- |
| 正常 scoped 知识问题 | 真实模型回答；引用属于 active scope；provider/strategy 可审计 |
| 问候/感谢 | 受控短回复；不检索、不调用模型、无引用 |
| 天气/美国总统 | 受控拒答；不检索、不调用模型、无引用 |
| 范围模糊内部问题 | `clarification_required`；无业务事实和引用 |
| 范围明确但无证据 | `insufficient_evidence`；不生成猜测性答案 |
| 只有部分证据 | `partial_answer`；明确覆盖缺口，只展示已用引用 |
| Runtime endpoint 未配置 | fullstack/production 启动失败；smoke chat 明确 unavailable |
| 模型宕机/不存在 | 503 或 `message.error`；无 completed answer/citation |
| Runtime 返回空 answer | contract violation；无替代文本、无 completed answer |
| Runtime 缺 execution facts | contract violation；Backend 不补造 |
| citation rank/hash/scope 非法 | 整个 answer 失败并回滚，不自动修复 |
| 直接调用 Runtime 且无 scope | 拒绝请求，不进行全量检索 |
| Qdrant/embedding profile 不匹配 | not-ready / retrieval failure，不回退 lexical/memory |
| 扫描 PDF 且 OCR 不可用 | ingestion failed `ocr_unavailable/no_indexable_content` |
| parser 产生零 chunk | task/revision failed，不进入 ready/publish |
| MinIO 宕机 | 上传或摄取失败；不转本地临时目录 |
| Redis/Postgres 宕机 | not-ready；相关请求失败，不转 memory/local queue |
| Web evidence API 失败 | 显示 unavailable/error，不显示空列表 |
| session 失效 | 跳转登录；不继续调用受保护业务 API |
| SSE 中途断开 | terminal error/cancelled；不发送 completed，不留部分引用 |
| 重复/重试请求 | attempt 可审计、幂等，不产生双消息/双 citation |

## 9. 分层验证门槛

### 9.1 静态与契约

- OpenAPI、JSON Schema、Go/Python/TypeScript 类型一致。
- production package/import graph 不包含 test provider、sample bootstrap 或 fake repository answer。
- 搜索禁止字符串与代码模式：固定 fallback answer、固定 citation、`normalizeRuntimeExecutionFacts`、空 answer 替换和 failure-to-empty catch。

### 9.2 自动化

- `make backend-test`
- `make runtime-test`
- `make typecheck`
- `make web-lint`
- `make integration`
- Postgres/K3/Qdrant/MinIO/Redis 真实 adapter smoke
- 新增 production profile configuration matrix
- 新增 dependency failure suite
- 新增 SSE terminal state/property tests

### 9.3 质量与产品回归

- prompt_v2 gate 和知识演进 fixture 不退化。
- A1 clarification slice 保持独立评分；基础设施失败不得被评分为拒答或澄清成功。
- 所有质量报告必须记录实际 provider、strategy、scope 和 `fallback_used=false`；test provider 报告不可审批。
- 浏览器覆盖正常回答、澄清、知识不足、部分回答、模型失败、session 失效和 admin API failure。

### 9.4 发布阻断条件

任一项成立则不得发布：

- 产品路径仍能生成固定/heuristic/fake assistant answer。
- 任一失败路径会写 completed answer 或 citation。
- 无 scope Runtime 请求能够读取生产知识索引。
- 零 chunk revision 能进入 ready/published。
- fullstack/production 会自动选择 memory/local/test adapter。
- readiness 与实际 adapter/profile 不一致。
- 当前 active config 与 Runtime profile 不匹配且尚未完成 forward release。
- 默认账号/对象存储凭据仍可在 production profile 启动。

## 10. 风险、取舍与不在本计划内的事项

### 10.1 预期取舍

- 删除兜底后，开发早期会看到更多 503/failed 状态；这是暴露真实依赖问题，不是功能退化。
- strict scope 与 provider 校验会使旧测试和直接 Runtime 调用失败；测试应改用显式 fixture/scope，而不是恢复兼容分支。
- 失败 attempt 持久化会增加 schema 与 UI 状态复杂度，但能支持重试、审计和事故定位。
- readiness 深度检查需要避免高频真实模型生成；provider capability/ping 与单独 full smoke 应分层。

### 10.2 不在本计划内

- 不直接实现 OCR；只修正能力声明和失败语义。
- 不直接升级生成模型；模型比较继续走 A1/Q1。
- 不在本计划中完成企业 IdP/SSO；这里只要求 production 禁用默认凭据并保留后续身份源接口。
- 不执行 dense cutover；只要求当前 strategy/embedding/collection 事实一致。
- 不回写或删除历史回答、citation、eval report。

## 11. 已批准的决策基线

以下六项作为后续实现约束：

1. 使用 `test / smoke / fullstack_dev / production` 四档显式 profile；smoke 不声明真实知识回答能力。
2. 失败聊天保留独立 execution attempt；默认方案是 assistant `failed` 占位无正文、无引用，具体表结构由 F0 冻结。
3. Runtime 产品接口强制 scope + Backend 服务认证，删除 legacy unscoped 调用。
4. OCR 未实现时，扫描 PDF 明确失败，不允许“索引成功但无可检索内容”。
5. `lexical_compat_v1 / bge-m3` 错配通过新 immutable retrieval config 和 Q1 forward release 修正；不改写历史配置或会话。
6. 生产依赖故障只能关闭能力或返回失败，不允许恢复 fake answer、fake citation 或全量检索。

F0 已冻结四个实现级细节，见 [Fail-closed execution 设计](../../design-docs/single-agent-v1-fail-closed-execution-design.md)：新增 append-only `chat_execution_attempts`、稳定 HTTP/SSE error map、私有网络 bearer service token（支持有限 previous-token rotation）以及 `legacy_fallback`/`invalid_for_quality` 只读质量标记。它们不改变上述六条产品决策。

## 12. 当前状态与批准后的唯一下一动作

- 当前状态：`F1-F8 local fullstack hardening verified`；F0 共享契约与 F1 Backend fail-closed 已落地，local active release 已通过 Q1 前向切换。
- 已完成：Backend 固定回答/citation、空回答替换、execution fact 补造和本地 completed-SSE 均已删除；`018` migration 新增 forward-only execution attempt，成功的 Postgres chat 写入与 attempt succeeded 同事务；失败不产生 completed assistant/citation。Backend 全量测试通过。
- F7 已完成的代码迁移：`020` 只标记缺 strict observation 的历史 evidence 与历史 eval report 为 `legacy_fallback/invalid_for_quality`；新含 `test_provider/test_injected` 标记的报告同样禁止进入 Q1；`021` 创建规范 `rcfg_lexical_v2 / lexical_compat_v1 / baseline-lexical-embedder`，不修改 `rcfg_001`、历史会话或 active release。
- 已完成 Q1：`qexp_003` 固定 `snap_001`、manifest、数据集 SHA、`cfg_004/rcfg_lexical_v2` 的完整 generation/retrieval 条件；`runtime-remote` baseline/candidate 均为 `4/4`，逐样本记录真实 `openai_compatible` generation、scoped Qdrant retrieval、无 fallback 的 execution observation。审批后产生 immutable `qrel_001`，仅新会话使用 `rcfg_lexical_v2`；历史 `rcfg_001` 会话不改写。
- 审计保留：`qexp_001` 的错误 dataset hash draft 和 `qexp_002` 的不完整 evaluator 条件/失败报告均保留，未批准、未发布；它们证明不能把 placeholder hash、代理 502 或缺少 `response_style/top_p` 的 run 当作质量证据。
- 已验证：strict fullstack profile 下，历史 `rcfg_001 / bge-m3` 请求返回 `runtime_contract_violation`；新会话 `conv_049` 使用 `qrel_001` 成功生成 grounded answer、citation anchor、provenance 和实际 Runtime observation。Runtime 停止时 Backend 返回 `runtime_unavailable`，不写伪回答；重启后 readiness 与 grounded chat 均恢复。
- 唯一下一动作：如需真正 production promotion，先按 harness 的 `80+` 冻结发布集与 staging 观察窗口完成独立授权；不得把本地 `snap_001` 四题 config-repair gate 宣称为生产规模质量结论。

## 13. F0 契约冻结的详细执行计划

### F0-0：锁定 owner、写入范围与顺序

- Owner：`p` 是 F0 的唯一 contract writer。
- 写入范围：`docs/product-specs/**`、`docs/design-docs/**`、`docs/harness/**`、`packages/contracts/**`、本执行计划；不修改产品服务代码、migration、active release 或部署变量。
- 并行规则：F0 未完成前，任何 worker 不得编辑 `packages/contracts/`、Postgres migration、`domain.ChatRuntimeResponse`、chat service 或 Runtime API schema。
- 交付：更新 `single-agent-v1-parallel-claims.md` 后，才能派发 F1/F2 等实现包。

### F0-1：冻结面向用户的结果与错误语义

产出：产品规格、Web UI 设计和 OpenAPI 一致定义以下结果：

| 类别 | 用户可见状态 | 是否写 completed assistant | 是否有用户 citation |
| --- | --- | ---:| ---:|
| `grounded_answer` / `partial_answer` | 回答卡片 | 是 | 是，仅实际使用来源 |
| `clarification_required` | 澄清卡片 | 是 | 否 |
| `insufficient_evidence` | 知识不足卡片 | 是 | 否 |
| `controlled_response` | 普通交流或范围提示 | 是 | 否 |
| execution failure | 失败/重试卡片 | 否；或仅 `failed` 占位 | 否 |

冻结稳定错误码、可重试性、错误阶段和 JSON/SSE terminal 行为。错误文案保持用户安全；依赖主机、凭据和内部 prompt 只进入脱敏管理员 evidence。

### F0-2：冻结 Backend ↔ Runtime 真实性契约

产出：更新 `chat-runtime-request/response` schema、OpenAPI、Go domain 与 Python Pydantic schema 的设计说明；实现阶段仅由相应 owner 落地。

必须明确：

- `knowledge_query`、`revalidate` 的 scope required rule；controlled intent 的 scope/provenance 必须为空。
- Runtime response 的 identity、answer disposition、non-empty content、execution facts、generation provider、strategy/embedding identity、citation/anchor invariants。
- citation rank 必须正数、唯一且来自本次 response；不得由 Backend/repository 自动补齐。
- provider 未执行、检索未执行、generation 未执行使用明确 observation，而不是缺字段或猜测字段。
- Runtime service auth header/credential lifecycle；服务认证失败在 Runtime 边界拒绝，不能进行检索。
- JSON response 与 SSE final response 使用同一 validator；`message.completed` 与 `message.error` 互斥。

### F0-3：冻结 authoritative write path 与数据迁移设计

产出：Backend 设计补充 state machine、Postgres migration design、K7 evidence read model 设计和 rollback/forward-fix 说明。

需决定并记录：

1. 建议新增 append-only `chat_execution_attempts` / events；或扩展既有 execution evidence，但必须能表达 `accepted/running/succeeded/failed/cancelled`、retry parent、error code/stage 和时间线。
2. assistant `completed`、citations、anchors、snapshot provenance 和 success evidence 在一条事务内提交；失败只能留下 attempt/failed placeholder，不能留下部分 citation。
3. request idempotency 的约束键、重复提交和断流重试行为。
4. 历史 fallback message/evidence 只读标记为 `legacy_fallback` 或 `invalid_for_quality`；不回填、不删除、不自动修正。
5. migration 是 forward-only；紧急回滚只允许应用回退或关闭能力，不能恢复旧伪成功写路径。

### F0-4：冻结摄取、profile、readiness 的边界

产出：Runtime/Backend 设计、部署 runbook、Makefile command contract 和测试矩阵。

- `no_indexable_content`、`ocr_unavailable`、embedding/index projection failure 的 task/revision state transition。
- `SEE_AGENT_PROFILE` 的允许值、各 profile 可使用 adapter、必需变量、禁止默认值与启动失败规则。
- `/healthz` 只表示 liveness；`/readyz` 的依赖、短超时、缓存策略和不泄漏细节的 response contract。
- `fullstack-preflight` 是只读依赖检查；`fullstack-smoke` 只使用隔离账号/fixture，明确记录真实 provider 或 test provider。
- production profile 的 secrets、对象存储和 Runtime network access 最小门槛。

### F0-5：建立先失败的回归基线和验收映射

产出：测试清单与 fixture 设计，不在本阶段改变行为。

- Backend：无 Runtime、空 Runtime response、缺 facts、非法 rank/anchor、scope mismatch、writeback failure、SSE 中途失败、重复 retry。
- Runtime：无 scope、无/错误 service token、provider/Qdrant down、profile mismatch、sample docs 不得进入产品 app。
- Ingestion：空文本、扫描 PDF、空 JSON/Sheet/Slide、损坏文件、向量写入失败、K3 不得推进 ready。
- Web：503 不得显示空列表，SSE error 不得显示来源，session expiry 必须回登录。
- 环境：缺 Postgres/Redis/MinIO/Qdrant/model/secrets 时 fullstack/production 不能 ready；test/smoke 只能显式启用 fixture adapter。

退出标准：每条现存伪成功均有一个可复现、当前失败的测试或 fixture；F1-F8 的实现 owner 可只按冻结 schema 和错误矩阵工作，不再解释产品语义。

## 14. F0 后的实现波次与交接

| 波次 | 工作包 | owner 写入范围 | 依赖 | 集成门 |
| --- | --- | --- | --- | --- |
| 0 | F0 contract freeze | `p`：docs/contracts/plan | commander 的原则性批准 | schema/OpenAPI/docs review 完成 |
| 1 | F1 Backend fail-closed | Backend/domain/repository/service/httpapi/migration/tests | F0 | Backend unit + isolated Postgres + API/SSE negatives |
| 2 | F2 Runtime strictness | Runtime app/schema/pipeline/tests | F1 | Runtime API/pipeline negative suite |
| 3 | F3 ingestion truth | Runtime ingestion/parser + Backend K3 integration | F2；Runtime 单 writer | seven-format + K3/Qdrant/MinIO failure suite |
| 4A | F4 profile/startup | Backend/Runtime bootstrap, Makefile, compose/runbook | F1-F3 | config matrix + clean environment smoke |
| 4B | F6 Web error truth | Web/API proxy/component/E2E | F1 stable error codes | browser happy/error/session paths |
| 5 | F5 readiness/diagnostics | Backend/Runtime readiness, CLI/runbook | F2、F4 | dependency stop/recover suite |
| 6 | F7 migration/release | Q1 config, historical evidence marking, docs | F1-F6 | forward release + history compatibility |
| 7 | F8 strict rollout | deployment/staging/observability | F5、F7 | fullstack gate + observation exit criteria |

并行限制：F1 必须完成后才能开始 F2，确保 Backend 已能把 Runtime 的严格拒绝和契约错误记录为真实失败；F3 必须等待 F2，以避免两个 Runtime writer 冲突。F6 只消费已冻结的 Backend error contract，可在 F2/F3 期间由独立 Web owner 实现；F4 与 F5 需由同一 deployment/config owner 串行；F7 是唯一能触及 active config forward release 的工作包。

每个进入实现的工作包都必须先在 `single-agent-v1-parallel-claims.md` 认领，写明 owner、精确文件范围、最后验证、阻塞项和唯一下一动作。`p` 始终保留 schema、migration 设计、integration 和最终验收权。

## 14.1 实施检查点（2026-07-24）

- 已完成并回归验证：F1 的 Backend fail-closed chat/attempt 写路径；F3 的零 chunk 与无 OCR 扫描 PDF 失败；F4 的显式 `fullstack_dev` 启动门禁、禁用旧 `*-rag-dev` 默认命令与只读 preflight；F6 的 Web SSE 缺终态失败和 evidence unavailable 状态。
- F2 已完成：Runtime response 记录 `answer_disposition`、policy/retrieval/generation observation、provider/model、collection/strategy/embedding、实际 candidate count 与 used citation ranks；未产生有效 anchor 的模型正文直接失败，候选来源不会作为用户引用。Backend 在 `fullstack_dev/production` 拒绝旧布尔事实或不一致 observation，并在 success evidence transaction 原样记录观察数据；`019` 为 forward-only evidence 扩展。
- F5 已完成代码与自动化门：严格 profile 的 `/readyz` 对 Qdrant、模型 provider 与对象存储执行短 timeout 探测并缓存 2 秒；Backend 的 PostgreSQL/Redis/Runtime 探测保持不变；受 bearer service token 保护的 `/v1/diagnostics/capabilities` 返回实际 provider、strategy、embedding 与 collection；`/healthz` 仍只是 liveness。真实 stop/recover fullstack gate 属于 F8 staging 验收。
- F7 完成：`020/021` 质量资格与 canonical lexical config 已迁移；真实 remote Q1 `qexp_003` 在 exact immutable conditions 下通过 baseline/candidate 并发布 `qrel_001`。历史/失败报告仍是只读审计，不能越过 approval。
- F8 本地全栈完成：`fullstack-preflight`、`fullstack-smoke` 通过；Runtime stop → Backend `runtime_unavailable` → Runtime restart → readiness/chat recovery 已实测。`conv_049` 的 strict success evidence 为 `legacy_fallback=false`、`invalid_for_quality=false`、provider `openai_compatible`、strategy `lexical_compat_v1`。
- 已验证：`make backend-test`、`make runtime-test`（106 passed）、`make fullstack-smoke`、`make typecheck`、`make web-lint`、`make web-build`，以及 eval runner integration `29 passed`。Web production build 需要允许 Next/Turbopack 创建本机 worker/临时端口；编译和静态生成均通过。
- 生产边界：此计划的本地 fullstack验收已经完成；生产环境的长观察窗口、80+ 发布集与真实部署变更不在当前本机授权范围内，必须作为下一次受控 rollout 单独记录。

## 15. F0 完成后的首个实现包

首个实现包固定为 F1，而不是立即切换 profile 或 Runtime strictness，理由是 Backend 必须先能正确接受 Runtime 的失败和契约错误：

1. 移除固定 chat answer/citation 与本地 fallback SSE completed path。
2. 令空回答、缺 facts、非法 citation/anchor 进入统一 `runtime_contract_violation`。
3. 落地 attempt/failed 状态的最小 authoritative write path。
4. 证明失败时数据库没有 completed assistant、citation 或错误的 execution facts。
5. 通过后才允许 F2 让 Runtime 严格拒绝无 scope/无服务认证请求。

这保证 Runtime 先变严格时，旧 Backend 不会把新的失败响应错误解释成成功或非结构化 500。
