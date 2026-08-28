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
#
# The Debian generation is named rather than left to the plain "static" tag,
# which today resolves to this same image but will move to debian14 on its own.
# A base image should change when someone decides it does.
FROM gcr.io/distroless/static-debian13:nonroot@sha256:1c2c046bc09ed40fad370b599a0b1ae7987f55b01e247cf27a7c27cd97e5bbc7

COPY --from=builder /arex /usr/local/bin/arex
EXPOSE 9100
ENTRYPOINT ["/usr/local/bin/arex"]
CMD ["-config", "/etc/arex/config.yaml"]
