ARG PROTOCOL=xmpp

# -----------------------------------------------------------------
# Stage 1: Certificate Generation (from openssl-config)
# -----------------------------------------------------------------
FROM alpine:3.19 AS cert-generation
RUN apk update && apk add --no-cache openssl
COPY openssl-config/ /

RUN openssl genrsa -out ca.key 4096 && \
    openssl req -new -nodes -key ca.key -config csrconfig_ca.txt -out ca.csr && \
    openssl req -x509 -nodes -in ca.csr -days 365 -key ca.key -config certconfig_ca.txt -extensions req_ext -out ca.crt && \
    openssl genrsa -out server.key 4096 && \
    openssl req -new -nodes -key server.key -config csrconfig_server.txt -out server.csr && \
    openssl x509 -req -in server.csr -days 365 -CA ca.crt -CAkey ca.key \
    -extfile certconfig_server.txt -extensions req_ext -CAcreateserial -out server.crt

# -----------------------------------------------------------------
# Stage 2: Go Builder (Single unified binary)
# -----------------------------------------------------------------
FROM golang:1.22-alpine AS go-builder
WORKDIR /build

COPY go.mod go.sum* ./
RUN go mod download

COPY pkg/ ./pkg/
COPY api_client/ ./api_client/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/ecovacs-controller ./api_client

# -----------------------------------------------------------------
# Target: xmpp (Lightweight OZMO 930 Privacy Server - NO Mosquitto)
# -----------------------------------------------------------------
FROM alpine:3.19 AS xmpp
WORKDIR /app

# Provision TLS certificates generated from openssl-config
COPY --from=cert-generation /ca.crt /certs/ca.crt
COPY --from=cert-generation /server.crt /certs/server.crt
COPY --from=cert-generation /server.key /certs/server.key
RUN chmod 644 /certs/*

# Application binary
COPY api_client/ecovacs-controller-linux-amd64 /app/ecovacs-controller

ENV PROTOCOL=xmpp

# 8007: Lookup discovery, 5223: TLS XMPP, 8000: REST API & Map
EXPOSE 8007 5223 8000

ENTRYPOINT ["/app/ecovacs-controller"]
CMD ["-protocol=xmpp"]

# -----------------------------------------------------------------
# Target: mqtt (Deebot 900/901 Server - includes Mosquitto Broker)
# -----------------------------------------------------------------
FROM alpine:3.19 AS mqtt
RUN apk add --no-cache mosquitto
WORKDIR /app

# Provision TLS certificates generated from openssl-config
COPY --from=cert-generation /ca.crt /certs/ca.crt
COPY --from=cert-generation /server.crt /certs/server.crt
COPY --from=cert-generation /server.key /certs/server.key
RUN chmod 644 /certs/*

# Setup Mosquitto config
RUN mkdir -p /mosquitto/config
COPY --from=cert-generation /ca.crt /mosquitto/config/ca.crt
COPY --from=cert-generation /server.crt /mosquitto/config/server.crt
COPY --from=cert-generation /server.key /mosquitto/config/server.key
COPY mosquitto.conf /mosquitto/config/mosquitto.conf
RUN chown -R mosquitto:mosquitto /mosquitto/config && chmod 644 /mosquitto/config/*

# Application binary & startup script
COPY api_client/ecovacs-controller-linux-amd64 /app/ecovacs-controller
RUN printf '#!/bin/sh\nmosquitto -c /mosquitto/config/mosquitto.conf &\nexec /app/ecovacs-controller -protocol=mqtt "$@"\n' > /app/entrypoint.sh && \
    chmod +x /app/entrypoint.sh

ENV PROTOCOL=mqtt

# 8883: TLS MQTT Broker, 8000: REST API & Map
EXPOSE 8883 8000

ENTRYPOINT ["/app/entrypoint.sh"]

# -----------------------------------------------------------------
# Final image selected via ARG PROTOCOL (xmpp or mqtt)
# -----------------------------------------------------------------
FROM ${PROTOCOL} AS final
