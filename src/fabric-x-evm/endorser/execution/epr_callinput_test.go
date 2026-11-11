package execution

// A call input that ends inside a tainted word. The caller stores x = SLOAD(0) at memory 0x40 and calls a callee
// with the input [0x20, 0x50), which holds only the 16 leading bytes of x; the callee stores CALLDATALOAD(0x20),
// that is, those bytes followed by the zeros the EVM pads call data with. The program tracer must extract a
// program whose outputs equal the EVM's on other bases. Control: an input [0x50, 0x80), which starts inside the
// tainted word, still ends extraction (site 15).

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRCallInputStraddle(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	callee := eprGasDeploy(t, e, owner, []byte{0x60, 0x20, 0x35, 0x60, 0x01, 0x55, 0x00}) // SSTORE(1, CALLDATALOAD(0x20))
	caller := func(argsOff byte) common.Address {
		code := []byte{0x60, 0x00, 0x54, 0x60, 0x40, 0x52, // MSTORE(0x40, SLOAD(0))
			0x60, 0x00, 0x60, 0x00, 0x60, 0x30, 0x60, argsOff, 0x60, 0x00, 0x73} // retLen, retOff, argsLen, argsOff, value
		code = append(code, callee.Bytes()...)
		code = append(code, 0x62, 0x03, 0x0d, 0x40, 0xf1, 0x50, // CALL(200000, callee, ...); POP
			0x60, 0x00, 0x54, 0x60, 0x01, 0x01, 0x60, 0x00, 0x55, 0x00) // SSTORE(0, SLOAD(0) + 1)
		return eprGasDeploy(t, e, owner, code)
	}
	calleeSlot1 := storeKey(callee, common.BigToHash(big.NewInt(1)))
	run := func(c common.Address, x *big.Int, tr *EPRProgTracer) (*big.Int, *big.Int) {
		snap := e.sdb.Snapshot()
		defer e.sdb.RevertToSnapshot(snap)
		e.setKey(storeKey(c, common.Hash{}), x)
		evm := e.evm(user, nil)
		if tr != nil {
			evm = e.evm(user, tr.Hooks())
		}
		if _, _, err := evm.Call(user, c, nil, vm.NewGasBudget(1_000_000), new(uint256.Int)); err != nil {
			t.Fatalf("x = %v: %v", x, err)
		}
		return e.getKey(storeKey(c, common.Hash{})), e.getKey(calleeSlot1)
	}
	pow := func(n uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), n) }
	endorsed := new(big.Int).Add(pow(200), new(big.Int).Add(new(big.Int).Mul(big.NewInt(5), pow(130)), big.NewInt(77)))

	t.Run("ends-inside-word", func(t *testing.T) {
		c := caller(0x20)
		slot0 := storeKey(c, common.Hash{})
		tr := NewEPRProgTracer()
		run(c, endorsed, tr)
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		p := tr.Program(writes)
		if p == nil {
			t.Fatalf("no program (lostAt=%q)", tr.lostAt)
		}
		q, ok := DecodeEPRProgram(p.Encode())
		if !ok {
			t.Fatal("program does not round-trip")
		}
		bases := []*big.Int{endorsed, new(big.Int).Add(pow(220), big.NewInt(1)), big.NewInt(1), pow(129), big.NewInt(0),
			new(big.Int).Add(new(big.Int).Mul(big.NewInt(3), pow(150)), new(big.Int).Add(pow(127), big.NewInt(9)))}
		for _, x := range bases {
			want0, want1 := run(c, x, nil)
			out, ok := q.Eval(func(k string) []byte {
				if k == slot0 {
					return x.Bytes()
				}
				return nil
			})
			if !ok {
				t.Fatalf("x = %v: guards fail", x)
			}
			got0, got1 := new(big.Int).SetBytes(out[slot0]), new(big.Int).SetBytes(out[calleeSlot1])
			if got0.Cmp(want0) != 0 || got1.Cmp(want1) != 0 {
				t.Fatalf("x = %v: program writes %v, %v; the EVM writes %v, %v", x, got0, got1, want0, want1)
			}
		}
		t.Logf("program: nodes=%d guards=%d, outputs equal the EVM's on %d bases", len(p.Nodes), len(p.Guards), len(bases))
	})

	t.Run("starts-inside-word", func(t *testing.T) {
		c := caller(0x50)
		tr := NewEPRProgTracer()
		run(c, endorsed, tr)
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		if p := tr.Program(writes); p != nil || !strings.Contains(tr.lostAt, "site=15") {
			t.Fatalf("an input starting inside a tainted word was accepted (program %v, lostAt=%q)", p != nil, tr.lostAt)
		}
	})
}
