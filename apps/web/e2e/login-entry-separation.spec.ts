import { expect, test } from "@playwright/test";

test("普通用户入口不暴露管理员信息或快捷凭据", async ({ page }) => {
  await page.goto("/login");

  await expect(page.getByText("请使用您的内部账号登录。")).toBeVisible();
  await expect(page.getByTestId("login-shortcut-user")).toBeVisible();
  await expect(page.getByTestId("login-shortcut-knowledge-admin")).toHaveCount(0);
  await expect(page.getByTestId("login-shortcut-admin")).toHaveCount(0);
  await expect(page.getByTestId("login-admin-entry")).toBeVisible();
  await page.getByTestId("login-admin-entry").click();
  await expect(page).toHaveURL(/\/admin\/login\?next=%2Fknowledge$/);
  await expect(page.getByText("管理员可管理知识库和配置。")).toHaveCount(0);
});

test("受保护管理页会转至统一管理员登录入口", async ({ page }) => {
  await page.goto("/knowledge");
  await expect(page).toHaveURL(/\/admin\/login\?next=%2Fknowledge$/);
  await expect(page.getByText("进入管理工作台")).toBeVisible();
  await expect(page.getByTestId("login-shortcut-user")).toHaveCount(0);
  await expect(page.getByTestId("login-shortcut-knowledge-admin")).toBeVisible();
  await expect(page.getByTestId("login-shortcut-admin")).toBeVisible();
});
