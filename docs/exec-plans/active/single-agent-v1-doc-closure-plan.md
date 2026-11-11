# 单智能体 V1 文档补齐路线图

## 文档控制

- 状态：活动草案
- 版本：`v1.0`
- 目标：把“方向清楚但实现细节不足”的文档状态，推进到可支持多会话并行开发

## 1. 当前判断

当前文档体系已经具备：

- 产品范围说明
- 系统边界说明
- 工作区与目录布局说明
- 评测方向说明
- 并行开发顺序说明

当前仍然不足的部分主要是：

- 模块实现级设计不够具体
- 契约字段与错误语义还未冻结
- 基础设施、测试、评测数据仍停留在目录占位或说明级
- 各服务局部 `README` / `AGENTS.md` 还不能支撑无口头补充交接

## 1.1 当前进度

- 已完成：`D1 API 设计说明`
- 已完成：`D2 契约字段冻结` 的首版基线
- 已完成：`D3 Runtime 内部设计`
- 已完成：`D4 本地基础设施与联调说明`
- 已完成：`D5 Web 模块设计`
- 已完成：`D6 Backend 模块设计`
- 已完成：`D7 测试计划`
- 已完成：`D8 Eval 数据与报告说明`
- 下一优先级：`agent 任务模板与交接模板实例`

## 2. 完备性结论

按“是否足以支持多会话并行编码”衡量：

- 开发顺序：`较清晰`
- 模块职责：`基本清晰`
- 模块实现文档：`不完备`
- 契约文档：`不完备`
- 基础设施与测试文档：`不完备`
- 大规模并行开发就绪度：`未达标`

## 3. 缺口优先级

### A 级：必须先补，补完才能放心并行开工

1. API 与跨服务契约设计说明
2. OpenAPI 和 JSON Schema 字段冻结
3. AI Runtime 摄取与检索内部数据流设计
4. 本地基础设施编排与环境变量约定

### B 级：应在首批编码期尽快补齐

1. Web 模块页面与状态流设计
2. Backend 模块 handler / service / storage 分层设计
3. Runtime 模块 parser registry / chunking / citation formatter 结构设计
4. 集成测试与 e2e 测试方案

### C 级：可在主链路跑通后补齐

1. Prompt 资产组织与版本化说明
2. Config schema 版本化说明
3. Eval baseline 数据集扩充与报告格式说明
4. agent 任务模板与交接模板实例

## 4. 逐项补齐顺序

建议按以下顺序处理：

1. `docs/api/` 下补齐 V1 API 设计说明
2. `packages/contracts/` 下冻结 OpenAPI 与两个核心 schema
3. `services/ai-runtime-py/` 对应的实现设计文档补齐
4. `infra/compose/` 与本地联调 runbook 补齐
5. `apps/web/` 的页面与状态流文档补齐
6. `services/backend-go/` 的模块分层文档补齐
7. `tests/integration/` 与 `tests/e2e/` 测试计划补齐
8. `data/eval-datasets/` 与 harness 执行说明补齐

## 5. 文档项清单

### D1 API 设计说明

- 当前状态：仅有 `packages/contracts/openapi.yaml` 草案和 `docs/api/README.md` 占位
- 目标产物：
  - `docs/api/single-agent-v1-api-design.md`
- 完成标准：
  - 每个端点组有职责说明
  - 有统一命名规范、分页、错误码、流式返回规则
  - 聊天、知识库、配置、评测与日志至少有最小请求 / 响应对象定义
- 解锁工作：
  - `P0`
  - `P1`
  - `P3`
  - `P6`

### D2 契约字段冻结

- 当前状态：存在占位 schema，但字段不全
- 目标产物：
  - 完整化 `packages/contracts/openapi.yaml`
  - 完整化 `packages/contracts/chat-runtime-request.schema.json`
  - 完整化 `packages/contracts/ingestion-task.schema.json`
  - 必要时新增错误码与 locator schema
- 完成标准：
  - 消除关键字段歧义
  - 定义任务状态、引用对象、流式消息结构
  - Web / Backend / Runtime 无需私下补字段
- 解锁工作：
  - `P0`
  - `P1`
  - `P2`
  - `P3`
  - `P4`

### D3 Runtime 内部设计

- 当前状态：SDD 只给了方向，没有模块级实现设计
- 目标产物：
  - `docs/design-docs/single-agent-v1-runtime-ingestion-design.md`
- 完成标准：
  - 写清 parser registry、标准化中间表示、chunk schema、locator 生成规则
  - 写清 preview / run 两类入口的差异
  - 写清本地模型适配层边界
- 解锁工作：
  - `P2`
  - `P5`
  - `P7`

### D4 本地基础设施与联调说明

- 当前状态：`infra/compose/README.md` 为占位
- 目标产物：
  - `infra/compose/docker-compose.yml`
  - `docs/runbooks/local-stack-bootstrap.md`
- 完成标准：
  - Postgres / Redis / MinIO / Qdrant 端口和卷路径明确
  - AI Runtime 与本地模型的环境变量命名明确
  - 有最小启动顺序和健康检查方法
- 解锁工作：
  - `P1`
  - `P2`
  - `P4`
  - `P8`
  - `P9`

### D5 Web 模块设计

- 当前状态：只有职责描述
- 目标产物：
  - `docs/design-docs/single-agent-v1-web-ui-design.md`
- 完成标准：
  - 页面树、状态流、上传交互、引用卡片模型明确
  - 与契约字段一一对齐
- 解锁工作：
  - `P3`
  - `P6`

### D6 Backend 模块设计

- 当前状态：只有职责描述
- 目标产物：
  - `docs/design-docs/single-agent-v1-backend-design.md`
- 完成标准：
  - handler / service / repository / task dispatch 分层明确
  - 快照、配置版本、文件状态机明确
- 解锁工作：
  - `P1`
  - `P4`

### D7 测试计划

- 当前状态：目录 README 占位
- 目标产物：
  - `docs/harness/single-agent-v1-integration-test-plan.md`
- 完成标准：
  - 集成测试矩阵、e2e 冒烟路径、样例数据映射明确
- 解锁工作：
  - `P4`
  - `P6`
  - `P8`

### D8 Eval 数据与报告说明

- 当前状态：只有 seed template
- 目标产物：
  - `docs/harness/single-agent-v1-eval-baseline-plan.md`
  - 扩充后的 `data/eval-datasets/`
- 完成标准：
  - 首批 `30-50` 条题目组织方式明确
  - 报告字段和比较维度明确
- 解锁工作：
  - `P7`
  - `P10`

## 6. 当前推荐动作

当前最应该先做的是：

1. `agent 任务模板与交接模板实例`
2. `服务级 runbook 细化`
3. `实现阶段代码初始化`

原因：

- `D1` 与 `D2` 已经把 API 面和关键字段先冻结住了
- `D3` 已经把 AI Runtime 内部结构和边界补齐
- `D4` 已经补齐联调底座与启动顺序
- `D5` 已经为前端实现提供页面和状态流底稿
- `D6` 已经把 Backend 分层、状态机和编排责任补齐
- `D7` 已经把 integration / e2e 路径落成测试计划
- `D8` 已经把 eval 数据与报告规划补齐，并扩充了首批种子样例

## 7. 交付节奏建议

- 第一步：先补 `D1`
- 第二步：基于 `D1` 冻结 `D2`
- 第三步：再补 `D3`
- 第四步：补 `D4` 到 `D8`

这条顺序可以把“先边界、后字段、再模块实现、最后联调与评测”的依赖关系保持清晰。
