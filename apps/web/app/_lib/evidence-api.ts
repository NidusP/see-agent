import type { AnswerDisposition, Coverage, SourceLocator } from "./chat-api";
import { requestJSON } from "./request-json";

export type ClarificationResolutionAction = "resolved" | "abandoned" | "needs_more";

export interface Clarification {
  readonly missing_dimensions: readonly string[];
  readonly question: string;
}

export interface ClarificationResolution {
  readonly pending_clarification_id: string;
  readonly pending_state_version: number;
  readonly action: ClarificationResolutionAction;
  readonly resolved_query?: string;
}

export interface RetrievalCandidate {
  readonly chunk_id: string;
  readonly file_id: string;
  readonly document_id: string;
  readonly revision_id: string;
  readonly chunk_content_sha256: string;
  readonly knowledge_snapshot_id: string;
  readonly rank: number;
  readonly score: number;
  readonly source_locator: SourceLocator;
}

export interface ExecutionObservation {
  readonly policy: "performed" | "not_run";
  readonly policy_provider?: string;
  readonly policy_model_id?: string;
  readonly policy_call_count?: number;
  readonly retrieval: {
    readonly status: "performed" | "not_run";
    readonly strategy?: string;
    readonly embedding_model?: string;
    readonly collection?: string;
    readonly candidate_count?: number;
  };
  readonly generation: {
    readonly status: "performed" | "not_run";
    readonly provider?: string;
    readonly model_id?: string;
  };
  readonly fallback_used: boolean;
}

export interface MessageExecutionEvidence {
  message_id: string;
  conversation_id: string;
  request_id: string;
  config_version: string;
  retrieval_config_version: string;
  knowledge_snapshot_id?: string;
  knowledge_snapshot_manifest_hash?: string;
  freshness_status?: string;
  model_id: string;
  generation_config: Record<string, unknown>;
  retrieval_config: Record<string, unknown>;
  usage?: Record<string, unknown>;
  refusal: boolean;
  finish_reason: string;
  completed_at: string;
  intent: "knowledge_query" | "social_greeting" | "social_thanks" | "social_farewell" | "capability_help" | "off_topic" | "out_of_scope";
  context_resolution?: {
    intent: "knowledge_query" | "social_greeting" | "social_thanks" | "social_farewell" | "capability_help" | "off_topic" | "out_of_scope";
    route: "rag" | "controlled_reply" | "clarification";
    kind: "follow_up" | "standalone" | "topic_shift" | "clarification_required";
    resolved_query?: string;
    basis_message_ids: string[];
    router_policy_version?: "conversation_router_v1";
    resolver_provider?: string;
    resolver_model_id?: string;
    resolver_performed: boolean;
    confidence: number;
  };
  retrieval_performed: boolean;
  generation_performed: boolean;
  knowledge_used: boolean;
  readonly answer_disposition?: AnswerDisposition;
  readonly answer_policy_version?: string;
  readonly policy_evaluation_performed?: boolean;
  readonly answer_generation_performed?: boolean;
  readonly coverage?: Coverage;
  readonly clarification?: Clarification;
  readonly resolution?: ClarificationResolution;
  readonly resolved_query?: string;
  readonly execution?: ExecutionObservation;
  readonly retrieval_candidates?: readonly RetrievalCandidate[];
  readonly used_citation_ranks?: readonly number[];
  citations?: Array<{
    rank: number;
    filename: string;
    document_id?: string;
    revision_id?: string;
    chunk_id: string;
    score: number;
    citation_label: string;
  }>;
}

export interface MessageExecutionEvidencePage {
  items: MessageExecutionEvidence[];
  next_cursor?: string;
}

export interface EvidenceFilters {
  conversation_id?: string;
  snapshot_id?: string;
  revision_id?: string;
}

export interface KnowledgeDeliveryEvidence {
  delivery_id: string;
  topic: string;
  document_id?: string;
  revision_id?: string;
  snapshot_id?: string;
  status: string;
  attempt_count: number;
  available_at: string;
  last_error?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
}

export interface EvalReport {
  report_id: string;
  dataset_id: string;
  model_id: string;
  config_version: string;
  retrieval_config_version: string;
  generation_config: Record<string, unknown>;
  metrics: Record<string, unknown>;
  completed_at: string;
  created_at: string;
}

export function listExecutionEvidence(filters: EvidenceFilters = {}): Promise<MessageExecutionEvidencePage> {
  const query = new URLSearchParams({ limit: "20" });
  for (const [key, value] of Object.entries(filters)) {
    if (value?.trim()) {
      query.set(key, value.trim());
    }
  }
  return requestJSON<MessageExecutionEvidencePage>(`/api/admin/evidence/executions?${query}`);
}

export function getMessageExecutionEvidence(messageID: string): Promise<{ evidence: MessageExecutionEvidence }> {
  return requestJSON<{ evidence: MessageExecutionEvidence }>(`/api/admin/evidence/messages/${encodeURIComponent(messageID)}`);
}

export function listKnowledgeDeliveryEvidence(): Promise<{ items: KnowledgeDeliveryEvidence[] }> {
  return requestJSON<{ items: KnowledgeDeliveryEvidence[] }>("/api/admin/evidence/deliveries?limit=8");
}

export function listEvalReports(): Promise<{ items: EvalReport[] }> {
  return requestJSON<{ items: EvalReport[] }>("/api/eval/reports?limit=8");
}
