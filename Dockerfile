FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/pyrosync ./cmd/pyrosync

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/pyrosync /pyrosync
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/pyrosync"]
CMD ["serve"]
