# OVH Cloud Provider

Read when:

- choosing `provider: ovh`;
- setting up OVHcloud Public Cloud credentials for Crabbox;
- debugging OVH region, flavor, image, or cleanup behavior;
- changing `internal/providers/ovh` or `internal/cli/ovh.go`.

OVH Cloud is a managed SSH lease provider for Linux Public Cloud instances.
Crabbox provisions the instance, SSH key, security rules, boot disk, and public
IP. After the instance exists, the normal Crabbox SSH path owns readiness, sync,
command execution, results, touch labels, release, and cleanup.

## When To Use

Use OVH when:

- your billing, compliance, or existing footprint is already on OVHcloud;
- you want Linux Public Cloud capacity in Europe, Canada, or APAC regions;
- you need a cost-effective alternative to the three largest cloud providers.

Provider names:

```text
ovh
ovhcloud
ovh-cloud
```

`ovhcloud` and `ovh-cloud` are aliases. Crabbox canonicalizes them to `ovh`
before direct or brokered lease requests.

## Target Matrix

| Target OS | Windows Mode | Supported |
|---|---|---|
| linux | — | yes |
| windows | normal | no |
| windows | wsl2 | no |
| macos | — | no |

## Configuration

### YAML

```yaml
provider: ovh
ovh:
  endpoint: ovh-eu           # API endpoint (ovh-eu, ovh-ca, ovh-us)
  applicationKey: ""         # OVH Application Key
  applicationSecret: ""      # OVH Application Secret
  consumerKey: ""            # OVH Consumer Key
  serviceName: ""            # Public Cloud project ID
  region: GRA11              # default region
  image: "Ubuntu 24.04"      # image name (partial match)
  rootGB: 400                # root disk size in GB
  sshCIDRs: []               # SSH source CIDRs
```

### Environment Variables

| Variable | Config Path |
|---|---|
| `CRABBOX_OVH_ENDPOINT` | `ovh.endpoint` |
| `CRABBOX_OVH_APPLICATION_KEY` / `OVH_APPLICATION_KEY` | `ovh.applicationKey` |
| `CRABBOX_OVH_APPLICATION_SECRET` / `OVH_APPLICATION_SECRET` | `ovh.applicationSecret` |
| `CRABBOX_OVH_CONSUMER_KEY` / `OVH_CONSUMER_KEY` | `ovh.consumerKey` |
| `CRABBOX_OVH_SERVICE_NAME` / `OVH_SERVICE_NAME` | `ovh.serviceName` |
| `CRABBOX_OVH_REGION` | `ovh.region` |
| `CRABBOX_OVH_IMAGE` | `ovh.image` |
| `CRABBOX_OVH_ROOT_GB` | `ovh.rootGB` |

## Class Mapping

| Class | Default Flavor | Fallback |
|---|---|---|
| standard | b3-8 | b2-7 |
| fast | b3-32 | b2-30, c3-32 |
| large | b3-64 | b2-60, c3-64 |
| beast | b3-128 | b2-120, c3-128 |

## Features

- SSH access via provisioned public IP
- Crabbox sync (rsync via SSH)
- Cleanup (orphan instance reaping)
- Tailscale overlay support
- Coordinator support (brokered mode via Cloudflare Worker)

## Unsupported

- Spot/preemptible instances (OVH does not offer spot pricing on Public Cloud)
- Windows targets
- Snapshots/checkpoints
- Desktop, browser, or code capabilities

## Lifecycle

1. Resolve endpoint, region, flavor, and image from config.
2. Create a per-lease SSH key on the OVH project.
3. Look up the flavor by name in the configured region; fail with a clear
   message if the flavor is not found.
4. Look up the image by name substring in the configured region.
5. Provision the instance with SSH key, monthly billing disabled.
6. Wait for the public IPv4 address (up to 2-minute timeout).
7. Wait for SSH readiness and the Crabbox ready marker.
8. Touch labels during active runs (labels are tracked via coordinator or
   local claim files; OVH has no native label support).
9. Delete the instance on release, also cleaning up the per-lease SSH key.

## Direct Auth

Direct mode uses OVH's Application Key + Secret + Consumer Key credential model
via the `go-ovh` SDK. All three credentials and the project service name (ID)
are required.

Local setup:

1. Go to https://api.ovh.com/createToken/ and create a token with the
   following API path permissions:
   - `GET /cloud/project/*`
   - `POST /cloud/project/*/instance`
   - `GET /cloud/project/*/instance/*`
   - `DELETE /cloud/project/*/instance/*`
   - `GET /cloud/project/*/flavor`
   - `GET /cloud/project/*/image`
   - `POST /cloud/project/*/sshkey`
   - `DELETE /cloud/project/*/sshkey/*`
2. Set the credentials as environment variables or in your Crabbox config.

```sh
export OVH_APPLICATION_KEY="your-app-key"
export OVH_APPLICATION_SECRET="your-app-secret"
export OVH_CONSUMER_KEY="your-consumer-key"
export OVH_SERVICE_NAME="your-project-id"
```

Smoke test:

```sh
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
printf 'provider: ovh\n' > "$tmp"
env -u CRABBOX_COORDINATOR -u CRABBOX_COORDINATOR_TOKEN \
  CRABBOX_CONFIG="$tmp" \
  crabbox run --provider ovh --market on-demand --no-sync -- echo ovh-ok
```

## Brokered Auth

Brokered mode uses Worker-side OVH credentials. Developer machines do not
need OVH credentials when the coordinator owns provisioning.

Required Worker secrets:

```text
OVH_ENDPOINT
OVH_APPLICATION_KEY
OVH_APPLICATION_SECRET
OVH_CONSUMER_KEY
OVH_SERVICE_NAME
```

## Troubleshooting

`ovh application key is required`

Set `CRABBOX_OVH_APPLICATION_KEY` or `ovh.applicationKey` in your config.

`ovh flavor "b3-8" not found in region GRA11`

The configured flavor is not available in the chosen region. Try a different
region or a different flavor class. Run `crabbox doctor --provider ovh` to
check reachability.

`ovh image "Ubuntu 24.04" not found in region`

The image name is not available in this region. Images vary by region. Try
"Ubuntu 22.04" or list available images through the OVH API.

## Limitations

- Linux only.
- No spot/preemptible instances.
- No native label support (labels tracked via coordinator or claim files).
- No cross-region flavor fallback (unlike GCP).
- SSH key cleanup on ReleaseLease is not yet implemented (keys remain on
  the project after lease end).
- Image matching uses substring comparison, which may match unexpected images.

Related docs:

- [Provider reference](README.md)
- [Provider backends](../provider-backends.md)
- [Capacity and fallback](../features/capacity-fallback.md)
