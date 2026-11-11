import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";
const knowledgeAdminUsername = process.env.SMOKE_KNOWLEDGE_ADMIN_USERNAME ?? "knowledge_admin";
const knowledgeAdminPassword = process.env.SMOKE_KNOWLEDGE_ADMIN_PASSWORD ?? "knowledge_admin123";

test("跨账号会话链接不泄露内容并显示统一提示", async ({ page }) => {
  await page.goto("/login?next=/chat");
  await page.getByTestId("login-username").fill(userUsername);
  await page.getByTestId("login-password").fill(userPassword);
  await Promise.all([
    page.waitForURL((url) => url.pathname === "/chat"),
    page.getByTestId("login-submit").click(),
  ]);

  const title = `普通用户私有会话 ${Date.now()}`;
  const created = await page.evaluate(async (conversationTitle) => {
    const response = await fetch("/api/chat/conversations", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title: conversationTitle }),
    });
    return { status: response.status, body: await response.json() };
  }, title);
  expect(created.status).toBe(201);
  const createdPayload = created.body as { conversation: { conversation_id: string } };

  await page.getByRole("button", { name: "退出" }).click();
  await expect(page).toHaveURL(/\/login/);
  await page.goto("/admin/login?next=/chat");
  await page.getByTestId("login-username").fill(knowledgeAdminUsername);
  await page.getByTestId("login-password").fill(knowledgeAdminPassword);
  await Promise.all([
    page.waitForURL((url) => url.pathname === "/chat"),
    page.getByTestId("login-submit").click(),
  ]);

  await page.goto(`/chat/${createdPayload.conversation.conversation_id}`);
  await expect(page.getByTestId("chat-main").getByText("会话不存在或当前账号无权访问。")).toBeVisible();
  await expect(page).not.toHaveURL(new RegExp(`/chat/${createdPayload.conversation.conversation_id}$`));
  await expect(page.getByRole("heading", { name: title })).toHaveCount(0);
});
