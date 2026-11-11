export type MessageRole = "system" | "user" | "assistant";
export type MessageStatus = "pending" | "streaming" | "completed" | "failed";
export type FinishReason = "stop" | "length" | "refusal" | "error";
export type SourceFormat = "txt" | "md" | "json" | "pdf" | "docx" | "xlsx" | "pptx";
export type FreshnessStatus = "current" | "possibly_stale" | "revalidated" | "unknown";
export type ChatIntent = "knowledge_query" | "social_greeting" | "social_thanks" | "social_farewell" | "capability_help" | "off_topic" | "out_of_scope";
export type AnswerDisposition = "grounded_answer" | "partial_answer" | "clarification_required" | "insufficient_evidence" | "controlled_response";

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

export interface Coverage {
  readonly requested_count: number;
  readonly supported_count: number;
  readonly uncovered_description: string;
}

export interface Conversation {
  conversation_id: string;
  owner_id: string;
  title: string;
  created_at: string;
  updated_at: string;
  config_version: string;
  retrieval_config_version: string;
  knowledge_snapshot_id: string;
  model_id: string;
}

export interface Message {
  message_id: string;
  conversation_id: string;
  role: MessageRole;
  content: string;
  status: MessageStatus;
  created_at: string;
  finish_reason?: FinishReason;
  citations_count?: number;
  knowledge_snapshot_id?: string;
  knowledge_snapshot_manifest_hash?: string;
  answered_at?: string;
  freshness_status?: FreshnessStatus;
  citation_anchors?: CitationAnchor[];
  intent?: ChatIntent;
  knowledge_used?: boolean;
  readonly answer_disposition?: AnswerDisposition;
  readonly answer_policy_version?: string;
  readonly policy_evaluation_performed?: boolean;
  readonly answer_generation_performed?: boolean;
  readonly clarification_id?: string;
  readonly coverage?: Coverage;
  readonly execution?: ExecutionObservation;
}

export interface SourceLocator {
  source_format: SourceFormat;
  page_no?: number;
  heading_path?: string[];
  paragraph_index?: number;
  sheet_name?: string;
  cell_range?: string;
  slide_no?: number;
  slide_title?: string;
  block_label?: string;
  json_path?: string;
}

export interface Citation {
  message_id: string;
  chunk_id: string;
  file_id: string;
  document_id?: string;
  revision_id?: string;
  chunk_content_sha256?: string;
  knowledge_snapshot_id?: string;
  filename: string;
  rank: number;
  score: number;
  citation_label: string;
  source_locator: SourceLocator;
  snippet: string;
}

export interface CitationAnchor {
  start_rune: number;
  end_rune: number;
  citation_ranks: number[];
}

export interface ConversationListResponse {
  items: Conversation[];
  total: number;
}

export interface CreateConversationResponse {
  conversation: Conversation;
}

export interface ConversationDetailResponse {
	conversation: Conversation;
	messages: Message[];
}

export interface RenameConversationResponse {
	conversation: Conversation;
}

export interface SendMessageResponse {
  user_message: Message;
  assistant_message: Message;
}

export interface MessageStartedPayload {
  request_id?: string;
  conversation_id?: string;
  assistant_message_id?: string;
  user_message: Message;
  assistant_message: Message;
}

export interface MessageDeltaPayload {
  request_id?: string;
  message_id: string;
  delta: string;
  status?: MessageStatus;
}

export interface MessageCompletedPayload {
  request_id?: string;
  conversation_id?: string;
  assistant_message_id?: string;
  user_message: Message;
  assistant_message: Message;
}

export interface StreamMessageHandlers {
  onStarted?: (payload: MessageStartedPayload) => void;
  onDelta?: (payload: MessageDeltaPayload) => void;
  onCompleted?: (payload: MessageCompletedPayload) => void;
}

export interface StreamMessageOptions {
  signal?: AbortSignal;
}

export interface MessageSourcesResponse {
  message_id: string;
  knowledge_snapshot_id?: string;
  knowledge_snapshot_manifest_hash?: string;
  freshness_status?: FreshnessStatus;
  items: Citation[];
}

export type RevalidationRunStatus = "queued" | "running" | "completed" | "failed";
export type RevalidationOutcome = "still_valid" | "needs_update" | "insufficient_evidence";

export interface RevalidationErrorDetail {
  code: string;
  message: string;
  retryable?: boolean;
}

export interface RevalidationResult {
  revalidation_id: string;
  content: string;
  knowledge_snapshot_id: string;
  knowledge_snapshot_manifest_hash: string;
  outcome: RevalidationOutcome;
  citations: Citation[];
  completed_at: string;
}

export interface RevalidationRun {
  revalidation_id: string;
  conversation_id: string;
  original_message_id: string;
  status: RevalidationRunStatus;
  trigger: "manual";
  reason?: string;
  latest_snapshot_id: string;
  latest_snapshot_manifest_hash: string;
  related_update_event_ids: string[];
  requested_at: string;
  completed_at?: string;
  outcome?: RevalidationOutcome;
  result?: RevalidationResult;
  error?: RevalidationErrorDetail;
}

export interface RevalidationRunEnvelope {
  revalidation: RevalidationRun;
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

export async function listConversations(): Promise<ConversationListResponse> {
  return requestJSON<ConversationListResponse>("/api/chat/conversations");
}

export async function createConversation(title?: string): Promise<CreateConversationResponse> {
  return requestJSON<CreateConversationResponse>("/api/chat/conversations", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ title: title ?? "" }),
  });
}

export async function getConversationDetail(conversationId: string): Promise<ConversationDetailResponse> {
	return requestJSON<ConversationDetailResponse>(`/api/chat/conversations/${encodeURIComponent(conversationId)}`);
}

export async function renameConversation(conversationId: string, title: string): Promise<RenameConversationResponse> {
	return requestJSON<RenameConversationResponse>(`/api/chat/conversations/${encodeURIComponent(conversationId)}`, {
		method: "PATCH",
		headers: {
			"Content-Type": "application/json",
		},
		body: JSON.stringify({ title }),
	});
}

export async function sendMessage(conversationId: string, content: string): Promise<SendMessageResponse> {
  return requestJSON<SendMessageResponse>(`/api/chat/conversations/${encodeURIComponent(conversationId)}/messages`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({ content, stream: false }),
  });
}

export async function streamMessage(
  conversationId: string,
  content: string,
  handlers: StreamMessageHandlers = {},
  options: StreamMessageOptions = {},
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(`/api/chat/conversations/${encodeURIComponent(conversationId)}/messages`, {
      method: "POST",
      headers: {
        Accept: "text/event-stream",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ content, stream: true }),
      cache: "no-store",
      signal: options.signal,
    });
  } catch (error) {
    if (isAbortError(error)) {
      throw new ApiError(localizeApiError("request_aborted", undefined, 0), 0, "request_aborted");
    }
    throw error;
  }

  if (!response.ok) {
    const payload = await readResponsePayload(response);
    const error = readError(payload);
    throw new ApiError(localizeApiError(error.code, error.message, response.status), response.status, error.code);
  }

  if (!response.body) {
    throw new ApiError("浏览器不支持流式响应。", response.status);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let terminalEvent: "completed" | undefined;

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      const result = extractCompletedSSEBlocks(buffer);
      buffer = result.rest;
      for (const block of result.blocks) {
        terminalEvent = dispatchSSEBlock(block, handlers, response.status, terminalEvent);
      }
    }

    buffer += decoder.decode();
    if (buffer.trim()) {
      terminalEvent = dispatchSSEBlock(buffer, handlers, response.status, terminalEvent);
    }
    if (!terminalEvent) {
      throw new ApiError(
        "对话服务在返回完成结果前中断，本次消息没有被当作成功回答。",
        response.status,
        "stream_terminal_event_missing",
      );
    }
  } catch (error) {
    if (isAbortError(error)) {
      throw new ApiError(localizeApiError("request_aborted", undefined, response.status), response.status, "request_aborted");
    }
    throw error;
  } finally {
    reader.releaseLock();
  }
}

export async function getMessageSources(conversationId: string, messageId: string): Promise<MessageSourcesResponse> {
  return requestJSON<MessageSourcesResponse>(
    `/api/chat/conversations/${encodeURIComponent(conversationId)}/sources/${encodeURIComponent(messageId)}`,
  );
}

export async function revalidateMessage(input: {
  conversationId: string;
  messageId: string;
  idempotencyKey: string;
  reason?: string;
}): Promise<RevalidationRunEnvelope> {
  return requestJSON<RevalidationRunEnvelope>(
    `/api/chat/conversations/${encodeURIComponent(input.conversationId)}/messages/${encodeURIComponent(input.messageId)}/revalidate`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Idempotency-Key": input.idempotencyKey,
      },
      body: JSON.stringify(input.reason ? { reason: input.reason } : {}),
    },
  );
}

export async function getRevalidation(conversationId: string, revalidationId: string): Promise<RevalidationRunEnvelope> {
  return requestJSON<RevalidationRunEnvelope>(
    `/api/chat/conversations/${encodeURIComponent(conversationId)}/revalidations/${encodeURIComponent(revalidationId)}`,
  );
}

export function createChatIdempotencyKey() {
  return crypto.randomUUID();
}

export function formatDateTime(iso: string) {
  if (!iso) {
    return "-";
  }

  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone: "UTC",
  }).format(new Date(iso));
}

export function formatLocator(sourceLocator: SourceLocator) {
  switch (sourceLocator.source_format) {
    case "pdf":
      return sourceLocator.page_no ? `第 ${sourceLocator.page_no} 页` : "PDF 片段";
    case "docx":
      if (sourceLocator.heading_path?.length) {
        return sourceLocator.heading_path.join(" / ");
      }
      return sourceLocator.paragraph_index !== undefined ? `段落 ${sourceLocator.paragraph_index}` : "DOCX 片段";
    case "xlsx":
      return [sourceLocator.sheet_name, sourceLocator.cell_range].filter(Boolean).join(" · ") || "表格片段";
    case "pptx":
      return [sourceLocator.slide_no ? `第 ${sourceLocator.slide_no} 页幻灯片` : undefined, sourceLocator.slide_title]
        .filter(Boolean)
        .join(" · ") || "幻灯片片段";
    case "json":
      return sourceLocator.json_path ?? "JSON 片段";
    case "txt":
    case "md":
      return sourceLocator.block_label ?? sourceLocator.heading_path?.join(" / ") ?? "文本片段";
    default:
      return "来源片段";
  }
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
  const payload = text ? (JSON.parse(text) as unknown) : undefined;

  if (!response.ok) {
    const error = readError(payload);
    throw new ApiError(localizeApiError(error.code, error.message, response.status), response.status, error.code);
  }

  return payload as T;
}

async function readResponsePayload(response: Response) {
  const text = await response.text();
  if (!text) {
    return undefined;
  }
  try {
    return JSON.parse(text) as unknown;
  } catch {
    return { message: text };
  }
}

function extractCompletedSSEBlocks(buffer: string) {
  const normalized = buffer.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const parts = normalized.split("\n\n");
  return {
    blocks: parts.slice(0, -1).filter((block) => block.trim().length > 0),
    rest: parts[parts.length - 1] ?? "",
  };
}

function dispatchSSEBlock(
  block: string,
  handlers: StreamMessageHandlers,
  status: number,
  terminalEvent: "completed" | undefined,
): "completed" | undefined {
  let eventName = "message";
  const dataLines: string[] = [];

  for (const line of block.split("\n")) {
    if (!line || line.startsWith(":")) {
      continue;
    }
    const separatorIndex = line.indexOf(":");
    const field = separatorIndex >= 0 ? line.slice(0, separatorIndex) : line;
    const rawValue = separatorIndex >= 0 ? line.slice(separatorIndex + 1) : "";
    const value = rawValue.startsWith(" ") ? rawValue.slice(1) : rawValue;

    if (field === "event") {
      eventName = value;
    }
    if (field === "data") {
      dataLines.push(value);
    }
  }

  if (dataLines.length === 0) {
    return;
  }

  let payload: unknown;
  try {
    payload = JSON.parse(dataLines.join("\n")) as unknown;
  } catch {
    throw new ApiError("对话服务返回了无法验证的流式事件，本次消息没有被当作成功回答。", status, "runtime_contract_violation");
  }
  if (terminalEvent) {
    throw new ApiError("对话服务在完成事件后继续输出数据，本次消息没有被当作成功回答。", status, "runtime_contract_violation");
  }
  switch (eventName) {
    case "message.started":
      handlers.onStarted?.(payload as MessageStartedPayload);
      return terminalEvent;
    case "message.delta":
      handlers.onDelta?.(payload as MessageDeltaPayload);
      return terminalEvent;
    case "message.completed":
      handlers.onCompleted?.(payload as MessageCompletedPayload);
      return "completed";
    case "message.error": {
      const error = readError(payload);
      throw new ApiError(localizeApiError(error.code, error.message, status), status, error.code);
    }
    default:
      return terminalEvent;
  }
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
    case "conversation_not_found":
      return "会话不存在或当前账号无权访问。";
    case "runtime_unavailable":
      return "Runtime 暂不可用，本次消息没有被伪装成成功回复。";
    case "knowledge_snapshot_unavailable":
      return "当前知识快照暂不可用，本次回答未完成，请稍后重试。";
    case "snapshot_scope_violation":
      return "知识范围校验未通过，本次回答未完成，请稍后重试。";
    case "runtime_citation_anchor_violation":
      return "回答依据校验未通过，本次回答未完成，请稍后重试。";
    case "revalidation_invalid":
      return "该历史回答缺少可核验的知识证据。";
    case "revalidation_not_found":
      return "未找到该核验任务，请刷新后重试。";
    case "idempotency_key_reused":
      return "该核验操作标识已用于不同请求，请重新发起核验。";
    case "missing_idempotency_key":
      return "本次核验缺少幂等标识，请刷新后重试。";
    case "request_aborted":
      return "已停止生成，可重试上一条。";
    case "backend_unavailable":
      return "Backend 暂不可用，请稍后重试。";
    default:
      return message ?? `请求失败，状态码 ${status}`;
  }
}

function isAbortError(error: unknown) {
  return (
    (error instanceof DOMException && error.name === "AbortError") ||
    (error instanceof Error && error.name === "AbortError")
  );
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
