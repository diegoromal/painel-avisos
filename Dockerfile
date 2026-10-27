# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/painel-avisos . \
 && mkdir -p /out/data

# ---- runtime ----
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 10001 painel
ENV TZ=America/Sao_Paulo \
    PAINEL_PORT=8080 \
    PAINEL_DATA=/data
COPY --from=build /out/painel-avisos /usr/local/bin/painel-avisos
COPY --from=build --chown=10001:10001 /out/data /data
USER painel
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/painel-avisos"]
