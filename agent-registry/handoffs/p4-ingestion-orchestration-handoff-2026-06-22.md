# Handoff

- workstream: `P4 ingestion orchestration`
- owner/session: `p / current session`
- handoff_time: `2026-06-22`
- status: `done`

# Completed

- changed_files:
  - `services/backend-go/internal/runtime/client.go`
  - `services/backend-go/internal/service/{service.go,ingestion.go}`
  - `services/backend-go/internal/repository/memory.go`
  - `services/backend-go/internal/httpapi/handlers.go`
  - `services/backend-go/internal/server/server.go`
  - `services/backend-go/internal/domain/types.go`
  - `services/ai-runtime-py/app/pipelines/ingestion_pipeline.py`
  - `services/ai-runtime-py/tests/test_health.py`
  - `tests/integration/test_stack_smoke.py`
  - `services/backend-go/README.md`
  - `services/backend-go/AGENTS.md`
  - `tests/integration/README.md`
  - `docs/design-docs/single-agent-v1-sdd.md`
  - `docs/design-docs/single-agent-v1-backend-design.md`
  - `docs/harness/single-agent-v1-integration-test-plan.md`
  - `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
- behavior_now:
  - `POST /api/kb/files` 会把上传文件写入本地临时目录，立即返回 `queued` 任务，然后在后台调用 runtime `/v1/ingestion/run`。
  - `POST /api/kb/files/{file_id}/reindex` 会创建新任务并复用已有 `object_uri` 重新派发。
  - `GET /api/kb/files/{file_id}` 现在能看到 `latest_task.status` 从 `queued/running` 进入 `succeeded/failed`。
  - runtime 解析异常不再直接冒成 500，而会返回 `failed + error.code`，当前至少稳定覆盖 `parse_failure` / `unexpected_parse_failure`。
- verified_commands:
  - `make backend-test`
  - `make runtime-test`
  - `make integration`

# Open Items

- unfinished_work:
  - 当前上传文件只落本地临时目录，未接真实对象存储。
  - backend 仍是内存仓储，任务状态没有历史表或任务列表接口。
  - runtime 成功后只把结果回写成 summary chunk，尚未把真实 chunk 明细同步回 backend。
- blockers:
  - 无硬阻塞；后续若做 `P9`，需要补对象存储 / 向量库 / 本地模型 runbook。
- risks:
  - 异步编排目前依赖进程内 goroutine，服务重启后不会保留任务执行上下文。
  - 上传文件 URI 目前是本地绝对路径，只适合单机联调。

# Coordination Notes

- contract_assumptions:
  - 现有 `packages/contracts/ingestion-task.schema.json` 与 `ingestion-task-result.schema.json` 足够支撑 `P4` 最小闭环。
  - 当前没有新增对外产品 API，只增强了既有上传 / 详情 / 重建索引语义。
- files_that_should_remain_serialized:
  - `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
  - `docs/design-docs/single-agent-v1-backend-design.md`
  - `docs/harness/single-agent-v1-integration-test-plan.md`
- who_needs_to_be_notified:
  - 接手 `P6` 的会话可直接基于现在的 `latest_task` 状态接前端上传工作流。
  - 接手 `P8/P9` 的会话要注意当前仍是本地绝对路径 + 进程内 goroutine 的联调基线。

# Next Step

- recommended_first_action:
  - 优先接 `P6` 把上传页状态刷新和失败提示接到真实 backend。
- recommended_command:
  - `sed -n '257,299p' docs/exec-plans/active/single-agent-v1-parallel-workstreams.md`
