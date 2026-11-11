# 单智能体 V1 K9-R2 旧知识退役与检索策略发布设计

## 文档控制

- 状态：`verified`
- 更新日期：`2026-07-18`
- 工作包：`K9-R2`
- 上位设计：[角色分离](single-agent-v1-role-separation-design.md)、[K4 lifecycle](single-agent-v1-k4-lifecycle-api-design.md)、[K8-R3 校准](single-agent-v1-k8-dense-refusal-calibration-design.md)、[Q1 质量运营](single-agent-v1-quality-operations-release-design.md)

## 1. 目标与非目标

系统管理员必须能在产品内查看和退役旧知识（包括当前产品测试 document），但 Backend 必须证明存在可服务的新知识和对应的通过验收，才允许停用或删除。系统管理员也必须能在产品中选择 lexical 或 native dense 的候选检索配置，但检索切换仍须通过真实业务 scope 的 Q1 发布，不能因为界面有一个下拉框而直接改变 active release。

非目标：自动识别文件是否“测试数据”、自动删除、自动生成真值、把 fixture 阈值复制到业务 dense collection、允许知识库管理员发布策略或绕过 Q1。

## 2. 权威对象与退役状态

Postgres 是 readiness 的唯一判定者。Web 只展示 Backend 计算的状态，不能按文件名、按钮状态或 cookie 自行判断。

| 目标 document 状态 | readiness | 必须同时成立的事实 | 允许系统管理员操作 |
| --- | --- | --- | --- |
| `active` | `ready_to_disable` | active snapshot 包含目标 document、还包含至少一个其他 document；存在对**当前 active snapshot/manifest**的 passing onboarding run | disable |
| `disabled` | `ready_to_delete` | 当前 active snapshot 非空且不包含目标 document；存在对该 new-only/current snapshot 的 passing onboarding run | delete |
| 任意其他情形 | `blocked` | 缺 replacement snapshot、缺通过 run、scope/manifest 已变或目标状态非法 | 无 |

此规则故意要求两次验收：先在“旧+新” successor snapshot 证明并存安全，再 disable 旧 document；随后在不包含旧 document 的 successor snapshot 重跑，才可 delete。历史 message/citation/provenance 不在此规则内，不被改写。

## 3. API、角色与失败语义

- 新增系统管理员只读 API：`GET /api/kb/documents/{document_id}/retirement-readiness`，返回 document id、stage、active snapshot/manifest、matching passing run id、human-readable blockers。
- `POST /api/kb/documents/{id}/disable` 和 `DELETE /api/kb/documents/{id}` 改为系统管理员 API。service/repository 在与 lifecycle transition 同一 transaction 中重新判定 readiness；UI 读取结果只是提示，不能替代这一检查。
- readiness 不通过返回 `409 retirement_not_ready`，不改变 document、snapshot、update event 或 outbox；idempotent replay 仍返回原 mutation。
- `knowledge_admin` 仍可上传、等待 ready、创建 onboarding case/run；`system_admin` 确认 publish、读取 readiness、disable/delete。

## 4. lexical 与语义检索发布

| 操作 | 系统管理员产品操作 | 是否改变 active release |
| --- | --- | --- |
| 保持 lexical、仅加业务知识 | publish revision → onboarding | 否，沿用当前 lexical release |
| 选择 `native_dense_v1`、embedding/index/rerank/boundary 变更 | `/config` 创建 immutable candidate，固定真实业务 snapshot/dataset 后创建 Q1 experiment | 否 |
| Q1 gate、report identity、审批均通过 | publish Q1 release | 是，仅新 conversation |

`lexical_compat_v1` 是当前发布路径；`native_dense_v1` 是已验证但未对业务发布的候选。语义检索不是给旧配置打一个开关：必须有匹配 embedding collection、index、真实 snapshot、onboarding 与 Q1 对照证据。任何失败保持 lexical active release，并可由系统管理员回滚。

## 5. Web 流程

1. 知识库管理员上传业务 document，等待 revision `ready`。
2. 系统管理员确认 publish；新 snapshot 先保留旧+新成员。
3. 知识库管理员在 `/knowledge` 创建和运行 onboarding；系统显示 passing/failing evidence。
4. 系统管理员打开旧 document 详情，读取 `retirement-readiness`：未达到时只显示 blocker；达到时才启用 disable。
5. disable 后系统管理员重跑 new-only onboarding；readiness 变为 `ready_to_delete` 后才启用 delete。
6. 若需要切 dense，系统管理员从 `/config` 创建 candidate 并走 Q1；不得在知识页面绕过。

## 6. 验收

- user/knowledge_admin 对 readiness、disable/delete 返回 `403`；system_admin 可读取。
- 无其他 active member、无 matching passing run、snapshot manifest 已变化、旧 document 仍在 current snapshot、目标状态不匹配均返回 `409`。
- 同一 transaction 下 readiness 通过才写 disable/delete；竞争中 active snapshot 改变时 fail closed。
- system_admin 可创建 lexical/dense candidate；dense candidate 没有 Q1 publish 不影响新 conversation；legacy admin 兼容为 system_admin。

## 7. 实现与验证记录

- 已实现系统管理员 `GET /api/kb/documents/{document_id}/retirement-readiness`、以及系统管理员专属的 disable/delete route；知识库管理员继续负责上传、索引和 onboarding。
- Lifecycle service 只调用带 readiness 的 Postgres transition；同一 transaction 重新锁定 active snapshot、校验 manifest 对应的 passing run，再写 document/snapshot/event/outbox。
- 已验证 isolated Postgres 生命周期：old+new passing run 后可 disable；new-only passing run 前 delete 被阻断，之后才可 delete。Web 在系统管理员详情页展示 blocker 并只在相应 stage 启用按钮。
- Postgres lifecycle/worker/readiness、Backend API、Web disabled state、existing K4-K8/Q1/integration gates 回归通过。
