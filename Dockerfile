FROM golang:1.22 as builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY scheduler ./scheduler
COPY config ./config

# Собираем kube-scheduler с нашим плагином (scheduler/main.go).
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/drf-scheduler ./scheduler

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/drf-scheduler /drf-scheduler

ENTRYPOINT ["/drf-scheduler"]
