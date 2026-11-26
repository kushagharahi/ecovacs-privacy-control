# Legacy Deebot 900 / 901 Series — MQTTS MITM Reference

> [!NOTE]
> This repository now targets **on-robot execution for the OZMO 930 (DR930 / XMPP)** via `cmd/ecovacs-bot`, which requires no DNS hijacking, external Mosquitto broker, or custom CA. This document preserves the historical MITM setup notes and OpenSSL/Mosquitto configs for older **Deebot 900 / 901 (RV1107)** models.

---

## How It Works

Older Deebot 900 / 901 robots connect to `mq-ww.ecouser.net:8883` over MQTTS (MQTT over TLS) and do not enforce certificate pinning. Intercepting control requires:

1. **DNS Override**: Resolve `*-ww.ecouser.net` (specifically `mq-ww.ecouser.net`) to your local Mosquitto host IP.
2. **Self-Signed CA & Wildcard Server Certificate**: Generate a CA and server certificate matching `DNS:*-ww.ecouser.net`.
3. **Local MQTTS Broker**: Run Mosquitto on port `8883` using the generated CA, server certificate, and private key.
4. **MQTT Topic Layout**:
   - Commands published to robot: `iot/p2p/<cmdName>/x/x/x/<bot_serial>/<device_type>/<resource>/q/x/x` (XML `<ctl .../>` payload)
   - Robot telemetry/responses subscribed on: `iot/atr/#` and `iot/p2p/+/<bot_serial>/<device_type>/<resource>/#`

---

## OpenSSL Configuration Templates

### CA Certificate Config (`certconfig_ca.txt`)
```ini
[ req ]
default_md = sha256
prompt = no
req_extensions = req_ext
distinguished_name = req_distinguished_name
[ req_distinguished_name ]
commonName = ecovacs-privacy-control CA
organizationName = ecovacs-privacy-control
[ req_ext ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
keyUsage=critical,keyCertSign,cRLSign
basicConstraints=critical,CA:true,pathlen:1
```

### CA CSR Config (`csrconfig_ca.txt`)
```ini
[ req ]
default_md = sha256
prompt = no
req_extensions = req_ext
distinguished_name = req_distinguished_name
[ req_distinguished_name ]
commonName = ecovacs-privacy-control CA
organizationName = ecovacs-privacy-control
[ req_ext ]
keyUsage=critical,keyCertSign,cRLSign
basicConstraints=critical,CA:true,pathlen:1
```

### Server Certificate Config (`certconfig_server.txt`)
```ini
[ req ]
default_md = sha256
prompt = no
req_extensions = req_ext
distinguished_name = req_distinguished_name
[ req_distinguished_name ]
commonName = ecovacs-privacy-control
organizationName = ecovacs-privacy-control
[ req_ext ]
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always,issuer
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth,clientAuth
basicConstraints=critical,CA:false
subjectAltName = @alt_names
[ alt_names ]
DNS.0 = *-ww.ecouser.net
```

### Server CSR Config (`csrconfig_server.txt`)
```ini
[ req ]
default_md = sha256
prompt = no
req_extensions = req_ext
distinguished_name = req_distinguished_name
[ req_distinguished_name ]
commonName = ecovacs-privacy-control
organizationName = ecovacs-privacy-control
[ req_ext ]
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth,clientAuth
basicConstraints=critical,CA:false
subjectAltName = @alt_names
[ alt_names ]
DNS.0 = *-ww.ecouser.net
```

### Generating the Certificates
```bash
# 1. Generate CA key, CSR, and self-signed CA cert
openssl genrsa -out ca.key 2048
openssl req -new -key ca.key -out ca.csr -config csrconfig_ca.txt
openssl x509 -req -in ca.csr -signkey ca.key -out ca.crt -days 3650 -extfile certconfig_ca.txt -extensions req_ext

# 2. Generate server key, CSR, and CA-signed wildcard cert for *-ww.ecouser.net
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr -config csrconfig_server.txt
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 3650 -extfile certconfig_server.txt -extensions req_ext
```

---

## Mosquitto MQTTS Broker Configuration (`mosquitto.conf`)

```conf
# Port to use for the default listener.
port 8883

cafile /mosquitto/config/ca.crt

# Path to the PEM encoded server certificate.
certfile /mosquitto/config/server.crt

# Path to the PEM encoded keyfile.
keyfile /mosquitto/config/server.key

# Allow anonymous users to connect?
allow_anonymous true
```

---

## Historical Firmware Images

Historical RV1107 firmware images for Deebot 900/901 series robots remain archived in `900_binaries/`.
