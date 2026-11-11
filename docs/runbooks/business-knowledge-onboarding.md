# 业务知识导入与测试知识安全退役

## 目的与边界

本 runbook 用于把产品内的测试知识逐步替换为真实业务知识。它不删除 `data/eval-datasets/**` 中的长期评测 fixture、报告或评分规则，也不修改历史回答、citation、snapshot manifest 或 provenance。

本流程有两条路径：

| 路径 | 何时使用 | 是否需要 Q1 quality release |
| --- | --- | --- |
| 知识更新 | 保持当前已发布的 retrieval strategy、embedding/index、rerank 与 evidence boundary | 否；沿用当前 release，完成 onboarding eval 后发布 successor snapshot。 |
| 检索变更 | 同时切到 native dense，或更换 embedding/index/reranker/`evidence_min_score` | 是；必须以真实业务 collection/snapshot 完成 Q1 baseline/candidate/report/approval 后才发布。 |

不要把 Qwen fixture 的 `.51` 复制到另一 embedding 模型、collection、chunk/index 或业务语料。它只证明固定 K8-R3 fixture 的分离能力。

## 上传前清单

1. 确认管理员已经拿到可上传的业务文件及其内容授权；支持 `txt`、`md`、`json`、`pdf`、`docx`、`xlsx`、`pptx`。
2. 为每份文件准备业务显示名、预期生效时间（如需要）和至少一个可核验事实；为每种实际上传格式准备至少一条 must-answer onboarding 问题与预期来源位置。
3. 准备至少五条当前业务知识库中应拒答的问题。它们必须没有对应的业务证据，不能用“回答不喜欢的题目”充数。
4. 记录当前 active snapshot、retrieval config version 和 active quality release。不要在上传期间同时修改这些配置。
5. 选择路径：只有明确要切换 dense/profile/index/boundary 时才选择“检索变更”；否则保持当前 lexical release。

## 导入和并存验证

1. 知识库管理员在 `/knowledge` 上传每份新文件；系统管理员确认 ready revision 的 publish。每一次 mutation 使用独立 idempotency key；不要绕过 Web/Backend 直接写 Postgres、对象存储或 Qdrant。
2. 记录 `document_id`、`revision_id`、checksum、delivery id 和 source format。等待 delivery/Runtime projection 成功，并确认 revision 为 `ready`、具有 chunk manifest。
3. 对每个 ready revision 显式 publish。Backend 会从现有 active snapshot 复制成员、加入/替换目标 revision，并创建包含**旧+新**知识的 successor snapshot；不得手动构造空 snapshot。
4. 在该 successor snapshot 上运行 onboarding eval：
   - 每条 must-answer 必须引用新 revision，且 locator/答案要点正确；
   - must-refuse 不得得到无关 citation；
   - upload/reindex 失败、scope/manifest 不一致或 runtime error 一律视为环境失败，不能当成质量分数；
   - 保持 lexical release 时，确认当前 lexical quality gate 无回归；选择检索变更时，完成 Q1 experiment/report/gate/approval。
5. 管理员审阅来源抽屉和 execution evidence：新文档 citation、snapshot manifest、config pair 与 eval report 必须一致。未通过时保留旧 snapshot，修复文件或索引后重新上传 revision。

## 测试知识退役

系统管理员只能在产品的“退役安全门”显示对应 ready stage 后执行；Backend/Postgres 会在同一 transaction 重新确认，不能只依赖界面。

1. 知识库管理员先在包含**旧+新**的 current snapshot 完成 passing onboarding。系统管理员读取旧 document 的 readiness 为 `ready_to_disable` 后，一次只 disable 一个测试 document；确认返回 successor snapshot，且新 document 仍在 active members 中。
2. 用 new-only successor snapshot 重跑 onboarding 问题和 must-refuse 问题。每个 must-answer citation 必须来自新 revision；测试 document 不得再出现。
3. 对不含旧文档的 current/new-only snapshot 完成第二次 passing onboarding；系统管理员确认 readiness 变为 `ready_to_delete` 后，再对该已 disabled document 执行 delete。delete 是软删除；历史消息、历史 citation、评测 fixture 和 provenance 保留。
4. 对下一份测试 document 重复上述步骤。绝不先删除最后一个 active document；否则系统会正确进入 `knowledge_snapshot_unavailable`，造成知识问答中断。

## 回滚与异常

- revision 未 ready、manifest 缺失、onboarding 失败：不 publish，旧 active snapshot 继续服务。
- 已 publish 但未 delete：停止后续 disable，并发布包含旧 revision 的 successor snapshot。
- 配置/检索变更失败：通过 Q1 forward rollback 恢复上一 release；不要改写历史 conversation。
- 已 delete：不要用数据库 reset 恢复。依靠保留的 object/revision lifecycle、snapshot/provenance 记录和既有恢复流程处理。

完成时记录：新 snapshot id/manifest、所有新 revision、onboarding report、审批/quality release（如适用）、被 disable/delete 的测试 document、以及 rollback target。
