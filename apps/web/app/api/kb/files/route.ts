import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function GET(request: NextRequest) {
  return proxyBackend(`/api/kb/files${request.nextUrl.search}`, undefined, request);
}

export async function POST(request: NextRequest) {
  const body = await request.formData();
  return proxyBackend("/api/kb/files", {
    method: "POST",
    body,
  }, request);
}
