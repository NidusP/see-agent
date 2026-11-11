import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

test("有限普通交流明确标注未使用知识库", async ({ page }) => {
  const conversation = {
    conversation_id: "conv_controlled_intent",
    title: "有限交流",
    created_at: "2026-07-16T00:00:00Z",
    updated_at: "2026-07-16T00:00:00Z",
    config_version: "cfg_004",
    retrieval_config_version: "retrieval_001",
    model_id: "llama3.2:latest",
  };
  const knowledgeRefusal = {
    message_id: "msg_knowledge_refusal", conversation_id: conversation.conversation_id, role: "assistant",
    content: "当前检索到的证据不足以可靠回答这个问题。", status: "completed", created_at: "2026-07-16T00:00:01Z",
    finish_reason: "refusal", citations_count: 0, knowledge_snapshot_id: "snap_test", freshness_status: "current",
    intent: "knowledge_query", knowledge_used: false,
  };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (pathname === "/api/chat/conversations" && method === "GET") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "GET") {
      await route.fulfill({ json: { conversation, messages: [knowledgeRefusal] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/messages` && method === "POST") {
      const body = route.request().postDataJSON() as { content?: string };
      const outOfScope = body.content === "今天天气如何";
      const intent = outOfScope ? "out_of_scope" : "social_greeting";
      const user = { message_id: `msg_${intent}_user`, conversation_id: conversation.conversation_id, role: "user", content: body.content ?? "你好", status: "completed", created_at: "2026-07-16T00:01:00Z", intent, knowledge_used: false };
      const assistant = {
        message_id: `msg_${intent}_assistant`, conversation_id: conversation.conversation_id, role: "assistant",
        content: outOfScope
          ? "我不能回答天气、美国总统等公共开放世界问题。我作为大模型助手只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题；你可以换成知识库相关的问题。"
          : "你好！我是一个大模型助手，只依据已发布知识库的 RAG 检索结果回答制度、流程和业务问题。你想查询什么？",
        status: "completed", created_at: "2026-07-16T00:01:01Z", finish_reason: outOfScope ? "refusal" : "stop", citations_count: 0, intent, knowledge_used: false,
      };
      await route.fulfill({
        contentType: "text/event-stream",
        body: [
          "event: message.started",
          `data: ${JSON.stringify({ conversation_id: conversation.conversation_id, user_message: user, assistant_message: { ...assistant, content: "", status: "streaming" } })}`,
          "",
          "event: message.delta",
          `data: ${JSON.stringify({ message_id: assistant.message_id, delta: assistant.content, status: "streaming" })}`,
          "",
          "event: message.completed",
          `data: ${JSON.stringify({ conversation_id: conversation.conversation_id, user_message: user, assistant_message: assistant })}`,
          "",
        ].join("\n"),
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected request" } } });
  });

  await page.goto(`/login?next=${encodeURIComponent(`/chat/${conversation.conversation_id}`)}`);
  await page.getByTestId("login-username").fill(userUsername);
  await page.getByTestId("login-password").fill(userPassword);
  await Promise.all([
    page.waitForURL(new RegExp(`/chat/${conversation.conversation_id}$`)),
    page.getByTestId("login-submit").click(),
  ]);

  await page.getByTestId("chat-composer-input").fill("你好");
  await page.getByTestId("chat-send-button").click();
  const assistant = page.getByTestId("assistant-message").filter({ hasText: "你想查询什么" });
  await expect(assistant).toBeVisible();
  await expect(assistant.getByTestId("message-knowledge-scope")).toHaveCount(0);
  await expect(assistant.getByTestId("message-answer-details")).toHaveCount(0);
  await expect(assistant.getByTestId("message-revalidate-action")).toHaveCount(0);
  await expect(assistant.getByTestId("assistant-citation-link")).toHaveCount(0);

  await page.getByTestId("chat-composer-input").fill("今天天气如何");
  await page.getByTestId("chat-send-button").click();
  const publicWorldRefusal = page.getByTestId("assistant-message").filter({ hasText: "公共开放世界问题" });
  await expect(publicWorldRefusal).toBeVisible();
  await expect(publicWorldRefusal).toContainText("RAG 检索结果");
  await expect(publicWorldRefusal.getByTestId("message-knowledge-scope")).toHaveCount(0);
  await expect(publicWorldRefusal.getByTestId("message-answer-details")).toHaveCount(0);
  await expect(publicWorldRefusal.getByTestId("assistant-citation-link")).toHaveCount(0);

  const refusal = page.getByTestId("assistant-message").filter({ hasText: "证据不足" });
  await refusal.getByTestId("message-answer-details").locator("summary").click();
  await expect(refusal.getByTestId("message-knowledge-scope")).toContainText("snap_test");
});
