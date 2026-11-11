const baseURL = (process.env.BACKEND_API_BASE_URL ?? "http://127.0.0.1:8080").replace(/\/$/, "");
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const seedDocuments = [
  { key: "travel-expense", displayName: "差旅与费用管理制度（本地开发示例）", filename: "差旅与费用管理制度.md" },
  { key: "information-security", displayName: "信息安全与访问管理规范（本地开发示例）", filename: "信息安全与访问管理规范.md" },
  { key: "leave-remote-work", displayName: "请假与远程办公制度（本地开发示例）", filename: "请假与远程办公制度.md" },
];
const seedDirectory = resolve(dirname(fileURLToPath(import.meta.url)), "../../data/local-dev-knowledge/v1");

async function main() {
  const cookie = await login();
  for (const document of seedDocuments) {
    const existing = await findDocument(cookie, document.displayName);
    if (existing?.activeSnapshotId) {
      console.log(`Seed document already published: ${document.displayName} (${existing.activeSnapshotId})`);
      continue;
    }
    const revision = existing ?? (await uploadSeedDocument(cookie, document));
    await waitForReadyRevision(cookie, revision.documentId, revision.revisionId);
    const published = await request(cookie, `/api/kb/revisions/${encodeURIComponent(revision.revisionId)}/publish`, {
      method: "POST",
      headers: { "Idempotency-Key": `local-rag-seed-publish-${document.key}-${revision.revisionId}` },
    });
    console.log(`Published ${document.displayName}: ${published.snapshot?.snapshot_id ?? "unknown"}`);
  }
}

async function login() {
  const response = await fetch(`${baseURL}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username: process.env.SMOKE_ADMIN_USERNAME ?? "admin", password: process.env.SMOKE_ADMIN_PASSWORD ?? "admin123" }),
  });
  await ensureSuccess(response, "admin login");

  const setCookies = typeof response.headers.getSetCookie === "function" ? response.headers.getSetCookie() : [response.headers.get("set-cookie") ?? ""];
  const cookie = setCookies
    .filter(Boolean)
    .map((value) => value.split(";", 1)[0])
    .join("; ");
  if (!cookie) {
    throw new Error("admin login did not return a session cookie");
  }
  return cookie;
}

async function findDocument(cookie, displayName) {
  const page = await request(cookie, "/api/kb/documents?limit=100");
  const document = page.items?.find((item) => item.display_name === displayName);
  if (!document) {
    return undefined;
  }

  const detail = await request(cookie, `/api/kb/documents/${encodeURIComponent(document.document_id)}`);
  const publishedRevision = detail.revisions?.find((item) => item.revision?.status === "published")?.revision;
  const revision = detail.revisions?.find((item) => ["uploaded", "queued", "indexing", "ready"].includes(item.revision?.status))?.revision;
  if (!revision?.revision_id && !publishedRevision?.revision_id) {
    throw new Error(`seed document ${document.document_id} has no usable revision`);
  }
  return {
    documentId: document.document_id,
    revisionId: revision?.revision_id ?? publishedRevision?.revision_id,
    // `active_snapshot_id` in a document detail response is the global active
    // pointer. Only a published revision proves this specific document belongs
    // to that snapshot.
    activeSnapshotId: publishedRevision ? detail.active_snapshot_id : undefined,
  };
}

async function uploadSeedDocument(cookie, document) {
  const content = await readFile(resolve(seedDirectory, document.filename));
  const body = new FormData();
  body.append("file", new Blob([content], { type: "text/markdown" }), document.filename);
  body.append("display_name", document.displayName);
  const payload = await request(cookie, "/api/kb/documents", {
    method: "POST",
    headers: { "Idempotency-Key": `local-rag-seed-${document.key}-v1` },
    body,
  });
  const documentId = payload.document?.document_id;
  const revisionId = payload.revision?.revision_id;
  if (!documentId || !revisionId) {
    throw new Error(`bootstrap upload returned an incomplete lifecycle payload: ${JSON.stringify(payload)}`);
  }
  console.log(`Uploaded ${document.displayName}: ${revisionId}`);
  return { documentId, revisionId };
}

async function waitForReadyRevision(cookie, documentId, revisionId) {
  if (!revisionId) {
    throw new Error("a revision id is required to await indexing");
  }
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const detail = await request(cookie, `/api/kb/documents/${encodeURIComponent(documentId)}`);
    const revision = detail.revisions?.find((item) => item.revision?.revision_id === revisionId)?.revision;
    if (revision?.status === "ready") {
      console.log(`Revision indexed and ready: ${revisionId}`);
      return;
    }
    if (revision?.status === "failed") {
      throw new Error(`seed revision ${revisionId} failed indexing`);
    }
    await sleep(1000);
  }
  throw new Error(`timed out waiting for seed revision ${revisionId} to become ready; ensure fullstack Backend and Runtime are running`);
}

async function request(cookie, path, init = {}) {
  const headers = new Headers(init.headers);
  headers.set("Cookie", cookie);
  const response = await fetch(`${baseURL}${path}`, { ...init, headers });
  await ensureSuccess(response, `${init.method ?? "GET"} ${path}`);
  return response.json();
}

async function ensureSuccess(response, operation) {
  if (response.ok) {
    return;
  }
  const body = await response.text();
  throw new Error(`${operation} failed with ${response.status}: ${body}`);
}

function sleep(milliseconds) {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

main().catch((error) => {
  console.error(error instanceof Error ? error.message : error);
  process.exitCode = 1;
});
