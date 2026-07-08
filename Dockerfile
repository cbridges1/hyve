# This Dockerfile is only used for local `docker build` testing. Release
# images are built by GoReleaser (see .goreleaser.yaml) from the
# already-cross-compiled binary for each target platform — GoReleaser's
# docker build never runs this multi-stage Go build stage itself, only the
# runtime stage's package list is what actually matters for release images.
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hyve .

FROM alpine:3.20

# Tools module auth/create/delete/status scripts commonly shell out to:
# git for repo operations, kubectl/helm for spec.resources, curl/jq/openssl
# for auth flows. Cloud-provider CLIs (aws/gcloud/az/civo) are intentionally
# not included — install whichever your modules need on top of this image.
RUN apk add --no-cache ca-certificates bash git curl jq openssl \
    kubectl helm

COPY --from=builder /hyve /usr/local/bin/hyve

WORKDIR /repo
ENTRYPOINT ["hyve"]
CMD ["--help"]
