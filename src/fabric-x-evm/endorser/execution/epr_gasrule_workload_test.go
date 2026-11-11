package execution

// The per-opcode gas rule must accept every transaction of the evaluated
// workloads at the gas limits the experiments use, so that the accepted set, and hence every throughput
// result, is unchanged. Transfers (USDC, USDT and Zipf all run on the USDC contract) are checked at a 300k
// limit, stricter than the datasets' 5M; SmallBank at its 300k; DEX router swaps at their 800k. Each line
// GASRULE ... is parsed by the server queue.

import (
	"encoding/json"
	"math/big"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

// Also counts programs lost to the GAS marker rule (sites 93, 94 of epr_prog_tracer.go); the
// workloads' extraction, and hence every throughput result, must not change.
type eprGasTally struct {
	name             string
	n, short, failed int
	marker           int
	min              int64
	seen             bool
}

func (g *eprGasTally) prog(p *EPRProgTracer) {
	if strings.Contains(p.lostAt, "site=93") || strings.Contains(p.lostAt, "site=94") {
		g.marker++
	}
}

func (g *eprGasTally) add(rules ...*eprGasRule) {
	g.n++
	for _, r := range rules {
		if r.short {
			g.short++
			break
		}
	}
	for _, r := range rules {
		if r.seen && (!g.seen || r.minMargin < g.min) {
			g.min, g.seen = r.minMargin, true
		}
	}
}

func (g *eprGasTally) report(t *testing.T) {
	t.Logf("GASRULE workload=%s txs=%d rejected=%d reverted=%d min_margin=%d marker_lost=%d", g.name, g.n, g.short,
		g.failed, g.min, g.marker)
	if g.short > 0 {
		t.Errorf("%s: the gas rule rejects %d of %d transactions", g.name, g.short, g.n)
	}
	if g.marker > 0 {
		t.Errorf("%s: the GAS marker rule drops the program of %d of %d transactions", g.name, g.marker, g.n)
	}
}

func TestEPRGasRuleWorkloads(t *testing.T) {
	// transfers on the USDC contract
	b := &testing.B{}
	sdb, proxy, _ := eprBenchState(b, 0)
	env := &eprDiffEnv{sdb: sdb, proxy: proxy, ctx: vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}}
	rng := rand.New(rand.NewSource(1107))
	tr := &eprGasTally{name: "transfer"}
	for c := 0; c < 300; c++ {
		from := common.BigToAddress(big.NewInt(0x10000 + rng.Int63n(1000)))
		to := common.BigToAddress(big.NewInt(0x20000 + rng.Int63n(1000)))
		value := 1 + rng.Int63n(1_000_000_000)
		bTo := rng.Int63n(1_000_000_000_000)
		if c%3 == 0 {
			bTo = 0 // a fresh recipient: zero-to-nonzero write
		}
		d := NewEPRTracer()
		if ok, _, _ := env.run(from, to, value, value+rng.Int63n(1_000_000_000_000), bTo, d); !ok {
			tr.failed++
			continue
		}
		tr.add(&d.gas)
	}
	tr.report(t)

	// SmallBank at its dataset gas limit
	if path := os.Getenv("D16_SMALLBANK_JSON"); path == "" {
		t.Log("D16_SMALLBANK_JSON not set: SmallBank skipped")
	} else {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var art struct {
			ABI json.RawMessage `json:"abi"`
			Bin string          `json:"bin"`
		}
		if err := json.Unmarshal(raw, &art); err != nil {
			t.Fatal(err)
		}
		sb, _ := abi.JSON(strings.NewReader(string(art.ABI)))
		e := newEPRUniEnv(t)
		_, bank, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
		if err != nil {
			t.Fatal(err)
		}
		initBal := new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil)
		if err := e.call(e.owner, bank, nil, sb, "init", big.NewInt(0), big.NewInt(16), initBal, initBal); err != nil {
			t.Fatal(err)
		}
		acct := func() *big.Int { return big.NewInt(rng.Int63n(16)) }
		amt := func() *big.Int { return big.NewInt(1 + rng.Int63n(1000)) }
		ops := map[string]func() []interface{}{
			"depositChecking": func() []interface{} { return []interface{}{acct(), amt()} },
			"transactSavings": func() []interface{} { return []interface{}{acct(), amt()} },
			"sendPayment":     func() []interface{} { a := rng.Int63n(15); return []interface{}{big.NewInt(a), big.NewInt(a + 1), amt()} },
			"writeCheck":      func() []interface{} { return []interface{}{acct(), amt()} },
			"amalgamate":      func() []interface{} { a := rng.Int63n(15); return []interface{}{big.NewInt(a), big.NewInt(a + 1)} },
			"balance":         func() []interface{} { return []interface{}{acct()} },
		}
		g := &eprGasTally{name: "smallbank"}
		for name, args := range ops {
			for c := 0; c < 50; c++ {
				in, err := sb.Pack(name, args()...)
				if err != nil {
					t.Fatal(err)
				}
				snap := e.sdb.Snapshot()
				d, p := NewEPRTracer(), NewEPRProgTracer()
				_, _, err = e.evm(e.user, EPRCombineHooks(d, p)).Call(e.user, bank, in, vm.NewGasBudget(300_000), new(uint256.Int))
				e.sdb.RevertToSnapshot(snap)
				if err != nil {
					g.failed++
					continue
				}
				g.add(&d.gas, &p.gasRule)
				g.prog(p)
			}
		}
		g.report(t)
	}

	// DEX router swaps at their dataset gas limit
	if path := os.Getenv("D16_ROUTER_JSON"); path == "" {
		t.Log("D16_ROUTER_JSON not set: DEX skipped")
	} else {
		raw, _ := os.ReadFile(path)
		var art struct {
			ABI json.RawMessage `json:"abi"`
			Bin string          `json:"bin"`
		}
		if err := json.Unmarshal(raw, &art); err != nil {
			t.Fatal(err)
		}
		rABI, _ := abi.JSON(strings.NewReader(string(art.ABI)))
		e := newEPRUniEnv(t)
		_, router, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
		if err != nil {
			t.Fatal(err)
		}
		for _, tok := range []common.Address{e.t0, e.t1} {
			if err := e.call(e.owner, tok, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000_000)); err != nil {
				t.Fatal(err)
			}
			if err := e.call(e.user, tok, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
				t.Fatal(err)
			}
		}
		g := &eprGasTally{name: "dex"}
		for c := 0; c < 200; c++ {
			tokenIn := e.t0
			if c%2 == 1 {
				tokenIn = e.t1
			}
			in, err := rABI.Pack("swapExactIn", e.pair, tokenIn, big.NewInt(1_000_000+rng.Int63n(1_000_000_000)), big.NewInt(1))
			if err != nil {
				t.Fatal(err)
			}
			snap := e.sdb.Snapshot()
			d, p := NewEPRTracer(), NewEPRProgTracer()
			_, _, err = e.evm(e.user, EPRCombineHooks(d, p)).Call(e.user, router, in, vm.NewGasBudget(800_000), new(uint256.Int))
			e.sdb.RevertToSnapshot(snap)
			if err != nil {
				g.failed++
				continue
			}
			g.add(&d.gas, &p.gasRule)
			g.prog(p)
		}
		g.report(t)
	}
}
