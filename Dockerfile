# For a local `docker build` from source. Release images are built by
# GoReleaser from the cross-compiled binary (Dockerfile.release); keep the
# runtime stage the same as that one.

# The UI (ui/), embedded in the binary by internal/webui.
FROM node:24-alpine AS ui
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY ui /src/ui
RUN mkdir -p /src/internal/webui/dist && npm run build

FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /src/internal/webui/dist internal/webui/dist
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hyve .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /hyve /usr/local/bin/hyve
USER 65532:65532
ENTRYPOINT ["hyve"]
CMD ["--help"]
