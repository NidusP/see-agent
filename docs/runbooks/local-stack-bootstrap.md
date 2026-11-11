# 本地联调栈启动说明

## 目的

记录单智能体 V1 在本地开发机上的最小联调栈，包括基础依赖、模型服务、环境变量和推荐启动顺序。

## 1. 最小联调栈

本地联调默认包含：

- `Postgres`
- `Redis`
- `MinIO`
- `Qdrant`
- 本地模型服务
  - 开发机默认推荐：`Ollama`
  - 更高并发或远程 GPU 主机：`vLLM`

## 2. 已落地资产

- Compose 文件：
  - `infra/compose/docker-compose.yml`
- 环境变量示例：
  - `infra/compose/.env.example`
- 根级命令入口：
  - `make infra-up`
  - `make infra-down`
  - `make infra-ps`
  - `make infra-logs`
  - `make infra-check`

## 3. 端口约定

本项目的 Docker 依赖默认避开常见宿主机端口，避免与其他项目的 Postgres、Redis、MinIO 或 Qdrant 冲突。
冒号左侧是宿主机端口，右侧是容器内部端口；例如 `15432:5432` 表示本机访问 `localhost:15432`，容器内仍是 Postgres 标准端口 `5432`。

- Web：`3000`
- Backend：`8080`
- AI Runtime：`8000`
- Postgres：宿主机 `15432` -> 容器 `5432`
- Redis：宿主机 `16379` -> 容器 `6379`
- MinIO API：宿主机 `19000` -> 容器 `9000`
- MinIO Console：宿主机 `19001` -> 容器 `9001`
- Qdrant HTTP：宿主机 `16333` -> 容器 `6333`
- Qdrant gRPC：宿主机 `16334` -> 容器 `6334`
- Local LLM：`11434`

## 4. 环境变量建议

建议后续服务统一使用：

- `DATABASE_URL`
- `REDIS_URL`
- `REDIS_INGESTION_QUEUE`
- `AUTH_SESSION_STORE`
- `REDIS_SESSION_PREFIX`
- `OBJECT_STORE_ENDPOINT`
- `OBJECT_STORE_BUCKET`
- `OBJECT_STORE_ACCESS_KEY`
- `OBJECT_STORE_SECRET_KEY`
- `OBJECT_STORE_REGION`
- `QDRANT_URL`
- `QDRANT_COLLECTION`
- `QDRANT_TIMEOUT_SECONDS`
- `RUNTIME_RETRIEVAL_STRATEGY`（产品路径使用 `native_dense_v1`）
- `EMBEDDING_BASE_URL`
- `EMBEDDING_MODEL`
- `EMBEDDING_DIMENSIONS`
- `EMBEDDING_API_KEY`
- `EMBEDDING_TIMEOUT_SECONDS`
- `LOCAL_LLM_BASE_URL`
- `LOCAL_LLM_API_KEY`
- `LOCAL_LLM_DEFAULT_MODEL_ID`
- `LOCAL_LLM_TIMEOUT_SECONDS`
- `BACKEND_RUNTIME_CHAT_TIMEOUT_SECONDS`（5–600；必须不超过 `LOCAL_LLM_TIMEOUT_SECONDS`）

示例值可参考：

- `infra/compose/.env.example`

## 5. 模型服务建议

### 5.1 本机原生 Ollama

若 Ollama 尚未运行，使用 `ollama serve` 启动。默认监听本机即可，无需向局域网开放。

推荐原因：

- 本地启动成本低
- 与 OpenAI-compatible 适配层组合简单
- 适合作为开发默认值

### 5.2 远程或 GPU 主机 vLLM

适用场景：

- 本机资源不足
- 需要更稳定的吞吐或更大上下文窗口

约束：

- 对上仍需暴露 OpenAI-compatible 接口
- 不允许让 Web 或 Backend 感知具体 serving 实现差异

## 6. 推荐启动顺序

### 6.0 一键启动（推荐）

在仓库根目录执行：

```bash
make dev
```

根级 `make dev` 是当前推荐的开发入口。它会依次完成：

1. 检查 Docker、Docker Compose、Ollama、Go、Python、Node.js、pnpm、curl 和 openssl。
2. 检查 Ollama 服务以及 `qwen3:14b`、`qwen3-embedding:0.6b` 是否已下载；缺失时停止并给出 `ollama serve` 或 `ollama pull` 命令。
3. 检查 `$HOME/.config/see-agent/fullstack-secrets.env` 中的 Runtime service token。文件缺失、占位值、长度不足时，本次命令重新生成并设置为用户私有权限。
4. 启动 Postgres、Redis、MinIO、Qdrant，并运行健康检查。
5. 按需安装 Python Runtime、Go Backend 和 Web 依赖。
6. 运行 `make fullstack-preflight`；随后由 Runtime 启动期 warm-up 验证真实模型生成能力。
7. 按 Runtime → Backend → Web 顺序启动应用，等待 Runtime `/readyz`、Backend `/readyz` 和 Web 首页响应。
8. 运行 `make fullstack-smoke`，成功后输出访问地址和可选的示例知识导入命令。

Runtime、Backend、Web 会以本次启动的子进程运行，日志保存在 `.local/dev/logs/<启动时间>/`。启动过程中按 `Ctrl-C` 会停止本次启动的三个应用进程；Docker 依赖和数据卷保持不变。已处于 ready 状态的应用会被复用，端口被占用但 readiness 失败时命令会停止并提示冲突。

如果一键启动失败，命令会在终端输出失败阶段和相应日志尾部；也可以查看提示中的日志目录。示例知识不会自动导入，服务就绪后按需执行：

```bash
make bootstrap-rag-knowledge
```

该命令会上传、索引并发布 `data/local-dev-knowledge/v1/` 下的三份本地示例制度。

### 6.1 基础依赖

在仓库根目录执行。需要 Docker 和 Docker Compose、Python 3.12+、Go、Node.js 及 pnpm；前端 pnpm 版本以 `apps/web/package.json` 为准。

```bash
make infra-check
make infra-up
RUNNING=1 make infra-check
make runtime-install
make web-install
```

`Makefile` 中 `GO_BIN` 的默认值是原开发机的绝对路径；其他环境若该路径不存在，在需要运行 Backend 的终端执行 `export GO_BIN="$(command -v go)"`。

说明：

- `make infra-check` 默认只校验本地文件、Compose 配置、服务声明和环境变量模板，不要求容器已启动。
- `RUNNING=1 make infra-check` 会额外检查容器是否运行，以及 `Postgres`、`Redis`、`MinIO`、`Qdrant` 的基础健康端点。
- Qdrant 官方镜像不内置 `curl`；Compose 中的 Qdrant healthcheck 使用镜像内置 `bash` 探测 `/healthz`，宿主机运行检查仍使用本机 `curl` 访问 `localhost:16333`。
- 普通 `make integration` 默认仍走本地临时上传目录；要验证真实 MinIO 对象链路，先启动 MinIO，再运行 `make minio-ingestion-smoke`。
- 普通 `make integration` 默认仍走内存 Backend repository；要验证真实 Postgres 元数据持久化，先启动 Postgres，再运行 `make postgres-repository-smoke`。
- 普通 `make integration` 默认仍走进程内本地 ingestion queue；要验证真实 Redis 队列派发，先启动 Redis，再运行 `make redis-ingestion-smoke`。
- 普通 `make integration` 默认仍走内存 session；要验证真实 Redis session 持久化，先启动 Redis，再运行 `make redis-session-smoke`。

### 6.2 本地模型

确认 Ollama 已启动后，首次准备回答模型和文本向量模型：

```bash
ollama pull qwen3:14b
ollama pull qwen3-embedding:0.6b
```

模板默认使用 `qwen3:14b` 生成回答，使用 `qwen3-embedding:0.6b` 生成 1024 维文本向量，并以 `native_dense_v1` 检索。不要复用历史的 `see_agent_chunks` 词法 collection；模板使用独立的 `see_agent_dense_qwen_chunks_v1`。已有数据库中的配置不会被环境变量覆盖，须在管理员配置页核对生效的模型及检索配置，按需创建和发布匹配的新版本。

`make runtime-native-qdrant-smoke` 与 `make runtime-native-dense-smoke` 可用于排查向量数据库和文本向量链路；它们不证明生成模型的回答质量。Runtime 会校验 collection 的距离类型和维度，模型或配置不匹配时不会静默回退到词法检索。

### 6.3 持久化全栈开发模式

本模式用于开发真实产品链路，而不是只验证内存 fallback。先确保 Docker 依赖和 Ollama 均已就绪。

首次启动且尚无密钥文件时，生成一次 Runtime service token。它是 Backend 调用 Runtime 的内部 bearer 凭据，不是 Ollama API key；仅保存到本机权限受限目录，不得写入仓库或浏览器变量。已有文件时保留并复用，避免覆盖正在使用的凭据：

```bash
mkdir -p "$HOME/.config/see-agent"
chmod 700 "$HOME/.config/see-agent"
if [ ! -e "$HOME/.config/see-agent/fullstack-secrets.env" ]; then
  token="$(openssl rand -hex 32)"
  (umask 077; printf 'export SEE_AGENT_RUNTIME_SERVICE_TOKEN=%s\n' "$token" > "$HOME/.config/see-agent/fullstack-secrets.env")
  unset token
fi
```

在 Runtime、Backend 和执行 `make fullstack-smoke` 的各自终端，进入仓库根目录并导入同一组变量。先加载公开模板，再加载 secret 文件，使 secret 覆盖模板中的空变量：

```bash
set -a
. infra/compose/.env.example
set +a
. "$HOME/.config/see-agent/fullstack-secrets.env"
```

启动顺序：

```bash
# Terminal 1
make fullstack-preflight
make runtime-fullstack-dev

# Terminal 2
make backend-fullstack-dev

# Terminal 3，进入仓库根目录
BACKEND_API_BASE_URL=http://127.0.0.1:8080 make web-dev

# Terminal 4，导入上述环境变量后确认 Backend 和 Runtime 均已可用
make fullstack-smoke
```

`fullstack_dev` / `production` 会拒绝空值、`replace-with-local-dev-secret` 和少于 32 个字符的 token。`make fullstack-preflight` 必须先通过，才启动 Runtime 或 Backend。两个启动命令也会自动运行预检；旧的 `*-rag-dev` 入口已退役。Runtime 启动时会先向真实模型发送一次不持久化的预热请求，成功后 `/readyz` 才会就绪。Backend 默认请求超时 60 秒，不能大于 Runtime 的模型超时 90 秒。

访问 `http://127.0.0.1:3000`。已有数据库中的模型配置不会被环境变量覆盖；请通过管理员配置页核对当前版本，并在配置变更后创建新会话验证。

### 6.4 可选：初始化示例知识

若希望向空知识库导入仓库提供的示例制度，在服务就绪后执行：

```bash
make bootstrap-rag-knowledge
```

该命令会逐篇检查三份示例文档，上传缺失的文档、等待索引并发布，会写入本地知识数据；即使已有其他生效的知识快照，仍可能补充并发布缺失的示例文档。已发布的同名示例文档会跳过。也可由知识库管理员手动上传，再由系统管理员发布。没有已发布的知识快照时，知识问答会提示 `knowledge_snapshot_unavailable`，表示尚无可用知识，而非模型已完成检索。

### 6.5 Token 轮换

1. 生成 new token，并在 Runtime 终端设置 current 为 new、`SEE_AGENT_RUNTIME_SERVICE_TOKEN_PREVIOUS` 为 old，然后重启 Runtime。
2. 将 Backend 的 current token 改为 new 并重启 Backend；它只发送 current token。
3. 执行 `make fullstack-smoke` 和一次受保护 Runtime 请求确认正常。
4. 在短暂兼容窗口后移除 previous 并再次重启 Runtime。previous 不得为空、占位、短于 32 字符或等于 current。

### 6.6 启动控制台判读

Backend 启动后应显示：

- Postgres/Memory repository；
- Redis/Local queue 与 session store；
- AI Runtime、对象存储和 K3 delivery；
- active Agent config 的 `model_id`；
- active Retrieval config 的 strategy 与 embedding model。

Runtime 启动后应显示：

- Qdrant/Memory vector store 与 collection；
- active retrieval strategy；
- embedding provider/model；
- generation provider endpoint、默认模型提示与 fallback 状态；
- local/S3 object resolver。

所有 endpoint 都必须脱敏。`active model_id` 表示 Backend 将给新会话绑定的模型；Runtime 的 generation provider 表示实际可接收该模型请求的服务，两者必须同时符合预期。启动摘要的 `configured/initialized` 不替代下方 health/smoke：模型真实连通仍以 `make local-model-smoke` 为准。

## 7. 健康检查

### 7.0 统一自检入口

- 配置级自检：
  - `make infra-check`
- 已启动容器健康检查：
  - `RUNNING=1 make infra-check`

### 7.1 基础依赖

- Postgres:
  - `docker compose -f infra/compose/docker-compose.yml exec postgres pg_isready -U see_agent -d see_agent`
- Redis:
  - `docker compose -f infra/compose/docker-compose.yml exec redis redis-cli ping`
- MinIO:
  - `curl http://localhost:19000/minio/health/live`
- Qdrant:
  - `curl http://localhost:16333/healthz`

### 7.2 应用服务

- AI Runtime:
  - `curl http://localhost:8000/healthz`
- Backend:
  - `curl http://localhost:8080/healthz`
- Web:
  - `curl http://localhost:3000`

持久化全栈最小验收：

```bash
RUNNING=1 make infra-check
make local-model-smoke
WEB_BASE_URL=http://127.0.0.1:3000 make web-ui-smoke
```

### 7.3 本地模型

- Ollama:
  - `curl http://localhost:11434/api/tags`
- 真实模型适配层：
  - `make local-model-smoke`

## 8. 常见联调问题

### 8.1 Runtime 连不上本地模型

优先检查：

- `LOCAL_LLM_BASE_URL` 是否指向 `http://localhost:11434/v1`
- `ollama serve` 是否已启动
- `qwen3:14b` 是否已拉取，并与生效的 Agent Config `model_id` 一致
- 启动控制台的 generation provider 与模型端点是否匹配；`unavailable` 表示端点未配置

### 8.2 MinIO 能启动但上传失败

优先检查：

- `OBJECT_STORE_ENDPOINT`
- bucket 名称
- 访问凭证是否和服务配置一致
- 全局代理是否劫持了 `localhost:19000`；Backend 和 Runtime 的对象存储 client 默认不读取代理环境变量，但手动 curl / 第三方工具仍可能受影响

### 8.3 Qdrant 可访问但检索为空

优先检查：

- collection 是否已创建
- `RUNTIME_RETRIEVAL_STRATEGY` 是否为 `native_dense_v1`，以及生效的检索配置是否匹配
- native dense embedding provider、`EMBEDDING_DIMENSIONS` 是否与 collection 配置一致
- 是否误把既有 `see_agent_chunks` 作为 native dense collection；应使用新 collection 并重新摄取
- `index_version` 是否与当前摄取结果匹配

### 8.4 Redis 队列已启动但摄取不前进

优先检查：

- `REDIS_URL` 是否指向宿主机端口 `redis://localhost:16379/0`
- `REDIS_INGESTION_QUEUE` 是否和 Backend 配置一致
- Backend 启动日志中是否因 Redis Ping 失败退出
- Runtime `/healthz` 是否可访问；队列消费成功但 runtime 不可达时，任务会进入 `failed` 并带 `runtime_unavailable`

### 8.5 Redis session 已配置但登录态重启后丢失

优先检查：

- `AUTH_SESSION_STORE` 是否为 `redis`，或是否已配置 `REDIS_URL` 触发默认 Redis session
- `REDIS_SESSION_PREFIX` 是否和重启前一致
- Backend 启动日志中是否因 Redis Ping 失败退出
- 浏览器是否仍带 `see_agent_session` HttpOnly cookie；`see_agent_role` 仅用于前端体验，不能代表后端授权

