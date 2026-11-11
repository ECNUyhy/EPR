package execution

// Endorsement-side overhead of the EPR tracer on real USDC (FiatTokenV2_2 behind its proxy)
// transfers, measured on go-ethereum's in-memory state (no Fabric-X, no I/O): EVM execution alone vs EVM
// execution with the taint tracer attached.
//
// go test ./endorser/execution -run XXX -bench BenchmarkEPRTracer -benchtime 20000x

import (
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func eprBenchState(b *testing.B, n int) (*state.StateDB, common.Address, []common.Address) {
	raw, err := os.ReadFile("../../integration/perf/testdata/USDC_contract.json")
	if err != nil {
		b.Fatal(err)
	}
	var accts map[string]struct {
		Code    string            `json:"code"`
		Storage map[string]string `json:"storage"`
	}
	if err := json.Unmarshal(raw, &accts); err != nil {
		b.Fatal(err)
	}
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	if err != nil {
		b.Fatal(err)
	}
	_ = rawdb.NewMemoryDatabase
	var proxy common.Address
	for a, acc := range accts {
		addr := common.HexToAddress(a)
		sdb.SetCode(addr, common.FromHex(acc.Code), tracing.CodeChangeUnspecified)
		for k, v := range acc.Storage {
			sdb.SetState(addr, common.HexToHash(k), common.HexToHash(v))
		}
		if strings.EqualFold(a, "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48") {
			proxy = addr
		}
	}
	users := make([]common.Address, n)
	slot := common.BigToHash(big.NewInt(9))
	for i := range users {
		users[i] = common.BigToAddress(big.NewInt(int64(0x100000 + i)))
		k := crypto.Keccak256Hash(common.LeftPadBytes(users[i].Bytes(), 32), slot.Bytes())
		sdb.SetState(proxy, k, common.BigToHash(big.NewInt(1e18)))
	}
	return sdb, proxy, users
}

func eprBenchRun(b *testing.B, withTracer bool) {
	const nUsers = 1000
	sdb, proxy, users := eprBenchState(b, nUsers)
	selector := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	cfg := params.MainnetChainConfig
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}
	var deltas, total int
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		from, to := users[i%nUsers], users[(i*7+1)%nUsers]
		input := append(append(append([]byte{}, selector...), common.LeftPadBytes(to.Bytes(), 32)...),
			common.LeftPadBytes(big.NewInt(int64(1+i%1000)).Bytes(), 32)...)
		vmCfg := vm.Config{}
		var tr *EPRTracer
		if withTracer {
			tr = NewEPRTracer()
			vmCfg.Tracer = tr.Hooks()
		}
		evm := vm.NewEVM(blockCtx, sdb, cfg, vmCfg)
		evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
		snap := sdb.Snapshot()
		ret, _, err := evm.Call(from, proxy, input, vm.NewGasBudget(200_000), new(uint256.Int))
		if err != nil {
			b.Fatalf("transfer %d: %v (%x)", i, err, ret)
		}
		if tr != nil {
			for _, st := range tr.slots {
				if st.written {
					total++
					if _, _, _, why := st.extract(map[string]bool{}, true); why == "" {
						deltas++
					}
				}
			}
		}
		sdb.RevertToSnapshot(snap)
	}
	b.StopTimer()
	if withTracer && total > 0 {
		b.ReportMetric(float64(deltas)/float64(total), "extractable")
	}
}

func BenchmarkEPRTracerOff(b *testing.B) { eprBenchRun(b, false) }
func BenchmarkEPRTracerOn(b *testing.B)  { eprBenchRun(b, true) }
