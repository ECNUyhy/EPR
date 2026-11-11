package execution

// A call whose gas operand is computed from a storage read gives the callee a different gas
// allowance on another base, so the callee's GAS output may differ even though no SSTORE or EXP preceded it.
// The program tracer must then treat GAS as a marker (gasDep), as it does after an SSTORE. The contract below
// calls itself with gas = SLOAD(0); the callee either branches on GAS > 50000 (case "gasleft-branch") or
// writes SLOAD(0)+1 without observing gas (case "no-gas-use"), and writes storage slot 1. Before the fix the
// tracer saw an untainted GAS in the callee, so the branch carried no guard and the program wrote the
// endorsed branch's constant on every base (wrong when slot 0 is small enough to flip the branch).
// Extraction must either fall back (no program) or yield a program that matches the EVM on every perturbed
// base on which its guards hold. The perturbed bases leave the callee enough gas, as A7 requires.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRProgramCallGasFromStorage(t *testing.T) {
	head := []byte{
		0x36, 0x60, 0x15, 0x57, // CALLDATASIZE; PUSH1 sub; JUMPI
		0x60, 0x00, 0x60, 0x00, 0x60, 0x01, 0x60, 0x00, 0x60, 0x00, 0x30, // retLen, retOff, argsLen=1, argsOff, value, self
		0x60, 0x00, 0x54, 0xf1, // CALL(gas = SLOAD(0), ...)
		0x50, 0x00, // POP; STOP
		0x5b, // sub: JUMPDEST (0x15)
	}
	cases := []struct {
		name        string
		sub         []byte
		needProgram bool
	}{
		{"gasleft-branch", []byte{
			0x62, 0x00, 0xc3, 0x50, 0x5a, 0x11, 0x60, 0x25, 0x57, // PUSH3 50000; GAS; GT; PUSH1 hi; JUMPI
			0x60, 0x02, 0x60, 0x01, 0x55, 0x00, // SSTORE(1, 2); STOP
			0x5b, 0x60, 0x01, 0x60, 0x01, 0x55, 0x00, // hi (0x25): JUMPDEST; SSTORE(1, 1); STOP
		}, false},
		{"no-gas-use", []byte{
			0x60, 0x01, 0x60, 0x00, 0x54, 0x01, 0x60, 0x01, 0x55, 0x00, // SSTORE(1, SLOAD(0) + 1); STOP
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runtime := append(append([]byte{}, head...), tc.sub...)
			if runtime[0x15] != 0x5b || (tc.name == "gasleft-branch" && runtime[0x25] != 0x5b) {
				t.Fatal("bad jump target")
			}
			initCode := append([]byte{0x60, byte(len(runtime)), 0x80, 0x60, 0x0b, 0x60, 0x00, 0x39, 0x60, 0x00, 0xf3}, runtime...)
			sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
			if err != nil {
				t.Fatal(err)
			}
			e := &eprUniEnv{t: t, sdb: sdb, ctx: vm.BlockContext{
				CanTransfer: core.CanTransfer, Transfer: core.Transfer,
				BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
				Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
			}}
			owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
			_, c, _, err := e.evm(owner, nil).Create(owner, initCode, vm.NewGasBudget(1_000_000), new(uint256.Int))
			if err != nil {
				t.Fatal(err)
			}
			slot0, slot1 := storeKey(c, common.Hash{}), storeKey(c, common.BigToHash(big.NewInt(1)))
			run := func(x int64, tr *EPRProgTracer) *big.Int {
				snap := e.sdb.Snapshot()
				defer e.sdb.RevertToSnapshot(snap)
				e.setKey(slot0, big.NewInt(x))
				evm := e.evm(user, nil)
				if tr != nil {
					evm = e.evm(user, tr.Hooks())
				}
				if _, _, err := evm.Call(user, c, nil, vm.NewGasBudget(2_000_000), new(uint256.Int)); err != nil {
					t.Fatalf("call: %v", err)
				}
				return e.getKey(slot1)
			}
			if tc.name == "gasleft-branch" { // the EVM's write depends on the callee's gas
				if a, b := run(100_000, nil), run(30_000, nil); a.Int64() != 1 || b.Int64() != 2 {
					t.Fatalf("contract does not branch on gas: %v %v", a, b)
				}
			}

			tr := NewEPRProgTracer()
			run(100_000, tr) // endorsed base: slot 0 = 100000
			writes := map[string]bool{}
			for s, w := range tr.written {
				if w {
					writes[s] = true
				}
			}
			p := tr.Program(writes)
			if p == nil {
				if tc.needProgram {
					t.Fatalf("no program although the callee does not observe gas (lostAt=%q)", tr.lostAt)
				}
				t.Logf("no program: falls back to plain writes (lostAt=%q)", tr.lostAt)
				return
			}
			q, ok := DecodeEPRProgram(p.Encode())
			if !ok {
				t.Fatal("program does not round-trip")
			}
			held := 0
			for _, x := range []int64{100_000, 1_000_000, 60_000, 50_100, 49_000, 30_000, 25_000} {
				want := run(x, nil)
				out, ok := q.Eval(func(k string) []byte {
					if k == slot0 {
						return big.NewInt(x).Bytes()
					}
					return nil
				})
				if !ok {
					continue // guards fail: the committer keeps the plain path
				}
				held++
				if got := new(big.Int).SetBytes(out[slot1]); got.Cmp(want) != 0 {
					t.Fatalf("slot0=%d: program writes %v to slot 1, the EVM writes %v", x, got, want)
				}
			}
			if tc.needProgram && held == 0 {
				t.Fatal("guards fail on every base")
			}
			t.Logf("program: nodes=%d guards=%d, guards hold and outputs equal on %d bases", len(p.Nodes), len(p.Guards), held)
		})
	}
}
