"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";

import {
  formatDateTime,
  getAgentConfig,
  getRetrievalConfig,
  listConfigVersions,
  updateAgentConfig,
  updateRetrievalConfig,
  ApiError as ConfigApiError,
  type AgentConfig,
  type RetrievalConfig,
} from "../_lib/config-api";
import { ApiError as RequestApiError } from "../_lib/request-json";
import { recoverFromUnauthenticated } from "../_lib/auth-recovery";
import {
  getMessageExecutionEvidence,
  listEvalReports,
  listExecutionEvidence,
  listKnowledgeDeliveryEvidence,
  type EvalReport,
  type EvidenceFilters,
  type KnowledgeDeliveryEvidence,
  type MessageExecutionEvidence,
} from "../_lib/evidence-api";
import {
  answerFeedbackKindsForEvidence,
  approveQualityExperiment,
  attachQualityExperimentFeedback,
  createQualityExperiment,
  createQualityFeedback,
  freezeQualityExperiment,
  getEvalReport,
  getQualityExperiment,
  listQualityExperiments,
  listQualityFeedback,
  listQualityReleases,
  publishQualityExperiment,
  rollbackQualityRelease,
  isA1DispositionFeedbackKind,
  type AnswerFeedback,
  type AnswerFeedbackKind,
  type EvalReportDetail,
  type QualityExperiment,
  type QualityRelease,
} from "../_lib/quality-api";
import {
  activateKnowledgeSnapshot,
  createIdempotencyKey,
  formatDateTime as formatKnowledgeDateTime,
  getKnowledgeDocument,
  listKnowledgeDocuments,
  prepareKnowledgePublish,
  type KnowledgeDocumentDetailResponse,
  type KnowledgeDocumentListItem,
  type KnowledgeRevision,
  type KnowledgeSnapshotSummary,
} from "../_lib/knowledge-api";
import { PrepublishPanel } from "./prepublish-panel";
import { Button } from "./ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./ui/alert-dialog";
import { Input } from "./ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "./ui/select";
import { Switch } from "./ui/switch";
import { Textarea } from "./ui/textarea";

interface AgentFormState {
  modelID: string;
  systemPromptVersion: string;
  responseStyle: string;
  temperature: string;
  topP: string;
  maxOutputTokens: string;
}

interface RetrievalFormState {
  retrievalStrategy: "native_dense_v1";
  embeddingModel: string;
  topK: string;
  rerankEnabled: boolean;
  evidenceMinScore: string;
}

interface EvidenceFilterState {
  conversationID: string;
  snapshotID: string;
  revisionID: string;
}

type EvidenceDomain = "execution" | "delivery" | "evaluation";
type EvidenceAvailability = Partial<Record<EvidenceDomain, string>>;

interface ConfigChange {
  label: string;
  before: string;
  after: string;
}

interface ConfigContextValue {
  agentConfig?: AgentConfig;
  retrievalConfig?: RetrievalConfig;
  configVersions: AgentConfig[];
  retrievalConfigVersions: RetrievalConfig[];
  executionEvidence: MessageExecutionEvidence[];
  deliveryEvidence: KnowledgeDeliveryEvidence[];
  evalReports: EvalReport[];
  selectedEvidence?: MessageExecutionEvidence;
  agentForm: AgentFormState;
  retrievalForm: RetrievalFormState;
  evidenceFilters: EvidenceFilterState;
  loading: boolean;
  refreshing: boolean;
  savingAgent: boolean;
  savingRetrieval: boolean;
  evidenceLoading: boolean;
  evidenceAvailability: EvidenceAvailability;
  error?: string;
  successMessage?: string;
  lastLoadedAt?: string;
  setAgentForm: (value: AgentFormState) => void;
  setRetrievalForm: (value: RetrievalFormState) => void;
  setEvidenceFilters: (value: EvidenceFilterState) => void;
  refresh: () => Promise<void>;
  saveAgentConfig: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  saveRetrievalConfig: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  queryEvidence: (event: FormEvent<HTMLFormElement>) => Promise<void>;
  selectEvidence: (messageID: string) => Promise<void>;
  handleUnauthenticated: (error: unknown) => boolean;
}

const ConfigContext = createContext<ConfigContextValue | null>(null);

const emptyAgentForm: AgentFormState = {
  modelID: "",
  systemPromptVersion: "",
  responseStyle: "concise",
  temperature: "0.2",
  topP: "1",
  maxOutputTokens: "1024",
};

const emptyRetrievalForm: RetrievalFormState = {
  retrievalStrategy: "native_dense_v1",
  embeddingModel: "",
  topK: "6",
  rerankEnabled: false,
  evidenceMinScore: "0.15",
};

const emptyEvidenceFilters: EvidenceFilterState = {
  conversationID: "",
  snapshotID: "",
  revisionID: "",
};

export function ConfigWorkspaceProvider({ children }: { children: ReactNode }) {
  const [agentConfig, setAgentConfig] = useState<AgentConfig>();
  const [retrievalConfig, setRetrievalConfig] = useState<RetrievalConfig>();
  const [configVersions, setConfigVersions] = useState<AgentConfig[]>([]);
  const [retrievalConfigVersions, setRetrievalConfigVersions] = useState<RetrievalConfig[]>([]);
  const [executionEvidence, setExecutionEvidence] = useState<MessageExecutionEvidence[]>([]);
  const [deliveryEvidence, setDeliveryEvidence] = useState<KnowledgeDeliveryEvidence[]>([]);
  const [evalReports, setEvalReports] = useState<EvalReport[]>([]);
  const [selectedEvidence, setSelectedEvidence] = useState<MessageExecutionEvidence>();
  const [agentForm, setAgentForm] = useState<AgentFormState>(emptyAgentForm);
  const [retrievalForm, setRetrievalForm] = useState<RetrievalFormState>(emptyRetrievalForm);
  const [evidenceFilters, setEvidenceFilters] = useState<EvidenceFilterState>(emptyEvidenceFilters);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [savingAgent, setSavingAgent] = useState(false);
  const [savingRetrieval, setSavingRetrieval] = useState(false);
  const [evidenceLoading, setEvidenceLoading] = useState(false);
  const [evidenceAvailability, setEvidenceAvailability] = useState<EvidenceAvailability>({});
  const [error, setError] = useState<string>();
  const [successMessage, setSuccessMessage] = useState<string>();
  const [lastLoadedAt, setLastLoadedAt] = useState<string>();

  const recoverUnauthenticated = useCallback(async () => {
    await recoverFromUnauthenticated(currentConfigPath());
  }, []);

  const handleUnauthenticated = useCallback((requestError: unknown) => {
    if (!isUnauthenticatedError(requestError)) {
      return false;
    }
    void recoverUnauthenticated();
    return true;
  }, [recoverUnauthenticated]);

  const refresh = useCallback(async (options?: { silent?: boolean }) => {
    if (options?.silent) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    setError(undefined);

    try {
      const [agentResponse, retrievalResponse, versionsResponse] = await Promise.all([
        getAgentConfig(),
        getRetrievalConfig(),
        listConfigVersions(),
      ]);
      const evidenceResults = await Promise.allSettled([
        listExecutionEvidence(),
        listKnowledgeDeliveryEvidence(),
        listEvalReports(),
      ]);
      setAgentConfig(agentResponse.config);
      setRetrievalConfig(retrievalResponse.config);
      setConfigVersions(versionsResponse.items);
      setRetrievalConfigVersions(versionsResponse.retrieval_items ?? []);
      const availability: EvidenceAvailability = {};
      const [executionResult, deliveryResult, evaluationResult] = evidenceResults;
      if (executionResult.status === "fulfilled") {
        setExecutionEvidence(executionResult.value.items);
        setSelectedEvidence(executionResult.value.items[0]);
      } else {
        availability.execution = readError(executionResult.reason);
      }
      if (deliveryResult.status === "fulfilled") {
        setDeliveryEvidence(deliveryResult.value.items);
      } else {
        availability.delivery = readError(deliveryResult.reason);
      }
      if (evaluationResult.status === "fulfilled") {
        setEvalReports(evaluationResult.value.items);
      } else {
        availability.evaluation = readError(evaluationResult.reason);
      }
      setEvidenceAvailability(availability);
      setAgentForm(formFromAgentConfig(agentResponse.config));
      setRetrievalForm(formFromRetrievalConfig(retrievalResponse.config));
      setLastLoadedAt(new Date().toISOString());
    } catch (refreshError) {
      if (handleUnauthenticated(refreshError)) {
        return;
      }
      setError(readError(refreshError));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [handleUnauthenticated]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void refresh();
    }, 0);

    return () => window.clearTimeout(timer);
  }, [refresh]);

  const saveAgentConfig = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      setError(undefined);
      setSuccessMessage(undefined);

      try {
        const payload = parseAgentForm(agentForm);
        if (agentConfig && sameAgentConfig(payload, agentConfig)) {
          setSuccessMessage("未检测到 Agent 配置变更，不会创建候选版本。");
          return;
        }
        setSavingAgent(true);
        const response = await updateAgentConfig(payload);
        setAgentConfig(response.config);
        setAgentForm(formFromAgentConfig(response.config));
        const versionsResponse = await listConfigVersions();
        setConfigVersions(versionsResponse.items);
        setRetrievalConfigVersions(versionsResponse.retrieval_items ?? []);
        setSuccessMessage(`Agent 配置候选已保存为 ${response.config.config_version}。`);
      } catch (saveError) {
        if (handleUnauthenticated(saveError)) {
          return;
        }
        setError(readError(saveError));
      } finally {
        setSavingAgent(false);
      }
    },
    [agentConfig, agentForm, handleUnauthenticated],
  );

  const saveRetrievalConfig = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      setError(undefined);
      setSuccessMessage(undefined);

      try {
        const payload = parseRetrievalForm(retrievalForm);
        if (retrievalConfig && sameRetrievalConfig(payload, retrievalConfig)) {
          setSuccessMessage("未检测到检索配置变更，不会创建候选版本。");
          return;
        }
        setSavingRetrieval(true);
        const response = await updateRetrievalConfig(payload);
        setRetrievalConfig(response.config);
        setRetrievalForm(formFromRetrievalConfig(response.config));
        const versionsResponse = await listConfigVersions();
        setConfigVersions(versionsResponse.items);
        setRetrievalConfigVersions(versionsResponse.retrieval_items ?? []);
        setSuccessMessage(`检索配置候选已保存为 ${response.config.retrieval_config_version}。`);
      } catch (saveError) {
        if (handleUnauthenticated(saveError)) {
          return;
        }
        setError(readError(saveError));
      } finally {
        setSavingRetrieval(false);
      }
    },
    [retrievalConfig, retrievalForm, handleUnauthenticated],
  );

  const queryEvidence = useCallback(async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setEvidenceLoading(true);
    setError(undefined);
    setEvidenceAvailability((current) => ({ ...current, execution: undefined }));
    try {
      const filters: EvidenceFilters = {
        conversation_id: evidenceFilters.conversationID,
        snapshot_id: evidenceFilters.snapshotID,
        revision_id: evidenceFilters.revisionID,
      };
      const response = await listExecutionEvidence(filters);
      setExecutionEvidence(response.items);
      setSelectedEvidence(response.items[0]);
    } catch (queryError) {
      if (handleUnauthenticated(queryError)) {
        return;
      }
      setEvidenceAvailability((current) => ({ ...current, execution: readError(queryError) }));
    } finally {
      setEvidenceLoading(false);
    }
  }, [evidenceFilters, handleUnauthenticated]);

  const selectEvidence = useCallback(async (messageID: string) => {
    setEvidenceLoading(true);
    setError(undefined);
    try {
      const response = await getMessageExecutionEvidence(messageID);
      setSelectedEvidence(response.evidence);
    } catch (selectionError) {
      if (handleUnauthenticated(selectionError)) {
        return;
      }
      setError(readError(selectionError));
    } finally {
      setEvidenceLoading(false);
    }
  }, [handleUnauthenticated]);

  const value = useMemo<ConfigContextValue>(
    () => ({
      agentConfig,
      retrievalConfig,
      configVersions,
      retrievalConfigVersions,
      executionEvidence,
      deliveryEvidence,
      evalReports,
      selectedEvidence,
      agentForm,
      retrievalForm,
      evidenceFilters,
      loading,
      refreshing,
      savingAgent,
      savingRetrieval,
      evidenceLoading,
      evidenceAvailability,
      error,
      successMessage,
      lastLoadedAt,
      setAgentForm,
      setRetrievalForm,
      setEvidenceFilters,
      refresh: () => refresh({ silent: true }),
      saveAgentConfig,
      saveRetrievalConfig,
      queryEvidence,
      selectEvidence,
      handleUnauthenticated,
    }),
    [
      agentConfig,
      agentForm,
      configVersions,
      deliveryEvidence,
      evalReports,
      executionEvidence,
      selectedEvidence,
      evidenceFilters,
      evidenceAvailability,
      error,
      lastLoadedAt,
      loading,
      evidenceLoading,
      handleUnauthenticated,
      queryEvidence,
      refresh,
      retrievalConfig,
      retrievalConfigVersions,
      retrievalForm,
      refreshing,
      saveAgentConfig,
      saveRetrievalConfig,
      selectEvidence,
      savingAgent,
      savingRetrieval,
      successMessage,
    ],
  );

  return <ConfigContext.Provider value={value}>{children}</ConfigContext.Provider>;
}

export function ConfigWorkspace({ onOpenDetails }: { onOpenDetails?: () => void } = {}) {
  const config = useConfigContext();
  const agentConfigChanged = hasAgentConfigChanges(config.agentForm, config.agentConfig);
  const retrievalConfigChanged = hasRetrievalConfigChanges(config.retrievalForm, config.retrievalConfig);

  return (
    <div className="content-stack">
      <header className="page-header">
        <div>
          <p className="eyebrow">配置工作区</p>
          <h2>Agent 与检索配置</h2>
        </div>
        <div className="header-chip-row">
          {onOpenDetails ? <Button type="button" variant="outline" size="sm" data-testid="open-config-details" onClick={onOpenDetails}>配置详情</Button> : null}
          <Button type="button" variant="outline" size="sm" onClick={() => void config.refresh()} disabled={config.refreshing || config.loading}>
            {config.refreshing ? "刷新中" : "刷新"}
          </Button>
          <span className="header-chip">{config.agentConfig?.config_version ?? "agent -"}</span>
          <span className="header-chip">{config.retrievalConfig?.retrieval_config_version ?? "retrieval -"}</span>
        </div>
      </header>

      {config.error ? <p className="inline-error">{config.error}</p> : null}
      {config.successMessage ? <p className="success-copy">{config.successMessage} 保存候选不会发布到线上；必须经受控实验、人工审批和发布后，才会影响之后创建的新会话。</p> : null}
      {config.loading ? <p className="loading-copy">配置加载中...</p> : null}

      <KnowledgePublishApproval />

      <section className="config-grid">
        <article className="config-card">
          <div className="section-heading">
            <h3>Agent 配置</h3>
            <span>{config.agentConfig?.config_version ?? "-"}</span>
          </div>
          <details className="parameter-guide" open>
            <summary>同一问题下，回答会怎样变化？</summary>
            <p>以下以“员工报销提交期限是多少天？”为例，仅说明可能的表达差异；知识范围、引用真实性和是否发布不由这些示例决定。</p>
            <ul>
              <li><strong>response_style</strong> 从 <code>concise</code> 改为更详细的风格，可能从“应在费用发生后 15 个自然日内提交。”扩展为“15 个自然日；逾期需说明原因并由财务复核。”；两者都必须只依据已检索证据。</li>
              <li><strong>temperature / top_p</strong> 较低时同一证据通常有更稳定的措辞和顺序；提高后可能换一种概括或段落顺序，但不会创造新的规则。一次实验只改变其中一个采样条件。</li>
              <li><strong>max_output_tokens</strong> 较小的预算可能只容纳期限结论；较大的预算可以容纳例外和审批说明。更长不代表更正确或引用更多有效证据。</li>
            </ul>
            <p className="parameter-guide-boundary"><strong>模型与 prompt：</strong>它们可能改变组织、拒答格式或表达质量，但不能补齐未上传知识；请在相同 snapshot、检索配置和问题集下比较候选。</p>
          </details>
          <form className="config-form" onSubmit={(event) => void config.saveAgentConfig(event)}>
            <label>
              <span>model_id</span>
              <Input
                data-testid="agent-model-id"
                value={config.agentForm.modelID}
                onChange={(event) => config.setAgentForm({ ...config.agentForm, modelID: event.target.value })}
                disabled={config.loading || config.savingAgent}
              />
              <small className="field-help"><strong>负责生成表达的模型。</strong>例：同一条“15 天”证据可能被组织成一句结论或带步骤的解释；它不会补齐未上传、未检索到的知识。</small>
            </label>
            <label>
              <span>system_prompt_version</span>
              <Select
                value={config.agentForm.systemPromptVersion}
                onValueChange={(value) => config.setAgentForm({ ...config.agentForm, systemPromptVersion: value ?? "" })}
                disabled={config.loading || config.savingAgent}
              >
                <SelectTrigger data-testid="agent-system-prompt-version" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="prompt_v1">prompt_v1</SelectItem>
                  <SelectItem value="prompt_v2">prompt_v2</SelectItem>
                </SelectContent>
              </Select>
              <small className="field-help"><strong>证据与拒答规则版本。</strong>例：不同版本可能改变“证据不足”的说明格式或引用组织；变更后要与既有版本进行受控比较，不能直接当作线上发布。</small>
            </label>
            <label>
              <span>response_style</span>
              <Input
                value={config.agentForm.responseStyle}
                onChange={(event) => config.setAgentForm({ ...config.agentForm, responseStyle: event.target.value })}
                disabled={config.loading || config.savingAgent}
              />
              <small className="field-help"><strong>答案表达方式。</strong>例：简洁模式先给“15 个自然日”，详细模式可补充逾期处理；不改变知识范围或引用真实性。</small>
            </label>
            <label>
              <span>temperature</span>
              <Input
                type="number"
                step="0.1"
                min="0"
                max="2"
                value={config.agentForm.temperature}
                onChange={(event) => config.setAgentForm({ ...config.agentForm, temperature: event.target.value })}
                disabled={config.loading || config.savingAgent}
              />
              <small className="field-help"><strong>生成随机性。</strong>例：从低值提高后，同一答案的措辞、举例或段落顺序可能更有变化；不会提高事实正确性，也不能代替检索证据。</small>
            </label>
            <label>
              <span>top_p</span>
              <Input
                type="number"
                step="0.1"
                min="0"
                max="1"
                value={config.agentForm.topP}
                onChange={(event) => config.setAgentForm({ ...config.agentForm, topP: event.target.value })}
                disabled={config.loading || config.savingAgent}
              />
              <small className="field-help"><strong>候选词汇范围。</strong>例：提高后可能使用更多不同的连接词或概括方式；与 temperature 同时变化时无法归因，因此应一次只改一个。</small>
            </label>
            <label>
              <span>max_output_tokens</span>
              <Input
                type="number"
                min="1"
                value={config.agentForm.maxOutputTokens}
                onChange={(event) => config.setAgentForm({ ...config.agentForm, maxOutputTokens: event.target.value })}
                disabled={config.loading || config.savingAgent}
              />
              <small className="field-help"><strong>答案长度上限。</strong>例：较小预算可能只回答“15 天”，较大预算可解释逾期复核；过大不等于更有依据。</small>
            </label>
            <p className="loading-copy">创建时间：{config.agentConfig ? formatDateTime(config.agentConfig.created_at) : "-"}</p>
            <p className="loading-copy">保存只创建候选版本。候选不会自动替换线上；请在下方质量运营区完成实验、审批和发布。</p>
            <Button type="submit" data-testid="save-agent-config" disabled={config.loading || config.savingAgent || !agentConfigChanged}>
              {config.savingAgent ? "保存中" : agentConfigChanged ? "保存 Agent 配置" : "未检测到变更"}
            </Button>
          </form>
        </article>

        <article className="config-card">
          <div className="section-heading">
            <h3>检索配置</h3>
            <span>{config.retrievalConfig?.retrieval_config_version ?? "-"}</span>
          </div>
          <details className="parameter-guide" open>
            <summary>同一问题下，证据和拒答会怎样变化？</summary>
            <p>以下仍以报销期限问题为例。它们说明候选证据与回答资格如何变化；每一项都必须在固定知识 scope 与受控评测中验证。</p>
            <ul>
              <li><strong>top_k</strong> 从 3 提高到 8，可能让模型同时看到期限、逾期和审批片段；也可能引入无关片段。它是候选数量，不是可信度或“答案更全”的保证。</li>
              <li><strong>evidence_min_score</strong> 以 <code>0.30</code> 与 <code>0.51</code> 为例：降低边界可能让较弱匹配进入回答；提高边界会让边缘问题更早拒答。这些数值只是说明，不是可直接复制的推荐值。</li>
              <li><strong>rerank</strong> 只重排已召回的候选；它可能把直接说明“15 天”的片段排到前面，却不能从未索引内容中找出新证据。</li>
            </ul>
            <p className="parameter-guide-boundary"><strong>策略与 embedding：</strong>词法检索更依赖词面重合，语义检索更可能匹配“报销何时申报”这类改写；但策略必须与 Runtime profile 一致。更换 embedding model 不是即时调参，必须新建 collection、重索引并重新校准。</p>
          </details>
          <form className="config-form" onSubmit={(event) => void config.saveRetrievalConfig(event)}>
            <label>
              <span>retrieval_strategy</span>
              <Select
                value={config.retrievalForm.retrievalStrategy}
                onValueChange={(value) => config.setRetrievalForm({ ...config.retrievalForm, retrievalStrategy: value as RetrievalFormState["retrievalStrategy"] })}
                disabled={config.loading || config.savingRetrieval}
              >
                <SelectTrigger data-testid="retrieval-strategy" className="w-full"><SelectValue className="sr-only" /><span>{retrievalStrategyLabel()}</span></SelectTrigger>
                <SelectContent>
                  <SelectItem value="native_dense_v1">原生语义向量（native_dense_v1）</SelectItem>
                </SelectContent>
              </Select>
              <small className="field-help"><strong>选择如何找证据。</strong>语义策略可匹配“报销期限”与“什么时候申报报销”这类改写；它必须与 Runtime 已部署 profile 完全一致。</small>
            </label>
            <label>
              <span>embedding_model</span>
              <Input
                value={config.retrievalForm.embeddingModel}
                onChange={(event) => config.setRetrievalForm({ ...config.retrievalForm, embeddingModel: event.target.value })}
                disabled={config.loading || config.savingRetrieval}
              />
              <small className="field-help"><strong>向量语义空间的模型。</strong>例：更换后“申报”和“提交”的相似度可能变化；必须新建 collection、重新索引并重新校准，不能混用旧向量。</small>
            </label>
            <label>
              <span>top_k</span>
              <Input
                type="number"
                min="1"
                value={config.retrievalForm.topK}
                onChange={(event) => config.setRetrievalForm({ ...config.retrievalForm, topK: event.target.value })}
                disabled={config.loading || config.savingRetrieval}
              />
              <small className="field-help"><strong>交给生成模型的候选证据条数。</strong>例：从 3 增至 8 可同时带入期限和逾期规则，也可能加入无关片段；它不是可信度，必须以评测决定。</small>
            </label>
            <label>
              <span>evidence_min_score（证据拒答边界）</span>
              <Input
                type="number"
                min="0.0001"
                max="1"
                step="0.01"
                value={config.retrievalForm.evidenceMinScore}
                onChange={(event) => config.setRetrievalForm({ ...config.retrievalForm, evidenceMinScore: event.target.value })}
                disabled={config.loading || config.savingRetrieval}
              />
              <small className="field-help"><strong>拒答的最低证据边界。</strong>例：提高边界会让边缘匹配更早拒答，降低会允许较弱匹配进入回答；只能在相同语料、模型、索引与策略下比较。</small>
            </label>
            <label className="checkbox-row">
              <Switch
                checked={config.retrievalForm.rerankEnabled}
                onCheckedChange={(checked) => config.setRetrievalForm({ ...config.retrievalForm, rerankEnabled: checked })}
                disabled={config.loading || config.savingRetrieval}
              />
              <span>启用 rerank</span>
              <small className="field-help"><strong>对候选证据二次排序。</strong>例：可将直接写明“15 天”的片段排在概述前；它不是拒答修复开关，启用或关闭都要作为独立实验验证。</small>
            </label>
            <p className="loading-copy">保存只创建候选版本，不能跳过报告 gate、审批或发布。</p>
            <p className="loading-copy">创建时间：{config.retrievalConfig ? formatDateTime(config.retrievalConfig.created_at) : "-"}</p>
            <Button type="submit" data-testid="save-retrieval-config" disabled={config.loading || config.savingRetrieval || !retrievalConfigChanged}>
              {config.savingRetrieval ? "保存中" : retrievalConfigChanged ? "保存检索配置" : "未检测到变更"}
            </Button>
          </form>
        </article>
      </section>

      <section className="table-card evidence-query-card" data-testid="evidence-query-workspace">
        <div className="section-heading">
          <div>
            <h3>回答运行证据</h3>
            <p className="loading-copy">按会话、知识快照或 revision 追溯回答当时使用的知识与配置。</p>
          </div>
          <span>{config.executionEvidence.length} 条</span>
        </div>
        <form className="evidence-filter-form" onSubmit={(event) => void config.queryEvidence(event)}>
          <label>
            <span>conversation_id</span>
            <Input
              value={config.evidenceFilters.conversationID}
              onChange={(event) => config.setEvidenceFilters({ ...config.evidenceFilters, conversationID: event.target.value })}
              placeholder="conv_..."
            />
          </label>
          <label>
            <span>snapshot_id</span>
            <Input
              value={config.evidenceFilters.snapshotID}
              onChange={(event) => config.setEvidenceFilters({ ...config.evidenceFilters, snapshotID: event.target.value })}
              placeholder="snap_..."
            />
          </label>
          <label>
            <span>revision_id</span>
            <Input
              value={config.evidenceFilters.revisionID}
              onChange={(event) => config.setEvidenceFilters({ ...config.evidenceFilters, revisionID: event.target.value })}
              placeholder="rev_..."
            />
          </label>
          <Button type="submit" disabled={config.evidenceLoading}>{config.evidenceLoading ? "查询中" : "查询证据"}</Button>
        </form>
        <div className="evidence-result-list">
          {config.executionEvidence.map((item) => (
            <Button
              key={item.message_id}
              type="button"
              variant="ghost"
              className="evidence-list-row"
              aria-pressed={config.selectedEvidence?.message_id === item.message_id}
              onClick={() => { void config.selectEvidence(item.message_id); onOpenDetails?.(); }}
            >
              <span>
                <strong>{item.message_id}</strong>
                <small>{item.conversation_id}</small>
              </span>
              <span>
                <small>{item.knowledge_snapshot_id ?? "未使用知识库"}</small>
                <small>{formatDateTime(item.completed_at)}</small>
              </span>
            </Button>
          ))}
        </div>
        {config.evidenceAvailability.execution ? (
          <div className="inline-error" role="alert" data-testid="execution-evidence-unavailable">
            <strong>回答运行证据暂不可用。</strong>
            <p>{config.evidenceAvailability.execution}</p>
          </div>
        ) : null}
        {!config.loading && !config.evidenceLoading && !config.evidenceAvailability.execution && config.executionEvidence.length === 0 ? (
          <div className="table-empty-state">
            <h3>没有匹配的回答证据。</h3>
            <p>完成一条 PostgreSQL scoped 回答，或调整筛选条件后重试。</p>
          </div>
        ) : null}
      </section>
    </div>
  );
}

function KnowledgePublishApproval() {
  const [documents, setDocuments] = useState<KnowledgeDocumentListItem[]>([]);
  const [selectedDetail, setSelectedDetail] = useState<KnowledgeDocumentDetailResponse>();
  const [loading, setLoading] = useState(true);
  const [actionRevisionID, setActionRevisionID] = useState<string>();
  const [preparedSnapshots, setPreparedSnapshots] = useState<Record<string, KnowledgeSnapshotSummary>>({});
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();

  const pendingRevisions = useMemo(
    () =>
      documents.flatMap((document) =>
        document.current_revision?.status === "ready"
          ? [{ document, revision: document.current_revision }]
          : [],
      ),
    [documents],
  );

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const response = await listKnowledgeDocuments({ status: "active" });
      setDocuments(response.items);
    } catch (reason) {
      setError(readError(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(timer);
  }, [refresh]);

  const inspectRevision = useCallback(async (documentID: string) => {
    try {
      setSelectedDetail(await getKnowledgeDocument(documentID));
    } catch (reason) {
      setError(readError(reason));
    }
  }, []);

  const prepare = useCallback(
    async (revision: KnowledgeRevision) => {
      setActionRevisionID(revision.revision_id);
      setError(undefined);
      setNotice(undefined);
      try {
        const response = await prepareKnowledgePublish(revision.revision_id, createIdempotencyKey());
        if (response.snapshot) {
          setPreparedSnapshots((current) => ({ ...current, [revision.revision_id]: response.snapshot! }));
          setNotice(response.snapshot.acceptance?.display_label ?? "已准备待发布快照，当前问答未切换。");
        }
      } catch (reason) {
        setError(readError(reason));
      } finally {
        setActionRevisionID(undefined);
      }
    },
    [],
  );

  const activate = useCallback(
    async (revision: KnowledgeRevision, document: KnowledgeDocumentListItem, snapshot: KnowledgeSnapshotSummary, acknowledgeUnvalidated: boolean) => {
      setActionRevisionID(revision.revision_id);
      setError(undefined);
      setNotice(undefined);
      try {
        const response = await activateKnowledgeSnapshot(snapshot.snapshot_id, createIdempotencyKey(), acknowledgeUnvalidated);
        setNotice(response.snapshot?.acceptance?.display_label ?? "已完成知识发布。");
        setPreparedSnapshots((current) => {
          const next = { ...current };
          delete next[revision.revision_id];
          return next;
        });
        await Promise.all([refresh(), inspectRevision(document.document_id)]);
      } catch (reason) {
        setError(readError(reason));
      } finally {
        setActionRevisionID(undefined);
      }
    },
    [inspectRevision, refresh],
  );

  return (
    <section className="table-card knowledge-publish-approval" data-testid="knowledge-publish-approval">
      <div className="section-heading">
        <div>
          <p className="eyebrow">治理审批</p>
          <h3>知识发布审批</h3>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void refresh()} disabled={loading}>
          {loading ? "刷新中" : "刷新"}
        </Button>
      </div>
      <p className="muted-copy">知识库管理员上传并完成索引后，revision 会出现在这里。准备发布只生成待发布快照，不切换线上问答。上线验收是推荐项；未跑或未通过时仍可在确认后直接发布。停用和删除仍要求相应通过验收。配置、模型和检索策略发布仍走质量实验，不在这里处理。</p>
      {error ? <p className="inline-error" role="alert">{error}</p> : null}
      {notice ? <p className="success-copy" role="status">{notice}</p> : null}
      {loading ? <p className="loading-copy">发布队列加载中...</p> : null}
      {!loading && pendingRevisions.length === 0 ? (
        <div className="table-empty-state">
          <h4>暂无待发布 revision。</h4>
          <p>上传文件并等待投影状态变为 ready 后，会出现在这里。</p>
        </div>
      ) : (
        <div className="version-list">
          {pendingRevisions.map(({ document, revision }) => (
            <article key={revision.revision_id} className="version-row" data-testid={"pending-publish-" + revision.revision_id}>
              <div>
                <strong>{document.display_name}</strong>
                <p>{revision.filename} · {revision.revision_id}</p>
                <small>投影状态：{revision.status} · 更新时间：{formatKnowledgeDateTime(revision.updated_at)}</small>
              </div>
              <div className="quality-actions">
                <Button type="button" variant="outline" size="sm" onClick={() => void inspectRevision(document.document_id)}>
                  查看详情
                </Button>
                <Button
                  type="button"
                  size="sm"
                  data-testid={"prepare-publish-" + revision.revision_id}
                  onClick={() => void prepare(revision)}
                  disabled={actionRevisionID !== undefined}
                >
                  {actionRevisionID === revision.revision_id ? "准备中" : "准备发布"}
                </Button>
              </div>
              {preparedSnapshots[revision.revision_id] || document.pending_snapshot ? (
                <PrepublishPanel
                  mode="publish"
                  snapshot={(preparedSnapshots[revision.revision_id] ?? document.pending_snapshot)!}
                  onActivate={(acknowledgeUnvalidated) => {
                    const snapshot = preparedSnapshots[revision.revision_id] ?? document.pending_snapshot;
                    return snapshot ? activate(revision, document, snapshot, acknowledgeUnvalidated) : Promise.resolve();
                  }}
                  onReload={() => prepare(revision)}
                />
              ) : null}
            </article>
          ))}
        </div>
      )}
      {selectedDetail ? (
        <p className="loading-copy">
          当前查看：{selectedDetail.document.display_name}，共 {selectedDetail.revisions.length} 个 revision。
        </p>
      ) : null}
    </section>
  );
}

export function ConfigDetailWorkspace() {
  const config = useConfigContext();

  return (
    <div className="content-stack detail-stack">
      <header className="page-header compact">
        <div>
          <p className="eyebrow">配置详情</p>
          <h2>版本历史与风险提示</h2>
        </div>
      </header>

      <section className="risk-card">
        <h3>快照规则</h3>
        <p>配置变更只影响新会话；历史会话继续绑定自己的 `config_version`、`retrieval_config_version` 和 `knowledge_snapshot_id`。</p>
      </section>

      <section className="detail-summary">
        <h3>当前版本</h3>
        <dl className="config-list">
          <div>
            <dt>config_version</dt>
            <dd>{config.agentConfig?.config_version ?? "-"}</dd>
          </div>
          <div>
            <dt>retrieval_config_version</dt>
            <dd>{config.retrievalConfig?.retrieval_config_version ?? "-"}</dd>
          </div>
          <div>
            <dt>last_loaded</dt>
            <dd>{config.lastLoadedAt ? formatDateTime(config.lastLoadedAt) : "-"}</dd>
          </div>
        </dl>
      </section>

      <section className="table-card compact-card">
        <div className="section-heading">
          <h3>Agent 版本历史</h3>
          <span>{config.configVersions.length} 条</span>
        </div>
        <div className="version-list">
          {config.configVersions.map((version, index) => (
            <article key={version.config_version} className="version-row">
              <div>
                <strong>{version.config_version}</strong>
                <p>{version.model_id}</p>
                <VersionDiff changes={agentConfigChanges(version, config.configVersions[index - 1])} />
              </div>
              <span>{formatDateTime(version.created_at)}</span>
            </article>
          ))}
        </div>
        {!config.loading && config.configVersions.length === 0 ? (
          <div className="table-empty-state">
            <h3>暂无版本历史。</h3>
            <p>保存 Agent 配置后会生成新版本。</p>
          </div>
        ) : null}
      </section>

      <section className="table-card compact-card">
        <div className="section-heading">
          <h3>检索版本历史</h3>
          <span>{config.retrievalConfigVersions.length} 条</span>
        </div>
        <div className="version-list">
          {config.retrievalConfigVersions.map((version, index) => (
            <article key={version.retrieval_config_version} className="version-row">
              <div>
                <strong>{version.retrieval_config_version}</strong>
                <p>{version.retrieval_strategy} · {version.embedding_model}</p>
                <VersionDiff changes={retrievalConfigChanges(version, config.retrievalConfigVersions[index - 1])} />
              </div>
              <span>{formatDateTime(version.created_at)}</span>
            </article>
          ))}
        </div>
        {!config.loading && config.retrievalConfigVersions.length === 0 ? (
          <div className="table-empty-state">
            <h3>暂无检索版本历史。</h3>
            <p>保存检索配置后会生成新版本。</p>
          </div>
        ) : null}
      </section>

      <section className="table-card compact-card" data-testid="execution-evidence-detail">
        <div className="section-heading">
          <h3>回答追溯详情</h3>
          <span>{config.selectedEvidence?.message_id ?? "未选择"}</span>
        </div>
        {config.selectedEvidence ? (
          <div className="evidence-detail-stack">
            <dl className="config-list">
              <div><dt>conversation</dt><dd>{config.selectedEvidence.conversation_id}</dd></div>
              <div><dt>intent</dt><dd>{config.selectedEvidence.intent}</dd></div>
              <div><dt>回答处置</dt><dd>{answerDispositionLabel(config.selectedEvidence.answer_disposition)}</dd></div>
              <div><dt>snapshot</dt><dd>{config.selectedEvidence.knowledge_snapshot_id ?? "未使用知识库"}</dd></div>
              <div><dt>manifest</dt><dd>{config.selectedEvidence.knowledge_snapshot_manifest_hash ?? "加载详情后显示"}</dd></div>
              <div><dt>freshness</dt><dd>{config.selectedEvidence.freshness_status ?? "-"}</dd></div>
              <div><dt>agent config</dt><dd>{config.selectedEvidence.config_version}</dd></div>
              <div><dt>retrieval config</dt><dd>{config.selectedEvidence.retrieval_config_version}</dd></div>
              <div><dt>model</dt><dd>{config.selectedEvidence.model_id}</dd></div>
              <div><dt>completed</dt><dd>{formatDateTime(config.selectedEvidence.completed_at)}</dd></div>
              <div><dt>answer policy</dt><dd>{formatPerformed(config.selectedEvidence.policy_evaluation_performed)}</dd></div>
              <div><dt>retrieval</dt><dd>{config.selectedEvidence.retrieval_performed ? "已执行" : "未执行"}</dd></div>
              <div><dt>generation</dt><dd>{config.selectedEvidence.generation_performed ? "已执行" : "未执行"}</dd></div>
              <div><dt>answer generation</dt><dd>{formatPerformed(config.selectedEvidence.answer_generation_performed)}</dd></div>
              <div><dt>知识用于事实回答</dt><dd>{config.selectedEvidence.knowledge_used ? "是" : "否"}</dd></div>
            </dl>
            {config.selectedEvidence.answer_disposition === "clarification_required" && config.selectedEvidence.clarification ? (
              <section className="evidence-a1-detail" data-testid="evidence-clarification-detail">
                <h4>澄清原因</h4>
                <p>缺失维度：{config.selectedEvidence.clarification.missing_dimensions.join("、")}</p>
                <p>问题：{config.selectedEvidence.clarification.question}</p>
                {config.selectedEvidence.resolution ? <p>后续状态：{clarificationResolutionLabel(config.selectedEvidence.resolution.action)}</p> : null}
              </section>
            ) : null}
            {config.selectedEvidence.answer_disposition === "insufficient_evidence" ? <section className="evidence-a1-detail" data-testid="evidence-insufficient-evidence-detail"><h4>知识不足处置</h4><p>范围已进入检索，但当前已发布知识没有足够证据支持事实回答。</p></section> : null}
            {config.selectedEvidence.coverage ? (
              <section className="evidence-a1-detail" data-testid="evidence-coverage-detail">
                <h4>覆盖情况</h4>
                <p>本次已验证 {config.selectedEvidence.coverage.supported_count}/{config.selectedEvidence.coverage.requested_count} 项。</p>
                <p>未覆盖：{config.selectedEvidence.coverage.uncovered_description}</p>
              </section>
            ) : null}
            <div>
              <h4>实际参数</h4>
              <pre className="evidence-json">{JSON.stringify({ generation: config.selectedEvidence.generation_config, retrieval: config.selectedEvidence.retrieval_config, usage: config.selectedEvidence.usage }, null, 2)}</pre>
            </div>
            <section className="evidence-a1-detail" data-testid="retrieval-candidate-diagnostics">
              <h4>检索候选诊断</h4>
              {(config.selectedEvidence.retrieval_candidates ?? []).length > 0 ? (
                <div className="version-list">
                  {config.selectedEvidence.retrieval_candidates?.map((candidate) => (
                    <div key={`${candidate.rank}-${candidate.chunk_id}`} className="version-row">
                      <div><strong>候选 #{candidate.rank}</strong><p>{candidate.document_id} / {candidate.revision_id}</p></div>
                      <span>score {candidate.score.toFixed(2)}</span>
                    </div>
                  ))}
                </div>
              ) : <p className="loading-copy">本次没有可展示的候选诊断。</p>}
            </section>
            <div>
              <h4>引用 revisions</h4>
              <div className="version-list">
                {(config.selectedEvidence.citations ?? []).map((citation) => (
                  <article key={`${citation.rank}-${citation.chunk_id}`} className="version-row">
                    <div><strong>{citation.revision_id ?? citation.filename}</strong><p>{citation.citation_label}</p></div>
                    <span>#{citation.rank}</span>
                  </article>
                ))}
              </div>
              {config.selectedEvidence.citations === undefined ? <p className="loading-copy">点击左侧记录加载 citation revision 详情。</p> : null}
            </div>
          </div>
        ) : (
          <div className="table-empty-state">
            <h3>请选择一条回答。</h3>
            <p>详情不会读取当前 active snapshot 来改写历史 provenance。</p>
          </div>
        )}
      </section>

      <section className="table-card compact-card" data-testid="delivery-evidence-list">
        <div className="section-heading"><h3>Delivery evidence</h3><span>{config.deliveryEvidence.length} 条</span></div>
        {config.evidenceAvailability.delivery ? <div className="inline-error" role="alert" data-testid="delivery-evidence-unavailable"><strong>Delivery evidence 暂不可用。</strong><p>{config.evidenceAvailability.delivery}</p></div> : null}
        <div className="version-list">
          {config.deliveryEvidence.map((item) => (
            <article key={item.delivery_id} className="version-row">
              <div><strong>{item.delivery_id}</strong><p>{item.revision_id ?? item.snapshot_id ?? item.topic}</p></div>
              <span>{item.status}</span>
            </article>
          ))}
        </div>
        {!config.loading && !config.evidenceAvailability.delivery && config.deliveryEvidence.length === 0 ? <div className="table-empty-state"><h3>暂无 delivery evidence。</h3><p>当前没有符合条件的投递记录。</p></div> : null}
      </section>

      <section className="table-card compact-card" data-testid="eval-evidence-list">
        <div className="section-heading"><h3>评测报告登记</h3><span>{config.evalReports.length} 条</span></div>
        {config.evidenceAvailability.evaluation ? <div className="inline-error" role="alert" data-testid="eval-evidence-unavailable"><strong>评测报告暂不可用。</strong><p>{config.evidenceAvailability.evaluation}</p></div> : null}
        <div className="version-list">
          {config.evalReports.map((item) => (
            <article key={item.report_id} className="version-row">
              <div><strong>{item.report_id}</strong><p>{item.dataset_id} · {item.model_id}</p></div>
              <span>{formatDateTime(item.completed_at)}</span>
            </article>
          ))}
        </div>
        {!config.loading && !config.evidenceAvailability.evaluation && config.evalReports.length === 0 ? <div className="table-empty-state"><h3>暂无评测报告。</h3><p>尚未登记与当前权限范围匹配的报告。</p></div> : null}
      </section>

      <QualityOperationsWorkspace />
    </div>
  );
}

function VersionDiff({ changes }: { changes: ConfigChange[] | undefined }) {
  if (!changes) {
    return <p className="version-initial">初始版本</p>;
  }
  if (changes.length === 0) {
    return <p className="version-initial">字段未变化</p>;
  }
  return (
    <ul className="version-diff-list">
      {changes.map((change) => <li key={change.label}><strong>{change.label}</strong>：{change.before} → {change.after}</li>)}
    </ul>
  );
}

interface QualityExperimentFormState {
  baselineConfigVersion: string;
  baselineRetrievalConfigVersion: string;
  candidateConfigVersion: string;
  candidateRetrievalConfigVersion: string;
  knowledgeSnapshotID: string;
  knowledgeSnapshotManifestHash: string;
  datasetID: string;
  datasetSHA256: string;
  conditions: string;
  acceptance: string;
}

function QualityOperationsWorkspace() {
  const config = useConfigContext();
  const selectedEvidenceMessageID = config.selectedEvidence?.message_id;
  const handleUnauthenticated = config.handleUnauthenticated;
  const [feedback, setFeedback] = useState<AnswerFeedback[]>([]);
  const [experiments, setExperiments] = useState<QualityExperiment[]>([]);
  const [releases, setReleases] = useState<QualityRelease[]>([]);
  const [selectedExperiment, setSelectedExperiment] = useState<QualityExperiment>();
  const [baselineReport, setBaselineReport] = useState<EvalReportDetail>();
  const [candidateReport, setCandidateReport] = useState<EvalReportDetail>();
  const [feedbackKind, setFeedbackKind] = useState<AnswerFeedbackKind>("incorrect");
  const [feedbackComment, setFeedbackComment] = useState("");
  const [correctedAnswer, setCorrectedAnswer] = useState("");
  const [expectedCitationRanks, setExpectedCitationRanks] = useState("");
  const [decisionReason, setDecisionReason] = useState("");
  const [experimentForm, setExperimentForm] = useState<QualityExperimentFormState>(() => qualityExperimentFormFrom(config));
  const [loading, setLoading] = useState(true);
  const [actionLoading, setActionLoading] = useState(false);
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();
  const [confirmation, setConfirmation] = useState<{ title: string; description: string; confirmLabel: string; action: () => Promise<void> }>();
  const availableFeedbackKinds = useMemo(() => answerFeedbackKindsForEvidence(config.selectedEvidence), [config.selectedEvidence]);
  const selectedFeedbackKind = availableFeedbackKinds.includes(feedbackKind) ? feedbackKind : availableFeedbackKinds[0] ?? "incorrect";
  const isDispositionFeedback = isA1DispositionFeedbackKind(selectedFeedbackKind);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [feedbackResponse, experimentResponse, releaseResponse] = await Promise.all([
        listQualityFeedback(selectedEvidenceMessageID),
        listQualityExperiments(),
        listQualityReleases(),
      ]);
      setFeedback(feedbackResponse.items);
      setExperiments(experimentResponse.items);
      setReleases(releaseResponse.items);
    } catch (refreshError) {
      if (handleUnauthenticated(refreshError)) {
        return;
      }
      setError(`质量记录暂不可用：${readError(refreshError)}`);
    } finally {
      setLoading(false);
    }
  }, [handleUnauthenticated, selectedEvidenceMessageID]);

  useEffect(() => {
    const timer = window.setTimeout(() => void refresh(), 0);
    return () => window.clearTimeout(timer);
  }, [refresh]);

  const runAction = useCallback(async (action: () => Promise<void>) => {
    setActionLoading(true);
    setError(undefined);
    setNotice(undefined);
    try {
      await action();
      await refresh();
    } catch (actionError) {
      if (handleUnauthenticated(actionError)) {
        return;
      }
      setError(readError(actionError));
    } finally {
      setActionLoading(false);
    }
  }, [handleUnauthenticated, refresh]);

  const selectExperiment = useCallback(async (experimentID: string) => {
    setActionLoading(true);
    setError(undefined);
    try {
      const response = await getQualityExperiment(experimentID);
      const experiment = response.experiment;
      setSelectedExperiment(experiment);
      const [baseline, candidate] = await Promise.all([
        experiment.baseline_report_id ? getEvalReport(experiment.baseline_report_id).then((result) => result.report).catch((error) => { handleUnauthenticated(error); return undefined; }) : Promise.resolve(undefined),
        experiment.candidate_report_id ? getEvalReport(experiment.candidate_report_id).then((result) => result.report).catch((error) => { handleUnauthenticated(error); return undefined; }) : Promise.resolve(undefined),
      ]);
      setBaselineReport(baseline);
      setCandidateReport(candidate);
    } catch (selectionError) {
      if (handleUnauthenticated(selectionError)) {
        return;
      }
      setError(readError(selectionError));
    } finally {
      setActionLoading(false);
    }
  }, [handleUnauthenticated]);

  const submitFeedback = (event: FormEvent<HTMLFormElement>) => void runAction(async () => {
    event.preventDefault();
    if (!config.selectedEvidence) throw new Error("请先从回答运行证据中选择一条已有执行证据的回答。");
    if (isDispositionFeedback && !feedbackComment.trim()) throw new Error("请说明该回答为何应调整处置。");
    const ranks = isDispositionFeedback ? [] : parseCitationRanks(expectedCitationRanks);
    const response = await createQualityFeedback({
      message_id: config.selectedEvidence.message_id,
      kind: selectedFeedbackKind,
      comment: optionalText(feedbackComment),
      corrected_answer: optionalText(correctedAnswer),
      expected_citation_ranks: ranks.length > 0 ? ranks : undefined,
    });
    setFeedback((items) => [response.feedback, ...items]);
    setFeedbackComment("");
    setCorrectedAnswer("");
    setExpectedCitationRanks("");
    setNotice("反馈已追加到所选回答的证据链；它不会直接改写知识库、Runtime 参数或线上会话。");
  });

  const submitExperiment = (event: FormEvent<HTMLFormElement>) => void runAction(async () => {
    event.preventDefault();
    const response = await createQualityExperiment({
      baseline_config_version: requiredText(experimentForm.baselineConfigVersion, "baseline config_version"),
      baseline_retrieval_config_version: requiredText(experimentForm.baselineRetrievalConfigVersion, "baseline retrieval_config_version"),
      candidate_config_version: requiredText(experimentForm.candidateConfigVersion, "candidate config_version"),
      candidate_retrieval_config_version: requiredText(experimentForm.candidateRetrievalConfigVersion, "candidate retrieval_config_version"),
      knowledge_snapshot_id: requiredText(experimentForm.knowledgeSnapshotID, "knowledge_snapshot_id"),
      knowledge_snapshot_manifest_hash: requiredText(experimentForm.knowledgeSnapshotManifestHash, "knowledge_snapshot_manifest_hash"),
      dataset_id: requiredText(experimentForm.datasetID, "dataset_id"),
      dataset_sha256: requiredText(experimentForm.datasetSHA256, "dataset_sha256"),
      conditions: parseJSONObject(experimentForm.conditions, "冻结条件"),
      acceptance: parseJSONObject(experimentForm.acceptance, "验收门槛"),
    });
    setSelectedExperiment(response.experiment);
    setExperiments((items) => [response.experiment, ...items]);
    setNotice("候选实验已创建为草稿；候选配置仍与线上新会话隔离。请先附着审核反馈、冻结条件并等待受控 runner 登记报告。");
  });

  const attachFeedback = (feedbackID: string) => void runAction(async () => {
    if (!selectedExperiment) return;
    const response = await attachQualityExperimentFeedback(selectedExperiment.experiment_id, feedbackID);
    setSelectedExperiment(response.experiment);
    setNotice("反馈已附着到实验；只有未冻结实验可以继续收集反馈。");
  });

  const freezeExperiment = () => void runAction(async () => {
    if (!selectedExperiment) return;
    const response = await freezeQualityExperiment(selectedExperiment.experiment_id);
    setSelectedExperiment(response.experiment);
    setNotice("条件已冻结。浏览器不会运行模型；受控 runner 必须登记完全匹配的报告后才能进入审核。");
  });

  const approveExperiment = () => void runAction(async () => {
    if (!selectedExperiment) return;
    const response = await approveQualityExperiment(selectedExperiment.experiment_id, optionalText(decisionReason));
    setSelectedExperiment(response.experiment);
    setNotice("候选已获人工审批，但尚未发布；只有发布事务才会影响之后创建的会话。");
  });

  const publishExperiment = () => {
    if (!selectedExperiment) return;
    setConfirmation({
      title: "发布到未来新会话？",
      description: "发布只影响之后创建的会话，历史会话不会迁移。只有已审批候选才能继续。",
      confirmLabel: "确认发布",
      action: async () => {
        const response = await publishQualityExperiment(selectedExperiment.experiment_id);
        setNotice(`已创建发布记录 ${response.release.release_id}。仅之后创建的会话会读取该不可变配置对。`);
      },
    });
  };

  const rollbackRelease = (releaseID: string) => {
    setConfirmation({
      title: "以前滚方式回退？",
      description: "系统会创建新的回退 release，不会修改历史 release 或既有会话。",
      confirmLabel: "确认创建回退 release",
      action: async () => {
        const response = await rollbackQualityRelease(releaseID);
        setNotice(`已创建回退 release ${response.release.release_id}；它只影响之后创建的会话。`);
      },
    });
  };

  return (
    <section className="quality-operations" data-testid="quality-operations-workspace">
      <div className="section-heading">
        <div>
          <h3>质量运营与受控发布</h3>
          <p className="loading-copy">反馈先进入候选实验与受控评测；保存候选、审批和发布是三个不同步骤。</p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => void refresh()} disabled={loading || actionLoading}>{loading ? "加载中" : "刷新质量记录"}</Button>
      </div>
      {error ? <p className="inline-error">{error}</p> : null}
      {notice ? <p className="success-copy">{notice}</p> : null}

      <article className="risk-card">
        <h3>发布安全边界</h3>
        <p>单条反馈不是训练数据或发布授权；候选不会自动调参或改变线上。只有通过 gate 的报告、人工审批和原子发布共同完成后，配置对才会应用到之后创建的会话；回退会前滚创建新 release。</p>
      </article>

      <section className="quality-grid">
        <article className="table-card compact-card">
          <div className="section-heading"><h3>记录回答质量反馈</h3><span>{config.selectedEvidence?.message_id ?? "未选择回答"}</span></div>
          <p className="loading-copy">反馈永远锚定已有 assistant message 和 execution evidence。请先在“回答运行证据”中选择目标回答。</p>
          <form className="config-form" onSubmit={submitFeedback}>
            <label><span>反馈类型</span><Select value={selectedFeedbackKind} onValueChange={(value) => { const kind = value as AnswerFeedbackKind; setFeedbackKind(kind); if (isA1DispositionFeedbackKind(kind)) setExpectedCitationRanks(""); }} disabled={!config.selectedEvidence || actionLoading}><SelectTrigger data-testid="feedback-kind" className="w-full"><SelectValue className="sr-only" /><span>{qualityFeedbackLabel(selectedFeedbackKind)}</span></SelectTrigger><SelectContent>{availableFeedbackKinds.map((kind) => <SelectItem key={kind} value={kind}>{qualityFeedbackLabel(kind)}</SelectItem>)}</SelectContent></Select></label>
            <label><span>{isDispositionFeedback ? "说明（必填）" : "说明（可选）"}</span><Textarea value={feedbackComment} onChange={(event) => setFeedbackComment(event.target.value)} disabled={!config.selectedEvidence || actionLoading} required={isDispositionFeedback} /></label>
            <label><span>修正答案（可选）</span><Textarea value={correctedAnswer} onChange={(event) => setCorrectedAnswer(event.target.value)} disabled={!config.selectedEvidence || actionLoading} /></label>
            {!isDispositionFeedback ? <label><span>期望引用 rank（可选，逗号分隔）</span><Input value={expectedCitationRanks} onChange={(event) => setExpectedCitationRanks(event.target.value)} placeholder="1,2" disabled={!config.selectedEvidence || actionLoading} /></label> : null}
            <Button type="submit" disabled={!config.selectedEvidence || actionLoading || (isDispositionFeedback && !feedbackComment.trim())}>追加不可变反馈</Button>
          </form>
          <div className="version-list quality-list">
            {feedback.map((item) => <article key={item.feedback_id} className="version-row"><div><strong>{qualityFeedbackLabel(item.kind)}</strong><p>{item.comment ?? item.corrected_answer ?? item.message_id}</p></div><span>{formatDateTime(item.created_at)}</span></article>)}
          </div>
          {!loading && feedback.length === 0 ? <p className="loading-copy">所选回答暂无质量反馈。</p> : null}
        </article>

        <article className="table-card compact-card">
          <div className="section-heading"><h3>创建候选实验</h3><span>草稿不生效</span></div>
          <Button type="button" variant="outline" size="sm" onClick={() => setExperimentForm(qualityExperimentFormFrom(config))} disabled={actionLoading}>填入当前配置与所选证据 scope</Button>
          <form className="config-form quality-form" onSubmit={submitExperiment}>
            <label><span>baseline config_version</span><Input value={experimentForm.baselineConfigVersion} onChange={(event) => setExperimentForm({ ...experimentForm, baselineConfigVersion: event.target.value })} /></label>
            <label><span>baseline retrieval_config_version</span><Input value={experimentForm.baselineRetrievalConfigVersion} onChange={(event) => setExperimentForm({ ...experimentForm, baselineRetrievalConfigVersion: event.target.value })} /></label>
            <label><span>candidate config_version</span><Input value={experimentForm.candidateConfigVersion} onChange={(event) => setExperimentForm({ ...experimentForm, candidateConfigVersion: event.target.value })} /></label>
            <label><span>candidate retrieval_config_version</span><Input value={experimentForm.candidateRetrievalConfigVersion} onChange={(event) => setExperimentForm({ ...experimentForm, candidateRetrievalConfigVersion: event.target.value })} /></label>
            <label><span>knowledge_snapshot_id</span><Input value={experimentForm.knowledgeSnapshotID} onChange={(event) => setExperimentForm({ ...experimentForm, knowledgeSnapshotID: event.target.value })} /></label>
            <label><span>knowledge_snapshot_manifest_hash</span><Input value={experimentForm.knowledgeSnapshotManifestHash} onChange={(event) => setExperimentForm({ ...experimentForm, knowledgeSnapshotManifestHash: event.target.value })} /></label>
            <label><span>dataset_id</span><Input value={experimentForm.datasetID} onChange={(event) => setExperimentForm({ ...experimentForm, datasetID: event.target.value })} /></label>
            <label><span>dataset_sha256</span><Input value={experimentForm.datasetSHA256} onChange={(event) => setExperimentForm({ ...experimentForm, datasetSHA256: event.target.value })} /></label>
            <label><span>conditions（JSON object）</span><Textarea value={experimentForm.conditions} onChange={(event) => setExperimentForm({ ...experimentForm, conditions: event.target.value })} /></label>
            <label><span>acceptance（JSON object）</span><Textarea value={experimentForm.acceptance} onChange={(event) => setExperimentForm({ ...experimentForm, acceptance: event.target.value })} /></label>
            <Button type="submit" disabled={actionLoading}>创建隔离候选</Button>
          </form>
        </article>
      </section>

      <section className="table-card compact-card">
        <div className="section-heading"><div><h3>实验、报告与审批</h3><p className="loading-copy">报告由受控 runner 登记；此处只读取并对照，不提供浏览器侧模型运行。</p></div><span>{experiments.length} 个</span></div>
        <div className="evidence-result-list">
          {experiments.map((experiment) => <Button key={experiment.experiment_id} type="button" variant="ghost" className="evidence-list-row" aria-pressed={selectedExperiment?.experiment_id === experiment.experiment_id} onClick={() => void selectExperiment(experiment.experiment_id)}><span><strong>{experiment.experiment_id}</strong><small>{experiment.baseline_config_version} → {experiment.candidate_config_version}</small></span><span><small>{qualityStatusLabel(experiment.status)}</small><small>{formatDateTime(experiment.created_at)}</small></span></Button>)}
        </div>
        {!loading && experiments.length === 0 ? <div className="table-empty-state"><h3>暂无受控实验。</h3><p>先创建候选；它不会影响线上新会话。</p></div> : null}
        {selectedExperiment ? <ExperimentDetail experiment={selectedExperiment} feedback={feedback} baselineReport={baselineReport} candidateReport={candidateReport} actionLoading={actionLoading} decisionReason={decisionReason} onDecisionReasonChange={setDecisionReason} onAttachFeedback={attachFeedback} onFreeze={freezeExperiment} onApprove={approveExperiment} onPublish={publishExperiment} /> : null}
      </section>

      <section className="table-card compact-card">
        <div className="section-heading"><div><h3>不可变发布记录</h3><p className="loading-copy">发布只影响之后创建的会话。回退会创建新的前滚 release。</p></div><span>{releases.length} 条</span></div>
        <div className="version-list quality-list">
          {releases.map((release) => <article key={release.release_id} className="version-row"><div><strong>{release.release_id}</strong><p>{release.config_version} · {release.retrieval_config_version}</p><p>{release.experiment_id ? `来源实验：${release.experiment_id}` : "bootstrap / 回退记录"}</p></div><span>{release.status}<br />{formatDateTime(release.created_at)}<br /><Button type="button" variant="outline" size="sm" disabled={actionLoading} onClick={() => rollbackRelease(release.release_id)}>以前滚方式回退</Button></span></article>)}
        </div>
        {!loading && releases.length === 0 ? <div className="table-empty-state"><h3>尚无发布记录。</h3><p>候选必须先通过评测并完成审批，才能发布。</p></div> : null}
      </section>
      <AlertDialog open={Boolean(confirmation)} onOpenChange={(open) => { if (!open) setConfirmation(undefined); }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmation?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirmation?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={actionLoading}>取消</AlertDialogCancel>
            <AlertDialogAction
              disabled={actionLoading}
              onClick={() => {
                if (!confirmation) return;
                const action = confirmation.action;
                setConfirmation(undefined);
                void runAction(action);
              }}
            >
              {confirmation?.confirmLabel ?? "确认"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}

function ExperimentDetail({ experiment, feedback, baselineReport, candidateReport, actionLoading, decisionReason, onDecisionReasonChange, onAttachFeedback, onFreeze, onApprove, onPublish }: {
  experiment: QualityExperiment;
  feedback: AnswerFeedback[];
  baselineReport?: EvalReportDetail;
  candidateReport?: EvalReportDetail;
  actionLoading: boolean;
  decisionReason: string;
  onDecisionReasonChange: (value: string) => void;
  onAttachFeedback: (feedbackID: string) => void;
  onFreeze: () => void;
  onApprove: () => void;
  onPublish: () => void;
}) {
  const canCollectFeedback = experiment.status === "draft" || experiment.status === "collecting_feedback";
  return <div className="quality-experiment-detail" data-testid="quality-experiment-detail">
    <div className="section-heading"><h4>{experiment.experiment_id}</h4><span>{qualityStatusLabel(experiment.status)}</span></div>
    <dl className="config-list"><div><dt>scope</dt><dd>{experiment.knowledge_snapshot_id}</dd></div><div><dt>manifest</dt><dd>{experiment.knowledge_snapshot_manifest_hash}</dd></div><div><dt>dataset</dt><dd>{experiment.dataset_id} · {experiment.dataset_sha256}</dd></div><div><dt>conditions hash</dt><dd>{experiment.conditions_sha256}</dd></div></dl>
    <div className="report-comparison"><ReportComparison title="Baseline report" reportID={experiment.baseline_report_id} report={baselineReport} /><ReportComparison title="Candidate report" reportID={experiment.candidate_report_id} report={candidateReport} /></div>
    <p className="loading-copy">冻结条件：{JSON.stringify(experiment.conditions)}。验收门槛：{JSON.stringify(experiment.acceptance)}。</p>
    {canCollectFeedback ? <div className="button-row"><span className="loading-copy">可附着当前筛选到的反馈：</span>{feedback.map((item) => <Button key={item.feedback_id} type="button" variant="outline" size="sm" disabled={actionLoading || experiment.feedback_ids?.includes(item.feedback_id)} onClick={() => onAttachFeedback(item.feedback_id)}>{experiment.feedback_ids?.includes(item.feedback_id) ? "已附着" : `附着 ${item.feedback_id}`}</Button>)}</div> : null}
    <div className="quality-actions">
      <Button type="button" disabled={actionLoading || !canCollectFeedback} onClick={onFreeze}>冻结条件，等待受控评测</Button>
      <label><span>审批理由（可选）</span><Input value={decisionReason} onChange={(event) => onDecisionReasonChange(event.target.value)} disabled={actionLoading || experiment.status !== "review_ready"} /></label>
      <Button type="button" disabled={actionLoading || experiment.status !== "review_ready"} onClick={onApprove}>人工审批候选</Button>
      <Button type="button" disabled={actionLoading || experiment.status !== "approved"} onClick={onPublish}>发布到未来新会话</Button>
    </div>
  </div>;
}

function ReportComparison({ title, reportID, report }: { title: string; reportID?: string; report?: EvalReportDetail }) {
  return <article className="report-card"><h4>{title}</h4><p>{reportID ?? "尚未由受控 runner 登记"}</p>{report ? <><p>{report.dataset_id} · {report.model_id}</p><pre className="evidence-json">{JSON.stringify(report.metrics, null, 2)}</pre></> : reportID ? <p className="loading-copy">报告详情暂不可用。</p> : null}</article>;
}

function qualityExperimentFormFrom(config: ConfigContextValue): QualityExperimentFormState {
  return {
    baselineConfigVersion: config.agentConfig?.config_version ?? "",
    baselineRetrievalConfigVersion: config.retrievalConfig?.retrieval_config_version ?? "",
    candidateConfigVersion: "",
    candidateRetrievalConfigVersion: "",
    knowledgeSnapshotID: config.selectedEvidence?.knowledge_snapshot_id ?? "",
    knowledgeSnapshotManifestHash: config.selectedEvidence?.knowledge_snapshot_manifest_hash ?? "",
    datasetID: "",
    datasetSHA256: "",
    conditions: "{}",
    acceptance: "{}",
  };
}

function qualityFeedbackLabel(kind: AnswerFeedbackKind) {
  return ({ correct: "回答正确", incorrect: "回答错误", should_refuse: "应当拒答", missing_evidence: "缺少证据", wrong_evidence: "引用错误", outdated_knowledge: "知识已过期", knowledge_gap: "知识缺口", should_clarify: "本应澄清", unnecessary_clarification: "不应澄清", incomplete_coverage: "覆盖不完整" } as const)[kind];
}

function answerDispositionLabel(disposition?: MessageExecutionEvidence["answer_disposition"]) {
  if (!disposition) {
    return "旧记录未标注";
  }
  return ({ grounded_answer: "完整有依据回答", partial_answer: "已覆盖部分", clarification_required: "需要补充范围", insufficient_evidence: "当前知识不足", controlled_response: "受控非知识回复" } as const)[disposition];
}

function formatPerformed(value?: boolean) {
  return value === undefined ? "旧记录未标注" : value ? "已执行" : "未执行";
}

function clarificationResolutionLabel(action: "resolved" | "abandoned" | "needs_more") {
  return ({ resolved: "已补充并解决", abandoned: "已改问其他问题", needs_more: "仍需补充" } as const)[action];
}

function retrievalStrategyLabel() {
  return "原生语义向量（native_dense_v1）";
}

function qualityStatusLabel(status: QualityExperiment["status"]) {
  return ({ draft: "草稿", collecting_feedback: "收集反馈", ready_for_eval: "等待评测", evaluating: "评测中", review_ready: "等待审核", approved: "已审批", published: "已发布", cancelled: "已取消", failed: "评测失败", rejected: "已拒绝", superseded: "已被替代" } as const)[status];
}

function parseCitationRanks(value: string) {
  if (!value.trim()) return [];
  const ranks = value.split(",").map((item) => Number(item.trim()));
  if (ranks.some((rank) => !Number.isInteger(rank) || rank < 1)) throw new Error("期望引用 rank 必须是以逗号分隔的正整数。");
  return ranks;
}

function parseJSONObject(value: string, label: string) {
  try {
    const parsed: unknown = JSON.parse(value);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") throw new Error("not object");
    return parsed as Record<string, unknown>;
  } catch {
    throw new Error(`${label}必须是 JSON object。`);
  }
}

function optionalText(value: string) { return value.trim() || undefined; }
function requiredText(value: string, label: string) { const trimmed = value.trim(); if (!trimmed) throw new Error(`请填写 ${label}。`); return trimmed; }

function useConfigContext() {
  const context = useContext(ConfigContext);
  if (!context) {
    throw new Error("Config workspace must be rendered inside ConfigWorkspaceProvider");
  }
  return context;
}

function formFromAgentConfig(config: AgentConfig): AgentFormState {
  return {
    modelID: config.model_id,
    systemPromptVersion: config.system_prompt_version,
    responseStyle: config.response_style,
    temperature: String(config.temperature),
    topP: String(config.top_p),
    maxOutputTokens: String(config.max_output_tokens),
  };
}

function formFromRetrievalConfig(config: RetrievalConfig): RetrievalFormState {
  return {
    retrievalStrategy: config.retrieval_strategy,
    embeddingModel: config.embedding_model,
    topK: String(config.top_k),
    rerankEnabled: config.rerank_enabled,
    evidenceMinScore: String(config.evidence_min_score),
  };
}

function parseAgentForm(form: AgentFormState) {
  const temperature = Number(form.temperature);
  const topP = Number(form.topP);
  const maxOutputTokens = Number(form.maxOutputTokens);
  if (form.modelID.trim() === "" || form.systemPromptVersion.trim() === "" || form.responseStyle.trim() === "" || !Number.isFinite(temperature) || !Number.isFinite(topP) || topP < 0 || topP > 1 || !Number.isInteger(maxOutputTokens) || maxOutputTokens < 1) {
    throw new Error("请填写有效的 Agent 配置。");
  }
  return {
    model_id: form.modelID.trim(),
    system_prompt_version: form.systemPromptVersion.trim(),
    response_style: form.responseStyle.trim(),
    temperature,
    top_p: topP,
    max_output_tokens: maxOutputTokens,
  };
}

function parseRetrievalForm(form: RetrievalFormState) {
  const topK = Number(form.topK);
  const evidenceMinScore = Number(form.evidenceMinScore);
  if (form.embeddingModel.trim() === "" || !Number.isInteger(topK) || topK < 1 || !Number.isFinite(evidenceMinScore) || evidenceMinScore <= 0 || evidenceMinScore > 1) {
    throw new Error("请填写有效的检索配置。");
  }
  return {
    retrieval_strategy: form.retrievalStrategy,
    embedding_model: form.embeddingModel.trim(),
    top_k: topK,
    rerank_enabled: form.rerankEnabled,
    evidence_min_score: evidenceMinScore,
  };
}

function hasAgentConfigChanges(form: AgentFormState, current?: AgentConfig) {
  if (!current) return false;
  try {
    return !sameAgentConfig(parseAgentForm(form), current);
  } catch {
    return true;
  }
}

function hasRetrievalConfigChanges(form: RetrievalFormState, current?: RetrievalConfig) {
  if (!current) return false;
  try {
    return !sameRetrievalConfig(parseRetrievalForm(form), current);
  } catch {
    return true;
  }
}

function sameAgentConfig(payload: ReturnType<typeof parseAgentForm>, current: AgentConfig) {
  return payload.model_id === current.model_id
    && payload.system_prompt_version === current.system_prompt_version
    && payload.response_style === current.response_style
    && payload.temperature === current.temperature
    && payload.top_p === current.top_p
    && payload.max_output_tokens === current.max_output_tokens;
}

function sameRetrievalConfig(payload: ReturnType<typeof parseRetrievalForm>, current: RetrievalConfig) {
  return payload.retrieval_strategy === current.retrieval_strategy
    && payload.embedding_model === current.embedding_model
    && payload.top_k === current.top_k
    && payload.rerank_enabled === current.rerank_enabled
    && payload.evidence_min_score === current.evidence_min_score;
}

function agentConfigChanges(current: AgentConfig, previous?: AgentConfig): ConfigChange[] | undefined {
  if (!previous) return undefined;
  return [
    configChange("model_id", previous.model_id, current.model_id),
    configChange("system_prompt_version", previous.system_prompt_version, current.system_prompt_version),
    configChange("response_style", previous.response_style, current.response_style),
    configChange("temperature", previous.temperature, current.temperature),
    configChange("top_p", previous.top_p, current.top_p),
    configChange("max_output_tokens", previous.max_output_tokens, current.max_output_tokens),
  ].filter((change): change is ConfigChange => change !== undefined);
}

function retrievalConfigChanges(current: RetrievalConfig, previous?: RetrievalConfig): ConfigChange[] | undefined {
  if (!previous) return undefined;
  return [
    configChange("retrieval_strategy", previous.retrieval_strategy, current.retrieval_strategy),
    configChange("embedding_model", previous.embedding_model, current.embedding_model),
    configChange("top_k", previous.top_k, current.top_k),
    configChange("rerank_enabled", previous.rerank_enabled, current.rerank_enabled),
    configChange("evidence_min_score", previous.evidence_min_score, current.evidence_min_score),
  ].filter((change): change is ConfigChange => change !== undefined);
}

function configChange(label: string, before: string | number | boolean, after: string | number | boolean): ConfigChange | undefined {
  if (before === after) return undefined;
  return { label, before: formatConfigValue(before), after: formatConfigValue(after) };
}

function formatConfigValue(value: string | number | boolean) {
  return typeof value === "boolean" ? (value ? "开启" : "关闭") : String(value);
}

function readError(error: unknown) {
  if (error instanceof Error) {
    return error.message;
  }
  return "配置请求失败。";
}

function isUnauthenticatedError(error: unknown) {
  return (
    (error instanceof ConfigApiError || error instanceof RequestApiError)
    && error.code === "unauthenticated"
  );
}

function currentConfigPath() {
  return `${window.location.pathname}${window.location.search}`;
}
