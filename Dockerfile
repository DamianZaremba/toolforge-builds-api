FROM docker-registry.wikimedia.org/golang1.19:latest as builder

WORKDIR /src

COPY go.mod .
COPY go.sum .

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -a -installsuffix cgo -ldflags="-w -s" -o /tmp/toolforge-builds-api ./cmd/builds-api

# Runtime image
FROM scratch AS base

# TODO: what are the ca certs needed for?
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /tmp/toolforge-builds-api /bin/toolforge-builds-api
ENTRYPOINT ["/bin/toolforge-builds-api"]
