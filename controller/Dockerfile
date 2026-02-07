FROM golang:1.24 AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION:-dev} -X main.commit=${COMMIT:-unknown}" \
    -o /controller ./cmd/controller/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /controller /controller
USER nonroot:nonroot
ENTRYPOINT ["/controller"]
