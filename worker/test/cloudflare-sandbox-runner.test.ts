import { beforeEach, describe, expect, it, vi } from "vitest";

type StoredValue = unknown;

class MemoryStorage {
  readonly values = new Map<string, StoredValue>();

  async get<T>(key: string): Promise<T | undefined> {
    return this.values.get(key) as T | undefined;
  }

  async put<T>(key: string, value: T): Promise<void> {
    this.values.set(key, value);
  }
}

class MockContainer {
  readonly ctx: { storage: MemoryStorage };
  readonly schedules: Array<{ when: Date | number; callback: string }> = [];
  deletedSchedules: string[] = [];
  destroyed = false;
  started = false;

  constructor(ctx: { storage: MemoryStorage }) {
    this.ctx = ctx;
  }

  async startAndWaitForPorts(): Promise<void> {
    this.started = true;
  }

  async containerFetch(input: string | URL): Promise<Response> {
    const url = new URL(String(input));
    if (url.pathname === "/v1/exec") {
      return new Response('{"type":"complete","exitCode":0}\n', {
        headers: { "Content-Type": "application/x-ndjson" },
      });
    }
    if (url.pathname === "/v1/files") {
      return Response.json({ ok: true, path: url.searchParams.get("path") });
    }
    return Response.json({ ok: true });
  }

  async getState(): Promise<{ status: string }> {
    return { status: "running" };
  }

  async destroy(): Promise<void> {
    this.destroyed = true;
  }

  deleteSchedules(name: string): void {
    this.deletedSchedules.push(name);
  }

  async schedule(when: Date | number, callback: string): Promise<unknown> {
    this.schedules.push({ when, callback });
    return {
      taskId: "task_1",
      callback,
      time: when instanceof Date ? when.getTime() / 1000 : when,
    };
  }

  async fetch(): Promise<Response> {
    return Response.json({ ok: true });
  }
}

vi.mock("@cloudflare/containers", () => ({
  Container: MockContainer,
  getContainer: (namespace: { get(id: string): unknown }, id: string) => namespace.get(id),
}));

const { Sandbox } = await import("../src/cloudflare_sandbox_runner");

describe("Cloudflare sandbox runner lifecycle", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-05-13T18:00:00Z"));
  });

  it("stores lease metadata and schedules cleanup at the idle deadline", async () => {
    const sandbox = new Sandbox({ storage: new MemoryStorage() });

    const response = await sandbox.fetch(
      new Request("http://crabbox.internal/__crabbox/create", {
        method: "POST",
        body: JSON.stringify({
          id: "cbx_test",
          workdir: "/workspace/repo",
          ttlSeconds: 3600,
          idleTimeoutSeconds: 600,
          labels: { repo: "my-app" },
        }),
      }),
    );

    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toMatchObject({
      id: "cbx_test",
      state: "running",
      expiresAt: "2026-05-13T18:10:00.000Z",
      idleTimeoutSeconds: 600,
      ttlSeconds: 3600,
    });
    expect(sandbox.started).toBe(true);
    expect(sandbox.schedules).toHaveLength(1);
    expect(sandbox.schedules[0]?.when).toEqual(new Date("2026-05-13T18:10:00Z"));
  });

  it("touches the lease after streamed command completion", async () => {
    const storage = new MemoryStorage();
    const sandbox = new Sandbox({ storage });
    await sandbox.fetch(
      new Request("http://crabbox.internal/__crabbox/create", {
        method: "POST",
        body: JSON.stringify({
          id: "cbx_test",
          workdir: "/workspace/repo",
          idleTimeoutSeconds: 600,
        }),
      }),
    );

    vi.setSystemTime(new Date("2026-05-13T18:05:00Z"));
    const response = await sandbox.fetch(
      new Request("http://crabbox.internal/__crabbox/exec-stream", {
        method: "POST",
        body: JSON.stringify({ command: "echo hi", cwd: "/workspace/repo" }),
      }),
    );
    await response.text();
    await vi.runAllTimersAsync();

    const status = await sandbox.fetch(new Request("http://crabbox.internal/__crabbox/status"));
    await expect(status.json()).resolves.toMatchObject({
      state: "running",
      lastTouchedAt: "2026-05-13T18:05:00.000Z",
      expiresAt: "2026-05-13T18:15:00.000Z",
    });
  });

  it("expires and destroys the container after the deadline", async () => {
    const sandbox = new Sandbox({ storage: new MemoryStorage() });
    await sandbox.fetch(
      new Request("http://crabbox.internal/__crabbox/create", {
        method: "POST",
        body: JSON.stringify({
          id: "cbx_test",
          workdir: "/workspace/repo",
          idleTimeoutSeconds: 10,
        }),
      }),
    );

    vi.setSystemTime(new Date("2026-05-13T18:00:11Z"));
    await sandbox.expireIfIdle();

    expect(sandbox.destroyed).toBe(true);
    const response = await sandbox.fetch(
      new Request("http://crabbox.internal/__crabbox/exec-stream", {
        method: "POST",
        body: JSON.stringify({ command: "echo hi", cwd: "/workspace/repo" }),
      }),
    );
    expect(response.status).toBe(410);
    await expect(response.json()).resolves.toMatchObject({
      error: "sandbox expired",
      state: "expired",
    });
  });
});
