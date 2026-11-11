import { WorkspaceShell } from "../_components/workspace-shell";
import { getSessionRole } from "../_lib/auth-session";

export default async function ConfigPage() {
  return <WorkspaceShell activeSection="config" currentRole={await getSessionRole()} />;
}
