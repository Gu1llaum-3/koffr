#!/bin/sh
# Regenerates the throwaway key material the P-004 rig needs.
#
# None of it is committed: private keys do not belong in a repository, and these
# are worthless anyway. Run this before `docker compose --profile p004 up`.
set -e
cd "$(dirname "$0")"

mkdir -p tls ssh

# CA and a server certificate for the hidden database. The SAN is what the
# verify-full test actually checks.
openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
    -keyout tls/ca.key -out tls/ca.crt -subj "/CN=koffr-probe-ca" 2>/dev/null
openssl req -newkey rsa:2048 -nodes -keyout tls/server.key -out tls/server.csr \
    -subj "/CN=pg-hidden" 2>/dev/null
printf 'subjectAltName=DNS:pg-hidden\n' > tls/ext.cnf
openssl x509 -req -in tls/server.csr -CA tls/ca.crt -CAkey tls/ca.key \
    -CAcreateserial -out tls/server.crt -days 30 -extfile tls/ext.cnf 2>/dev/null
chmod 600 tls/server.key

rm -f ssh/probe_key ssh/probe_key.pub
ssh-keygen -t ed25519 -N '' -f ssh/probe_key -C koffr-probe >/dev/null
cp ssh/probe_key.pub bastion/authorized_keys

echo "key material regenerated in spikes/tls and spikes/ssh"
