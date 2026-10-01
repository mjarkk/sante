# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
# The SQLite driver is pure Go, so the binary is static and cross-compiles without cgo.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/sante . \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sante /usr/local/bin/sante
COPY --from=build --chown=65532:65532 /out/data /data
COPY config.example.yaml /etc/sante/config.yaml
ENV SANTE_CONFIG=/etc/sante/config.yaml \
    SANTE_DB=/data/sante.db
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/usr/local/bin/sante", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/sante"]
CMD ["serve"]
