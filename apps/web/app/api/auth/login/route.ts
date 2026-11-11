import { type NextRequest } from "next/server";

import { proxyBackend } from "../../../_lib/backend-proxy";

export const dynamic = "force-dynamic";

export async function POST(request: NextRequest) {
  const payload = await request.text();
  return proxyBackend(
    "/api/auth/login",
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: payload || "{}",
    },
    request,
  );
}
