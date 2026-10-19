# For a local `docker build` from source. Release images are built by
# GoReleaser from the cross-compiled binary (Dockerfile.release); keep the
# runtime stage the same as that one.
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /hyve .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /hyve /usr/local/bin/hyve
USER 65532:65532
ENTRYPOINT ["hyve"]
CMD ["--help"]
