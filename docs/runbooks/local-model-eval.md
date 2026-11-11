# 本地模型与 Eval Runbook

## 目的

说明如何把 See Agent Runtime 接到 OpenAI-compatible 本地模型端点，并用同一套 eval harness 产出真实模型 baseline。

## 1. 当前运行模式

Runtime 只保留 OpenAI-compatible generation 路径。配置 `LOCAL_LLM_BASE_URL` 后，`/v1/chat/run` 调用 chat completions，`/v1/chat/stream` 使用模型原生 stream；未配置 endpoint 或请求失败时返回明确、可重试的模型错误，不生成替代回答。

`make runtime-fullstack-dev` 不再填充 Qdrant、本机模型或对象存储的默认值：先导入一份明确的 `fullstack_dev` 环境，再运行 `make fullstack-preflight`。启动控制台会显示实际 generation provider、默认模型提示和可用状态；每次生成还会记录 Backend 请求中的实际 `model_id`。integration/e2e 使用显式 test-only OpenAI-compatible provider，不污染产品 Runtime。

## 2. 环境变量

常用变量：

```bash
export LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1
export LOCAL_LLM_API_KEY=local-dev
export LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest
export LOCAL_LLM_TIMEOUT_SECONDS=90
```

说明：

- `LOCAL_LLM_BASE_URL` 必须是 OpenAI-compatible API base URL。
- `LOCAL_LLM_API_KEY` 本地模型通常可以填任意非空值；如果端点不要求鉴权，也建议保留占位值，避免客户端初始化差异。
- `LOCAL_LLM_DEFAULT_MODEL_ID` 只决定新建 Backend repository 的初始 Agent Config；已有环境应在管理员配置页创建新的模型配置版本。
- `LOCAL_LLM_TIMEOUT_SECONDS` 对本机 3B 模型的完整 eval 建议从 `90` 秒开始；实际值依机器性能与 `max_output_tokens` 调整。
- Runtime 已无 generation fallback 开关；模型不可用时始终失败。
- 质量报告除 `model_id` 外还必须记录实际 generation provider 和 `fallback_used=false`；缺少这两项时不能作为模型选型或 Q1 发布证据。

## 3. Ollama 示例

如果本地 Ollama 已启用 OpenAI-compatible endpoint，可按以下方式运行：

```bash
export LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1
export LOCAL_LLM_API_KEY=ollama
export LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest
export LOCAL_LLM_TIMEOUT_SECONDS=90
make local-model-smoke
make eval-runtime-baseline
```

`make local-model-smoke` 直接校验 Ollama 模型列表和 Runtime 使用的 OpenAI-compatible client；通过即证明该模型端点可真实生成。随后 `make eval-runtime-baseline` 才用于评估完整 RAG 链路。新会话会使用 `LOCAL_LLM_DEFAULT_MODEL_ID`，已有环境则应在 Backend Config 中创建新的模型版本。

`make eval-runtime-baseline` 会把同一个 `LOCAL_LLM_DEFAULT_MODEL_ID` 传给 eval runner；可通过该环境变量临时指定其他已安装模型，报告中的 model id 必须与之相同。

当模型为 `llama3.2:latest` 且使用 R3 基准参数时，使用 `make eval-runtime-gate` 进行回归验收。它会先保存报告，再在模型、参数或质量指标不符合版本化 gate 时返回非零：

```bash
EVAL_TEMPERATURE=0.2 EVAL_MAX_OUTPUT_TOKENS=1024 make eval-runtime-gate
```

## 4. vLLM 示例

如果使用 vLLM 或其他 OpenAI-compatible server，确保 endpoint 暴露 `/chat/completions`：

```bash
export LOCAL_LLM_BASE_URL=http://127.0.0.1:8001/v1
export LOCAL_LLM_API_KEY=local-dev
export LOCAL_LLM_TIMEOUT_SECONDS=60
make eval-runtime-baseline
```

GPU 机器上建议先用小样本手动请求确认模型可用，再跑完整 `40` 条 baseline。

## 5. Eval 命令

字段校验和 deterministic mock：

```bash
make eval-baseline
```

Runtime-local baseline：

```bash
make eval-runtime-baseline
```

真实模型 baseline：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
LOCAL_LLM_API_KEY=local-dev \
LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest \
LOCAL_LLM_TIMEOUT_SECONDS=90 \
make eval-runtime-baseline
```

真实模型质量门：

```bash
LOCAL_LLM_BASE_URL=http://127.0.0.1:11434/v1 \
LOCAL_LLM_API_KEY=ollama \
LOCAL_LLM_DEFAULT_MODEL_ID=llama3.2:latest \
LOCAL_LLM_TIMEOUT_SECONDS=90 \
EVAL_TEMPERATURE=0.2 \
EVAL_MAX_OUTPUT_TOKENS=1024 \
make eval-runtime-gate
```

报告位置：

```text
data/eval-datasets/reports/runtime/latest.json
data/eval-datasets/reports/runtime/latest.md
```

这些报告是本地生成物，不作为稳定事实源提交。需要沉淀结论时，把关键指标、失败分类和下一步动作记录到 `docs/harness/`。

## 6. 排障

常见问题：

- `connection refused`：模型服务未启动、端口不对，或 base URL 缺少 `/v1`。
- eval 在模型未启动时仍产出回答：这是回归缺陷；当前 Runtime 应返回模型错误，不存在 generation fallback。
- stream 卡住：先跑非流式 `make eval-runtime-baseline`，再检查 Runtime `/v1/chat/stream`。
- 本机代理影响 localhost：Runtime 的 Qdrant 与对象存储 client 已禁用代理环境读取；模型端点如受代理影响，应在 shell 中设置 `NO_PROXY=127.0.0.1,localhost`。
