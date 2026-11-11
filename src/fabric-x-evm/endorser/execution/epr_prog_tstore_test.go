package execution

// Transient storage written by a reverted frame is rolled back in the program tracer, as storage
// is. The contract below stores 7 in transient slot 0, calls itself, and the sub-call stores the storage slot
// 0 (a program input) in transient slot 0 and reverts; the caller then copies transient slot 0 to storage
// slot 1. The EVM writes 7 whatever slot 0 holds, so the program's output for slot 1 must be the constant 7.
// Before the fix the tracer kept the reverted frame's write, and the program output slot 0 instead (wrong
// on any other state; at endorsement slot 0 is also 7, so the endorsed run alone does not reveal it).

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRProgramTransientRevert(t *testing.T) {
	runtime := []byte{
		0x36, 0x60, 0x21, 0x57, // CALLDATASIZE; PUSH1 sub; JUMPI
		0x60, 0x07, 0x60, 0x00, 0x5d, // TSTORE(0, 7)
		0x60, 0x00, 0x60, 0x00, 0x60, 0x01, 0x60, 0x00, 0x60, 0x00, 0x30, 0x62, 0x0f, 0x42, 0x40, 0xf1, // CALL(1e6, self, 0, 0, 1, 0, 0)
		0x50,                               // POP
		0x60, 0x00, 0x5c, 0x60, 0x01, 0x55, // SSTORE(1, TLOAD(0))
		0x00,                               // STOP
		0x5b,                               // sub: JUMPDEST
		0x60, 0x00, 0x54, 0x60, 0x00, 0x5d, // TSTORE(0, SLOAD(0))
		0x60, 0x00, 0x60, 0x00, 0xfd, // REVERT(0, 0)
	}
	if runtime[0x21] != 0x5b {
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

	tr := NewEPRProgTracer()
	if got := run(7, tr); got.Int64() != 7 {
		t.Fatalf("endorsed run wrote %v", got)
	}
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
	for _, x := range []int64{7, 9, 0, 1_000_000} {
		want := run(x, nil)
		out, ok := q.Eval(func(k string) []byte {
			if k == slot0 {
				return big.NewInt(x).Bytes()
			}
			return nil
		})
		if !ok {
			t.Fatalf("slot0=%d: guards fail although the EVM follows the endorsed path", x)
		}
		if got := new(big.Int).SetBytes(out[slot1]); got.Cmp(want) != 0 {
			t.Fatalf("slot0=%d: program writes %v to slot 1, the EVM writes %v", x, got, want)
		}
	}
}
