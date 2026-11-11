# 并行工作包任务模板

把下面模板复制到会话说明、任务单或 handoff 中使用。

```md
# Task

- workstream:
- owner/session:
- target milestone:

# Scope

- allowed_write_paths:
- read_before_start:
- do_not_edit_without_coordination:

# Acceptance

- required_commands:
- required_docs_to_update:
- done_definition:

# Coordination

- upstream_dependencies:
- downstream_consumers:
- exact_contract_fields_to_watch:
- blocker_record_location:

# Start Checklist

- [ ] 已阅读相关 spec / design / harness 文档
- [ ] 已更新 `docs/exec-plans/active/single-agent-v1-parallel-claims.md`
- [ ] 已确认本工作包默认可写路径
- [ ] 已记录本次不会改动的共享文件

# Completion Checklist

- [ ] 代码或文档改动已完成
- [ ] 验证命令已记录
- [ ] 阻塞和风险已写明
- [ ] 已准备 handoff 或 closeout
```
