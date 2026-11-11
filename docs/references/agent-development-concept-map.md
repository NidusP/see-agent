# Agent 开发通用概念地图

## 用途与边界

本文是学习参考，不是 see-agent 的功能承诺或技术选型清单。概念应按所解决的问题选择，不能因为框架提供某个抽象就默认引入。具体产品契约仍以 `docs/product-specs/` 和设计文档为准。

## 1. 会话理解

| 概念 | 解决的问题 | 示例 |
| --- | --- | --- |
| conversation history | 当前输入缺少前文 | “那超过期限呢？”需要知道正在讨论报销期限。 |
| context selection / compression | 历史过长或包含噪声 | 只给模型最近相关轮次和已压缩的旧主题摘要。 |
| coreference resolution | 指代对象不明确 | “它需要谁审批？”中的“它”指上一轮的差旅报销。 |
| query resolution / rewriting | 口语短句不能直接检索 | 将“那期限呢？”变为“差旅报销申请的提交期限是什么？”。 |
| dialogue state tracking | 对话正处在澄清、确认或任务执行的哪个阶段 | 用户回答“差旅报销”时补全上一轮澄清，而不是开始新问题。 |
| follow-up detection | 区分追问与独立问题 | “具体怎么申请？”通常承接上一轮流程。 |
| topic-shift detection | 避免把旧话题硬拼到新问题 | 从“差旅报销”转问“请假流程”时创建新的逻辑主题段。 |
| correction handling | 用户修正模型或自己的前提 | “不对，我问的是住宿超标。”应放弃此前报销期限解释。 |
| clarification | 范围不足时避免猜测 | “总结三条最重要规则”先问具体事项与排序口径。 |
| session / long-term memory | 区分会话临时事实与可持久偏好 | 本轮角色说明属于 session；语言偏好可经用户同意写入 profile。 |

## 2. RAG 与知识可靠性

| 概念 | 解决的问题 | 示例 |
| --- | --- | --- |
| chunking | 文档无法整体检索 | 按制度章节、段落、表格行切块。 |
| embedding / lexical retrieval | 匹配语义或精确术语 | “报销多久提交”命中“申请提交时限”。 |
| query expansion / HyDE | 用户表达与制度语言差异大 | 生成检索辅助表达，但不将其当作用户原话或答案。 |
| reranking | 初检候选不够精确 | 从 20 段候选中选出最直接支持答案的 3 段。 |
| metadata filtering | 防止检索错版本、错范围 | 只搜索当前发布 snapshot 的成员 revision。 |
| grounding / provenance | 防止模型脱离证据 | “10 个自然日”必须指向本次实际使用的 chunk。 |
| abstention / partial answer | 证据不足或只覆盖一部分时不伪造完整性 | 明确“当前知识只能验证两项”。 |

## 3. 行动与工作流

| 概念 | 解决的问题 | 示例 |
| --- | --- | --- |
| tool/function calling | 模型需要读取或改变外部系统 | 查询库存、创建工单。 |
| planning / decomposition | 复杂目标需要拆步骤 | “汇总季度费用问题并出报告”。 |
| ReAct | 推理、行动、观察交替 | 检索后发现缺数据，再调用审批系统。 |
| workflow/state machine | 固定、可审计的业务流程 | 上传 → 解析 → 审核 → 索引 → 发布。 |
| human-in-the-loop | 高风险动作需要人工确认 | 发布制度、删除知识、发送外部通知。 |
| multi-agent | 不同专业角色分工 | 研究、写作、审校分开；不是单智能体 RAG 的默认需求。 |
| MCP | 以统一协议接入外部工具和数据源 | 用同一方式连接 Git、数据库或企业系统。 |

## 4. 安全与可靠性

| 概念 | 解决的问题 | 示例 |
| --- | --- | --- |
| prompt-injection defense | 不可信文档诱导模型越权 | 文档中的“忽略规则”不能改变系统边界。 |
| authorization | 模型不能替代权限系统 | 普通用户不能通过 prompt 读取管理员 evidence。 |
| structured output validation | 模型输出不应直接成为命令 | tool 参数、query resolution 结果必须通过 schema。 |
| sandboxing | 工具执行不能伤害系统 | 代码工具不能任意访问主机文件。 |
| idempotency / retry / cancellation | 重复、失败、停止请求的可控处理 | 重复点击“提交”不创建两次审批。 |
| rate limit / quota / circuit breaker | 防止资源耗尽与级联故障 | 模型队列满时明确繁忙，不无限堆积。 |
| fail-closed | 依赖或证据缺失时不伪造成功 | 模型、检索或引用失败就返回失败/澄清。 |

## 5. 质量与运营

| 概念 | 解决的问题 | 示例 |
| --- | --- | --- |
| prompt/model/config versioning | 行为变化可追溯、可回滚 | 某回答使用 `prompt_v7` 与固定模型配置。 |
| evaluation harness / golden dataset | 不凭感觉判断质量 | “那超过期限呢？”成为长期回归样本。 |
| tracing / execution evidence | 排查一轮回答做了什么 | 看见解析 query、检索、模型调用和引用的真实链路。 |
| observability | 发现运行退化 | 首 token 延迟、拒答率、检索空结果率异常。 |
| controlled rollout | 候选策略不直接影响生产 | 新 resolver 先以固定评测和候选 release 验证。 |

## 选择原则

- 模型负责语义理解和产生候选；代码负责权限、状态、校验、持久化与失败边界。
- 成熟组件优先承担模型服务、向量库、队列、监控等通用基础设施；产品特有契约不能外包给框架默认行为。
- LangChain、LangGraph、LlamaIndex、DSPy、Semantic Kernel 等是实现工具，不是功能清单，也不能替代上述产品边界。
