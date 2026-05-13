import { Container, getContainer } from "@cloudflare/containers";

export class Sandbox extends Container {
  override defaultPort = 8787;
  override sleepAfter = "30m";
  override enableInternet = true;
}

type Env = {
  Sandbox: DurableObjectNamespace<Sandbox>;
  CRABBOX_RUNNER_TOKEN?: string;
};

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url);

    if (url.pathname === "/health") {
      return json({ ok: true, runner: "cloudflare-container" });
    }

    const auth = authorize(request, env);
    if (auth) return auth;

    if (url.pathname === "/v1/sandboxes" && request.method === "POST") {
      return createSandbox(request, env);
    }

    const match = url.pathname.match(/^\/v1\/sandboxes\/([^/]+)(?:\/([^/]+))?$/);
    if (!match) return json({ error: "not found" }, 404);

    const sandboxID = decodeURIComponent(match[1] ?? "");
    const action = match[2] ?? "";

    if (request.method === "GET" && action === "") {
      return getSandboxStatus(env, sandboxID);
    }
    if (request.method === "DELETE" && action === "") {
      return destroySandbox(env, sandboxID);
    }
    if (request.method === "POST" && action === "files") {
      return uploadFile(request, env, sandboxID, url);
    }
    if (request.method === "POST" && action === "exec-stream") {
      return execStream(request, env, sandboxID);
    }

    return json({ error: "not found" }, 404);
  },
};

async function createSandbox(request: Request, env: Env): Promise<Response> {
  const body = await readObject(request);
  const sandboxID = cleanSandboxID(stringField(body, "id") ?? stringField(body, "leaseId") ?? "");
  if (!sandboxID) return json({ error: "id is required" }, 400);

  const workdir = cleanAbsolutePath(stringField(body, "workdir") ?? "/workspace/crabbox");
  if (!workdir) return json({ error: "workdir must be an absolute path" }, 400);

  const container = getContainer(env.Sandbox, sandboxID);
  await ensureReady(container);
  const prepare = await execContainer(container, {
    command: `mkdir -p ${shellQuote(workdir)}`,
    cwd: "/",
    labels: sanitizeLabels(body["labels"]),
    workdir,
  });
  if (!prepare.ok) return prepare;

  return json({
    id: sandboxID,
    state: "running",
    workdir,
    labels: sanitizeLabels(body["labels"]),
    createdAt: new Date().toISOString(),
  });
}

async function getSandboxStatus(env: Env, sandboxID: string): Promise<Response> {
  const id = cleanSandboxID(sandboxID);
  if (!id) return json({ error: "id is required" }, 400);
  const container = getContainer(env.Sandbox, id);
  const state = await container.getState();
  return json({
    id,
    state: state.status,
    workdir: "/workspace",
  });
}

async function destroySandbox(env: Env, sandboxID: string): Promise<Response> {
  const id = cleanSandboxID(sandboxID);
  if (!id) return json({ error: "id is required" }, 400);
  const container = getContainer(env.Sandbox, id);
  await container.destroy();
  return json({ id, state: "stopped" });
}

async function uploadFile(
  request: Request,
  env: Env,
  sandboxID: string,
  url: URL,
): Promise<Response> {
  const id = cleanSandboxID(sandboxID);
  if (!id) return json({ error: "id is required" }, 400);
  const remotePath = cleanAbsolutePath(url.searchParams.get("path") ?? "");
  if (!remotePath) return json({ error: "path must be absolute" }, 400);
  if (!request.body) return json({ error: "request body is required" }, 400);

  const container = getContainer(env.Sandbox, id);
  await ensureReady(container);
  const uploadURL = new URL("http://container/v1/files");
  uploadURL.searchParams.set("path", remotePath);
  return container.containerFetch(uploadURL, {
    method: "POST",
    body: request.body,
    headers: {
      "Content-Type": "application/octet-stream",
    },
  });
}

async function execStream(request: Request, env: Env, sandboxID: string): Promise<Response> {
  const id = cleanSandboxID(sandboxID);
  if (!id) return json({ error: "id is required" }, 400);

  const body = await readObject(request);
  const command = stringField(body, "command")?.trim() ?? "";
  if (!command) return json({ error: "command is required" }, 400);

  const cwd = cleanAbsolutePath(stringField(body, "cwd") ?? "/workspace/crabbox");
  if (!cwd) return json({ error: "cwd must be an absolute path" }, 400);

  const container = getContainer(env.Sandbox, id);
  await ensureReady(container);
  return execContainer(container, {
    command,
    cwd,
    env: sanitizeEnv(body["env"]),
    timeoutMs: numberField(body, "timeoutMs"),
  });
}

async function ensureReady(container: DurableObjectStub<Sandbox>): Promise<void> {
  await container.startAndWaitForPorts({
    ports: 8787,
    cancellationOptions: {
      instanceGetTimeoutMS: 120_000,
      portReadyTimeoutMS: 120_000,
      waitInterval: 1_000,
    },
  });
}

async function execContainer(
  container: DurableObjectStub<Sandbox>,
  payload: Record<string, unknown>,
): Promise<Response> {
  return container.containerFetch("http://container/v1/exec", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
}

function authorize(request: Request, env: Env): Response | null {
  const expected = env.CRABBOX_RUNNER_TOKEN;
  if (!expected) return json({ error: "runner token is not configured" }, 503);
  const header = request.headers.get("Authorization") ?? "";
  const actual = header.startsWith("Bearer ") ? header.slice("Bearer ".length) : "";
  if (actual !== expected) return json({ error: "unauthorized" }, 401);
  return null;
}

async function readObject(request: Request): Promise<Record<string, unknown>> {
  const value = await request.json();
  return isRecord(value) ? value : {};
}

function json(value: unknown, status = 200): Response {
  return Response.json(value, { status });
}

function cleanSandboxID(value: string): string {
  const trimmed = value.trim();
  if (!/^[A-Za-z0-9_.:-]{1,128}$/.test(trimmed)) return "";
  return trimmed;
}

function cleanAbsolutePath(value: string): string {
  const trimmed = value.trim();
  if (!trimmed.startsWith("/") || trimmed.includes("\0")) return "";
  return trimmed;
}

function sanitizeLabels(value: unknown): Record<string, string> {
  if (!isRecord(value)) return {};
  const out: Record<string, string> = {};
  for (const [key, raw] of Object.entries(value)) {
    if (typeof raw === "string" && /^[A-Za-z0-9_.:-]{1,64}$/.test(key)) {
      out[key] = raw.slice(0, 256);
    }
  }
  return out;
}

function sanitizeEnv(value: unknown): Record<string, string> | undefined {
  if (!isRecord(value)) return undefined;
  const out: Record<string, string> = {};
  for (const [key, raw] of Object.entries(value)) {
    if (typeof raw === "string" && /^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
      out[key] = raw;
    }
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

function stringField(value: Record<string, unknown>, key: string): string | undefined {
  const field = value[key];
  return typeof field === "string" ? field : undefined;
}

function numberField(value: Record<string, unknown>, key: string): number | undefined {
  const field = value[key];
  return typeof field === "number" ? field : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", "'\"'\"'")}'`;
}
