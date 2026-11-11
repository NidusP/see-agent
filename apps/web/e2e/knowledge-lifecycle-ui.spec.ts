import { expect, test } from "@playwright/test";

const knowledgeAdminUsername = process.env.SMOKE_KNOWLEDGE_ADMIN_USERNAME ?? "knowledge_admin";
const knowledgeAdminPassword = process.env.SMOKE_KNOWLEDGE_ADMIN_PASSWORD ?? "knowledge_admin123";

test("知识库管理员可以上传文档并提交 replacement revision", async ({ page }) => {
  const revisionOne = {
    revision_id: "rev_001",
    document_id: "doc_001",
    filename: "policy-v1.md",
    mime_type: "text/markdown",
    source_format: "md",
    checksum_sha256: "checksum-001",
    parse_version: "parse-v1",
    index_version: "index-v1",
    status: "ready",
    created_at: "2026-07-14T00:00:00Z",
    updated_at: "2026-07-14T00:00:00Z",
  };
  const revisionTwo = {
    ...revisionOne,
    revision_id: "rev_002",
    filename: "policy-v2.md",
    checksum_sha256: "checksum-002",
    status: "ready",
    created_at: "2026-07-14T01:00:00Z",
    updated_at: "2026-07-14T01:00:00Z",
  };
  let document = {
    document_id: "doc_001",
    display_name: "报销政策",
    owner_id: "admin",
    status: "active",
    created_at: "2026-07-14T00:00:00Z",
    updated_at: "2026-07-14T00:00:00Z",
  };
  let revisions = [{ revision: revisionOne, delivery: { delivery_id: "delivery_001", status: "delivered", attempt_count: 1, updated_at: "2026-07-14T00:00:00Z" }, in_active_snapshot: false }];
  let activeSnapshotID: string | undefined;

  await page.goto("/admin/login?next=/chat");
  await page.getByTestId("login-username").fill(knowledgeAdminUsername);
  await page.getByTestId("login-password").fill(knowledgeAdminPassword);
  await Promise.all([
    page.waitForURL(/\/chat(?:\/.*)?$/),
    page.getByTestId("login-submit").click(),
  ]);

  await page.route("**/api/kb/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const method = request.method();
    const headers = request.headers();

    const fulfill = async (body: unknown, status = 200) => {
      await route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });
    };
    const documentList = () => ({
      items: document.status === "deleted" ? [] : [{ ...document, current_revision: revisions.at(-1)?.revision }],
      total: document.status === "deleted" ? 0 : 1,
    });
    const detail = () => ({
      document,
      revisions,
      ...(activeSnapshotID ? { active_snapshot_id: activeSnapshotID } : {}),
    });

    if (pathname === "/api/kb/files" && method === "GET") {
      await fulfill({ items: [], total: 0 });
      return;
    }
    if (pathname === "/api/kb/documents" && method === "GET") {
      await fulfill(documentList());
      return;
    }
    if (pathname === "/api/kb/documents" && method === "POST") {
      expect(headers["idempotency-key"]).toBeTruthy();
      expect(headers["content-type"]).toContain("multipart/form-data");
      await fulfill({ document, revision: revisionOne, delivery: revisions[0].delivery }, 202);
      return;
    }
    if (pathname === "/api/kb/documents/doc_001" && method === "GET") {
      await fulfill(detail());
      return;
    }
    if (pathname === "/api/kb/documents/doc_001/retirement-readiness" && method === "GET") {
      await fulfill({
        readiness: {
          document_id: "doc_001",
          stage: document.status === "disabled" ? "ready_to_delete" : activeSnapshotID ? "ready_to_disable" : "blocked",
          active_snapshot_id: activeSnapshotID,
          active_snapshot_manifest_hash: activeSnapshotID ? "manifest-002" : undefined,
          passing_onboarding_run_id: activeSnapshotID ? "run_001" : undefined,
          blockers: activeSnapshotID ? [] : ["当前 snapshot 没有可替代的新知识"],
        },
      });
      return;
    }
    if (pathname === "/api/kb/documents/doc_001/revisions" && method === "POST") {
      expect(headers["idempotency-key"]).toBeTruthy();
      expect(headers["content-type"]).toContain("multipart/form-data");
      revisions = [
        { revision: { ...revisionOne, status: "superseded" }, delivery: revisions[0].delivery, in_active_snapshot: false },
        { revision: revisionTwo, delivery: { delivery_id: "delivery_002", status: "delivered", attempt_count: 1, updated_at: "2026-07-14T01:00:00Z" }, in_active_snapshot: false },
      ];
      await fulfill({ document, revision: revisionTwo, delivery: revisions[1].delivery }, 202);
      return;
    }
    if (pathname === "/api/kb/revisions/rev_002/publish" && method === "POST") {
      expect(headers["idempotency-key"]).toBeTruthy();
      activeSnapshotID = "snapshot_002";
      revisions = [
        revisions[0],
        { revision: { ...revisionTwo, status: "published" }, delivery: revisions[1].delivery, in_active_snapshot: true },
      ];
      await fulfill({
        document,
        snapshot: {
          snapshot_id: activeSnapshotID,
          status: "published",
          manifest_sha256: "manifest-002",
          created_at: "2026-07-14T01:05:00Z",
          published_at: "2026-07-14T01:05:00Z",
        },
      });
      return;
    }
    if (pathname === "/api/kb/documents/doc_001/disable" && method === "POST") {
      expect(headers["idempotency-key"]).toBeTruthy();
      document = { ...document, status: "disabled", updated_at: "2026-07-14T01:10:00Z" };
      activeSnapshotID = undefined;
      await fulfill({ document });
      return;
    }
    if (pathname === "/api/kb/documents/doc_001" && method === "DELETE") {
      expect(headers["idempotency-key"]).toBeTruthy();
      document = { ...document, status: "deleted", updated_at: "2026-07-14T01:15:00Z" };
      await fulfill({ document });
      return;
    }

    await fulfill({ error: { code: "not_found", message: "Unexpected lifecycle request" } }, 404);
  });

  await page.goto("/knowledge");
  await expect(page.getByRole("heading", { name: "文档与版本生命周期" })).toBeVisible();
  await page.locator("#knowledge-document-file-input").setInputFiles({
    name: "policy-v1.md",
    mimeType: "text/markdown",
    buffer: Buffer.from("# 报销政策\n初始版本"),
  });
  await page.getByTestId("knowledge-create-submit").click();
  await expect(page.getByText("已创建文档并接受 revision rev_001。")).toBeVisible();
  await page.getByTestId("open-knowledge-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();

  await page.locator("#knowledge-revision-file-input").setInputFiles({
    name: "policy-v2.md",
    mimeType: "text/markdown",
    buffer: Buffer.from("# 报销政策\n替换版本"),
  });
  await page.getByRole("button", { name: "提交 revision" }).click();
  await expect(page.getByText("已接受 replacement revision rev_002。")).toBeVisible();
  await expect(page.getByRole("button", { name: "发布" })).toHaveCount(0);
  await expect(page.getByText("停用与删除须在通过新知识验收后，由系统管理员在系统配置台审批。")).toBeVisible();
});

test("知识库管理员看不到系统级停用操作", async ({ page }) => {
  const document = {
    document_id: "doc_conflict",
    display_name: "冲突文档",
    owner_id: "admin",
    status: "active",
    created_at: "2026-07-14T00:00:00Z",
    updated_at: "2026-07-14T00:00:00Z",
  };
  const readyRevision = {
    revision_id: "rev_conflict",
    document_id: "doc_conflict",
    filename: "policy.md",
    mime_type: "text/markdown",
    source_format: "md",
    checksum_sha256: "checksum-conflict",
    parse_version: "parse-v1",
    index_version: "index-v1",
    status: "ready",
    created_at: "2026-07-14T00:00:00Z",
    updated_at: "2026-07-14T00:00:00Z",
  };

  await page.goto("/admin/login?next=/chat");
  await page.getByTestId("login-username").fill(knowledgeAdminUsername);
  await page.getByTestId("login-password").fill(knowledgeAdminPassword);
  await Promise.all([
    page.waitForURL(/\/chat(?:\/.*)?$/),
    page.getByTestId("login-submit").click(),
  ]);

  await page.route("**/api/kb/**", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;
    const method = request.method();
    const fulfill = async (body: unknown, status = 200) => {
      await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    };

    if (pathname === "/api/kb/files") {
      await fulfill({ items: [], total: 0 });
      return;
    }
    if (pathname === "/api/kb/documents" && method === "GET") {
      await fulfill({ items: [{ ...document, current_revision: readyRevision }], total: 1 });
      return;
    }
    if (pathname === "/api/kb/documents/doc_conflict" && method === "GET") {
      await fulfill({ document, revisions: [{ revision: readyRevision, delivery: { delivery_id: "delivery_conflict", status: "delivered", attempt_count: 1, updated_at: "2026-07-14T00:00:00Z" } }] });
      return;
    }
    if (pathname === "/api/kb/documents/doc_conflict/retirement-readiness" && method === "GET") {
      await fulfill({ readiness: { document_id: "doc_conflict", stage: "ready_to_disable", blockers: [] } });
      return;
    }
    if (pathname === "/api/kb/documents/doc_conflict/disable" && method === "POST") {
      expect(request.headers()["idempotency-key"]).toBeTruthy();
      await fulfill({ error: { code: "document_has_inflight_revision", message: "inflight revision" } }, 409);
      return;
    }
    await fulfill({ error: { code: "not_found", message: "Unexpected lifecycle request" } }, 404);
  });

  await page.goto("/knowledge");
  await page.getByTestId("open-knowledge-details").click();
  await expect(page.getByTestId("workspace-detail-dialog")).toBeVisible();
  await expect(page.getByRole("button", { name: "停用文档" })).toHaveCount(0);
  await expect(page.getByText("停用与删除须在通过新知识验收后，由系统管理员在系统配置台审批。")).toBeVisible();
});
