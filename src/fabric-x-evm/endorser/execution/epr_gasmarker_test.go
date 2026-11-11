package execution

// The GAS marker of the program tracer. After an SSTORE the
// remaining gas may differ on another base, so GAS yields a marker that may only set a call's gas. The
// AND example passes the marker through AND 65536: the SSTORE of slot 0 costs 2,900 on the endorsed
// base (x = 1) and 20,000 on base 0, so the remaining gas drops from about 70,000 to about 52,900, bit 16
// clears, and the callee gets no gas at all; the caller reverts, while a program would still write x + 1.
// The tracer must now lose every marker expression other than the remaining gas plus or minus a constant
// (site 93). Controls: raw GAS, GAS - 5000 and GAS + 5000 yield programs whose outputs equal the EVM's on
// every base on which their guards hold. Two more tests cover a frame that runs out of gas after an SSTORE:
// end to end (a callee's CREATE) and at the rule itself (a call that fails before its callee starts).

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func TestEPRGasMarker(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	// callee: a loop of 1,200 iterations (about 31,200 gas) without state access
	child := eprGasDeploy(t, e, owner, []byte{
		0x61, 0x04, 0xb0, // PUSH2 1200
		0x5b, 0x60, 0x01, 0x90, 0x03, // loop (0x03): JUMPDEST; counter - 1
		0x80, 0x60, 0x03, 0x57, // DUP1; PUSH1 loop; JUMPI
		0x00,
	})
	// caller: SSTORE(0, SLOAD(0) + 1); ok = CALL(gasExpr, child, 0, no input, no output); if !ok REVERT
	parent := func(gasExpr []byte) []byte {
		code := []byte{0x60, 0x00, 0x54, 0x60, 0x01, 0x01, 0x60, 0x00, 0x55,
			0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x73}
		code = append(code, child.Bytes()...)
		code = append(code, gasExpr...)
		code = append(code, 0xf1, 0x15, 0x60, 0x00, 0x57, 0x00) // CALL; ISZERO; PUSH1 rev; JUMPI; STOP
		code[len(code)-3] = byte(len(code))
		return append(code, 0x5b, 0x60, 0x00, 0x80, 0xfd) // rev: JUMPDEST; REVERT(0, 0)
	}
	negAdd := func(k uint64) []byte { // PUSH32 2^256 - k; GAS; ADD: a subtraction as compilers encode it
		v := new(uint256.Int).Neg(uint256.NewInt(k)).Bytes32()
		return append(append([]byte{0x7f}, v[:]...), 0x5a, 0x01)
	}
	cases := []struct {
		name    string
		gasExpr []byte
		program bool // false: extraction must give up at site 93
	}{
		{"raw", []byte{0x5a}, true},
		{"minus-as-add", negAdd(5000), true},          // GAS + (2^256 - 5000)
		{"minus-as-add-wraps", negAdd(100000), false}, // GAS + (2^256 - 100000), GAS < 100000 when endorsed
		{"minus-const", []byte{0x61, 0x13, 0x88, 0x5a, 0x03}, true},          // GAS - 5000
		{"plus-const", []byte{0x61, 0x13, 0x88, 0x5a, 0x01}, true},           // GAS + 5000
		{"and-mask", []byte{0x62, 0x01, 0x00, 0x00, 0x5a, 0x16}, false},      // GAS & 65536 (the AND example)
		{"minus-wraps", []byte{0x62, 0x01, 0x86, 0xa0, 0x5a, 0x03}, false},   // GAS - 100000 < 0 when endorsed
		{"const-minus-gas", []byte{0x5a, 0x62, 0x01, 0xfb, 0xd0, 0x03}, false}, // 130000 - GAS
		{"times-two", []byte{0x60, 0x02, 0x5a, 0x02}, false},                 // GAS * 2
		{"two-readings", []byte{0x5a, 0x5a, 0x01}, false},                    // GAS + GAS
		{"plus-huge", []byte{0x68, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0x5a, 0x01}, false}, // GAS + 2^64
	}
	contracts := map[string]common.Address{}
	for _, tc := range cases {
		code := parent(tc.gasExpr)
		if code[code[len(code)-8]] != 0x5b {
			t.Fatalf("%s: bad revert target", tc.name)
		}
		contracts[tc.name] = eprGasDeploy(t, e, owner, code)
	}
	run := func(c common.Address, x int64, budget uint64, tr *EPRProgTracer) (*big.Int, error) {
		snap := e.sdb.Snapshot()
		defer e.sdb.RevertToSnapshot(snap)
		slot0 := storeKey(c, common.Hash{})
		e.setKey(slot0, big.NewInt(x))
		evm := e.evm(user, nil)
		if tr != nil {
			evm = e.evm(user, tr.Hooks())
		}
		_, _, err := evm.Call(user, c, nil, vm.NewGasBudget(budget), new(uint256.Int))
		return e.getKey(slot0), err
	}

	// a gas limit at which the AND example succeeds on the endorsed base (x = 1) and reverts on base 0
	and := contracts["and-mask"]
	budget := uint64(0)
	for b := uint64(70_000); b <= 100_000; b += 250 {
		_, err1 := run(and, 1, b, nil)
		v0, err0 := run(and, 0, b, nil)
		if err1 == nil && err0 != nil && v0.Int64() == 0 {
			budget = b
			break
		}
	}
	if budget == 0 {
		t.Fatal("no gas limit separates the two bases: the example does not reproduce")
	}
	t.Logf("gas limit %d: AND 65536 succeeds on x = 1 and reverts on x = 0 (the write of x + 1 is lost)", budget)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := contracts[tc.name]
			slot0 := storeKey(c, common.Hash{})
			tr := NewEPRProgTracer()
			if v, err := run(c, 1, budget, tr); err != nil || v.Int64() != 2 {
				t.Fatalf("endorsed run: %v %v", v, err)
			}
			if (tc.program || tc.name == "and-mask") && tr.GasShort() { // the example must pass the per-opcode rule
				t.Fatalf("the per-opcode rule rejects the endorsed run (pc %d, need %d): the case tests nothing",
					tr.gasRule.at, tr.gasRule.need)
			}
			writes := map[string]bool{}
			for s, w := range tr.written {
				if w {
					writes[s] = true
				}
			}
			p := tr.Program(writes)
			if !tc.program {
				if p != nil || !strings.Contains(tr.lostAt, "site=93") {
					t.Fatalf("marker expression accepted (program %v, lostAt=%q)", p != nil, tr.lostAt)
				}
				t.Logf("lost: %s", tr.lostAt)
				return
			}
			if p == nil {
				t.Fatalf("no program (lostAt=%q)", tr.lostAt)
			}
			q, ok := DecodeEPRProgram(p.Encode())
			if !ok {
				t.Fatal("program does not round-trip")
			}
			held := 0
			for _, x := range []int64{1, 0, 2, 7, 1 << 40} {
				want, err := run(c, x, budget, nil)
				if err != nil {
					t.Fatalf("x = %d: the EVM fails (%v) although the rule accepted the endorsed run", x, err)
				}
				out, ok := q.Eval(func(k string) []byte {
					if k == slot0 {
						return big.NewInt(x).Bytes()
					}
					return nil
				})
				if !ok {
					continue
				}
				held++
				if got := new(big.Int).SetBytes(out[slot0]); got.Cmp(want) != 0 {
					t.Fatalf("x = %d: program writes %v, the EVM writes %v", x, got, want)
				}
			}
			if held == 0 {
				t.Fatal("guards fail on every base")
			}
			t.Logf("program: nodes=%d guards=%d, outputs equal the EVM's on %d bases", len(p.Nodes), len(p.Guards), held)
		})
	}
}

// End to end: a caller gives a callee a fixed 50,000 gas; the callee writes SLOAD(0)+1 and then runs CREATE
// (32,000 gas). On the endorsed base (slot 0 = 0) the SSTORE costs 20,000, the callee has about 27,900 gas
// left (above the 20,000 the rule asks), and CREATE runs out of gas: the callee halts and its write is undone.
// On base 1 the SSTORE costs 2,900, CREATE succeeds and the write stays. Both tracers must reject the endorsed
// run; with 80,000 gas the CREATE succeeds on both bases and the run is accepted.
func TestEPRGasRuleFailedCreateInCallee(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	callee := eprGasDeploy(t, e, owner, []byte{
		0x60, 0x00, 0x54, 0x60, 0x01, 0x01, 0x60, 0x00, 0x55, // SSTORE(0, SLOAD(0) + 1)
		0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0xf0, 0x50, 0x00, // CREATE(0, 0, 0); POP; STOP
	})
	caller := func(gas uint32) common.Address {
		code := []byte{0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x73}
		code = append(code, callee.Bytes()...)
		code = append(code, 0x62, byte(gas>>16), byte(gas>>8), byte(gas), 0xf1, 0x50, 0x00) // PUSH3 gas; CALL; POP; STOP
		return eprGasDeploy(t, e, owner, code)
	}
	tight, roomy := caller(50_000), caller(80_000)
	slot0 := storeKey(callee, common.Hash{})
	run := func(to common.Address, x int64, d *EPRTracer, p *EPRProgTracer) *big.Int {
		snap := e.sdb.Snapshot()
		defer e.sdb.RevertToSnapshot(snap)
		e.setKey(slot0, big.NewInt(x))
		evm := e.evm(user, nil)
		if d != nil {
			evm = e.evm(user, EPRCombineHooks(d, p))
		}
		if _, _, err := evm.Call(user, to, nil, vm.NewGasBudget(1_000_000), new(uint256.Int)); err != nil {
			t.Fatalf("caller fails: %v", err)
		}
		return e.getKey(slot0)
	}
	if v0, v1 := run(tight, 0, nil, nil), run(tight, 1, nil, nil); v0.Int64() != 0 || v1.Int64() != 2 {
		t.Fatalf("the callee's fate should depend on the base: slot after base 0 = %v, after base 1 = %v", v0, v1)
	}
	d, p := NewEPRTracer(), NewEPRProgTracer()
	run(tight, 0, d, p)
	if !d.GasShort() || !p.GasShort() {
		t.Fatalf("a callee that runs out of gas after an SSTORE is accepted (delta %v, program %v)", d.GasShort(), p.GasShort())
	}
	d, p = NewEPRTracer(), NewEPRProgTracer()
	if v := run(roomy, 0, d, p); v.Int64() != 1 {
		t.Fatalf("control: slot %v", v)
	}
	if d.GasShort() || p.GasShort() {
		t.Fatalf("control rejected (delta at pc %d, program at pc %d)", d.gas.at, p.gasRule.at)
	}
	if v := run(roomy, 1, nil, nil); v.Int64() != 2 {
		t.Fatalf("control on base 1: slot %v", v)
	}
}

// A call whose base cost exceeds the gas left fails before its callee starts; on a base where an earlier
// SSTORE is cheaper it could start, so the rule must reject the endorsed run.
func TestEPRGasRuleFailedCall(t *testing.T) {
	var g eprGasRule
	g.step(0, 0x55, 50_000, 2_900, 0, true, false) // SSTORE: 47,100 left, need 20,000
	g.step(1, 0xf1, 30_000, 36_600, 0, true, true) // CALL with value to a new account: out of gas
	if !g.short {
		t.Fatal("a call that fails before its callee starts is accepted")
	}
	var h eprGasRule
	h.step(0, 0x55, 50_000, 2_900, 0, true, false)
	h.step(1, 0xf1, 30_000, 30_000, 0, true, false) // a call that starts with all the gas it may take
	h.step(2, 0x50, 25_000, 2, 0, true, false)
	if h.short {
		t.Fatal("cost within the gas left and no error: must be accepted")
	}
	h.step(3, 0xfa, 25_000, 2_600, 0, true, true) // STATICCALL whose failure shows only as an error
	if !h.short {
		t.Fatal("a failed call reported with an error only is accepted")
	}
	var k eprGasRule // a call that starts: its cost includes the allowance and stays within the gas
	k.step(0, 0x55, 50_000, 2_900, 0, true, false)
	k.step(1, 0xf1, 47_000, 46_300, 0, true, false)
	k.step(2, 0x50, 25_000, 2, 0, true, false)
	if k.short {
		t.Fatalf("a call that starts is rejected (pc %d)", k.at)
	}
}
