import { NextResponse } from "next/server";

const DEFAULT_BACKEND_API_BASE_URL = "http://127.0.0.1:8080";

export function backendURL(path: string) {
  const baseURL = (
    process.env.BACKEND_API_BASE_URL ??
    process.env.NEXT_PUBLIC_BACKEND_BASE_URL ??
    DEFAULT_BACKEND_API_BASE_URL
  ).replace(/\/+$/, "");
  const normalizedPath = path.startsWith("/") ? path : `/${path}`;
  return `${baseURL}${normalizedPath}`;
}

export async function proxyBackend(path: string, init?: RequestInit, sourceRequest?: Request) {
  try {
    const response = await fetch(backendURL(path), {
      ...init,
      cache: "no-store",
      headers: buildProxyHeaders(init?.headers, sourceRequest),
    });
    const contentType = response.headers.get("content-type") ?? "application/json; charset=utf-8";
    const headers = new Headers({
      "content-type": contentType,
      "cache-control": "no-store",
    });
    for (const cookie of getSetCookies(response.headers)) {
      headers.append("set-cookie", cookie);
    }

    if (contentType.includes("text/event-stream")) {
      headers.set("cache-control", "no-cache, no-transform");
      headers.set("x-accel-buffering", "no");
      return new Response(response.body, {
        status: response.status,
        headers,
      });
    }

    const body = await response.text();
    return new Response(body, {
      status: response.status,
      headers,
    });
  } catch (error) {
    return NextResponse.json(
      {
        error: {
          code: "backend_unavailable",
          message: error instanceof Error ? error.message : "Backend API is unavailable",
        },
      },
      { status: 503 },
    );
  }
}

function buildProxyHeaders(initHeaders?: HeadersInit, sourceRequest?: Request) {
  const headers = new Headers(initHeaders);
  const cookie = sourceRequest?.headers.get("cookie");
  if (cookie && !headers.has("cookie")) {
    headers.set("cookie", cookie);
  }
  const accept = sourceRequest?.headers.get("accept");
  if (accept && !headers.has("accept")) {
    headers.set("accept", accept);
  }
  return headers;
}

function getSetCookies(headers: Headers) {
  const headersWithCookies = headers as Headers & { getSetCookie?: () => string[] };
  const setCookies = headersWithCookies.getSetCookie?.();
  if (setCookies && setCookies.length > 0) {
    return setCookies;
  }
  const setCookie = headers.get("set-cookie");
  return setCookie ? [setCookie] : [];
}
