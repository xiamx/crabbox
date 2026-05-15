FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.26-bookworm AS runner-build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY cloudflare-container-runner/go.mod cloudflare-container-runner/main.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/crabbox-cloudflare-container-runner .

FROM docker.io/library/golang:1.26-bookworm AS go-runtime

FROM docker.io/library/node:24-bookworm

ARG TARGETARCH=amd64
ARG GH_VERSION=2.92.0
ARG PNPM_VERSION=10.24.0
ENV NPM_CONFIG_CACHE=/var/cache/crabbox/npm \
    PATH=/usr/local/go/bin:$PATH

RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl git jq ripgrep tar \
  && mkdir -p /var/cache/crabbox/npm /var/cache/crabbox/pnpm \
  && rm -rf /var/lib/apt/lists/* \
  && case "${TARGETARCH}" in \
      amd64|arm64) gh_arch="${TARGETARCH}" ;; \
      *) echo "unsupported GitHub CLI target arch: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
  && curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_${gh_arch}.tar.gz" -o /tmp/gh.tgz \
  && tar -xzf /tmp/gh.tgz -C /tmp \
  && install -m 0755 "/tmp/gh_${GH_VERSION}_linux_${gh_arch}/bin/gh" /usr/local/bin/gh \
  && rm -rf /tmp/gh.tgz "/tmp/gh_${GH_VERSION}_linux_${gh_arch}" \
  && corepack enable \
  && corepack prepare "pnpm@${PNPM_VERSION}" --activate \
  && pnpm config set store-dir /var/cache/crabbox/pnpm

COPY --from=go-runtime /usr/local/go /usr/local/go
COPY --from=runner-build /out/crabbox-cloudflare-container-runner /usr/local/bin/crabbox-cloudflare-container-runner
RUN ln -sf /usr/local/go/bin/go /usr/local/bin/go \
  && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

WORKDIR /workspace
EXPOSE 8787
ENTRYPOINT ["/usr/local/bin/crabbox-cloudflare-container-runner"]
