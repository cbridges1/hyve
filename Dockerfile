# This Dockerfile is only used for local `docker build` testing. Release
# images are built by GoReleaser (see .goreleaser.yaml) from the
# already-cross-compiled binary for each target platform — GoReleaser's
# docker build never runs this multi-stage Go build stage itself, only the
# runtime stage's package list is what actually matters for release images.
# The web console is embedded into the binary (internal/webui); only a
# placeholder is committed, so build the real one first.
FROM node:24-alpine AS ui-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build

FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui-builder /src/web/dist/ internal/webui/dist/
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hyve .

FROM alpine:3.20

# Tools module auth/create/delete/status scripts commonly shell out to:
# git for repo operations, kubectl/helm for spec.resources, curl/jq/openssl
# for auth flows. Cloud-provider CLIs (aws/gcloud/az/civo) are intentionally
# not included — install whichever your modules need on top of this image.
RUN apk add --no-cache ca-certificates bash git curl jq openssl \
    kubectl helm

COPY --from=builder /hyve /usr/local/bin/hyve
ENV HOME=/root
RUN mkdir -p /var/lib/hyve/modules

WORKDIR /repo
ENTRYPOINT ["hyve"]
CMD ["--help"]
