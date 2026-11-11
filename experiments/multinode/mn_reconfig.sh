#!/usr/bin/env bash
# Change Arma batching parameters of the multi-node network and redistribute the shared config and genesis
# block (crypto material unchanged). Run on 116.
#   mn_reconfig.sh <BatchCreationTimeout, e.g. 10ms> [requestbatchmaxinterval, e.g. 20ms]
set -eu
BCT=$1; RBI=${2:-}
M=/home/yhy/d16/multinode/cfg; T=$M/testdata; C="ssh -F $HOME/.ssh/config_d16mn -o BatchMode=yes"
sed -i -E "s/^(    BatchCreationTimeout: ).*/\1$BCT/" $T/shared_config.yaml
[ -n "$RBI" ] && sed -i -E "s/^(    requestbatchmaxinterval: ).*/\1$RBI/" $T/shared_config.yaml
grep -nE "BatchCreationTimeout|requestbatchmaxinterval" $T/shared_config.yaml
U=$(id -u):$(id -g)
docker run --rm --user $U -v $T:/config -v $T/crypto:/crypto --entrypoint /usr/local/bin/armageddon \
  ghcr.io/hyperledger/fabric-x-orderer:1.0.0 createSharedConfigProto --sharedConfigYaml=/config/shared_config.yaml --output=/config/crypto/ >/dev/null 2>&1
docker run --rm --user $U -v $T:/config ghcr.io/hyperledger/fabric-x-tools:1.0.0 configtxgen --channelID mychannel \
  --profile OrgsChannel --outputBlock /config/crypto/config-block.pb.bin --configPath /config >/dev/null 2>&1
chmod a+r $T/crypto/shared_config.binpb $T/crypto/config-block.pb.bin
for h in n112:/data/d2025yhy/mn n115:mn n118:mn; do
  scp -F $HOME/.ssh/config_d16mn -q $T/shared_config.yaml ${h%%:*}:${h#*:}/testdata/shared_config.yaml
  scp -F $HOME/.ssh/config_d16mn -q $T/crypto/shared_config.binpb $T/crypto/config-block.pb.bin ${h%%:*}:${h#*:}/testdata/crypto/
done
echo "RECONFIG BatchCreationTimeout=$BCT ${RBI:+requestbatchmaxinterval=$RBI} genesis=$(md5sum $T/crypto/config-block.pb.bin | cut -c1-8)"
