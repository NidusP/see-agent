import { expect, test } from "@playwright/test";

const adminUsername = process.env.SMOKE_ADMIN_USERNAME ?? "admin";
const adminPassword = process.env.SMOKE_ADMIN_PASSWORD ?? "admin123";
const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";
const knowledgeAdminUsername = process.env.SMOKE_KNOWLEDGE_ADMIN_USERNAME ?? "knowledge_admin";
const knowledgeAdminPassword = process.env.SMOKE_KNOWLEDGE_ADMIN_PASSWORD ?? "knowledge_admin123";

async function login(page: import("@playwright/test").Page, username: string, password: string, nextPath: string, entry: "user" | "admin" = "user") {
  await page.goto(`${entry === "admin" ? "/admin/login" : "/login"}?next=${encodeURIComponent(nextPath)}`);
  await page.getByTestId("login-username").fill(username);
  await page.getByTestId("login-password").fill(password);
  await Promise.all([
    page.waitForURL(new RegExp(`${nextPath.replace("/", "\\/")}$`)),
    page.getByTestId("login-submit").click(),
  ]);
}

test("普通用户获得聊天优先界面，系统管理员只进入配置台", async ({ page }) => {
  await login(page, userUsername, userPassword, "/chat");

  await expect(page.getByTestId("chat-first-shell")).toBeVisible();
  await expect(page.getByTestId("conversation-context-pane")).toBeVisible();
  await expect(page.getByText("智能问答", { exact: true }).first()).toBeVisible();
  await expect(page.getByTestId("workspace-current-section")).toHaveText("对话");
  await expect(page.getByTestId("workspace-switcher")).toHaveCount(0);
  await expect(page.getByTestId("global-navigation")).toHaveCount(0);

  await page.getByRole("button", { name: "退出" }).click();
  await page.waitForURL(/\/login$/);
  await page.goto("/admin/login?next=/chat");
  await page.getByTestId("login-username").fill(adminUsername);
  await page.getByTestId("login-password").fill(adminPassword);
  await Promise.all([
    page.waitForURL(/\/config$/),
    page.getByTestId("login-submit").click(),
  ]);
  await expect(page.getByTestId("system-config-shell")).toBeVisible();
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);
  await page.getByTestId("open-config-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();
  await expect(page.getByTestId("workspace-detail-dialog")).toContainText("版本历史与风险提示");
  await page.getByTestId("workspace-detail-close").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);
  await expect(page.getByTestId("workspace-current-section")).toHaveText("系统配置");
  await expect(page.getByTestId("conversation-context-pane")).toHaveCount(0);
  await expect(page.getByTestId("workspace-switcher")).toHaveCount(0);
  await page.goto("/knowledge");
  await page.waitForURL(/\/config$/);
  await expect(page.getByTestId("system-config-shell")).toBeVisible();
  await expect(page.getByText("负责生成表达的模型。")).toBeVisible();
  await expect(page.getByText("拒答的最低证据边界。")).toBeVisible();
});

test("系统管理员可以在配置台确认 ready revision 发布", async ({ page }) => {
  let published = false;
  const document = {
    document_id: "doc_pending",
    display_name: "新制度",
    owner_id: "knowledge_admin",
    status: "active",
    created_at: "2026-11-04T00:00:00Z",
    updated_at: "2026-11-04T00:00:00Z",
  };
  const revision = {
    revision_id: "rev_pending",
    document_id: "doc_pending",
    filename: "new-policy.md",
    mime_type: "text/markdown",
    source_format: "md",
    checksum_sha256: "sha-pending",
    parse_version: "parse-v1",
    index_version: "index-v1",
    status: "ready",
    created_at: "2026-11-04T00:00:00Z",
    updated_at: "2026-11-04T00:00:00Z",
  };
  await page.route("**/api/kb/documents?status=active", async (route) => {
    await route.fulfill({ json: { items: published ? [{ ...document, current_revision: { ...revision, status: "published" } }] : [{ ...document, current_revision: revision }], total: 1 } });
  });
  await page.route("**/api/kb/documents/doc_pending", async (route) => {
    await route.fulfill({ json: { document, revisions: [{ revision: published ? { ...revision, status: "published" } : revision, in_active_snapshot: published }], active_snapshot_id: published ? "snap_new" : "snap_old" } });
  });
  await page.route("**/api/kb/revisions/rev_pending/publish", async (route) => {
    expect(route.request().headers()["idempotency-key"]).toBeTruthy();
    published = true;
    await route.fulfill({ json: { document, snapshot: { snapshot_id: "snap_new", status: "published", manifest_sha256: "manifest-new", created_at: "2026-11-04T00:01:00Z", published_at: "2026-11-04T00:01:00Z" } } });
  });
  await page.route("**/api/admin/evidence/**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/eval/reports**", (route) => route.fulfill({ json: { items: [] } }));
  await page.route("**/api/admin/quality/**", (route) => route.fulfill({ json: { items: [] } }));

  await login(page, adminUsername, adminPassword, "/config", "admin");
  await expect(page.getByTestId("knowledge-publish-approval")).toBeVisible();
  await expect(page.getByTestId("pending-publish-rev_pending")).toContainText("new-policy.md");
  page.once("dialog", (dialog) => void dialog.accept());
  await page.getByTestId("publish-revision-rev_pending").click();
  await expect(page.getByText("已发布到新知识快照 snap_new。")).toBeVisible();
  await expect(page.getByText("暂无待发布 revision。")).toBeVisible();
});

test("知识库管理员只能进入知识库，不能进入配置工作区", async ({ page }) => {
  await login(page, knowledgeAdminUsername, knowledgeAdminPassword, "/chat", "admin");
  await expect(page.getByText("知识库管理员", { exact: true })).toBeVisible();
  await page.getByTestId("workspace-switcher").click();
  const workspaceMenu = page.getByTestId("workspace-switcher-menu");
  await expect(workspaceMenu).toContainText("对话");
  await expect(workspaceMenu).toContainText("知识库");
  await expect(workspaceMenu.getByRole("link", { name: "配置" })).toHaveCount(0);
  await Promise.all([
    page.waitForURL(/\/knowledge$/),
    workspaceMenu.getByRole("link", { name: "知识库" }).click(),
  ]);
  await page.goto("/config");
  await page.waitForURL(/\/chat\?notice=system_admin_required/);
});

test("系统配置台在视口内保持等高双栏滚动", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page, adminUsername, adminPassword, "/config", "admin");

  const shell = page.getByTestId("system-config-shell");
  const layout = page.getByTestId("system-config-layout");
  const main = layout.locator(".app-main");
  await expect(shell).toBeVisible();
  await expect(layout).toBeVisible();

  const [shellBox, mainBox] = await Promise.all([
    shell.boundingBox(),
    main.boundingBox(),
  ]);
  expect(shellBox).not.toBeNull();
  expect(mainBox).not.toBeNull();
  expect(shellBox!.height).toBeCloseTo(900, 0);
  expect(mainBox!.width).toBeGreaterThan(1200);
  await expect(layout).toHaveCSS("overflow-y", "hidden");
  await expect(main).toHaveCSS("overflow-y", "auto");
  await expect(page.locator("html")).toHaveCSS("overflow-y", "hidden");
  await expect(page.locator("body")).toHaveCSS("overflow-y", "hidden");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(layout).toHaveCSS("overflow-y", "auto");
  await expect(main).toHaveCSS("overflow-y", "visible");
  await expect(page.locator("html")).toHaveCSS("overflow-y", "hidden");
  await expect(page.locator("body")).toHaveCSS("overflow-y", "hidden");
});

test("聊天与知识库工作区共用视口壳，并在内部滚动", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await login(page, userUsername, userPassword, "/chat");

  const chatShell = page.getByTestId("chat-first-shell");
  const chatFrame = page.getByTestId("chat-workspace-frame");
  const chatMain = page.getByTestId("chat-main");
  const messageViewport = page.getByTestId("chat-message-viewport");
  const messageContent = page.locator(".chat-message-content");
  const composer = page.locator(".chat-first-main .composer-card");
  const composerShell = page.locator(".composer-input-shell");
  const sendButton = page.getByTestId("chat-send-button");
  await expect(chatShell).toBeVisible();
  const [chatShellBox, chatFrameBox, chatMainBox, messageViewportBox, messageContentBox, composerBox] = await Promise.all([
    chatShell.boundingBox(),
    chatFrame.boundingBox(),
    chatMain.boundingBox(),
    messageViewport.boundingBox(),
    messageContent.boundingBox(),
    composer.boundingBox(),
  ]);
  expect(chatShellBox).not.toBeNull();
  expect(chatFrameBox).not.toBeNull();
  expect(chatMainBox).not.toBeNull();
  expect(messageViewportBox).not.toBeNull();
  expect(messageContentBox).not.toBeNull();
  expect(composerBox).not.toBeNull();
  expect(chatShellBox!.height).toBeCloseTo(900, 0);
  expect(chatFrameBox!.height).toBeCloseTo(900, 0);
  expect(composerBox!.y + composerBox!.height).toBeLessThanOrEqual(chatMainBox!.y + chatMainBox!.height + 1);
  expect(messageContentBox!.width).toBeLessThanOrEqual(760);
  expect(composerBox!.width).toBeLessThanOrEqual(760);
  expect(messageContentBox!.x - messageViewportBox!.x).toBeGreaterThanOrEqual(32);
  await expect(chatMain).toHaveCSS("overflow-y", "hidden");
  await expect(messageViewport).toHaveCSS("overflow-y", "auto");
  expect(parseFloat(await messageViewport.evaluate((element) => getComputedStyle(element).paddingLeft))).toBeGreaterThan(24);
  const [composerShellBox, sendButtonBox] = await Promise.all([composerShell.boundingBox(), sendButton.boundingBox()]);
  expect(composerShellBox).not.toBeNull();
  expect(sendButtonBox).not.toBeNull();
  expect(sendButtonBox!.x).toBeGreaterThanOrEqual(composerShellBox!.x);
  expect(sendButtonBox!.y + sendButtonBox!.height).toBeLessThanOrEqual(composerShellBox!.y + composerShellBox!.height + 1);
  await expect(page.locator("html")).toHaveCSS("overflow-y", "hidden");
  await expect(page.locator("body")).toHaveCSS("overflow-y", "hidden");

  await page.setViewportSize({ width: 390, height: 844 });
  const mobileShellBox = await chatShell.boundingBox();
  expect(mobileShellBox).not.toBeNull();
  expect(mobileShellBox!.height).toBeCloseTo(844, 0);
  await expect(chatMain).toHaveCSS("overflow-y", "hidden");
  await expect(messageViewport).toHaveCSS("overflow-y", "auto");
  await expect(chatFrame).toHaveCSS("overflow-y", "hidden");

  await page.getByTestId("chat-sidebar-mobile-toggle").click();
  await expect(page.getByTestId("conversation-context-pane")).toBeVisible();
  await page.getByRole("button", { name: "退出" }).click();
  await page.waitForURL(/\/login$/);
  await login(page, knowledgeAdminUsername, knowledgeAdminPassword, "/knowledge", "admin");
  await page.setViewportSize({ width: 1440, height: 900 });
  const managementLayout = page.getByTestId("management-layout");
  const managementMain = managementLayout.locator(".app-main");
  const [managementMainBox] = await Promise.all([
    managementMain.boundingBox(),
  ]);
  expect(managementMainBox).not.toBeNull();
  expect(managementMainBox!.width).toBeGreaterThan(1000);
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);
  await page.getByTestId("open-knowledge-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("workspace-detail-dialog")).toHaveCount(0);
  await expect(managementLayout).toHaveCSS("overflow-y", "hidden");
  await expect(managementMain).toHaveCSS("overflow-y", "auto");

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(managementLayout).toHaveCSS("overflow-y", "auto");
  await expect(managementMain).toHaveCSS("overflow-y", "visible");
  await expect(page.locator("html")).toHaveCSS("overflow-y", "hidden");
  await expect(page.locator("body")).toHaveCSS("overflow-y", "hidden");
});

test("会话侧栏可收缩，移动端按需打开", async ({ page }) => {
  await login(page, userUsername, userPassword, "/chat");

  await page.setViewportSize({ width: 1440, height: 900 });
  const sidebar = page.getByTestId("conversation-context-pane");
  const main = page.getByTestId("chat-main");
  const expandedBox = await sidebar.boundingBox();
  const mainBox = await main.boundingBox();
  expect(expandedBox).not.toBeNull();
  expect(mainBox).not.toBeNull();
  expect(expandedBox!.x).toBeLessThan(mainBox!.x);

  await page.getByTestId("chat-sidebar-toggle").click();
  await expect(page.getByTestId("chat-first-shell")).toHaveClass(/is-sidebar-collapsed/);
  const collapsedBox = await sidebar.boundingBox();
  expect(collapsedBox).not.toBeNull();
  expect(collapsedBox!.width).toBeLessThan(expandedBox!.width);
  await expect(page.getByText("会话", { exact: true })).toBeHidden();
  await expect(page.getByTestId("chat-sidebar-toggle")).toBeVisible();
  await expect(page.getByTestId("chat-sidebar-toggle")).toHaveAttribute("aria-label", "展开会话侧栏");
  await expect(page.getByTestId("chat-new-conversation")).toHaveAttribute("aria-label", "新建对话");
  await page.getByTestId("chat-sidebar-toggle").click();
  await expect(page.getByTestId("chat-first-shell")).not.toHaveClass(/is-sidebar-collapsed/);
  await expect(page.getByText("会话", { exact: true })).toBeVisible();

  await page.getByTestId("chat-sidebar-toggle").click();
  await page.goto("/knowledge");
  await expect(page.getByTestId("chat-first-shell")).toHaveClass(/is-sidebar-collapsed/);
  await expect(page.getByTestId("chat-sidebar-toggle")).toBeVisible();
  await page.getByTestId("chat-sidebar-toggle").click();
  await expect(page.getByTestId("chat-first-shell")).not.toHaveClass(/is-sidebar-collapsed/);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByTestId("chat-sidebar-mobile-toggle").click();
  await expect(page.getByTestId("chat-first-shell")).toHaveClass(/is-sidebar-open/);
  await expect(sidebar).toBeVisible();
  await expect.poll(async () => (await sidebar.boundingBox())?.x ?? -999).toBeGreaterThanOrEqual(-1);
  await expect(page.getByTestId("chat-sidebar-toggle")).toBeHidden();
  await expect(page.getByTestId("chat-sidebar-mobile-close")).toBeVisible();
  await expect(page.getByTestId("chat-sidebar-mobile-close")).toHaveAttribute("aria-label", "关闭会话侧栏");
  await page.getByTestId("chat-sidebar-mobile-close").click();
  await expect(page.getByTestId("chat-first-shell")).not.toHaveClass(/is-sidebar-open/);
});

test("操作按钮维持紧凑圆角，状态标签保留胶囊形态", async ({ page }) => {
  await page.goto("/admin/login?next=/config");
  await expect(page.getByTestId("login-submit")).toHaveCSS("border-radius", "8px");
  await expect(page.getByTestId("login-shortcut-admin")).toHaveCSS("border-radius", "8px");

  await login(page, adminUsername, adminPassword, "/config", "admin");
  await expect(page.getByRole("button", { name: "刷新", exact: true })).toHaveCSS("border-radius", "6px");
  await expect(page.locator(".header-chip").first()).toHaveCSS("border-radius", "999px");
});
