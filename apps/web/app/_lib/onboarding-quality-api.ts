import { requestJSON } from "./request-json";

export interface OnboardingCase {
  case_id: string;
  dataset_id: string;
  case_type: "must_answer" | "must_refuse";
  question: string;
  expected_revision_ids?: string[];
  expected_locator_hints?: string[];
  expected_answer_points?: string[];
  review_status: "draft" | "draft_generated" | "reviewed";
  reviewed_by?: string;
  created_at: string;
  updated_at: string;
}
export interface OnboardingDataset {
  dataset_id: string;
  name: string;
  status: "draft" | "ready" | "superseded";
  knowledge_snapshot_id: string;
  knowledge_snapshot_manifest_hash: string;
  cases_sha256?: string;
  created_by: string;
  created_at: string;
  frozen_at?: string;
  cases?: OnboardingCase[];
}
export interface OnboardingCaseResult {
  case_id: string;
  passed: boolean;
  refusal: boolean;
  observed_answer: string;
  observed_citations?: Array<{ revision_id?: string; citation_label?: string; snippet?: string }>;
  expected_revision_hit?: boolean;
  expected_locator_hit?: boolean;
  answer_point_hit_rate?: number;
  failure_code?: string;
}
export interface OnboardingRun {
  run_id: string;
  dataset_id: string;
  status: "queued" | "running" | "passed" | "failed";
  knowledge_snapshot_id: string;
  knowledge_snapshot_manifest_hash: string;
  config_version: string;
  retrieval_config_version: string;
  cases_sha256: string;
  metrics?: Record<string, unknown>;
  error?: { code: string; message: string };
  created_at: string;
  started_at?: string;
  completed_at?: string;
  results?: OnboardingCaseResult[];
}

export function listOnboardingDatasets() { return requestJSON<{ items: OnboardingDataset[] }>("/api/admin/onboarding/datasets?limit=20"); }
export function getOnboardingDataset(datasetID: string) { return requestJSON<{ dataset: OnboardingDataset }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}`); }
export function createOnboardingDataset(payload: { name: string; knowledge_snapshot_id: string }) { return onboardingRequest<{ dataset: OnboardingDataset }>("/api/admin/onboarding/datasets", payload); }
export function createOnboardingCase(datasetID: string, payload: Omit<OnboardingCase, "case_id" | "dataset_id" | "created_at" | "updated_at" | "reviewed_by">) { return onboardingRequest<{ case: OnboardingCase }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/cases`, payload); }
export function freezeOnboardingDataset(datasetID: string) { return onboardingRequest<{ dataset: OnboardingDataset }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/freeze`); }
export function generateOnboardingDrafts(datasetID: string) { return onboardingRequest<{ cases: OnboardingCase[] }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/drafts`); }
export function reviewOnboardingCase(datasetID: string, caseID: string, payload: { question?: string; expected_revision_ids?: string[]; expected_locator_hints?: string[]; expected_answer_points?: string[] } = {}) { return onboardingRequest<{ case: OnboardingCase }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/cases/${encodeURIComponent(caseID)}/review`, payload); }
export function createOnboardingRun(datasetID: string) { return onboardingRequest<{ run: OnboardingRun }>(`/api/admin/onboarding/datasets/${encodeURIComponent(datasetID)}/runs`); }
export function getOnboardingRun(runID: string) { return requestJSON<{ run: OnboardingRun }>(`/api/admin/onboarding/runs/${encodeURIComponent(runID)}`); }

async function onboardingRequest<T>(url: string, payload: object = {}) {
  return requestJSON<T>(url, { method: "POST", headers: { "Content-Type": "application/json", "Idempotency-Key": globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}` }, body: JSON.stringify(payload) });
}
