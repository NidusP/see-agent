# Handoff

- workstream: `P5 retrieval and answer baseline`
- owner/session: `p / current session`
- handoff_time: `2026-06-22`
- status: `done`

# Completed

- changed_files:
  - `services/ai-runtime-py/app/main.py`
  - `services/ai-runtime-py/app/adapters/**`
  - `services/ai-runtime-py/app/pipelines/{answer_pipeline.py,retrieval_pipeline.py,chunking.py,chat_pipeline.py,ingestion_pipeline.py}`
  - `services/ai-runtime-py/app/domain/models.py`
  - `services/ai-runtime-py/tests/test_health.py`
  - `services/ai-runtime-py/tests/test_model_client.py`
  - `packages/prompts/runtime-chat-system-v1.txt`
  - `packages/prompts/runtime-refusal-v1.txt`
  - `services/ai-runtime-py/README.md`
  - `services/ai-runtime-py/AGENTS.md`
  - `docs/design-docs/single-agent-v1-request-prompt-lifecycle.md`
  - `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
- behavior_now:
  - `/v1/chat/run` 已从 placeholder 升级为最小 `retrieval -> prompt assembly -> OpenAI-compatible model adapter -> citations` 闭环。
  - retrieval 当前使用 `data/sample-docs/` 作为 baseline corpus，并在 runtime 进程内建立内存索引。
  - 当 `LOCAL_LLM_BASE_URL` 未配置或端点不可用时，runtime 会回退到 deterministic heuristic fallback。
  - retrieval miss 或低置信度命中会走 refusal path。
- verified_commands:
  - `make runtime-test`
  - `make test`
  - `make integration`

# Open Items

- unfinished_work:
  - `P4` 已完成最小摄取编排，但上传结果尚未真正接入 runtime 当前 retrieval corpus。
  - `P5` 二阶段尚未接入真实 knowledge snapshot、外部向量库、rerank、streaming、真实本地模型实连。
  - `P9` 本地模型环境与 infra runbook 仍未收口。
- blockers:
  - 本机当前未安装 `ollama`，无法验证真实本地模型端点。
  - backend 仍是最小骨架，尚未把上传任务结果持续喂给 runtime 检索侧。
- risks:
  - 现有 `source_filters.file_ids` 只对 baseline corpus 的 sample file id 有意义，未与真实上传文件 id 对齐。
  - 当前 retrieval score 阈值和词法 embedding 只适合作为 baseline，不代表生产质量。

# Coordination Notes

- contract_assumptions:
  - `packages/contracts/**` 当前足够支撑 `P5` baseline。
  - 若进入真实 uploader / runtime 编排，请重新检查 `P0 contract freeze` 中 upload MIME、preview 扩展元数据的冻结范围。
- files_that_should_remain_serialized:
  - `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
  - `docs/design-docs/single-agent-v1-request-prompt-lifecycle.md`
  - `services/ai-runtime-py/README.md`
  - `services/ai-runtime-py/AGENTS.md`
- who_needs_to_be_notified:
  - 下一会话若认领 `P6`、`P8` 或 `P9`，先更新认领板再改代码。

# Next Step

- recommended_first_action:
  - 优先认领 `P6` 做上传前端联调，或认领 `P9` 收口本地 infra 与模型 runbook。
- recommended_command:
  - `sed -n '257,340p' docs/exec-plans/active/single-agent-v1-parallel-workstreams.md`
