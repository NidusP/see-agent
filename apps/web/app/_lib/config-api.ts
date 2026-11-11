export interface AgentConfig {
  config_version: string;
  model_id: string;
  system_prompt_version: string;
  response_style: string;
  temperature: number;
  top_p: number;
  max_output_tokens: number;
  created_at: string;
}

export interface RetrievalConfig {
  retrieval_config_version: string;
  retrieval_strategy: "native_dense_v1";
  embedding_model: string;
  top_k: number;
  rerank_enabled: boolean;
  evidence_min_score: number;
  created_at: string;
}

export interface AgentConfigEnvelope {
  config: AgentConfig;
}

export interface RetrievalConfigEnvelope {
  config: RetrievalConfig;
}

export interface ConfigVersionListResponse {
  items: AgentConfig[];
  total: number;
  retrieval_items?: RetrievalConfig[];
  retrieval_total?: number;
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

export async function getAgentConfig(): Promise<AgentConfigEnvelope> {
  return requestJSON<AgentConfigEnvelope>("/api/agent/config");
}

export async function updateAgentConfig(payload: {
  model_id: string;
  system_prompt_version: string;
  response_style: string;
  temperature: number;
  top_p: number;
  max_output_tokens: number;
}): Promise<AgentConfigEnvelope> {
  return requestJSON<AgentConfigEnvelope>("/api/agent/config", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
}

export async function getRetrievalConfig(): Promise<RetrievalConfigEnvelope> {
  return requestJSON<RetrievalConfigEnvelope>("/api/agent/retrieval-config");
}

export async function updateRetrievalConfig(payload: {
  retrieval_strategy: "native_dense_v1";
  embedding_model: string;
  top_k: number;
  rerank_enabled: boolean;
  evidence_min_score: number;
}): Promise<RetrievalConfigEnvelope> {
  return requestJSON<RetrievalConfigEnvelope>("/api/agent/retrieval-config", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
}

export async function listConfigVersions(): Promise<ConfigVersionListResponse> {
  return requestJSON<ConfigVersionListResponse>("/api/agent/config/versions");
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
    case "system_admin_required":
      return "需要系统管理员权限。";
    case "knowledge_admin_required":
      return "需要知识库管理员权限。";
    case "invalid_agent_config":
      return "Agent 配置不完整，请检查模型、prompt 版本和输出 token。";
    case "invalid_retrieval_config":
      return "检索配置不完整，请检查 embedding 模型和 top_k。";
    default:
      return message ?? `请求失败，状态码 ${status}`;
  }
}
