# Builds the trusted core (pact-core, with the dashboard and demo seed data) and
# the mock targets from one context.

# --- dashboard: Next.js static export ---
FROM node:22-alpine AS web
RUN corepack enable
WORKDIR /src
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml* ./
COPY web/package.json web/
RUN pnpm install --frozen-lockfile || pnpm install
COPY web web
# The Docs pages in the dashboard are rendered from the same Markdown files as docs/ on GitHub.
COPY docs docs
RUN pnpm --filter web build

# --- Go binaries ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
COPY targets/mock targets/mock
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/pact-core ./cmd/pact-core \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mock ./targets/mock

# --- mock targets: deploy, secrets, iam, sink (ROLE selects) ---
FROM alpine:3.21 AS mock
RUN adduser -D -u 10003 mock
COPY --from=build /out/mock /usr/local/bin/mock
USER 10003
EXPOSE 8000
ENTRYPOINT ["/usr/local/bin/mock"]

# --- pact-core ---
FROM alpine:3.21 AS pact-core
RUN apk add --no-cache ca-certificates
COPY --from=build /out/pact-core /usr/local/bin/pact-core
COPY demo/web-app /seed/web-app
COPY demo/scenarios /seed/scenarios
COPY --from=web /src/web/out /app/web
ENV PACT_SEED_DIR=/seed/web-app PACT_SCENARIOS_DIR=/seed/scenarios PACT_WEB_DIR=/app/web \
    PACT_WORKSPACE_DIR=/workspace PACT_DATA_DIR=/data
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/pact-core"]
