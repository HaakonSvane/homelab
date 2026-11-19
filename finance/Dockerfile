# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/finance ./cmd/finance

# Runs as uid 65532 ("nonroot"), so ./data on the host must be owned by it (see README).
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/finance /finance
EXPOSE 8080
ENTRYPOINT ["/finance"]
CMD ["serve"]
