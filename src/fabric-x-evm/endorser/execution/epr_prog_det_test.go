package execution

import (
	"bytes"
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

// TestEPRProgramDeterministicEncoding: endorsers of different organizations must sign identical bytes.
// Trace the same router swap from the same state many times (Go randomizes map iteration on every
// range) and require byte-identical encodings.
func TestEPRProgramDeterministicEncoding(t *testing.T) {
	path := os.Getenv("D16_ROUTER_JSON")
	if path == "" {
		t.Skip("D16_ROUTER_JSON not set")
	}
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
	rABI, _ := abi.JSON(strings.NewReader(string(art.ABI)))
	e := newEPRUniEnv(t)
	_, router, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []common.Address{e.t0, e.t1} {
		if err := e.call(e.owner, tok, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000)); err != nil {
			t.Fatal(err)
		}
		if err := e.call(e.user, tok, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
			t.Fatal(err)
		}
	}
	for _, tokenIn := range []common.Address{e.t0, e.t1} {
		var first []byte
		for i := 0; i < 50; i++ {
			snap := e.sdb.Snapshot()
			tr := NewEPRProgTracer()
			if err := e.call(e.user, router, tr.Hooks(), rABI, "swapExactIn", e.pair, tokenIn, big.NewInt(1_000_000), big.NewInt(1)); err != nil {
				t.Fatalf("router swap: %v", err)
			}
			writes := map[string]bool{}
			for s, w := range tr.written {
				if w {
					writes[s] = true
				}
			}
			p := tr.Program(writes)
			e.sdb.RevertToSnapshot(snap)
			if p == nil {
				t.Fatalf("no program (lostAt=%q)", tr.lostAt)
			}
			enc := p.Encode()
			if i == 0 {
				first = enc
			} else if !bytes.Equal(first, enc) {
				t.Fatalf("tokenIn=%s run %d: encoding differs (%d vs %d bytes)", tokenIn.Hex(), i, len(first), len(enc))
			}
		}
		t.Logf("tokenIn=%s: 50 traces, identical %d-byte encodings", tokenIn.Hex(), len(first))
	}
}
