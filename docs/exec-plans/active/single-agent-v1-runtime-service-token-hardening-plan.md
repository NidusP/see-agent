# 单智能体 V1 Runtime Service Token 收口计划

## Goal

- 用户可见结果：本地全栈与 production 不再因模板占位 token 或空/过短 token 而启动；启动文档给出可安全生成、加载、验证和轮换的明确流程。
- 范围：Backend → Runtime bearer token 的启动 policy、运行模板、preflight、`.gitignore`、runbook/README 和回归测试。
- 非目标：不改用户登录、Ollama API key、Runtime 路由协议、token 持久化、Web secret 管理、Secret Manager 供应商或自动 token 生成/写入。

## Frozen Contract

- 权威：Backend 与 Runtime 各自只从进程环境读取 `SEE_AGENT_RUNTIME_SERVICE_TOKEN`；它们不写入、不回显、不持久化该值。Backend 继续是发送方，Runtime 继续是 constant-time 校验方。
- 严格 profile：`fullstack_dev` 与 `production` 的 current token 必须非空、长度至少 32，且不等于已知占位值 `replace-with-local-dev-secret`。`SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS` 若设置，应用同一规则且不得等于 current。
- `test` / `smoke` 不取得新的产品 token 默认值；现有 explicit test injection 行为不变。
- 失败：preflight 或任一服务启动失败；不得用匿名 Runtime、共享示例值、模型 fallback 或自动生成 token 继续。
- 轮换：Runtime 先接受 `current=new` + `previous=old`，再将 Backend 改为 `current=new`；验证后移除 previous。token 永不写入报告、日志或浏览器。
- 模板：`infra/compose/.env.example` 只声明空变量和注释；它不是可直接 source 后启动的 secret 来源。仓库根的 `.env.fullstack-dev` 必须被忽略，且不再是文档推荐的 secret 存放位置；本机权限受限的 home-directory 文件或生产 Secret Manager 才是允许来源。

## Execution

- [x] F2 secret contract 补充
  - Owner: p
  - Evidence: fail-closed design section 4 已补 secret policy、source、rotation 与失败语义。
- [x] Token policy implementation and regression tests
  - Owner: 小弟
  - Write scope: `services/backend-go/internal/server/**`, `services/backend-go/internal/httpapi/**`（如 readiness test 需要）, `services/ai-runtime-py/app/**`, respective tests, `scripts/ops/fullstack_preflight.sh`, preflight test harness, `infra/compose/.env.example`.
  - Constraints: 不改 Runtime HTTP payload/route、Backend runtime client header 格式、聊天/摄取行为、共享 schema、数据库或 active release；不得读取、生成、记录或提交真实 token。
  - Verification: `make fullstack-preflight-test` passed; `make runtime-test` passed (`112`); `GOCACHE=/tmp/see-agent-go-cache make backend-test` passed. The harness proves invalid values stop before Docker/network probes; Runtime auth tests prove current/previous accept and missing/wrong bearer reject.
- [x] Operator documentation integration
  - Owner: p
  - Dependencies: implementation policy/test names stable.
  - Scope: `.gitignore`、Makefile startup hint、README、local stack runbook、active plan/claims.
  - Verification: template contains an empty declaration; `.env.fullstack-dev` is ignored; README/Makefile/runbook reference `$HOME/.config/see-agent/fullstack-secrets.env`, creation, permissions and rotation without embedding a secret.
- [x] Acceptance and local fullstack validation
  - Owner: p
  - Dependencies: implementation + docs.
  - Verification: Runtime and Backend were restarted from the empty template plus the external secret file. Both preflights and `/readyz` passed; protected Runtime diagnostics rejected unauthenticated requests, accepted current, accepted previous during the explicit rotation overlap, and rejected that same previous token after current-only restart. Web was restarted at `:3000` and returned its expected login redirect. Existing dense cutover/Q1 was not resumed.

## Acceptance Matrix

| Case | Expected result |
| --- | --- |
| `fullstack_dev` / `production`, token missing | preflight and service startup fail with configuration error; no Runtime product route becomes anonymous. |
| token is `replace-with-local-dev-secret` | same failure before dependency probing/model use. |
| token length `<32` | same failure. |
| valid current token | Backend and Runtime start; protected Runtime requests require matching bearer token. |
| previous missing | valid current behavior unchanged. |
| previous equals current or invalid | Runtime startup fails. |
| rotation overlap | Runtime accepts old and new; Backend uses new; removing previous revokes old. |
| template source only | preflight fails with an actionable secret-loading message. |

## Rollback And Risks

- This is a forward security fix: existing environments using the placeholder will fail at next preflight/restart and must provision a real secret. There is no compatibility mode for a known token.
- Emergency rollback is to restore a previously valid secret through the deployment secret source, not to re-enable the placeholder or disable Runtime auth.
- `fullstack-preflight` must validate token policy before any network checks, so invalid configuration produces deterministic local feedback rather than dependency noise.

## Active Status

- Current state: `verified / 2026-07-27`.
- Last verified: p generated a new local token into the documented user configuration location with mode `0600`, then restarted Runtime and Backend from the template plus secret file. Both fullstack preflights passed; Runtime and Backend `/readyz` returned ready; Web was restarted on `:3000` and returned its expected login redirect.
- Security acceptance: an unauthenticated protected Runtime request returned `401`; current token returned `200`; a temporary previous token returned `200` only during the explicit overlap restart, then returned `401` after Runtime restarted current-only. The temporary previous-token file and response artifacts were removed. No token value was recorded in this plan, report, logs or repository.
- Automated evidence: `make fullstack-preflight-test` passed; `make runtime-test` passed (`112`); `GOCACHE=/tmp/see-agent-go-cache make backend-test` passed.
- Next action: none for F2. Future token rotation follows section 6.7.1 of the local stack runbook; the separate K8 dense cutover/Q1 work remains independently blocked by its own quality-release steps.
