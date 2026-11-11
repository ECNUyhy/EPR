package execution

// Gas headroom as a checked rule (assumption A7 of the extended version made a condition the
// tracers verify). On another base on which the guards hold, only two dynamic costs can differ from the
// endorsed run: SSTORE, whose price depends on the slot's original, current and new values (EIP-2200 with
// EIP-2929: at most 20,000 versus at least 100, so at most 19,900 more), and EXP, whose price grows by 50 gas
// per exponent byte (at most 1,600 more). Offsets, sizes, call targets, call values and account fields are
// guarded or version-checked, so every other cost is equal.
//
// Rule: at every opcode other than a call or create, the gas left after the opcode must be at least the
// extra cost the opcodes executed so far may incur, counted as 20,000 gas per SSTORE and 1,600 gas per EXP
// (programs only; a delta's labels never reach an exponent).
//
// Why it suffices: let E_k be that bound at opcode k. By induction, on any such base the gas available before
// opcode k is at least the endorsed one minus E_{k-1}. Within a frame this follows from the extra costs paid so
// far. A callee's allowance is min(gas operand, all but one 64th of the caller's gas): the operand is a
// constant, the GAS marker, or guarded (program tracer), and neither min nor the 63/64 rule enlarges a
// difference, so the allowance shrinks by at most the extra cost paid before the call (the program
// tracer's GAS marker is the remaining gas plus a fixed offset, see epr_prog_tracer.go; if it wraps around on
// another base it exceeds every allowance, which the 63/64 rule then caps). The rule makes the
// endorsed gas before opcode k exceed its endorsed cost plus E_k, hence its cost plus its own extra on the
// other base, so no frame runs out of gas, in particular no callee with a fixed allowance. The base cost of a
// call is covered by the check at the caller's next opcode, whose gas never exceeds the gas left after the
// call's base cost. The SSTORE stipend check (gas left above 2,300) holds since E_k >= 20,000.
//
// A call or create that fails before its callee starts (its base cost above the gas left, reported
// with an error or with a cost above the gas) could start on a base with more gas, so it also breaks the
// rule. Before the first SSTORE or EXP the gas is the same on every base, and afterwards the gas left is at
// least 20,000, more than any cold-access surcharge, so such a failure always shows up here.
//
// The rule replaces the earlier check of the gas left at the end of the transaction per distinct written key,
// which missed repeated writes to one slot (each write back to the original value makes the next one clean
// again) and callees with a fixed allowance.

import (
	"errors"

	"github.com/ethereum/go-ethereum/core/vm"
)

const (
	eprGasPerStore = 20_000
	eprGasPerExp   = 1_600
)

// eprGasRule accumulates the bound and records whether some opcode left less gas than the rule requires.
type eprGasRule struct {
	need      uint64
	short     bool
	at        uint64 // pc of the first opcode that broke the rule (diagnostics)
	depth     int
	minMargin int64 // smallest gas left after an opcode minus the bound (diagnostics)
	seen      bool
}

// step is called from a tracer's OnOpcode with the gas before the opcode and its cost (static plus dynamic,
// for calls and creates including the callee's allowance); failed reports the opcode's error.
func (g *eprGasRule) step(pc uint64, op byte, gas, cost uint64, depth int, countExp, failed bool) {
	switch op {
	case 0x55: // SSTORE
		g.need += eprGasPerStore
	case 0x0a: // EXP
		if countExp {
			g.need += eprGasPerExp
		}
	case 0xf0, 0xf1, 0xf2, 0xf4, 0xf5, 0xfa: // CREATE, CALL, CALLCODE, DELEGATECALL, CREATE2, STATICCALL
		// the cost includes the callee's allowance, so the caller's next opcode checks the bound; a call
		// that fails before its callee starts breaks the rule
		if !g.short && (failed || cost > gas) {
			g.short, g.at, g.depth = true, pc, depth
		}
		return
	}
	if m := int64(gas) - int64(cost) - int64(g.need); !g.seen || m < g.minMargin {
		g.minMargin, g.seen = m, true
	}
	if !g.short && (cost > gas || gas-cost < g.need) {
		g.short, g.at, g.depth = true, pc, depth
	}
}

// exit is called from a tracer's OnExit with the frame's error: a frame that halts for lack of gas after a
// value-dependent cost may run to completion on a base with more gas, so it breaks the rule. This also
// covers a call or create that fails before its callee starts, whatever the opcode hook reports for it.
func (g *eprGasRule) exit(depth int, err error) {
	if !g.short && g.need > 0 && err != nil && (errors.Is(err, vm.ErrOutOfGas) ||
		errors.Is(err, vm.ErrGasUintOverflow) || errors.Is(err, vm.ErrCodeStoreOutOfGas)) {
		g.short, g.depth = true, depth
	}
}

// GasShort reports whether the endorsed run broke the gas rule (extraction must then keep plain writes).
func (t *EPRTracer) GasShort() bool { return t.gas.short }

// GasShort reports whether the endorsed run broke the gas rule (extraction must then keep plain writes).
func (t *EPRProgTracer) GasShort() bool { return t.gasRule.short }
