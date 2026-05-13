# Cloudflare Sandbox Provider

Use `provider: cloudflare-sandbox` when Crabbox should run commands through a
Cloudflare Worker backed by a custom Cloudflare Containers image.

Cloudflare Sandbox is a delegated run provider. Crabbox owns local repo archive
creation, local lease claims, timing output, command rendering, and friendly
slugs. A small Worker runner owns container creation, file upload, command
execution, and teardown.

## Requirements

- A Cloudflare account with Workers, Durable Objects, and Containers access.
- Wrangler authenticated for deploys.
- Docker or a Docker-compatible CLI/daemon available to Wrangler for container
  image builds.
- A deployed Crabbox Cloudflare Sandbox runner with `CRABBOX_RUNNER_TOKEN` set
  as a Worker secret.

The Worker coordinator lives in `worker/src/cloudflare_sandbox_runner.ts`. The
container image is built from `worker/cloudflare-sandbox.Dockerfile` and starts
the HTTP runner in `worker/cloudflare-container-runner`. The deploy config is
`worker/wrangler.cloudflare-sandbox.jsonc`.

## Configuration

```yaml
provider: cloudflare-sandbox
cloudflareSandbox:
  apiUrl: https://crabbox-cloudflare-sandbox-runner.example.workers.dev
  workdir: /workspace/crabbox
```

Keep the bearer token in `CRABBOX_CLOUDFLARE_SANDBOX_TOKEN` or user-level
config, not in repo YAML. `CRABBOX_CLOUDFLARE_SANDBOX_URL` or
`CRABBOX_CLOUDFLARE_SANDBOX_API_URL` can also provide the runner URL.

Equivalent flags:

```sh
crabbox run \
  --provider cloudflare-sandbox \
  --cloudflare-sandbox-url https://runner.example.workers.dev \
  --cloudflare-sandbox-token "$CRABBOX_CLOUDFLARE_SANDBOX_TOKEN" \
  -- pnpm test
```

## Runner Deploy

Install Worker dependencies and verify the runner:

```sh
npm ci --prefix worker
npm run check:cloudflare-sandbox --prefix worker
npm run build:cloudflare-sandbox --prefix worker
```

Deploy with:

```sh
npm run deploy:cloudflare-sandbox --prefix worker
```

Then set the bearer token:

```sh
printf '%s' "$CRABBOX_CLOUDFLARE_SANDBOX_TOKEN" \
  | npx wrangler secret put CRABBOX_RUNNER_TOKEN \
      --config worker/wrangler.cloudflare-sandbox.jsonc
```

## Behavior

- `run` creates or reuses a Container Durable Object, uploads a gzipped archive
  of the local checkout, extracts it into `workdir`, and relays command output
  and exit status back to the CLI.
- `warmup` creates the sandbox and prepares the workdir. Warmed sandboxes remain
  alive until `crabbox stop`.
- `status` and `stop` resolve Crabbox's local claim and call the runner.
- `list` reports local Cloudflare Sandbox claims. Cloudflare does not expose a
  global Sandbox listing API through the runner.
- The container image includes common repo-test tools such as Git, GitHub CLI,
  `jq`, `ripgrep`, Node, and `pnpm`; project-specific dependencies still belong
  to the repo's own setup commands.

## Limitations

- SSH, VNC, browser desktop, code-server, Actions hydration, and `--download`
  are not supported.
- `--fresh-pr` is not supported for delegated archive sync.
- `--checksum` is not supported because the provider uses archive upload and
  extraction instead of Crabbox rsync.
- Container size and concurrency are controlled by
  `worker/wrangler.cloudflare-sandbox.jsonc`. Choose an `instance_type` and
  `max_instances` that match the account's Cloudflare Containers limits.
