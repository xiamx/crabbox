FROM docker.io/library/golang:1.25-bookworm AS runner-build

WORKDIR /src
COPY cloudflare-container-runner/go.mod cloudflare-container-runner/main.go ./
RUN go build -trimpath -ldflags="-s -w" -o /out/crabbox-cloudflare-runner .

FROM docker.io/library/node:24-bookworm

ARG GH_VERSION=2.92.0
ARG PNPM_VERSION=10.24.0

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git jq ripgrep tar \
  && rm -rf /var/lib/apt/lists/* \
  && curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.tar.gz" -o /tmp/gh.tgz \
  && tar -xzf /tmp/gh.tgz -C /tmp \
  && install -m 0755 "/tmp/gh_${GH_VERSION}_linux_amd64/bin/gh" /usr/local/bin/gh \
  && rm -rf /tmp/gh.tgz "/tmp/gh_${GH_VERSION}_linux_amd64" \
  && corepack enable \
  && corepack prepare "pnpm@${PNPM_VERSION}" --activate

COPY --from=runner-build /out/crabbox-cloudflare-runner /usr/local/bin/crabbox-cloudflare-runner

WORKDIR /workspace
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/crabbox-cloudflare-runner"]
