# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/obs .

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /out/obs /obs
EXPOSE 2112
USER nonroot:nonroot
ENTRYPOINT ["/obs"]
