export type SourceFormat = "txt" | "md" | "json" | "pdf" | "docx" | "xlsx" | "pptx";
export type KnowledgeDocumentStatus = "active" | "disabled" | "deleted";
export type KnowledgeRevisionStatus =
  | "legacy_unpublished"
  | "uploaded"
  | "queued"
  | "indexing"
  | "ready"
  | "published"
  | "superseded"
  | "failed"
  | "disabled"
  | "deleted";
export type RevisionDeliveryStatus = "pending" | "streamed" | "indexing" | "retry" | "delivered" | "dead_letter";

export interface KnowledgeDocument {
  document_id: string;
  display_name: string;
  owner_id: string;
  status: KnowledgeDocumentStatus;
  created_at: string;
  updated_at: string;
}

export interface KnowledgeRevision {
  revision_id: string;
  document_id: string;
  filename: string;
  mime_type: string;
  source_format: SourceFormat;
  checksum_sha256: string;
  parse_version: string;
  index_version: string;
  embedding_model?: string;
  effective_at?: string;
  chunk_manifest_sha256?: string;
  status: KnowledgeRevisionStatus;
  created_at: string;
  updated_at: string;
}

export interface RevisionDeliverySummary {
  delivery_id: string;
  status: RevisionDeliveryStatus;
  attempt_count: number;
  last_error?: string;
  updated_at: string;
}

export interface KnowledgeAcceptance {
  status: "accepted" | "unvalidated" | "failed";
  display_label: string;
  onboarding_run_id?: string;
  requires_unvalidated_acknowledgement: boolean;
}

export interface KnowledgeSnapshotSummary {
  snapshot_id: string;
  status: "building" | "pending_publish" | "published" | "retired";
  manifest_sha256: string;
  created_at: string;
  published_at?: string;
  members?: KnowledgeSnapshotMember[];
  acceptance?: KnowledgeAcceptance;
}

export interface KnowledgeUpdateEventSummary {
  event_id: string;
  event_type: "uploaded" | "indexed" | "published" | "replaced" | "disabled" | "deleted" | "reindexed";
  document_id: string;
  revision_id?: string;
  snapshot_id?: string;
  created_at: string;
  acceptance_status?: "accepted" | "unvalidated" | "failed";
  onboarding_run_id?: string;
}

export type KnowledgeUpdateEventType =
  | "uploaded"
  | "indexed"
  | "published"
  | "replaced"
  | "disabled"
  | "deleted"
  | "reindexed";
export type KnowledgeFreshnessScanStatus = "queued" | "running" | "retry" | "completed" | "failed";

export interface KnowledgeFreshnessScan {
  event_id: string;
  status: KnowledgeFreshnessScanStatus;
  attempt_count: number;
  candidate_count: number;
  marked_count: number;
  last_error?: string;
  updated_at: string;
  completed_at?: string;
}

export interface KnowledgeUpdateEvent {
  event_id: string;
  event_type: KnowledgeUpdateEventType;
  document_id?: string;
  revision_id?: string;
  snapshot_id?: string;
  actor_id?: string;
  correlation_id?: string;
  occurred_at: string;
  acceptance_status?: "accepted" | "unvalidated" | "failed";
  onboarding_run_id?: string;
  acceptance?: KnowledgeAcceptance;
  freshness_scan?: KnowledgeFreshnessScan;
}

export interface KnowledgeUpdateListResponse {
  items: KnowledgeUpdateEvent[];
  next_cursor?: string;
}

export interface KnowledgeSnapshotMember {
  snapshot_id: string;
  revision_id: string;
  document_id: string;
  checksum_sha256: string;
}

export interface KnowledgeSnapshot extends KnowledgeSnapshotSummary {
  parent_snapshot_id?: string;
  members: KnowledgeSnapshotMember[];
}

export interface KnowledgeSnapshotDetailResponse {
  snapshot: KnowledgeSnapshot;
}

export interface KnowledgeDocumentListItem extends KnowledgeDocument {
  current_revision?: KnowledgeRevision;
  acceptance?: KnowledgeAcceptance;
  pending_snapshot?: KnowledgeSnapshot;
}

export interface KnowledgeDocumentListResponse {
  items: KnowledgeDocumentListItem[];
  total: number;
  next_cursor?: string;
}

export interface KnowledgeDocumentRevision {
  revision: KnowledgeRevision;
  delivery?: RevisionDeliverySummary;
  in_active_snapshot?: boolean;
}

export interface KnowledgeDocumentDetailResponse {
  document: KnowledgeDocument;
  revisions: KnowledgeDocumentRevision[];
  active_snapshot_id?: string;
  acceptance?: KnowledgeAcceptance;
  pending_snapshot?: KnowledgeSnapshot;
}

export interface KnowledgeRevisionUploadResponse {
  document: KnowledgeDocument;
  revision: KnowledgeRevision;
  delivery: RevisionDeliverySummary;
}

export interface KnowledgeLifecycleMutationResponse {
  document: KnowledgeDocument;
  snapshot?: KnowledgeSnapshotSummary;
  update_event?: KnowledgeUpdateEventSummary;
}

export interface KnowledgeRetirementReadiness {
  document_id: string;
  stage: "blocked" | "ready_to_disable" | "ready_to_delete";
  active_snapshot_id?: string;
  active_snapshot_manifest_hash?: string;
  passing_onboarding_run_id?: string;
  blockers: string[];
}

export interface KnowledgeRetirementReadinessResponse {
  readiness: KnowledgeRetirementReadiness;
}

export interface LegacyKnowledgeFile {
  file_id: string;
  filename: string;
  source_format: SourceFormat;
  status: string;
  uploaded_at: string;
}

interface LegacyKnowledgeFileListResponse {
  items: LegacyKnowledgeFile[];
  total: number;
}

export class ApiError extends Error {
  status: number;
  code?: string;

  constructor(message: string, status: number, code?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export async function listKnowledgeDocuments(filters: { status?: KnowledgeDocumentStatus | "" } = {}) {
  const params = new URLSearchParams();
  if (filters.status) {
    params.set("status", filters.status);
  }
  const query = params.toString();
  return requestJSON<KnowledgeDocumentListResponse>(`/api/kb/documents${query ? `?${query}` : ""}`);
}

export async function getKnowledgeDocument(documentId: string) {
  return requestJSON<KnowledgeDocumentDetailResponse>(`/api/kb/documents/${encodeURIComponent(documentId)}`);
}

export async function getKnowledgeRetirementReadiness(documentId: string) {
  return requestJSON<KnowledgeRetirementReadinessResponse>(`/api/kb/documents/${encodeURIComponent(documentId)}/retirement-readiness`);
}

export async function uploadKnowledgeDocument(input: {
  file: File;
  displayName?: string;
  effectiveAt?: string;
  idempotencyKey: string;
}) {
  const body = new FormData();
  body.append("file", input.file);
  if (input.displayName) {
    body.append("display_name", input.displayName);
  }
  if (input.effectiveAt) {
    body.append("effective_at", input.effectiveAt);
  }

  return requestJSON<KnowledgeRevisionUploadResponse>("/api/kb/documents", {
    method: "POST",
    headers: idempotencyHeaders(input.idempotencyKey),
    body,
  });
}

export async function uploadKnowledgeRevision(input: {
  documentId: string;
  file: File;
  effectiveAt?: string;
  idempotencyKey: string;
}) {
  const body = new FormData();
  body.append("file", input.file);
  if (input.effectiveAt) {
    body.append("effective_at", input.effectiveAt);
  }

  return requestJSON<KnowledgeRevisionUploadResponse>(`/api/kb/documents/${encodeURIComponent(input.documentId)}/revisions`, {
    method: "POST",
    headers: idempotencyHeaders(input.idempotencyKey),
    body,
  });
}

export async function prepareKnowledgePublish(revisionId: string, idempotencyKey: string) {
  return requestJSON<KnowledgeLifecycleMutationResponse>(`/api/kb/revisions/${encodeURIComponent(revisionId)}/prepare-publish`, {
    method: "POST",
    headers: idempotencyHeaders(idempotencyKey),
  });
}

export async function activateKnowledgeSnapshot(snapshotId: string, idempotencyKey: string, acknowledgeUnvalidated: boolean) {
  return requestJSON<KnowledgeLifecycleMutationResponse>(`/api/kb/snapshots/${encodeURIComponent(snapshotId)}/activate`, {
    method: "POST",
    headers: { ...idempotencyHeaders(idempotencyKey), "Content-Type": "application/json" },
    body: JSON.stringify({ acknowledge_unvalidated: acknowledgeUnvalidated }),
  });
}

export async function publishKnowledgeRevision(revisionId: string, idempotencyKey: string, acknowledgeUnvalidated = false) {
  return requestJSON<KnowledgeLifecycleMutationResponse>(`/api/kb/revisions/${encodeURIComponent(revisionId)}/publish`, {
    method: "POST",
    headers: { ...idempotencyHeaders(idempotencyKey), "Content-Type": "application/json" },
    body: JSON.stringify({ acknowledge_unvalidated: acknowledgeUnvalidated }),
  });
}

export async function disableKnowledgeDocument(documentId: string, idempotencyKey: string) {
  return requestJSON<KnowledgeLifecycleMutationResponse>(`/api/kb/documents/${encodeURIComponent(documentId)}/disable`, {
    method: "POST",
    headers: idempotencyHeaders(idempotencyKey),
  });
}

export async function deleteKnowledgeDocument(documentId: string, idempotencyKey: string) {
  return requestJSON<KnowledgeLifecycleMutationResponse>(`/api/kb/documents/${encodeURIComponent(documentId)}`, {
    method: "DELETE",
    headers: idempotencyHeaders(idempotencyKey),
  });
}

export async function listKnowledgeUpdates(filters: { documentId?: string; limit?: number; nextCursor?: string } = {}) {
  const params = new URLSearchParams();
  if (filters.documentId) {
    params.set("document_id", filters.documentId);
  }
  if (filters.limit) {
    params.set("limit", String(filters.limit));
  }
  if (filters.nextCursor) {
    params.set("next_cursor", filters.nextCursor);
  }
  const query = params.toString();
  return requestJSON<KnowledgeUpdateListResponse>(`/api/kb/updates${query ? `?${query}` : ""}`);
}

export async function getKnowledgeSnapshot(snapshotId: string) {
  return requestJSON<KnowledgeSnapshotDetailResponse>(`/api/kb/snapshots/${encodeURIComponent(snapshotId)}`);
}

export async function listLegacyKnowledgeFiles() {
  return requestJSON<LegacyKnowledgeFileListResponse>("/api/kb/files");
}

export function createIdempotencyKey() {
  return crypto.randomUUID();
}

export function formatDateTime(iso?: string) {
  if (!iso) {
    return "-";
  }

  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return iso;
  }

  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
  }).format(date);
}

function idempotencyHeaders(idempotencyKey: string) {
  return {
    "Idempotency-Key": idempotencyKey,
  };
}

async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set("Accept", "application/json");

  const response = await fetch(url, {
    ...init,
    headers,
    cache: "no-store",
  });
  const text = await response.text();
  const payload = parsePayload(text);

  if (!response.ok) {
    const error = readError(payload);
    throw new ApiError(localizeApiError(error.code, error.message, response.status), response.status, error.code);
  }

  return payload as T;
}

function parsePayload(text: string): unknown {
  if (!text) {
    return undefined;
  }

  try {
    return JSON.parse(text) as unknown;
  } catch {
    return undefined;
  }
}

function readError(payload: unknown): { code?: string; message?: string } {
  if (!payload || typeof payload !== "object") {
    return {};
  }

  const maybeError = "error" in payload ? payload.error : undefined;
  if (maybeError && typeof maybeError === "object") {
    const code = "code" in maybeError && typeof maybeError.code === "string" ? maybeError.code : undefined;
    const message = "message" in maybeError && typeof maybeError.message === "string" ? maybeError.message : undefined;
    return { code, message };
  }

  if ("message" in payload && typeof payload.message === "string") {
    return { message: payload.message };
  }

  return {};
}

function localizeApiError(code: string | undefined, message: string | undefined, status: number) {
  switch (code) {
    case "unauthenticated":
      return "请先登录后再继续。";
    case "admin_required":
      return "需要管理员权限。";
    case "knowledge_admin_required":
      return "需要知识库管理员权限。";
    case "system_admin_required":
      return "需要系统管理员权限。";
    case "missing_idempotency_key":
      return "本次操作缺少幂等标识，请刷新后重试。";
    case "idempotency_key_reused":
      return "该操作标识已用于不同请求，请重新发起操作。";
    case "revision_not_ready":
      return "该 revision 尚未完成索引，暂不能发布。";
    case "document_not_active":
      return "该文档当前不可新增 revision。";
    case "document_not_disabled":
      return "请先停用文档，再执行删除。";
    case "document_has_inflight_revision":
      return "该文档仍有 revision 正在上传或索引，完成前不能停用。";
    case "retirement_not_ready":
      return message ?? "旧知识尚不满足退役条件：请先确认新知识已发布，并在当前快照完成通过的验收。";
    case "unvalidated_acknowledgement_required":
      return message ?? "直接发布需要确认尚未完成上线验收。";
    case "draft_generation_failed":
      return message ?? "无法根据当前知识起草可核验题目，请手工添加题目；这不影响直接发布。";
    case "stale_pending_snapshot":
      return message ?? "待发布快照已过期，请重新准备发布。";
    default:
      return message ?? `请求失败，状态码 ${status}`;
  }
}
