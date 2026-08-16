# Stage 1: build a static binary with the pinned toolchain and no network.
FROM golang:1.22 AS builder

ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0 \
    GOPROXY=off \
    GOFLAGS=-mod=mod \
    GOOS=linux

WORKDIR /src

# The module has no third-party requirements, so the manifest alone is enough
# to prime the build cache before the sources are copied.
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN go vet ./... \
 && go build -trimpath -ldflags="-s -w" -o /out/humpyard ./cmd/humpyard

# Stage 2: ship only the static binary.
FROM scratch

COPY --from=builder /out/humpyard /humpyard

ENTRYPOINT ["/humpyard"]
