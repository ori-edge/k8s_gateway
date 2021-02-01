FROM golang:1.14.4 as builder

ARG LDFLAGS

WORKDIR /build

# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

COPY cmd/ cmd/
COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build -ldflags "${LDFLAGS}" -o coredns cmd/coredns.go

FROM debian:stable-slim

RUN apt-get update && apt-get -uy upgrade
RUN apt-get -y install ca-certificates && update-ca-certificates

FROM scratch

COPY --from=0 /etc/ssl/certs /etc/ssl/certs
COPY --from=builder /build/coredns .

EXPOSE 53 53/udp
ENTRYPOINT ["/coredns"]
