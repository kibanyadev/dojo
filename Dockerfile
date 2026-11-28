# Go 1.27.4 matches go.mod: the official golang image has this exact tag.
ARG GO_IMAGE=golang:1.27.4-alpine3.24@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280

ARG NODE_IMAGE=node:24.22.3-alpine3.24@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5
ARG CADDY_IMAGE=caddy:2.11.9-alpine@sha256:003a97808e781aa9f7e75cc2f43d34d461099031faf5e20faefb9f61126836f0

# The fake GitHub needs git and ssh-keygen to answer commit reads; same Alpine release as the images above.
ARG ALPINE_IMAGE=alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

FROM ${GO_IMAGE} AS build-api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/dojo-api ./cmd/dojo-api
COPY internal/server ./internal/server
COPY internal/dbpool ./internal/dbpool
COPY internal/github ./internal/github
COPY internal/mail ./internal/mail
COPY internal/storage ./internal/storage
COPY internal/catalog ./internal/catalog
COPY internal/mentor ./internal/mentor
COPY internal/ai ./internal/ai
COPY internal/webpush ./internal/webpush
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dojo-api ./cmd/dojo-api

FROM ${GO_IMAGE} AS build-admin
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/dojo-admin ./cmd/dojo-admin
COPY internal/admin ./internal/admin
COPY internal/dbpool ./internal/dbpool
COPY internal/github ./internal/github
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dojo-admin ./cmd/dojo-admin

FROM ${GO_IMAGE} AS build-catalog-load
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/dojo-catalog-load ./cmd/dojo-catalog-load
COPY internal/catalog ./internal/catalog
COPY internal/catalogload ./internal/catalogload
COPY internal/dbpool ./internal/dbpool
COPY internal/storage ./internal/storage
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/dojo-catalog-load ./cmd/dojo-catalog-load

FROM ${GO_IMAGE} AS build-fake-github
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/fake-github ./cmd/fake-github
COPY internal/fakegithub ./internal/fakegithub
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/fake-github ./cmd/fake-github

FROM ${GO_IMAGE} AS build-fake-push
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/fake-push ./cmd/fake-push
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/fake-push ./cmd/fake-push

FROM ${GO_IMAGE} AS build-goose
# Pinned migration tool, built here so it never enters go.mod. Keep in step with the Makefile.
ARG GOOSE_VERSION=v3.30.1
RUN CGO_ENABLED=0 GOBIN=/out go install -trimpath -ldflags='-s -w' \
    -tags='no_clickhouse no_mssql no_mysql no_sqlite3 no_vertica no_ydb no_turso' \
    github.com/pressly/goose/v3/cmd/goose@${GOOSE_VERSION}

FROM scratch AS api
COPY --from=build-api /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-api /out/dojo-api /dojo-api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/dojo-api"]

# The operator service (ADR 0002): a separate image, never behind the edge.
FROM scratch AS dojo-admin
COPY --from=build-admin /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-admin /out/dojo-admin /dojo-admin
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/dojo-admin"]

# The catalog load job (design 3.8): a one-off task with the migration role, never a service.
FROM scratch AS dojo-catalog-load
COPY --from=build-catalog-load /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-catalog-load /out/dojo-catalog-load /dojo-catalog-load
USER 65532:65532
ENTRYPOINT ["/dojo-catalog-load"]

# The GitHub stand-in for local and test stacks only; never part of the api image.
FROM ${ALPINE_IMAGE} AS fake-github
# ssh-keygen refuses to run as a uid with no passwd entry, so 65532 gets one.
RUN apk add --no-cache git openssh-keygen && adduser -D -H -u 65532 -h /tmp fake
COPY --from=build-fake-github /out/fake-github /fake-github
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/fake-github"]

# The Web Push stand-in for local and test stacks only.
FROM scratch AS fake-push
COPY --from=build-fake-push /out/fake-push /fake-push
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/fake-push"]

FROM scratch AS migrate
COPY --from=build-goose /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build-goose /out/goose /goose
COPY db/migrations /migrations
USER 65532:65532
ENV GOOSE_DRIVER=postgres GOOSE_MIGRATION_DIR=/migrations
ENTRYPOINT ["/goose"]
CMD ["up"]

FROM ${NODE_IMAGE} AS build-web
# Same pnpm as web/package.json (corepack checks its hash); keep both Node pins in step with web/.nvmrc and app/.nvmrc.
ENV ASTRO_TELEMETRY_DISABLED=1
ARG PUBLIC_ORIGIN=http://localhost:8080
ENV PUBLIC_ORIGIN=${PUBLIC_ORIGIN}
WORKDIR /src
RUN corepack enable
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml web/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/astro.config.mjs ./
COPY web/src ./src
RUN pnpm build

FROM ${NODE_IMAGE} AS build-app
# The signed-in SPA: its own package, sharing only the design tokens with web/.
WORKDIR /src/app
RUN corepack enable
COPY app/package.json app/pnpm-lock.yaml app/pnpm-workspace.yaml app/.npmrc ./
RUN pnpm install --frozen-lockfile
COPY web/src/styles/tokens.css /src/web/src/styles/tokens.css
COPY app/index.html app/tsconfig.json app/vite.config.ts ./
COPY app/src ./src
COPY app/public ./public
RUN pnpm build

FROM ${CADDY_IMAGE} AS edge
COPY caddy/Caddyfile /etc/caddy/Caddyfile
COPY --from=build-web /src/dist /srv
COPY --from=build-app /src/app/dist /srv/app
USER 65532:65532
EXPOSE 8080
