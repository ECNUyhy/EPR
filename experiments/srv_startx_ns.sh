#!/usr/bin/env bash
# start the compose network from compose.d16.yml (already written) and create namespaces
. /home/yhy/d16/env.sh
D=/home/yhy/d16; cd $D/src/fabric-x-evm
docker compose -f compose.d16.yml up -d
for i in $(seq 1 90); do nc -z localhost 7001 && break; sleep 2; done
mkns(){ ns=$1; pol=$2
  for a in 1 2 3 4 5 6; do
    if docker run --rm --network fabric-x --user "$(id -u):$(id -g)" --entrypoint sh \
      -e FX_NS="$ns" -e FX_POLICY="$pol" \
      -v $D/data/bin/fxconfig:/usr/local/bin/fxconfig:ro \
      -v $PWD/testdata/fxconfig.yaml:/config/fxconfig.yaml:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/peers/fxconfig.org1.example.com/tls:/tls:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/users/channel_admin@org1.example.com/msp:/msp:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/msp/tlscacerts/tlsca.org1.example.com-cert.pem:/org-tls-ca.pem:ro \
      -v $PWD/testdata/crypto/ordererOrganizations/orderer-org-1/msp/tlscacerts/tlsca.orderer-org-1-cert.pem:/orderer-tls-ca.pem:ro \
      postgres:18.3-alpine3.23 -c 'fxconfig namespace list --config=/config/fxconfig.yaml 2>/dev/null | grep -q ") $FX_NS:" || fxconfig namespace create "$FX_NS" --policy="$FX_POLICY" --endorse --submit --wait --config=/config/fxconfig.yaml' >/tmp/ns.log 2>&1; then
      return 0; fi
    sleep $((a*2))
  done; return 1; }
mkns basic "AND('Org1MSP.member')" && mkns basic2of2 "AND('Org1MSP.member','Org2MSP.member')"
