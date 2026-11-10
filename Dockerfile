# The dashboard is built to static files and embedded in the Go binary, so the
# final image runs one process and contains no Node runtime.
FROM node:24.21.0-bookworm-slim AS web-build
WORKDIR /app
COPY package.json package-lock.json ./
COPY apps/web/package.json apps/web/package.json
COPY apps/docs/package.json apps/docs/package.json
COPY packages/clients/typescript/package.json packages/clients/typescript/package.json
COPY packages/clients/generator/package.json packages/clients/generator/package.json
RUN npm ci --workspace @scratchpad/web
COPY apps/web apps/web
# Vite writes the build to backend/internal/httpapi/dashboard/dist.
RUN npm run build -w @scratchpad/web

FROM golang:1.27.3-bookworm AS go-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend ./
COPY --from=web-build /app/backend/internal/httpapi/dashboard/dist ./internal/httpapi/dashboard/dist
RUN test -f internal/httpapi/dashboard/dist/index.html \
    && CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w" -o /scratchpad-server ./cmd/scratchpad-server

# openssh-client provides ssh-keygen, which the server runs to verify SSH
# sign-in proofs; ca-certificates is for outbound HTTPS (GitHub, AI providers).
FROM debian:bookworm-slim AS go-runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends openssh-client ca-certificates=20250419~deb12u2 \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 1000 --user-group --no-create-home --shell /usr/sbin/nologin scratchpad \
    && mkdir -m 0700 /data && chown scratchpad:scratchpad /data
ENV SCRATCHPAD_LISTEN_ADDR=0.0.0.0:3000 SCRATCHPAD_DATABASE_PATH=/data/scratchpad.sqlite
COPY --from=go-build /scratchpad-server /usr/local/bin/scratchpad-server
USER 1000:1000
EXPOSE 3000
# The image has no HTTP client; bash's /dev/tcp reads the readiness status line.
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s CMD ["bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/3000 && printf 'GET /ready HTTP/1.0\\r\\n\\r\\n' >&3 && head -n 1 <&3 | grep -q ' 200 '"]
ENTRYPOINT ["/usr/local/bin/scratchpad-server"]
CMD ["serve"]

# `go-runtime` is the name deployment runbooks build by target; an ordinary
# image build produces the same image.
FROM go-runtime AS runtime
