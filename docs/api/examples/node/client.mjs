// A small vde client for Node 20+: submit a document, poll with backoff, return the job.
// No dependencies: global fetch, FormData and Blob.
//
// Run it server-side only. EXTRACTION_TOKEN is a service credential and must never reach
// a browser or a shipped app; a web or mobile client talks to your own backend, which
// runs code like this.
//
//   EXTRACTION_URL=http://127.0.0.1:8090 EXTRACTION_TOKEN=... \
//     node docs/api/examples/node/client.mjs [document] [extractor.md] [judge.md] [schema.json]
//
// The arguments default to the generic sample in examples/samples/.

import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const TERMINAL = new Set(["accepted", "rejected", "failed"]);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export class ApiError extends Error {
  constructor(status, body, headers) {
    const e = body?.error ?? {};
    super(`${status} ${e.code ?? "error"}: ${e.message ?? ""}`.trim());
    this.status = status;
    this.code = e.code;
    this.requestId = e.request_id;
    this.retryAfter = headers.get("retry-after");
    this.quotaRemaining = headers.get("x-quota-remaining");
    this.quotaReset = headers.get("x-quota-reset");
  }

  // 400, 413, 415 and 422 mean the request itself is wrong; 401, 403 and 404 are
  // configuration or lifecycle problems. Everything else (429, 5xx) is worth retrying.
  get retryable() {
    return this.status === 429 || this.status >= 500;
  }
}

export class Client {
  constructor({ url, token }) {
    if (!url || !token) throw new Error("url and token are required");
    this.base = url.replace(/\/+$/, "") + "/v1";
    this.token = token;
  }

  async #request(method, path, init = {}) {
    const res = await fetch(this.base + path, {
      method,
      ...init,
      headers: { Authorization: `Bearer ${this.token}`, ...init.headers },
    });
    if (res.status === 204) return { res, body: null };
    const body = await res.json().catch(() => null);
    if (!res.ok) throw new ApiError(res.status, body, res.headers);
    return { res, body };
  }

  // submit sends one document. `document` is {bytes, type, name}; `pages` an optional list
  // of {bytes, type, name} (PNG or JPEG, page 1 first). `schema` may be omitted for
  // schema-less mode. Resolves to the job (status "queued").
  async submit({ document, pages = [], extractor, judge, schema, retention = "none", maxAttempts, idempotencyKey }) {
    const form = new FormData();
    const part = (p) => [new Blob([p.bytes], { type: p.type }), p.name ?? "document"];
    form.append("document", ...part(document));
    for (const p of pages) form.append("page", ...part(p));
    form.append("extractor_instructions", new Blob([extractor], { type: "text/markdown" }), "extractor.md");
    form.append("judge_instructions", new Blob([judge], { type: "text/markdown" }), "judge.md");
    if (schema !== undefined) {
      form.append("schema", new Blob([schema], { type: "application/schema+json" }), "schema.json");
    }
    form.append("retention", retention);
    if (maxAttempts) form.append("max_attempts", String(maxAttempts));

    const headers = {};
    if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
    // Do not set Content-Type: fetch adds the multipart boundary itself.
    const { body } = await this.#request("POST", "/jobs", { body: form, headers });
    return body;
  }

  get(id) {
    return this.#request("GET", `/jobs/${encodeURIComponent(id)}`).then((r) => r.body);
  }

  delete(id) {
    return this.#request("DELETE", `/jobs/${encodeURIComponent(id)}`).then(() => undefined);
  }

  quota() {
    return this.#request("GET", "/quota").then((r) => r.body);
  }

  // wait polls from 2 s, doubling to 30 s with a little jitter, until the job is terminal.
  // A poll that fails with 429, 5xx or a network error is repeated; the third failure in
  // a row is thrown. With retention "none" the terminal read is the only one: keep it.
  async wait(id, { timeoutMs = 10 * 60_000, onPoll } = {}) {
    const deadline = Date.now() + timeoutMs;
    let delay = 2000;
    let failures = 0;
    for (;;) {
      await sleep(delay * (1 + Math.random() * 0.1));
      try {
        const job = await this.get(id);
        failures = 0;
        onPoll?.(job);
        if (TERMINAL.has(job.status)) return job;
      } catch (err) {
        const transient = !(err instanceof ApiError) || err.retryable;
        if (!transient || ++failures >= 3) throw err;
      }
      if (Date.now() > deadline) throw new Error(`job ${id} not terminal after ${timeoutMs} ms`);
      delay = Math.min(delay * 2, 30_000);
    }
  }
}

// extract submits and waits. The idempotency key is a hash of everything that makes the
// request the same, so repeating a call after a lost response cannot pay twice.
export async function extract(client, args) {
  const hash = createHash("sha256");
  for (const p of [args.document.bytes, args.extractor, args.judge, args.schema ?? ""]) hash.update(p);
  const job = await client.submit({ ...args, idempotencyKey: args.idempotencyKey ?? hash.digest("hex") });
  return client.wait(job.id, { onPoll: (j) => console.error(`status: ${j.status}`) });
}

async function main() {
  const samples = resolve(dirname(fileURLToPath(import.meta.url)), "../../../../examples/samples");
  const [docPath = join(samples, "booking.txt"), extPath = join(samples, "extractor.md"),
    judgePath = join(samples, "judge.md"), schemaPath = join(samples, "schema.json")] = process.argv.slice(2);

  const client = new Client({ url: process.env.EXTRACTION_URL, token: process.env.EXTRACTION_TOKEN });
  const job = await extract(client, {
    document: { bytes: await readFile(docPath), type: "text/plain; charset=utf-8", name: basename(docPath) },
    extractor: await readFile(extPath, "utf8"),
    judge: await readFile(judgePath, "utf8"),
    schema: await readFile(schemaPath, "utf8"),
  });

  console.log(JSON.stringify(job, null, 2));
  process.exitCode = job.status === "accepted" ? 0 : 1;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((err) => {
    console.error(err instanceof ApiError ? `${err.message} (request ${err.requestId})` : err);
    process.exitCode = 2;
  });
}
