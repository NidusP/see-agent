# 单智能体 V1 K8-R4a 产品内知识上线验收设计

## 文档控制

- 状态：`active / implementation_verified`
- 更新日期：`2026-07-18`
- 工作包：`K8-R4a`
- 上位设计：[知识演进设计](single-agent-v1-knowledge-evolution-design.md)、[K5 snapshot provenance](single-agent-v1-k5-snapshot-provenance-design.md)、[Q1 质量运营](single-agent-v1-quality-operations-release-design.md)、[K8-R3 校准](single-agent-v1-k8-dense-refusal-calibration-design.md)

## 1. 目标与非目标

管理员在产品内上传真实知识后，必须能创建、审核和执行一套针对该知识的 onboarding 问题集；系统以固定 snapshot 和不可变 config pair 逐题运行 Runtime，给出可审计的“可上线 / 不可上线”结果。它解决“上传成功不等于回答正确”的缺口。

本包覆盖：问题集、问题、冻结、异步运行、逐题结果、gate verdict、管理台与测试知识退役的证据查询。

非目标：自动把 LLM 生成内容当作真值、自动调参、自动删除 document、修改历史回答、取代 Q1 的 config release，或从浏览器执行任意模型/任意 shell 命令。

## 2. 正确性的可验证定义

系统不能只从上传文件本身推导“事实正确”。业务管理员必须审核每道题的真值：

| 题型 | 管理员提供的真值 | 通过条件 |
| --- | --- | --- |
| `must_answer` | question、至少一个 expected document/revision、至少一个 locator hint、至少一个 answer point | Runtime 不拒答；至少一个 citation 命中预期 revision/locator；答案命中**全部**审核过的 answer points。 |
| `must_refuse` | question；明确没有 expected source | Runtime `refusal=true` 且没有 citation。 |

基于文档自动提问、从历史反馈预填或让模型建议答案可以后续增加，但输出只能标作 `draft_generated`；只有管理员改为 `reviewed` 的 case 可进入冻结问题集。题面、真值和 reviewer identity 都是可审计数据，不可由 Runtime 写入。

## 3. 权威边界、对象与状态

| 对象 | 权威与唯一 writer | 关键字段 | 状态 / 不变量 |
| --- | --- | --- | --- |
| `OnboardingDataset` | Postgres / Backend | dataset id、name、scope snapshot/manifest、case hash、reviewer | `draft -> ready -> superseded`；只允许 draft 编辑。 |
| `OnboardingCase` | Postgres / Backend | type、question、expected truth、review state | 仅 `reviewed` case 可 freeze；must-refuse 不得有 expected source。 |
| `OnboardingRun` | Postgres / Backend worker | frozen snapshot/manifest、agent/retrieval config pair、dataset hash、gate、run report | `queued -> running -> passed|failed`；同一 frozen identity/idempotency key 幂等。 |
| `OnboardingCaseResult` | Postgres / Backend worker | response/refusal/citations、grader facts、error | append-only，永不改写 Runtime 原 response。 |

Runtime 只接收 Backend 构造的 snapshot-scoped request 并返回 observation；它不读取或写入 dataset/run。Web 只调用 admin API、轮询状态和展示证据。任何 Runtime/worker failure 把 run 标为 `failed`，不把“未运行”误算作拒答或质量分数。

## 4. API 与写入路径

所有端点 admin-only；每个 mutation 要求 `Idempotency-Key`。具体 OpenAPI 在实现前冻结。

1. `POST /api/admin/onboarding/datasets` 创建 draft，必须指定已发布 snapshot；Backend 读取并保存其 manifest，不能接受客户端伪造 hash。
2. `POST /api/admin/onboarding/datasets/{id}/cases` 创建 draft case；Backend 校验题型真值、document/revision 属于目标 snapshot、locator 形状和 reviewer。冻结后不得改写；要修订真值则创建 successor dataset。
3. `POST /api/admin/onboarding/datasets/{id}/freeze` 仅当存在 reviewed must-answer 和 reviewed must-refuse 时计算 canonical case hash，转 `ready`。
4. `POST /api/admin/onboarding/datasets/{id}/runs` 仅对 ready dataset 创建 queued run；Backend 在同一 read transaction 冻结 active agent/retrieval config pair、dataset hash、snapshot/manifest 和 gate。content-only knowledge update 仍使用现有 active release；若选择不同 candidate config，调用方必须先走 Q1。
5. Backend worker 顺序执行受限 Runtime request，验证 response identity/scope/citation，并写 result 与 aggregated gate。无论通过或失败都不发布 config、也不自动 disable/delete document。
6. `GET /api/admin/onboarding/datasets`、`/{id}`、`/runs/{run_id}` 返回 dataset、case、case result、frozen identity 和 verdict，供 `/knowledge` 与 `/config` 读取。

`OnboardingRun.passed` 是测试 document 退役的可审计**前置证据**，但 K4 delete 仍只允许 disabled document；管理员仍需显式执行 disable/delete。这样普通 document 的日常删除不会被不相关的 onboarding job 隐式阻塞。

## 5. Gate、并发和失败

- 最小 gate：至少一条 reviewed must-answer、至少三条 reviewed must-refuse；所有 case 都通过；没有 runtime error；每条 must-answer 的 citation 属于目标 snapshot。若本次引入新的 source format，管理员应为每个新增格式补至少一条 must-answer；这是上线评审要求，未来可在 dataset 中显式记录“新增格式”后升级为硬约束。
- 一个 dataset freeze 后不可编辑；修订题目创建 successor dataset，不覆盖旧 run。
- run 以 lease/idempotency/retry 恢复，case result 以 `(run_id, case_id)` 唯一。并发开始相同 frozen identity 返回同一 run；不同 idempotency 请求创建独立可审计 run。
- snapshot 被 retire 后旧 run 仍可读；新 run 只接受当前 `published` snapshot，避免用已退役 scope 批准新知识。
- 无 citation、scope mismatch、非法 locator、model/provider error、case truth 不完整均为 fail-closed run/case failure。

## 6. Web 体验

在 `/knowledge` 增加“知识上线验收”工作区，而不是让管理员下载 JSONL 或运行命令：

1. 选择 successor snapshot，创建问题集；说明“上传成功不等于回答正确”。
2. 表单区分 must-answer 与 must-refuse，并提供 reviewed 状态、预期 revision/locator、答案要点和简明示例。
3. freeze 后显示不可变 case hash、snapshot manifest 与 config pair；启动 run 后显示队列/运行/失败状态。
4. 每题结果显示回答、refusal、citation、预期/实际 locator、答案要点与失败原因；总 gate 以通过/失败明确呈现。
5. 只有 passing run 才显示“可开始逐个退役测试知识”的提示；按钮仍跳转既有 K4 disable/delete 明确操作和确认框。

## 7. 验收与回滚

- Backend：admin/role/idempotency、truth validation、snapshot membership、freeze immutability、run retry、runtime failure、scope mismatch、must-answer/must-refuse grader、case result evidence。
- Web：draft/review/freeze/run/poll/result、错误/空态、无 passing run 时的退役提示、admin guard。
- Integration：真实 successor snapshot 的 scoped request 和 citation；existing K4/K5/Q1/lexical gate 不回归。
- 回滚：取消/失败 run 不影响 active snapshot 或 config；如果新知识已 publish 但 onboarding 不通过，发布包含旧 revision 的 successor snapshot，之后再修复并重跑。历史 conversation 不迁移。

### 7.1 实现验证（2026-07-18）

- migration `014`–`016` 创建 dataset/case/run/result、幂等记录和 counter；Postgres 是唯一 writer。
- Backend admin API、lease worker、固定 config/retrieval/snapshot scope、response/citation identity 校验和逐题 grader 已实现；worker 只能写本次 run evidence，不能发布配置、修改知识或删除 document。
- `/knowledge` 已提供中文验收工作区，允许创建、审核、冻结、排队与读取逐题结果。
- 已通过 Go service/repository/httpapi/server、isolated Postgres lifecycle 和 scoped Runtime worker smoke，以及 Web lint/typecheck/build。真实业务内容验收仍待管理员上传后执行。

## 8. 拒绝的方案

- 上传后让模型自己生成题目、答案并自动通过：没有独立真值，不能证明正确性。
- 浏览器直接调用 Runtime 或写 Qdrant：绕过 Backend/Postgres 权威、scope 与审计。
- 将 onboarding run 复用为 Q1 config publish：内容验收与参数发布是不同状态机；只有 config change 才需要 Q1。
