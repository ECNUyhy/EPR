package main

import (
	"os"
	"testing"

	fc "github.com/hyperledger/fabric-x-evm/common"
	"github.com/hyperledger/fabric-x-evm/endorser/execution"
	"github.com/hyperledger/fabric-x-evm/endorser/testimpl"
	gwconfig "github.com/hyperledger/fabric-x-evm/gateway/config"
)

// TestD16TwoOrgEndorserBuild (D16, two-organization runs) checks without a network that the org2 endorser of
// fabx-2of2-org2.yaml builds through the perf harness's endorser factory (Org2MSP identity, one-block KVS
// history), exactly as -extra-endorsers builds it.
func TestD16TwoOrgEndorserBuild(t *testing.T) {
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	_ = os.Chdir("../")
	cfg, err := gwconfig.Load("fabx-2of2-org2.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endorser == nil {
		t.Fatal("no endorser section")
	}
	ecfg := *cfg.Endorser
	ecfg.Database.HistorySize = 1
	evmConfig := execution.EVMConfig{ChainConfig: fc.BuildChainConfig(cfg.Network.ChainID)}
	factory := balancePrimingEndorserFactory(&testimpl.BalancePrimingConfig{Enabled: false})
	comp := factory(t, ecfg, cfg.Network.Channel, "basic2of2", evmConfig, cfg.Network.Protocol)
	if comp.KVS == nil || comp.Builder == nil || comp.Service == nil {
		t.Fatal("incomplete endorser components")
	}
	t.Logf("D16 TWOORG CHECK ok: endorser %s", ecfg.Name)
}
