import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

test("聊天在首个流事件前给出即时反馈，并保留下一条草稿", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 520 });
  const conversation = {
    conversation_id: "conv_stream_experience",
    title: "流式体验",
    created_at: "2026-07-15T00:00:00Z",
    updated_at: "2026-07-15T00:00:00Z",
    config_version: "cfg_001",
    retrieval_config_version: "retrieval_001",
    model_id: "llama3.2:latest",
  };
  let releaseStream: (() => void) | undefined;
  const streamGate = new Promise<void>((resolve) => {
    releaseStream = resolve;
  });

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (pathname === "/api/chat/conversations" && method === "GET") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "GET") {
      await route.fulfill({ json: { conversation, messages: [] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/messages` && method === "POST") {
      await streamGate;
      await route.fulfill({
        contentType: "text/event-stream",
        body: [
          "event: message.started",
          `data: ${JSON.stringify({
            conversation_id: conversation.conversation_id,
            user_message: { message_id: "msg_user_stream", conversation_id: conversation.conversation_id, role: "user", content: "请总结报销规则", status: "completed", created_at: "2026-07-15T00:01:00Z" },
            assistant_message: { message_id: "msg_assistant_stream", conversation_id: conversation.conversation_id, role: "assistant", content: "", status: "streaming", created_at: "2026-07-15T00:01:00Z" },
          })}`,
          "",
          "event: message.delta",
          `data: ${JSON.stringify({ message_id: "msg_assistant_stream", delta: "报销需要在 15 天内提交。", status: "streaming" })}`,
          "",
          "event: message.completed",
          `data: ${JSON.stringify({
            conversation_id: conversation.conversation_id,
            user_message: { message_id: "msg_user_stream", conversation_id: conversation.conversation_id, role: "user", content: "请总结报销规则", status: "completed", created_at: "2026-07-15T00:01:00Z" },
            assistant_message: { message_id: "msg_assistant_stream", conversation_id: conversation.conversation_id, role: "assistant", content: "报销需要在 15 天内提交。", status: "completed", created_at: "2026-07-15T00:01:01Z", citations_count: 0 },
          })}`,
          "",
        ].join("\n"),
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await page.goto(`/login?next=${encodeURIComponent(`/chat/${conversation.conversation_id}`)}`);
  await page.getByTestId("login-username").fill(userUsername);
  await page.getByTestId("login-password").fill(userPassword);
  await Promise.all([
    page.waitForURL(new RegExp(`/chat/${conversation.conversation_id}$`)),
    page.getByTestId("login-submit").click(),
  ]);

  const composer = page.getByTestId("chat-composer-input");
  const sendButton = page.getByTestId("chat-send-button");
  const compactComposerHeight = await composer.evaluate((element) => element.getBoundingClientRect().height);
  await expect(sendButton).toBeDisabled();
  await composer.fill("草稿第一行");
  await composer.press("Shift+Enter");
  await expect(composer).toHaveValue("草稿第一行\n");
  await expect.poll(() => composer.evaluate((element) => element.getBoundingClientRect().height)).toBeGreaterThan(compactComposerHeight);
  await expect(sendButton).toHaveCSS("border-radius", "50%");
  await expect(sendButton).toHaveCSS("width", "42px");
  await expect(sendButton).toHaveCSS("height", "42px");
  await composer.fill("请总结报销规则。\n".repeat(80));
  await composer.press("Enter");

  await expect(page.getByTestId("pending-user-message")).toContainText("请总结报销规则");
  await expect(page.getByTestId("pending-assistant-message")).toContainText("正在判断并处理请求");
  await expect(composer).toBeEnabled();
  const composerShell = page.locator(".composer-input-shell");
  const abortButton = page.getByTestId("chat-abort-button");
  await expect(abortButton).toBeVisible();
  const [composerBox, abortBox] = await Promise.all([composerShell.boundingBox(), abortButton.boundingBox()]);
  expect(composerBox).not.toBeNull();
  expect(abortBox).not.toBeNull();
  expect(abortBox!.x).toBeGreaterThanOrEqual(composerBox!.x);
  expect(abortBox!.y + abortBox!.height).toBeLessThanOrEqual(composerBox!.y + composerBox!.height + 1);
  const messageViewport = page.getByTestId("chat-message-viewport");
  await expect.poll(() => messageViewport.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);
  await messageViewport.evaluate((element) => {
    element.scrollTop = 0;
    element.dispatchEvent(new Event("scroll"));
  });
  const jumpToLatest = page.getByTestId("jump-to-latest");
  await expect(jumpToLatest).toBeVisible();
  await expect(jumpToLatest).toHaveAttribute("aria-label", "回到最新");
  await expect(jumpToLatest).toHaveAttribute("title", "回到最新");
  await expect(jumpToLatest).toHaveText("");
  await expect(jumpToLatest).toHaveCSS("border-radius", "50%");
  await expect(jumpToLatest).toHaveCSS("width", "40px");
  await expect(jumpToLatest).toHaveCSS("height", "40px");
  await jumpToLatest.click();
  await expect(jumpToLatest).toBeHidden();
  await composer.fill("下一条草稿不会丢失");

  releaseStream?.();
  await expect(page.getByTestId("assistant-message")).toContainText("报销需要在 15 天内提交。");
  await expect(page.getByTestId("assistant-message").getByTestId("message-status")).toHaveCount(0);
  await expect(page.getByTestId("chat-send-button")).toBeVisible();
  await expect(composer).toHaveValue("下一条草稿不会丢失");
});
