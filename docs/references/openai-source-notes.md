# OpenAI 来源说明

## 目的

本文件记录用于塑造 `see-agent` 这套纯 Markdown 初始化文档的 OpenAI 官方资料来源。

## 重要说明

OpenAI 似乎并未发布一套名称完全固定为以下形式的标准模板：

- `SPEC.md`
- `SDD.md`
- `HARNESS.md`

因此，本仓库是将 OpenAI 的公开指导映射为这些本地文档类型。

## 来源映射

### 1. Harness Engineering

来源：

- [OpenAI Harness Engineering](https://openai.com/zh-Hans-CN/index/harness-engineering/)

使用方式：

- 将 `AGENTS.md` 视为导航图，而不是完整知识库
- 使用结构化的 `docs/` 目录作为项目的持久记忆
- 对计划、决策和系统组织进行显式文档化

### 2. Building Agents Track

来源：

- [OpenAI Building Agents Track](https://developers.openai.com/tracks/building-agents/)

使用方式：

- 保持第一版范围聚焦
- 在简单基线跑通前避免过度建设
- 清晰定义智能体产品的行为与迭代路径

### 3. Evaluate Agent Workflows

来源：

- [OpenAI Evaluate Agent Workflows](https://developers.openai.com/api/docs/guides/agent-evals)

使用方式：

- 构建可重复的评测 harness
- 预先定义任务、数据集与评分方式
- 用可度量标准而不是直觉比较变更

### 4. Optimizing LLM Accuracy

来源：

- [OpenAI Optimizing LLM Accuracy](https://developers.openai.com/api/docs/guides/optimizing-llm-accuracy)

使用方式：

- 在微调之前，优先优化提示词、检索与上下文
- 将实验组织成有意识的优化循环

### 5. Guardrails Guide

来源：

- [OpenAI Guardrails Guide](https://openai.github.io/openai-agents-python/guardrails/)

使用方式：

- 要求明确的回答边界
- 将拒答与校验行为视为设计问题，而不是事后补丁

## 本地解释

基于这些来源，本仓库采用如下本地映射：

- `spec`
  - 产品范围、用户旅程、需求、非目标、验收标准
- `sdd`
  - 服务边界、运行流程、API、数据契约、部署形态
- `harness`
  - 数据集、graders、traces、实验矩阵、质量门槛、模型对比方法
- `plan`
  - 里程碑顺序与交付门槛

## 为什么这很重要

这套映射让项目能够保持：

- 文档优先
- 评测驱动
- 关注私有部署约束
- 优先 RAG，而不是过早微调
