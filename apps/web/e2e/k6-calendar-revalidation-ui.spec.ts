import { expect, test, type Page } from "@playwright/test";

const knowledgeAdminUsername = process.env.SMOKE_KNOWLEDGE_ADMIN_USERNAME ?? "knowledge_admin";
const knowledgeAdminPassword = process.env.SMOKE_KNOWLEDGE_ADMIN_PASSWORD ?? "knowledge_admin123";

const conversation = {
  conversation_id: "conv_k6",
  title: "历史回答核验",
  created_at: "2026-07-15T00:00:00Z",
  updated_at: "2026-07-15T00:05:00Z",
  config_version: "cfg_001",
  retrieval_config_version: "ret_001",
  knowledge_snapshot_id: "legacy_conversation_snapshot",
  model_id: "qwen2.5:14b-instruct",
};

async function login(page: Page) {
  await page.goto("/login?next=/chat");
  await page.getByTestId("login-username").fill(knowledgeAdminUsername);
  await page.getByTestId("login-password").fill(knowledgeAdminPassword);
  await Promise.all([
    page.waitForURL(/\/chat(?:\/.*)?$/),
    page.getByTestId("login-submit").click(),
  ]);
}

test("管理员能查看更新日历、freshness scan 与 immutable snapshot", async ({ page }) => {
  const document = {
    document_id: "doc_policy",
    display_name: "报销政策",
    owner_id: "admin",
    status: "active",
    created_at: "2026-07-15T00:00:00Z",
    updated_at: "2026-07-15T00:03:00Z",
  };
  const revision = {
    revision_id: "rev_policy_v2",
    document_id: document.document_id,
    filename: "expense-policy-v2.md",
    mime_type: "text/markdown",
    source_format: "md",
    checksum_sha256: "checksum_policy_v2",
    parse_version: "parse-v1",
    index_version: "index-v1",
    status: "published",
    created_at: "2026-07-15T00:00:00Z",
    updated_at: "2026-07-15T00:03:00Z",
  };

  await page.route("**/api/kb/**", async (route) => {
    const pathname = new URL(route.request().url()).pathname;
    const method = route.request().method();
    const fulfill = async (body: unknown, status = 200) => {
      await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    };

    if (pathname === "/api/kb/files" && method === "GET") {
      await fulfill({ items: [], total: 0 });
      return;
    }
    if (pathname === "/api/kb/documents" && method === "GET") {
      await fulfill({ items: [{ ...document, current_revision: revision }], total: 1 });
      return;
    }
    if (pathname === `/api/kb/documents/${document.document_id}` && method === "GET") {
      await fulfill({ document, revisions: [{ revision, in_active_snapshot: true }], active_snapshot_id: "snapshot_k6_v2" });
      return;
    }
    if (pathname === "/api/kb/updates" && method === "GET") {
      await fulfill({
        items: [
          {
            event_id: "event_publish_v2",
            event_type: "published",
            document_id: document.document_id,
            revision_id: revision.revision_id,
            snapshot_id: "snapshot_k6_v2",
            occurred_at: "2026-07-15T00:03:00Z",
            freshness_scan: {
              event_id: "event_publish_v2",
              status: "completed",
              attempt_count: 1,
              candidate_count: 4,
              marked_count: 2,
              updated_at: "2026-07-15T00:04:00Z",
              completed_at: "2026-07-15T00:04:00Z",
            },
          },
        ],
      });
      return;
    }
    if (pathname === "/api/kb/snapshots/snapshot_k6_v2" && method === "GET") {
      await fulfill({
        snapshot: {
          snapshot_id: "snapshot_k6_v2",
          status: "published",
          manifest_sha256: "manifest_k6_v2",
          created_at: "2026-07-15T00:03:00Z",
          published_at: "2026-07-15T00:03:00Z",
          members: [
            {
              snapshot_id: "snapshot_k6_v2",
              document_id: document.document_id,
              revision_id: revision.revision_id,
              checksum_sha256: revision.checksum_sha256,
            },
          ],
        },
      });
      return;
    }
    await fulfill({ error: { code: "not_found", message: "unexpected knowledge request" } }, 404);
  });

  await login(page);
  await page.goto("/knowledge");

  const calendar = page.getByTestId("knowledge-update-calendar");
  await expect(calendar).toContainText("published");
  await expect(calendar.getByTestId("knowledge-freshness-scan")).toContainText("候选 4 · 标记 2");

  await calendar.getByTestId("knowledge-update-snapshot").click();
  const snapshot = page.getByTestId("knowledge-snapshot-detail");
  await expect(snapshot).toContainText("manifest_k6_v2");
  await snapshot.getByText("查看成员 (1)").click();
  await expect(snapshot).toContainText("rev_policy_v2");
});

test("核验结果独立显示，原回答正文不会被改写", async ({ page }) => {
  const assistantMessage = {
    message_id: "msg_historical_answer",
    conversation_id: conversation.conversation_id,
    role: "assistant",
    content: "原始历史回答：报销需要遵循旧审批规则。",
    status: "completed",
    created_at: "2026-07-15T00:01:00Z",
    answered_at: "2026-07-15T00:01:02Z",
    finish_reason: "stop",
    citations_count: 1,
    knowledge_snapshot_id: "snapshot_k5_old",
    knowledge_snapshot_manifest_hash: "manifest_k5_old",
    freshness_status: "possibly_stale",
  };
  let revalidationReadCount = 0;

  await page.route("**/api/chat/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const method = request.method();
    const fulfill = async (body: unknown, status = 200) => {
      await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    };

    if (pathname === "/api/chat/conversations" && method === "GET") {
      await fulfill({ items: [conversation], total: 1 });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}` && method === "GET") {
      await fulfill({ conversation, messages: [assistantMessage] });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/sources/${assistantMessage.message_id}` && method === "GET") {
      await fulfill({
        message_id: assistantMessage.message_id,
        knowledge_snapshot_id: assistantMessage.knowledge_snapshot_id,
        knowledge_snapshot_manifest_hash: assistantMessage.knowledge_snapshot_manifest_hash,
        freshness_status: assistantMessage.freshness_status,
        items: [],
      });
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/messages/${assistantMessage.message_id}/revalidate` && method === "POST") {
      expect(request.headers()["idempotency-key"]).toBeTruthy();
      expect(request.postDataJSON()).toEqual({});
      await fulfill({
        revalidation: {
          revalidation_id: "revalidation_001",
          conversation_id: conversation.conversation_id,
          original_message_id: assistantMessage.message_id,
          status: "queued",
          trigger: "manual",
          latest_snapshot_id: "snapshot_k6_latest",
          latest_snapshot_manifest_hash: "manifest_k6_latest",
          related_update_event_ids: ["event_publish_v2"],
          requested_at: "2026-07-15T00:06:00Z",
        },
      }, 202);
      return;
    }
    if (pathname === `/api/chat/conversations/${conversation.conversation_id}/revalidations/revalidation_001` && method === "GET") {
      revalidationReadCount += 1;
      await fulfill({
        revalidation: {
          revalidation_id: "revalidation_001",
          conversation_id: conversation.conversation_id,
          original_message_id: assistantMessage.message_id,
          status: "completed",
          trigger: "manual",
          latest_snapshot_id: "snapshot_k6_latest",
          latest_snapshot_manifest_hash: "manifest_k6_latest",
          related_update_event_ids: ["event_publish_v2"],
          requested_at: "2026-07-15T00:06:00Z",
          completed_at: "2026-07-15T00:06:04Z",
          outcome: "needs_update",
          result: {
            revalidation_id: "revalidation_001",
            content: "最新核验正文：新版规则要求出发前审批。",
            knowledge_snapshot_id: "snapshot_k6_latest",
            knowledge_snapshot_manifest_hash: "manifest_k6_latest",
            outcome: "needs_update",
            completed_at: "2026-07-15T00:06:04Z",
            citations: [
              {
                message_id: assistantMessage.message_id,
                chunk_id: "chunk_k6_latest",
                file_id: "legacy_file_policy",
                document_id: "doc_policy",
                revision_id: "rev_policy_v2",
                chunk_content_sha256: "chunk_k6_latest_hash",
                knowledge_snapshot_id: "snapshot_k6_latest",
                filename: "expense-policy-v2.md",
                rank: 1,
                score: 0.97,
                citation_label: "差旅审批",
                source_locator: { source_format: "md", heading_path: ["差旅", "审批"] },
                snippet: "出发前必须完成审批。",
              },
            ],
          },
        },
      });
      return;
    }
    await fulfill({ error: { code: "not_found", message: "unexpected chat request" } }, 404);
  });

  await login(page);
  await Promise.all([
    page.waitForURL(new RegExp(`/chat/${conversation.conversation_id}$`)),
    page.getByRole("link", { name: /历史回答核验/ }).click(),
  ]);

  const assistant = page.getByTestId("assistant-message");
  await expect(assistant.getByTestId("assistant-message-content")).toHaveText(assistantMessage.content);
  await assistant.getByTestId("message-answer-details").locator("summary").click();
  await assistant.getByTestId("message-revalidate-action").click();
  await expect(assistant.getByTestId("answer-revalidation")).toContainText("已排队");

  await assistant.getByTestId("message-revalidation-refresh").click();
  await expect(assistant.getByTestId("revalidation-result")).toContainText("需要更新");
  await expect(assistant.getByTestId("revalidation-result-content")).toHaveText("最新核验正文：新版规则要求出发前审批。");
  await expect(assistant.getByTestId("assistant-message-content")).toHaveText(assistantMessage.content);
  await expect(assistant.getByTestId("revalidation-citations")).toContainText("rev_policy_v2");
  expect(revalidationReadCount).toBeGreaterThan(0);
});
