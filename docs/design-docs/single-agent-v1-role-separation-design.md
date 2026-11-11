# 单智能体 V1 角色分离与受控参数管理设计

## 文档控制

- 状态：`active / implementation_verified`
- 更新日期：`2026-07-18`
- 工作包：`K9-R1`
- 上位设计：[产品规格](../product-specs/single-agent-v1-spec.md)、[Backend 设计](single-agent-v1-backend-design.md)、[Q1 质量运营](single-agent-v1-quality-operations-release-design.md)、[K8-R4a onboarding](single-agent-v1-k8-r4-onboarding-quality-gate-design.md)

## 1. 目标与非目标

将现有泛化 `admin` 分离为知识库管理员与系统管理员：前者负责业务知识的全生命周期和 onboarding 验收，后者负责模型/检索参数候选、质量运营、发布与回滚。普通用户只保留聊天和自己会话的读取/写入权限。

目标是把“需要调整参数”完全放入产品受控工作流，而不是让管理员运行脚本、修改环境变量或直接写数据库；并保留候选、评测、人工审批、版本和回滚的安全边界。

非目标：本包不提供用户管理 UI、数据库用户目录、部门/文档级权限、自动调参、自动发布、自动删除知识，或让知识库管理员绕过 Q1 直接改变 active config。

## 2. 角色与兼容性

| 角色 | 身份 | 能力 |
| --- | --- | --- |
| `user` | 普通用户 | 自己的 chat/conversation/source/revalidation。 |
| `knowledge_admin` | 知识库管理员 | 除 publish 外的 `/api/kb/**` 生命周期、`/api/admin/onboarding/**`、知识更新/快照读取；可在 `/knowledge` 上传、等待索引、验收和退役知识。 |
| `system_admin` | 系统管理员 | 包含 `knowledge_admin` 的全部能力，并独占 `POST /api/kb/revisions/{revision_id}/publish`；另可访问 `/api/agent/**`、execution/delivery/eval evidence、`/api/admin/quality/**` 和 release/rollback；可在 `/config` 创建候选、比较、审批、发布或回滚。 |

已签发但尚未过期的 legacy `admin` session 必须视为 `system_admin`，避免升级时将已有系统管理员锁在外面；新默认系统管理员 session 一律签发 `system_admin`。Backend 是最终授权权威；Web role cookie 仅用于路由和导航体验。

## 3. 权限边界与写入路径

```text
knowledge_admin
  上传/索引知识 → ready revision
                         │ 等待系统管理员确认
                         ▼
system_admin
  publish revision → successor snapshot → onboarding run
                                      │ passing/failing evidence
                                      ▼
  config/retrieval candidate → Q1 comparison → approval → release/rollback
```

- 知识内容写入仍只经 Backend/Postgres 的 K4 lifecycle path；Qdrant、对象存储和 Runtime 不是授权写入者。`ready -> published` 由系统管理员确认，发布 event 的 actor 形成审计证据。
- 参数候选与发布仍只经 Backend/Postgres 的 Q1 path；知识库管理员不能调用候选配置、quality experiment、publish 或 rollback API。
- content-only 增量知识先由 `knowledge_admin` 做 onboarding。run 通过时沿用当前 release；只有质量失败或主动改变 retrieval/profile/index/boundary 时，`system_admin` 才创建 Q1 candidate。
- `system_admin` 不是聊天资源 owner 的 bypass；K9-S0 conversation ownership 不变。

## 4. 路由与 UI 合约

| 表面 | `user` | `knowledge_admin` | `system_admin` |
| --- | --- | --- | --- |
| `/chat`、`/api/chat/**` | 允许（仅 own） | 允许（仅 own） | API 仍允许（仅 own）；Web 重定向到 `/config`。 |
| `/knowledge`、`/api/kb/**`、`/api/admin/onboarding/**` | `403` / 隐藏 | 允许；但 revision publish 为 `403` | API 仍允许；Web 重定向到 `/config`，直到治理审批迁移完成。 |
| `/config`、`/api/agent/**`、evidence/eval、`/api/admin/quality/**` | `403` / 隐藏 | `403` / 隐藏 | 允许 |

Web 将显示“普通用户 / 知识库管理员 / 系统管理员”角色名称。知识库管理员只见“对话、知识库”，ready revision 显示“等待系统管理员确认发布”；系统管理员在 `U5` 后只进入“系统配置台”，而不显示聊天/知识库工作区。系统管理员的发布/退役 Backend authority 暂时保持，后续必须迁入配置台治理区，不能下放或静默删除。前端不得把角色 cookie 当成 API 授权证据。

## 5. 默认账户、迁移与回滚

- 开发默认账户：系统管理员 `admin/admin123`、知识库管理员 `knowledge_admin/knowledge_admin123`、普通用户 `user/user123`。
- 为了现有部署兼容，旧 `SEE_AGENT_ADMIN_USERNAME/PASSWORD` 仍作为系统管理员环境变量别名；新变量为 `SEE_AGENT_SYSTEM_ADMIN_USERNAME/PASSWORD` 和 `SEE_AGENT_KNOWLEDGE_ADMIN_USERNAME/PASSWORD`，新变量优先。
- 这是会话角色扩展，没有 Postgres domain data migration；Redis/memory session 记录内的 legacy `admin` 通过授权兼容规则继续有效，最多到其既有 TTL 结束。
- 回滚实现只需保留 legacy `admin -> system_admin` 授权映射；不能将新 `knowledge_admin` 误提升为系统管理员。

## 6. 验收与失败语义

- Backend：未登录 `401`；`user` 访问知识/配置均 `403`；`knowledge_admin` 能上传/索引/onboarding，但 publish/config/quality 均 `403`；`system_admin` 能确认 publish 和访问全部系统管理路由；legacy `admin` 仅作为系统管理员兼容通过。
- Web：角色 cookie 对应正确导航与直达页重定向；知识库管理员不能看见或进入 `/config`；系统管理员只看见并进入系统配置台。
- 现有 admin API 的 idempotency、K4-K8/Q1 状态机、conversation ownership 和历史 provenance 不得改变。
- 未知 role 一律按无管理权限处理，不能因前端 cookie 伪造而获得 API 权限。

### 6.1 实现验证（2026-07-18）

- Backend 新 session 签发 `system_admin`、`knowledge_admin` 或 `user`；`admin` 只保留为 legacy session 兼容值，且默认系统管理员保持 `user_admin` subject，避免破坏 K9-S0 legacy conversation ownership。
- `requireKnowledgeAdmin` 保护 KB/onboarding；`requireSystemAdmin` 保护 revision publish、agent config、evidence/eval 与 Q1 quality release。所有角色仍通过同一 Backend session authority 判断。
- Web `/knowledge` 与 `/config` 分别按知识库/系统管理员 role cookie 做体验级路由门禁；导航只显示角色允许的工作区，知识库管理员在 ready revision 上只会看到“等待系统管理员确认发布”。
- `go test ./...`、Web lint/typecheck/build、`make integration`（`54 passed`）通过；角色测试覆盖 user、knowledge_admin、system_admin、publish denial 和 legacy admin。
