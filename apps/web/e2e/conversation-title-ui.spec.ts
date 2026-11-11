import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

test("会话标题可键盘重命名，失败时保留草稿", async ({ page }) => {
  const conversation = {
    conversation_id: "conv_title_ui",
    owner_id: "user_user",
    title: "新对话",
    created_at: "2026-07-21T00:00:00Z",
    updated_at: "2026-07-21T00:00:00Z",
    config_version: "cfg_001",
    retrieval_config_version: "rcfg_001",
    knowledge_snapshot_id: "snapshot_active",
    model_id: "test-model",
  };
  let currentConversation = { ...conversation };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (pathname === "/api/chat/conversations" && method === "GET") {
      await route.fulfill({ json: { items: [currentConversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "GET") {
      await route.fulfill({ json: { conversation: currentConversation, messages: [] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "PATCH") {
      const payload = route.request().postDataJSON() as { title: string };
      if (payload.title === "无效标题") {
        await route.fulfill({
          status: 422,
          json: { error: { code: "validation_error", message: "title must contain 1 to 48 valid Unicode characters" } },
        });
        return;
      }
      currentConversation = { ...currentConversation, title: payload.title.trim().replace(/\s+/g, " ") };
      await route.fulfill({ json: { conversation: currentConversation } });
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

  const rename = page.getByTestId(`conversation-rename-${conversation.conversation_id}`);
  await rename.click();
  const input = page.getByTestId(`conversation-rename-input-${conversation.conversation_id}`);
  await expect(input).toBeFocused();
  await input.fill("  差旅报销规则  ");
  await input.press("Enter");
  await expect(page.locator(".chat-thread-header h1")).toHaveText("差旅报销规则");
  await expect(page.getByTestId(`conversation-rename-${conversation.conversation_id}`)).toBeVisible();

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByTestId(`conversation-rename-${conversation.conversation_id}`).click();
  await input.fill("无效标题");
  await input.press("Enter");
  await expect(input).toHaveValue("无效标题");
  await expect(page.getByRole("alert")).toBeVisible();
  await input.press("Escape");
  await expect(input).toHaveCount(0);
});
