# Local builds: compiles from source in this tree.
# Released images are built by GoReleaser from Dockerfile.goreleaser, which
# copies an already-built binary.
FROM golang:1.27-alpine AS builder

WORKDIR /src
COPY . .
# Static, so the binary runs on the distroless base below, which has no libc.
RUN CGO_ENABLED=0 go build -trimpath -o /arex .

# Pinned by digest, and deliberately the same digest as Dockerfile.goreleaser: a
# local build and a released image should not differ in what they are built on.
# The nonroot variant runs as UID 65532 and carries ca-certificates, which is
# all arex needs -- no shell, no package manager, nothing writable.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=builder /arex /usr/local/bin/arex
EXPOSE 9100
ENTRYPOINT ["/usr/local/bin/arex"]
CMD ["-config", "/etc/arex/config.json"]
