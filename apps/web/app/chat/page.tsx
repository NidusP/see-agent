import { WorkspaceShell } from "../_components/workspace-shell";
import { getSessionRole } from "../_lib/auth-session";

export default async function ChatPage() {
  return <WorkspaceShell activeSection="chat" showChatEmptyState currentRole={await getSessionRole()} />;
}
