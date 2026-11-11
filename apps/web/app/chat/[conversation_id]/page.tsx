import { WorkspaceShell } from "../../_components/workspace-shell";
import { getSessionRole } from "../../_lib/auth-session";

export default async function ConversationPage({
  params,
}: {
  params: Promise<{ conversation_id: string }>;
}) {
  const { conversation_id } = await params;

  return <WorkspaceShell activeSection="chat" selectedConversationId={conversation_id} currentRole={await getSessionRole()} />;
}
