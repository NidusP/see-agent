# 单智能体 V1 C1 回答内证据锚点设计

## 文档控制

- 状态：`verified`
- 版本：`v1.1`
- 更新日期：`2026-07-15`
- 工作包：`C1`
- 依赖：[K5 快照检索与回答 Provenance 设计](single-agent-v1-k5-snapshot-provenance-design.md)、[Web UI 设计](single-agent-v1-web-ui-design.md)。

## 1. 目标与非目标

### 1.1 目标

让用户直接从回答中的结论进入其知识依据：被证据支持的文本显示细下划线，hover/focus 显示来源摘要，点击固定来源详情。锚点必须与生成时冻结的 snapshot/revision citation 一起持久化，不能由 Web 通过字符串相似度猜测。

### 1.2 非目标

- 不做 PDF 图像框选、原文高亮或像素级定位。
- 不回填或改写历史消息；没有锚点的旧消息只保留原有消息级来源入口。
- 不把 K6 核验结果改写为带锚点回答；核验结果仍沿用独立 citation 卡片，后续可在兼容契约上扩展。
- 不更换 Runtime、Backend、Web 技术栈，也不修改 K5 snapshot 选择或 K6 stale 状态机。

## 2. 外部契约

`ChatRuntimeResponse`、浏览器 `Message` 和 `message.completed` 的 assistant message 增加可选字段：

```json
{
  "citation_anchors": [
    {
      "start_rune": 0,
      "end_rune": 18,
      "citation_ranks": [1, 2]
    }
  ]
}
```

- `start_rune` 从零开始，`end_rune` 为开区间；两者按最终 `assistant_message.content` 的 Unicode code point（Go/Python rune）计数，而非 UTF-8 byte 或 UTF-16 code unit。
- `end_rune > start_rune`；每个 `citation_ranks` 非空、去重、正整数。
- rank 必须出现在同一 response 的 `citations` 中。一个 citation 可支持多个 anchor；一个 anchor 可同时由多个 citation 支持。
- `citation_anchors` 缺失或为空是兼容状态，不表示错误，也不得被渲染为整段回答的精确引用。

Web 将 rune 偏移转换为 JavaScript 文本片段时必须使用 code point 安全的切分方法，例如 `Array.from(content)`；禁止直接将 rune 偏移当作 JavaScript 字符串下标。

## 3. Runtime 标记与解析

### 3.1 输入给模型的格式

正常回答的 system prompt 要求模型在每个有依据的完整结论后追加内部标记 `[[cite:<rank>]]`。多个来源使用连续标记，例如：

```text
报销应在差旅结束后十个自然日内提交。[[cite:1]][[cite:2]]
```

rank 来自 retrieved context 的稳定编号。该标记是 Runtime 与模型之间的临时传输格式，不属于用户可见回答，也不进入 conversation history。

### 3.2 解析规则

- Runtime stream parser 必须跨 token 边界识别标记，并在 `message.delta` 前移除完整标记，因此流式页面不会闪现内部语法。
- 每组连续 marker 绑定到其前一个完整句子或列表项；该范围形成一个 anchor。空范围、未知 rank、重复 rank 和越界范围不产生 anchor。
- marker 解析后 Runtime 返回无标记的正文与 anchor。若模型完全省略 marker，回答和 citations 仍正常返回，但 anchors 为空。
- test-only OpenAI-compatible provider 必须输出可解析 marker，以便 integration 与回归测试稳定覆盖这一能力；产品 Runtime 不保留 deterministic generation fallback。

结构化 tool/function output 在未来可替代 marker，但当前本地模型与 OpenAI-compatible adapter 的兼容面不一致，故本期不采用。

## 4. 权威写入与读取

### 4.1 Owner 与一致性

Backend 是 anchor 真实性的唯一裁决者。Runtime 只提出 anchors；Postgres 是 message/citation/anchor 的唯一持久化权威；Qdrant、Redis 和 Web 不写入或推断 anchors。

Backend 在现有 K5 scope/provenance 校验之后，验证 anchors 的 rune 边界和 citation rank，再在同一 transaction 写入：

1. user message
2. assistant message 与 immutable knowledge provenance
3. citations
4. `message_citation_anchors`

任何 anchor 验证或写入失败都回滚整个 exchange，向 Web 返回 retryable `runtime_citation_anchor_violation`。这避免“看起来有精确依据、实际没有可审计证据”的部分成功状态。

### 4.2 持久化与兼容

新增只追加的 `006_citation_anchors` migration：

```text
message_citation_anchors(
  message_id, anchor_index, start_rune, end_rune, citation_ranks JSONB
)
```

- 主键为 `(message_id, anchor_index)`，`message_id` 外键级联删除；读取按 `anchor_index ASC`。
- 不回填旧记录。legacy memory store 保留 anchors 仅在进程内，Postgres 重启后通过新表恢复。
- conversation detail 和 stream `message.completed` 的 assistant message 返回 anchors；`GET /sources` 继续按 message 返回 citation 详情和 snapshot provenance，不把 Web 需要的 anchor 位置重复塞入 source card。

## 5. Web 交互

- `CitationAnchorText` 根据 message anchors 将正文切分为普通文本与可操作锚点；重叠、乱序或越界数据在客户端防御性降级为普通文本，并记录可诊断错误，不扩大引用范围。
- hover/focus 调用现有 message sources API 并按 `citation_ranks` 过滤。加载时显示简短状态；失败时只影响来源卡。
- hover/focus 只更新临时来源预览与其所需的 sources 请求；不得因为悬停自动打开固定详情。
- 点击 anchor 会筛选并打开可关闭的 citation detail 抽屉；宽屏抽屉从右侧出现，窄屏从底部出现。点击其他 anchor 直接切换其内容。
- 键盘可用 `Tab` 聚焦、`Enter/Space` 触发固定详情、`Escape` 先关闭预览或详情抽屉并保留触发锚点焦点。

## 6. 失败、回滚与观测

- 无 marker：持久化无 anchor 的正常回答和 citations，Web 显示消息级来源入口。
- Runtime 传入无效 anchor：Backend 拒绝并不写 exchange；日志记录 request/message id 和错误类别，不记录完整答案或私有片段。
- `/sources` 暂不可用：已渲染的回答和下划线保留，hover/focus card 展示局部失败态并可重试。
- 回滚采用 roll-forward：保留 `006` 表和读取兼容；临时关闭 marker prompt 时新 answers 的 anchors 为空，不需要删除任何历史 evidence。

## 7. 验收

- Runtime 单测覆盖 Unicode、连续多 citation、marker 跨 token、无 marker、未知 rank 与显式 test-only provider。
- Contract fixture 和 JSON Schema 同时验证 Runtime response 的合法/非法 anchors。
- Backend 单测与隔离 Postgres smoke 覆盖 rank/rune 拒绝、transaction rollback、重启后的读取和 legacy 无 anchor 兼容。
- Web E2E 覆盖 hover、focus、点击固定、`Escape`、来源加载失败、普通用户聊天和宽/窄视口；页面中不出现 `[[cite:`。
- R5 与知识演进 fixture 继续通过；报告增加 evidence-anchor coverage/fidelity，防止 prompt 或模型升级悄悄移除锚点。
