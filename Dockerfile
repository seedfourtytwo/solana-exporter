FROM golang:1.22 as builder

COPY . /opt
WORKDIR /opt

RUN CGO_ENABLED=0 go build -o /opt/bin/app github.com/seedfourtytwo/solana-exporter/cmd/solana-exporter

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /opt/bin/app /

ENTRYPOINT ["/app"]
