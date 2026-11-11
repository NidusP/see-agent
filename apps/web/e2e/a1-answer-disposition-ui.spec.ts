import { expect, test, type Page } from "@playwright/test";

const userUsername = process.env.SMOKE_USER_USERNAME ?? "user";
const userPassword = process.env.SMOKE_USER_PASSWORD ?? "user123";

const conversation = {
  conversation_id: "conv_a1_dispositions",
  title: "回答处置",
  created_at: "2026-08-03T00:00:00Z",
  updated_at: "2026-08-03T00:05:00Z",
  config_version: "cfg_001",
  retrieval_config_version: "rcfg_001",
  model_id: "qwen3:14b",
};

async function login(page: Page) {
  await page.goto(`/login?next=${encodeURIComponent(`/chat/${conversation.conversation_id}`)}`);
  await page.getByTestId("login-username").fill(userUsername);
  await page.getByTestId("login-password").fill(userPassword);
  await Promise.all([
    page.waitForURL(new RegExp(`/chat/${conversation.conversation_id}$`)),
    page.getByTestId("login-submit").click(),
  ]);
}

test("A1 完成态只按后端处置显示来源和知识信息", async ({ page }) => {
  const partialContent = "已确认两项差旅报销规则。";
  const messages = [
    {
      message_id: "msg_a1_clarification",
      conversation_id: conversation.conversation_id,
      role: "assistant",
      content: "你要查询哪个办公地点的差旅报销制度？",
      status: "completed",
      created_at: "2026-08-03T00:01:00Z",
      citations_count: 1,
      knowledge_snapshot_id: "snap_a1",
      freshness_status: "current",
      intent: "knowledge_query",
      knowledge_used: false,
      answer_disposition: "clarification_required",
    },
    {
      message_id: "msg_a1_insufficient",
      conversation_id: conversation.conversation_id,
      role: "assistant",
      content: "当前已发布知识不足以确认该例外规则。",
      status: "completed",
      created_at: "2026-08-03T00:02:00Z",
      citations_count: 1,
      knowledge_snapshot_id: "snap_a1",
      freshness_status: "current",
      intent: "knowledge_query",
      knowledge_used: false,
      answer_disposition: "insufficient_evidence",
    },
    {
      message_id: "msg_a1_partial",
      conversation_id: conversation.conversation_id,
      role: "assistant",
      content: partialContent,
      status: "completed",
      created_at: "2026-08-03T00:03:00Z",
      citations_count: 1,
      knowledge_snapshot_id: "snap_a1",
      knowledge_snapshot_manifest_hash: "sha256:a1",
      freshness_status: "current",
      intent: "knowledge_query",
      knowledge_used: true,
      answer_disposition: "partial_answer",
      coverage: { requested_count: 3, supported_count: 2, uncovered_description: "第三项尚无可验证证据" },
      citation_anchors: [{ start_rune: 0, end_rune: Array.from(partialContent).length, citation_ranks: [1] }],
    },
  ];
  let sourceReads = 0;

  await page.route("**/api/chat/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === "/api/chat/conversations") {
      await route.fulfill({ json: { items: [conversation], total: 1 } });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}`) {
      await route.fulfill({ json: { conversation, messages } });
      return;
    }
    if (pathname.startsWith(`/api/chat/conversations/${conversation.conversation_id}/sources/`)) {
      sourceReads += 1;
      const messageID = pathname.split("/").at(-1);
      if (messageID !== "msg_a1_partial") {
        await route.fulfill({ status: 500, json: { error: { code: "unexpected_source_read", message: "A1 non-answer attempted to read sources" } } });
        return;
      }
      await route.fulfill({
        json: {
          message_id: "msg_a1_partial",
          knowledge_snapshot_id: "snap_a1",
          knowledge_snapshot_manifest_hash: "sha256:a1",
          freshness_status: "current",
          items: [{ message_id: "msg_a1_partial", chunk_id: "chunk_a1", file_id: "file_a1", document_id: "document_a1", revision_id: "revision_a1", chunk_content_sha256: "sha256:chunk", filename: "expense-policy.md", rank: 1, score: 0.9, citation_label: "差旅报销", source_locator: { source_format: "md", heading_path: ["差旅"] }, snippet: "已确认的两项规则。" }],
        },
      });
      return;
    }
    await route.fulfill({ status: 404, json: { error: { code: "not_found", message: "unexpected chat request" } } });
  });

  await login(page);

  const clarification = page.getByTestId("assistant-message").filter({ hasText: "哪个办公地点" });
  await expect(clarification.getByTestId("answer-disposition")).toHaveText("需要补充范围");
  await expect(clarification.getByTestId("assistant-citation-link")).toHaveCount(0);
  await expect(clarification.getByTestId("message-answer-details")).toHaveCount(0);
  await expect(clarification.getByTestId("citation-anchor")).toHaveCount(0);

  const insufficient = page.getByTestId("assistant-message").filter({ hasText: "不足以确认" });
  await expect(insufficient.getByTestId("answer-disposition")).toHaveText("当前知识不足");
  await expect(insufficient.getByTestId("assistant-citation-link")).toHaveCount(0);
  await expect(insufficient.getByTestId("message-answer-details")).toHaveCount(0);
  await expect(insufficient.getByTestId("citation-anchor")).toHaveCount(0);

  const partial = page.getByTestId("assistant-message").filter({ hasText: partialContent });
  await expect(partial.getByTestId("answer-disposition")).toHaveText("已覆盖部分");
  await expect(partial.getByTestId("answer-disposition-coverage")).toContainText("2/3");
  await expect(partial.getByTestId("assistant-citation-link")).toHaveText("查看 1 条已引用来源");
  await partial.getByTestId("assistant-citation-link").click();
  await expect(page.getByTestId("citation-panel")).toContainText("expense-policy.md");
  await expect.poll(() => sourceReads).toBe(1);
});
