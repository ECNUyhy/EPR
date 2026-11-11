package execution

// Committer-side cost of a guarded program: decoding the signed program of a swap through the DEX router
// (contracts/D16Router.sol) on a Uniswap-V2-style pair and evaluating it on the latest values, measured in
// memory (no Fabric-X, no I/O). The program is extracted once, before the timed loop; the benchmark also
// reports the number of nodes and the encoded size of the program.
//
// D16_ROUTER_JSON=/path/to/contracts/D16Router.json \
//   go test ./endorser/execution -run '^$' -bench BenchmarkEPRProgEval -benchtime 3000x

import (
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

// eprRouterBenchEnv deploys the router next to the pair of newEPRUniEnv, funds the user and returns the
// calldata of one swap.
func eprRouterBenchEnv(b *testing.B) (*eprUniEnv, common.Address, []byte) {
	path := os.Getenv("D16_ROUTER_JSON")
	if path == "" {
		b.Skip("D16_ROUTER_JSON not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	var art struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		b.Fatal(err)
	}
	rABI, _ := abi.JSON(strings.NewReader(string(art.ABI)))
	e := newEPRUniEnv(&testing.T{})
	_, router, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		b.Fatal(err)
	}
	if err := e.call(e.owner, e.t0, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000_000)); err != nil {
		b.Fatal(err)
	}
	if err := e.call(e.user, e.t0, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
		b.Fatal(err)
	}
	in, _ := rABI.Pack("swapExactIn", e.pair, e.t0, big.NewInt(1_000_000), big.NewInt(1))
	return e, router, in
}

// BenchmarkEPRProgEval decodes and evaluates the signed program of a router swap on the latest values.
func BenchmarkEPRProgEval(b *testing.B) {
	e, r, in := eprRouterBenchEnv(b)
	snap := e.sdb.Snapshot()
	p := NewEPRProgTracer()
	if _, _, err := e.evm(e.user, p.Hooks()).Call(e.user, r, in, vm.NewGasBudget(2_000_000), new(uint256.Int)); err != nil {
		b.Fatal(err)
	}
	writes := map[string]bool{}
	for s, w := range p.written {
		if w {
			writes[s] = true
		}
	}
	enc := p.Program(writes).Encode()
	e.sdb.RevertToSnapshot(snap)
	q, _ := DecodeEPRProgram(enc)
	vals := map[string][]byte{}
	for _, k := range q.Keys {
		vals[k] = e.getKey(k).Bytes()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pp, ok := DecodeEPRProgram(enc)
		if !ok {
			b.Fatal("decode")
		}
		if _, ok := pp.Eval(func(k string) []byte { return vals[k] }); !ok {
			b.Fatal("guards")
		}
	}
	b.ReportMetric(float64(len(q.Nodes)), "nodes")
	b.ReportMetric(float64(len(enc)), "prog_bytes")
}
