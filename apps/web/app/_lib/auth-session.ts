import { cookies } from "next/headers";

import { type SessionRole } from "./role";

export type { SessionRole } from "./role";

export async function getSessionRole(): Promise<SessionRole | undefined> {
  const cookieStore = await cookies();
  const role = cookieStore.get("see_agent_role")?.value;
  if (role === "user" || role === "knowledge_admin" || role === "system_admin" || role === "admin") {
    return role;
  }
  return undefined;
}
