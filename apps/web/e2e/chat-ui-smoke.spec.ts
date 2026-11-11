import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

test("普通用户可以登录、发送聊天并查看引用", async ({ page }) => {
  await page.goto("/login?next=/chat");

  const usernameInput = page.getByTestId("login-username");
  const passwordInput = page.getByTestId("login-password");
  const loginSubmit = page.getByTestId("login-submit");

  // Next dev hydration can replace server-rendered controlled input values.
  for (let attempt = 0; attempt < 4; attempt += 1) {
    await usernameInput.fill(userUsername);
    await passwordInput.fill(userPassword);
    if (await loginSubmit.isEnabled()) {
      break;
    }
    await page.waitForTimeout(250);
  }
  await expect(loginSubmit).toBeEnabled();
  await Promise.all([
    page.waitForURL(/\/chat(?:\/.*)?$/),
    loginSubmit.click(),
  ]);

  await expect(page.getByTestId("chat-main")).toBeVisible();

  await page.getByTestId("chat-composer-input").fill("差旅报销需要在多久内提交？");
  await page.getByTestId("chat-send-button").click();

  const assistantMessage = page.getByTestId("assistant-message");
  await expect(assistantMessage).toBeVisible({ timeout: 20_000 });
  await expect(assistantMessage.getByTestId("message-status")).toHaveCount(0);
  await expect(assistantMessage.getByTestId("message-answer-details")).toBeVisible();

  await assistantMessage.getByTestId("assistant-citation-link").click();
  await expect(page.getByTestId("citation-panel")).toBeVisible();
  const citationCards = page.getByTestId("citation-panel").locator(".citation-card");
  await expect.poll(() => citationCards.count()).toBeGreaterThan(0);
});
