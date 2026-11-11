import { requestJSON } from "./request-json";

export interface QualityFeedbackEvidenceTarget {
  readonly intent: string;
  readonly answer_disposition?: string;
}

export const answerFeedbackKinds = [
  "correct",
  "incorrect",
  "should_refuse",
  "missing_evidence",
  "wrong_evidence",
  "outdated_knowledge",
  "knowledge_gap",
  "should_clarify",
  "unnecessary_clarification",
  "incomplete_coverage",
] as const;

export type AnswerFeedbackKind = (typeof answerFeedbackKinds)[number];

export const a1DispositionFeedbackKinds = [
  "should_clarify",
  "unnecessary_clarification",
  "incomplete_coverage",
] as const;

export function isA1DispositionFeedbackKind(kind: AnswerFeedbackKind) {
  return a1DispositionFeedbackKinds.includes(kind as (typeof a1DispositionFeedbackKinds)[number]);
}

export function answerFeedbackKindsForEvidence(target?: QualityFeedbackEvidenceTarget): AnswerFeedbackKind[] {
  if (!target) {
    return answerFeedbackKinds.filter((kind) => !isA1DispositionFeedbackKind(kind));
  }

  return answerFeedbackKinds.filter((kind) => {
    if (!isA1DispositionFeedbackKind(kind)) {
      return true;
    }
    if (target.intent !== "knowledge_query") {
      return false;
    }
    switch (kind) {
      case "should_clarify":
        return ["grounded_answer", "partial_answer", "insufficient_evidence"].includes(target.answer_disposition ?? "");
      case "unnecessary_clarification":
        return target.answer_disposition === "clarification_required";
      case "incomplete_coverage":
        return ["grounded_answer", "partial_answer"].includes(target.answer_disposition ?? "");
    }
  });
}

export interface AnswerFeedback {
  feedback_id: string;
  message_id: string;
  conversation_id: string;
  actor_id: string;
  kind: AnswerFeedbackKind;
  comment?: string;
  corrected_answer?: string;
  expected_citation_ranks?: number[];
  created_at: string;
}

export const qualityExperimentStatuses = [
  "draft",
  "collecting_feedback",
  "ready_for_eval",
  "evaluating",
  "review_ready",
  "approved",
  "published",
  "cancelled",
  "failed",
  "rejected",
  "superseded",
] as const;

export type QualityExperimentStatus = (typeof qualityExperimentStatuses)[number];

export interface QualityExperiment {
  experiment_id: string;
  status: QualityExperimentStatus;
  created_by: string;
  baseline_config_version: string;
  baseline_retrieval_config_version: string;
  candidate_config_version: string;
  candidate_retrieval_config_version: string;
  knowledge_snapshot_id: string;
  knowledge_snapshot_manifest_hash: string;
  dataset_id: string;
  dataset_sha256: string;
  conditions: Record<string, unknown>;
  conditions_sha256: string;
  acceptance: Record<string, unknown>;
  feedback_ids?: string[];
  baseline_report_id?: string;
  candidate_report_id?: string;
  decision_reason?: string;
  release_id?: string;
  created_at: string;
  frozen_at?: string;
  approved_at?: string;
  published_at?: string;
}

export interface QualityRelease {
  release_id: string;
  status: "published" | "rolled_back";
  config_version: string;
  retrieval_config_version: string;
  experiment_id?: string;
  previous_release_id?: string;
  created_by: string;
  created_at: string;
}

export interface CreateQualityExperimentRequest {
  baseline_config_version: string;
  baseline_retrieval_config_version: string;
  candidate_config_version: string;
  candidate_retrieval_config_version: string;
  knowledge_snapshot_id: string;
  knowledge_snapshot_manifest_hash: string;
  dataset_id: string;
  dataset_sha256: string;
  conditions: Record<string, unknown>;
  acceptance: Record<string, unknown>;
}

export interface EvalReportDetail {
  report_id: string;
  dataset_id: string;
  model_id: string;
  config_version: string;
  retrieval_config_version: string;
  generation_config: Record<string, unknown>;
  metrics: Record<string, unknown>;
  completed_at: string;
  report_payload: Record<string, unknown>;
  created_at: string;
}

export function listQualityFeedback(messageID?: string) {
  const query = new URLSearchParams({ limit: "20" });
  if (messageID) query.set("message_id", messageID);
  return requestJSON<{ items: AnswerFeedback[] }>(`/api/admin/quality/feedback?${query}`);
}

export function createQualityFeedback(payload: {
  message_id: string;
  kind: AnswerFeedbackKind;
  comment?: string;
  corrected_answer?: string;
  expected_citation_ranks?: number[];
}) {
  return qualityRequest<{ feedback: AnswerFeedback }>("/api/admin/quality/feedback", payload);
}

export function listQualityExperiments() {
  return requestJSON<{ items: QualityExperiment[] }>("/api/admin/quality/experiments?limit=20");
}

export function getQualityExperiment(experimentID: string) {
  return requestJSON<{ experiment: QualityExperiment }>(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}`);
}

export function createQualityExperiment(payload: CreateQualityExperimentRequest) {
  return qualityRequest<{ experiment: QualityExperiment }>("/api/admin/quality/experiments", payload);
}

export function attachQualityExperimentFeedback(experimentID: string, feedbackID: string) {
  return qualityRequest<{ experiment: QualityExperiment }>(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}/feedback`, { feedback_id: feedbackID });
}

export function freezeQualityExperiment(experimentID: string) {
  return qualityRequest<{ experiment: QualityExperiment }>(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}/freeze`);
}

export function approveQualityExperiment(experimentID: string, reason?: string) {
  return qualityRequest<{ experiment: QualityExperiment }>(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}/approve`, reason ? { reason } : {});
}

export function publishQualityExperiment(experimentID: string) {
  return qualityRequest<{ release: QualityRelease }>(`/api/admin/quality/experiments/${encodeURIComponent(experimentID)}/publish`);
}

export function listQualityReleases() {
  return requestJSON<{ items: QualityRelease[] }>("/api/admin/quality/releases?limit=20");
}

export function rollbackQualityRelease(releaseID: string) {
  return qualityRequest<{ release: QualityRelease }>(`/api/admin/quality/releases/${encodeURIComponent(releaseID)}/rollback`);
}

export function getEvalReport(reportID: string) {
  return requestJSON<{ report: EvalReportDetail }>(`/api/eval/reports/${encodeURIComponent(reportID)}`);
}

async function qualityRequest<T>(url: string, payload: object = {}) {
  return requestJSON<T>(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": newIdempotencyKey(),
    },
    body: JSON.stringify(payload),
  });
}

function newIdempotencyKey() {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
