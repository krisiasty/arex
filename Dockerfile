FROM golang:1.27-alpine AS builder

WORKDIR /src
COPY . .
RUN go build -o /arex .

FROM alpine:3.23
RUN apk --no-cache add ca-certificates

# arex writes nothing and needs no privileges, so it runs as a fixed non-root
# UID. 65532 is the conventional "nonroot" id, and matching it here lets a
# Kubernetes securityContext set runAsNonRoot with readOnlyRootFilesystem.
RUN adduser -u 65532 -D -H -s /sbin/nologin nonroot
USER 65532:65532

COPY --from=builder /arex /usr/local/bin/arex
EXPOSE 9100
ENTRYPOINT ["arex"]
CMD ["-config", "/etc/arex/config.json"]
