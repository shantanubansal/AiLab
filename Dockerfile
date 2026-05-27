# Multi-service Dockerfile. The SERVICE build-arg picks which cmd/ to
# compile and run; one image per service is produced by passing a
# different SERVICE value (api, controller, builder, gateway, triggers,
# agentctl). The release workflow drives this in a matrix.

# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

# Cache modules separately from sources.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .
ARG SERVICE
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/app ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
