# We can really use the root CA as the server cert, but should we do that? This is more educational :-)
FROM alpine:3.15 as cert generation

# install openssl
RUN apk update && \
    apk add --no-cache openssl

COPY openssl-config/ /

## Create a certificate authority ##
# Generate a private key for the root CA
# this is the key used to sign the certificate requests
RUN openssl genrsa -out ca.key 4096

# Create a certificate signing request (CSR)
# This is where we specify the signing details for the root CA we want to generate
RUN openssl req -new -nodes -key ca.key -config csrconfig_ca.txt -out ca.csr

# Create the root CA using the CSR and key created above - valid for 1 year
RUN openssl req -x509 -nodes -in ca.csr -days 365 -key ca.key -config certconfig_ca.txt -extensions req_ext -out ca.crt

## Create a certificate for the server from our root CA created above ##
# Generate a private key for the server cert
# this is the key used to sign the certificate requests
RUN openssl genrsa -out server.key 4096

# Create a certificate signing request (CSR)
# This is where we specify the signing details for the server cert we want to generate
RUN openssl req -new -nodes -key server.key -config csrconfig_server.txt -out server.csr

# Create the root CA using the CSR and key created above - valid for 1 year
RUN openssl x509 -req -in server.csr -days 365 -CA ca.crt -CAkey ca.key \
    -extfile certconfig_server.txt -extensions req_ext -CAcreateserial -out server.crt

