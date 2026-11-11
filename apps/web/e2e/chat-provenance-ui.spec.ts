import { expect, test } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

const conversation = {
  conversation_id: "conv_provenance",
  title: "知识版本追溯",
  created_at: "2026-07-14T00:00:00Z",
  updated_at: "2026-07-14T00:05:00Z",
  config_version: "cfg_001",
  retrieval_config_version: "ret_001",
  knowledge_snapshot_id: "legacy_conversation_snapshot",
  model_id: "qwen2.5:14b-instruct",
};

async function login(page: import("@playwright/test").Page) {
  await page.goto("/login?next=/chat");
  await page.getByTestId("login-username").fill(userUsername);
  await page.getByTestId("login-password").fill(userPassword);
  await Promise.all([
    page.waitForURL(/\/chat(?:\/.*)?$/),
    page.getByTestId("login-submit").click(),
  ]);
}

async function openProvenanceConversation(page: import("@playwright/test").Page) {
  await Promise.all([
    page.waitForURL(new RegExp(`/chat/${conversation.conversation_id}$`)),
    page.getByRole("link", { name: /知识版本追溯/ }).click(),
  ]);
}

test("回答和引用面板展示 K5 快照、freshness 与 revision provenance", async ({ page }) => {
  const citationText = "报销申请必须在出发前获批，超过期限时需要直属负责人说明原因并由财务复核。";
  const assistantMessage = {
    message_id: "msg_provenance",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: citationText,
    status: "completed",
    created_at: "2026-07-14T00:05:00Z",
    answered_at: "2026-07-14T00:05:02Z",
    finish_reason: "stop",
    citations_count: 1,
    knowledge_snapshot_id: "snapshot_20260714_001",
    knowledge_snapshot_manifest_hash: "manifest_sha256_001",
    freshness_status: "current",
    citation_anchors: [{ start_rune: 0, end_rune: Array.from(citationText).length, citation_ranks: [1] }],
  };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/chat/conversations") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}`) {
      await route.fulfill({ json: { conversation, messages: [assistantMessage] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/sources/${assistantMessage.message_id}`) {
      await route.fulfill({
        json: {
          message_id: assistantMessage.message_id,
          knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
          knowledge_snapshot_manifest_hash: assistantMessage.knowledge_snapshot_manifest_hash,
          freshness_status: "current",
          items: [
            {
              message_id: assistantMessage.message_id,
              chunk_id: "chunk_001",
              file_id: "legacy_file_001",
              document_id: "document_expense_policy",
              revision_id: "revision_expense_policy_v2",
              chunk_content_sha256: "chunk_sha256_001",
              knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
              filename: "expense-policy-v2.md",
              rank: 1,
              score: 0.98,
              citation_label: "差旅审批",
              source_locator: { source_format: "md", heading_path: ["差旅", "审批"] },
              snippet: "出发前必须获得审批。",
            },
          ],
        },
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);
  await openProvenanceConversation(page);
  const assistant = page.getByTestId("assistant-message");
  await assistant.getByTestId("message-answer-details").locator("summary").click();
  await expect(assistant).toContainText("snapshot_20260714_001");
  await expect(assistant.getByTestId("knowledge-freshness")).toHaveText("知识状态：当前");

  const anchor = assistant.getByTestId("citation-anchor");
  await expect(anchor).toHaveText(citationText);
  await expect(assistant.getByTestId("assistant-citation-link")).toHaveText("查看 1 条已引用来源");
  await anchor.hover();
  await expect(assistant.getByTestId("citation-anchor-preview")).toContainText("expense-policy-v2.md");
  await expect(assistant.getByTestId("citation-anchor-preview")).toContainText("出发前必须获得审批。");
  await expect(page.getByTestId("citation-panel")).toHaveCount(0);
  await anchor.click();

  const panel = page.getByTestId("citation-panel");
  await expect(panel).toBeVisible();
  await expect(panel).toContainText("当前结论的依据");
  await expect(panel.getByTestId("knowledge-provenance")).toContainText("manifest_sha256_001");
  await expect(panel.getByTestId("citation-revision")).toHaveText("revision_expense_policy_v2");
  await expect(panel.getByTestId("citation-provenance")).toContainText("document_expense_policy");

  await page.getByTestId("citation-drawer-close").click();
  await expect(panel).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  expect(await anchor.evaluate((element) => {
    const range = document.createRange();
    range.selectNodeContents(element);
    return Array.from(range.getClientRects()).filter((rect) => rect.width > 0).length;
  })).toBeGreaterThan(1);
  await expect(anchor).toHaveCSS("text-decoration-line", "underline");
  await anchor.click();
  await expect(page.getByTestId("citation-panel")).toHaveCSS("position", "fixed");
  await expect(page.getByTestId("citation-panel")).toHaveCSS("bottom", "0px");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("citation-panel")).toHaveCount(0);
});

test("消息级来源入口只统计并展示正文已锚定的来源", async ({ page }) => {
  const firstConclusion = "费用需要先获得审批。";
  const secondConclusion = "超期时由财务复核。";
  const assistantMessage = {
    message_id: "msg_cited_sources_only",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: `${firstConclusion}未被正文引用的候选不应展示。${secondConclusion}`,
    status: "completed",
    created_at: "2026-07-14T00:05:00Z",
    citations_count: 3,
    knowledge_snapshot_id: "snapshot_20260714_001",
    knowledge_snapshot_manifest_hash: "manifest_sha256_001",
    freshness_status: "current",
    citation_anchors: [
      { start_rune: 0, end_rune: Array.from(firstConclusion).length, citation_ranks: [2] },
      {
        start_rune: Array.from(`${firstConclusion}未被正文引用的候选不应展示。`).length,
        end_rune: Array.from(`${firstConclusion}未被正文引用的候选不应展示。${secondConclusion}`).length,
        citation_ranks: [2, 3],
      },
    ],
  };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/chat/conversations") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}`) {
      await route.fulfill({ json: { conversation, messages: [assistantMessage] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/sources/${assistantMessage.message_id}`) {
      await route.fulfill({
        json: {
          message_id: assistantMessage.message_id,
          knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
          knowledge_snapshot_manifest_hash: assistantMessage.knowledge_snapshot_manifest_hash,
          freshness_status: "current",
          items: [1, 2, 3].map((rank) => ({
            message_id: assistantMessage.message_id,
            chunk_id: `chunk_${rank}`,
            file_id: `file_${rank}`,
            document_id: `document_${rank}`,
            revision_id: `revision_${rank}`,
            chunk_content_sha256: `chunk_sha256_${rank}`,
            knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
            filename: `source-${rank}.md`,
            rank,
            score: 1 - rank / 100,
            citation_label: `来源 ${rank}`,
            source_locator: { source_format: "md", heading_path: ["费用"] },
            snippet: `来源 ${rank} 的片段。`,
          })),
        },
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);
  await openProvenanceConversation(page);

  const assistant = page.getByTestId("assistant-message");
  const sourceLink = assistant.getByTestId("assistant-citation-link");
  await expect(sourceLink).toHaveText("查看 2 条已引用来源");
  await sourceLink.click();

  const cards = page.getByTestId("citation-panel").locator(".citation-card");
  await expect(cards).toHaveCount(2);
  await expect(cards.nth(0)).toContainText("#2");
  await expect(cards.nth(1)).toContainText("#3");
});

test("无 C1 anchor 的旧回答仍显示全部来源", async ({ page }) => {
  const assistantMessage = {
    message_id: "msg_legacy_sources",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: "旧回答保留消息级来源。",
    status: "completed",
    created_at: "2026-07-14T00:05:00Z",
    citations_count: 2,
    knowledge_snapshot_id: "snapshot_20260714_001",
    knowledge_snapshot_manifest_hash: "manifest_sha256_001",
    freshness_status: "current",
  };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/chat/conversations") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}`) {
      await route.fulfill({ json: { conversation, messages: [assistantMessage] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/sources/${assistantMessage.message_id}`) {
      await route.fulfill({
        json: {
          message_id: assistantMessage.message_id,
          knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
          knowledge_snapshot_manifest_hash: assistantMessage.knowledge_snapshot_manifest_hash,
          freshness_status: "current",
          items: [1, 2].map((rank) => ({
            message_id: assistantMessage.message_id,
            chunk_id: `legacy_chunk_${rank}`,
            file_id: `legacy_file_${rank}`,
            document_id: `legacy_document_${rank}`,
            revision_id: `legacy_revision_${rank}`,
            chunk_content_sha256: `legacy_chunk_sha256_${rank}`,
            knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
            filename: `legacy-source-${rank}.md`,
            rank,
            score: 1 - rank / 100,
            citation_label: `旧来源 ${rank}`,
            source_locator: { source_format: "md", heading_path: ["旧制度"] },
            snippet: `旧来源 ${rank} 的片段。`,
          })),
        },
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);
  await openProvenanceConversation(page);

  const assistant = page.getByTestId("assistant-message");
  const sourceLink = assistant.getByTestId("assistant-citation-link");
  await expect(sourceLink).toHaveText("查看 2 条来源");
  await sourceLink.click();
  await expect(page.getByTestId("citation-panel").locator(".citation-card")).toHaveCount(2);
});

test("证据来源暂不可用时，回答正文与锚点仍可阅读", async ({ page }) => {
  const assistantMessage = {
    message_id: "msg_anchor_source_failure",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: "报销申请必须在出发前获批。",
    status: "completed",
    created_at: "2026-07-14T00:05:00Z",
    citations_count: 1,
    knowledge_snapshot_id: "snapshot_20260714_001",
    knowledge_snapshot_manifest_hash: "manifest_sha256_001",
    freshness_status: "current",
    citation_anchors: [{ start_rune: 0, end_rune: 13, citation_ranks: [1] }],
  };

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/chat/conversations") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}`) {
      await route.fulfill({ json: { conversation, messages: [assistantMessage] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/sources/${assistantMessage.message_id}`) {
      await route.fulfill({ status: 503, json: { error: { code: "runtime_unavailable", message: "source backend unavailable" } } });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);
  await openProvenanceConversation(page);

  const assistant = page.getByTestId("assistant-message");
  const anchor = assistant.getByTestId("citation-anchor");
  await expect(anchor).toHaveText("报销申请必须在出发前获批。");
  await anchor.hover();
  await expect(assistant.getByTestId("citation-anchor-preview")).toContainText("依据暂不可用");
  await expect(anchor).toHaveText(assistantMessage.content);
});

test("旧兼容回答明确显示未知，快照范围错误可重试", async ({ page }) => {
  const legacyMessage = {
    message_id: "msg_legacy",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: "旧回答。",
    status: "completed",
    created_at: "2026-07-14T00:01:00Z",
    citations_count: 0,
  };
  let streamAttempts = 0;

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const method = route.request().method();
    if (pathname === "/api/chat/conversations" && method === "GET") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "GET") {
      await route.fulfill({ json: { conversation, messages: [legacyMessage] } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/messages` && method === "POST") {
      streamAttempts += 1;
      const code = streamAttempts === 1 ? "knowledge_snapshot_unavailable" : "snapshot_scope_violation";
      await route.fulfill({
        contentType: "text/event-stream",
        body: `event: message.error\ndata: {"error":{"code":"${code}","message":"internal message","retryable":true}}\n\n`,
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);
  await openProvenanceConversation(page);
  const assistant = page.getByTestId("assistant-message");
  await expect(assistant.getByTestId("citation-anchor")).toHaveCount(0);
  await assistant.getByTestId("message-answer-details").locator("summary").click();
  await expect(assistant.getByTestId("message-knowledge-scope")).toContainText("未知（旧兼容数据）");
  await expect(assistant.getByTestId("knowledge-freshness")).toHaveText("知识状态：未知");

  await page.getByTestId("chat-composer-input").fill("请依据知识库回答");
  await page.getByTestId("chat-send-button").click();
  await expect(page.getByTestId("pending-user-message")).toContainText("请依据知识库回答");
  await expect(page.getByTestId("pending-turn-error")).toContainText("当前没有已发布的知识版本。");
  await expect(page.getByTestId("pending-turn-error")).toContainText("还没有已发布的知识版本。管理员完成上传、索引和发布后即可重试。");
  await expect(page.getByTestId("chat-retry-button")).toBeEnabled();

  await page.getByTestId("chat-retry-button").click();
  await expect(page.getByTestId("pending-turn-error")).toContainText("知识范围校验未通过，本次回答未完成，请稍后重试。");
});
