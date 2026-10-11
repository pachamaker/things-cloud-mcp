# Release image (KI-16, ADR-0002): a static binary on distroless, run as the
# nonroot user. Base images are pinned by digest; bump the golang tag and
# digest together with the toolchain line in go.mod (KI-32).
#
# The builder runs on the build host and cross-compiles, so multi-arch builds
# need no emulation: the runtime stage only copies files.
FROM --platform=$BUILDPLATFORM golang:1.26.9-alpine@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# modernc.org/sqlite is pure Go, so CGO is off and the binary needs no libc;
# domain/civil embeds the zone database (time/tzdata).
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/things-server ./server \
 && mkdir -p /out/root && mkdir -m 0700 /out/root/data

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

ARG VERSION=dev
LABEL org.opencontainers.image.title="things-cloud-mcp" \
      org.opencontainers.image.description="Unofficial single-user MCP server for Things 3 over Things Cloud" \
      org.opencontainers.image.source="https://github.com/pachamaker/things-cloud-mcp" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY --from=builder /out/things-server /usr/local/bin/things-server
# /data as an entry of the copied tree keeps its 0700 mode (a copied
# directory's own mode is not kept). Docker seeds a new named volume from the
# image, so a volume mounted here starts out owned by the nonroot user.
COPY --from=builder --chown=65532:65532 /out/root/ /

USER 65532:65532
EXPOSE 8080

# The first full sync runs before the server listens; on a large account or
# after a 429 it can take minutes.
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --retries=3 \
  CMD ["/usr/local/bin/things-server", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/things-server"]
