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

export async function requestJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set("Accept", "application/json");
  const response = await fetch(url, { ...init, headers, cache: "no-store" });
  const text = await response.text();
  const payload = text ? (JSON.parse(text) as unknown) : undefined;
  if (!response.ok) {
    const error = readError(payload);
    throw new ApiError(error.message ?? `请求失败，状态码 ${response.status}`, response.status, error.code);
  }
  return payload as T;
}

function readError(payload: unknown): { code?: string; message?: string } {
  if (!payload || typeof payload !== "object") {
    return {};
  }
  const maybeError = "error" in payload ? payload.error : undefined;
  if (!maybeError || typeof maybeError !== "object") {
    return {};
  }
  return {
    code: "code" in maybeError && typeof maybeError.code === "string" ? maybeError.code : undefined,
    message: "message" in maybeError && typeof maybeError.message === "string" ? maybeError.message : undefined,
  };
}
