FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY . .
RUN go build -o /arex ./cmd/arex

FROM alpine:3.23
RUN apk --no-cache add ca-certificates
COPY --from=builder /arex /usr/local/bin/arex
EXPOSE 9100
ENTRYPOINT ["arex"]
CMD ["-config", "/etc/arex/config.json"]
