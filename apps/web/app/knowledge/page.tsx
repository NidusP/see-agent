import { WorkspaceShell } from "../_components/workspace-shell";
import { getSessionRole } from "../_lib/auth-session";

export default async function KnowledgePage() {
  return <WorkspaceShell activeSection="knowledge" currentRole={await getSessionRole()} />;
}
