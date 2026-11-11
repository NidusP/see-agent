import { expect, test } from "@playwright/test";

const conversation = {
  conversation_id: "conv_auth_recovery",
  title: "登录恢复",
  created_at: "2026-07-15T00:00:00Z",
  updated_at: "2026-07-15T00:00:00Z",
  config_version: "cfg_001",
  retrieval_config_version: "retrieval_001",
  model_id: "llama3.2:latest",
};

test("聊天认证失效后重登回原会话并恢复草稿", async ({ page }) => {
  let logoutCalls = 0;
  await page.route("**/api/auth/login", async (route) => {
    await route.fulfill({
      status: 200,
      headers: { "Set-Cookie": "see_agent_session=renewed-session; Path=/" },
      json: { user: { user_id: "user_user", username: "user", role: "user" } },
    });
  });
  await page.route("**/api/auth/logout", async (route) => {
    logoutCalls += 1;
    await route.fulfill({
      status: 200,
      headers: { "Set-Cookie": "see_agent_session=; Max-Age=0; Path=/" },
      json: { ok: true },
    });
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
      await route.fulfill({ status: 401, json: { error: { code: "unauthenticated", message: "session expired" } } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  const path = `/chat/${conversation.conversation_id}`;
  await page.goto(`/login?next=${encodeURIComponent(path)}`);
  await Promise.all([
    page.waitForURL(new RegExp(`${path}$`)),
    page.getByTestId("login-shortcut-user").click(),
  ]);

  const draft = "这条草稿需要在登录后保留";
  await page.getByTestId("chat-composer-input").fill(draft);
  await page.getByTestId("chat-send-button").click();

  await expect(page).toHaveURL(new RegExp(`/login\\?next=%2Fchat%2F${conversation.conversation_id}$`));
  expect(logoutCalls).toBe(1);

  await Promise.all([
    page.waitForURL(new RegExp(`${path}$`)),
    page.getByTestId("login-shortcut-user").click(),
  ]);
  await expect(page.getByTestId("chat-composer-input")).toHaveValue(draft);
});
