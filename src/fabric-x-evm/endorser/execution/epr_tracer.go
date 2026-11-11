/*
Extension (not upstream): guarded-delta extraction for Endorsement-Preserving Repair (EPR).

The tracer follows every EVM stack value with a label recording which storage slots it depends on:

	O(s)   the value of slot s as read at endorsement
	D(s)   O(s) + c, where c depends only on other (stable) slots or on no slot at all
	G(s)   a comparison outcome involving O(s)/D(s)
	X      any other function of storage values

Every comparison involving O(s)/D(s) against a value independent of s becomes an interval constraint on
the base value x of s that reproduces the endorsed outcome. Every value of the form x + c also contributes
"no wrap-around" constraints. If a storage write stores D(s) into s itself and s never escapes (memory,
logs, calls, return data, jump conditions, hashing, other slots), the write is replaced by the guarded
delta "s <- s + delta, provided lower <= s + delta <= upper" (EPRD2): for any base satisfying the guard,
the endorsed execution path and the written value are exactly those of a serial re-execution on that
base (Lemma 1 of the paper). Anything outside this fragment keeps its plain write (EOV/MVCC as usual).

Enabled with FX_EPR_EVM=1 in the endorser process.
*/

package execution

import (
	"encoding/binary"
	"math"
	"math/big"
	"os"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"
	"github.com/hyperledger/fabric-x-sdk/blocks"
)

// EPREnabled turns on guarded-delta extraction in endorsers.
var EPREnabled = os.Getenv("FX_EPR_EVM") == "1"

// eprAllowUnversioned admits slots read as absent (version-less). That is sound whenever an absent read
// really means zero; only the test harness's read-time balance priming fakes such reads, so it is allowed
// exactly when that priming is off (FX_NO_READ_PRIME=1).
var eprAllowUnversioned = os.Getenv("FX_NO_READ_PRIME") == "1"

var (
	eprTxs, eprTxsLost, eprDeltaWrites, eprPlainWrites atomic.Int64
)

// EPRExtractStats reports traced transactions, transactions where tracking was lost, storage writes
// turned into guarded deltas and storage writes left plain.
func EPRExtractStats() (txs, lost, deltas, plain int64) {
	return eprTxs.Load(), eprTxsLost.Load(), eprDeltaWrites.Load(), eprPlainWrites.Load()
}

type eprLabel struct {
	k   byte     // 'O', 'D', 'G', 'X'
	s   string   // slot of O/D/G
	dep []string // other slots the value depends on
}

type eprPend struct {
	active bool
	base   int
	outs   []*eprLabel // labels of the pushed outputs (missing entries are untainted)
	sload  string      // slot whose loaded value is the pushed output (captures O concretely)
}

type eprSlot struct {
	orig    *uint256.Int // O(s) at endorsement (first load)
	lo, hi  *big.Int     // constraints on the base value x (hi nil = unbounded)
	neq     []*big.Int   // x != v constraints
	leaked  bool
	bad     bool // unsupported pattern
	written bool
	multi   bool // loaded after being written, or written twice
	kind    byte // 'd' delta, 'p' plain (0 = not written)
	delta   *big.Int
	deps    []string // other slots whose endorsed values entered a constraint or the delta
}

// EPRTracer is a per-transaction taint tracker. Not safe for concurrent use.
type EPRTracer struct {
	stacks [][]*eprLabel // shadow stack per call depth
	pend   []eprPend     // pending outputs of the last opcode per call depth
	last   int
	slots  map[string]*eprSlot
	lost   bool
	stored bool // an SSTORE happened (its gas cost depends on stored values)
	keys   map[[52]byte]string
	// GasLeft is the gas left after execution (before refunds). Kept for callers; the headroom decision is
	// the per-opcode gas rule of epr_gas.go, which also covers repeated writes and fixed-allowance
	// callees.
	GasLeft uint64
	gas     eprGasRule
}

const eprGasPerSlot = 20_000

func (t *EPRTracer) addDeps(s string, ds []string) {
	st := t.slot(s)
	for _, d := range ds {
		if d != s {
			st.deps = eprUnion(st.deps, []string{d})
		}
	}
}

// NewEPRTracer returns a fresh tracer for one transaction.
func NewEPRTracer() *EPRTracer {
	return &EPRTracer{slots: map[string]*eprSlot{}}
}

// Hooks returns the go-ethereum tracing hooks driving the tracer.
func (t *EPRTracer) Hooks() *tracing.Hooks {
	return &tracing.Hooks{OnOpcode: t.onOpcode, OnExit: t.onExitGas}
}

var (
	two256    = new(big.Int).Lsh(big.NewInt(1), 256)
	two255    = new(big.Int).Lsh(big.NewInt(1), 255)
	maxInt64B = big.NewInt(math.MaxInt64)
	low63     = new(uint256.Int).SetUint64(math.MaxInt64)
)

// stack arity [inputs, outputs]; PUSH/DUP/SWAP handled separately.
var eprIO = func() map[byte][2]int {
	m := map[byte][2]int{}
	set := func(op byte, in, out int) { m[op] = [2]int{in, out} }
	for _, op := range []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x0a, 0x0b, 0x10, 0x11, 0x12, 0x13, 0x14,
		0x16, 0x17, 0x18, 0x1a, 0x1b, 0x1c, 0x1d, 0x20} {
		set(op, 2, 1)
	}
	set(0x08, 3, 1)
	set(0x09, 3, 1)
	for _, op := range []byte{0x15, 0x19, 0x1e, 0x31, 0x35, 0x3b, 0x3f, 0x40, 0x49, 0x51, 0x54, 0x5c} {
		set(op, 1, 1)
	}
	for _, op := range []byte{0x30, 0x32, 0x33, 0x34, 0x36, 0x38, 0x3a, 0x3d, 0x41, 0x42, 0x43, 0x44, 0x45,
		0x46, 0x47, 0x48, 0x4a, 0x58, 0x59, 0x5a, 0x5f} {
		set(op, 0, 1)
	}
	set(0x37, 3, 0)
	set(0x39, 3, 0)
	set(0x3c, 4, 0)
	set(0x3e, 3, 0)
	set(0x50, 1, 0)
	set(0x52, 2, 0)
	set(0x53, 2, 0)
	set(0x55, 2, 0)
	set(0x56, 1, 0)
	set(0x57, 2, 0)
	set(0x00, 0, 0)
	set(0x5b, 0, 0)
	set(0x5d, 2, 0)
	set(0x5e, 3, 0)
	for i := byte(0); i <= 4; i++ {
		set(0xa0+i, 2+int(i), 0)
	}
	set(0xf0, 3, 1)
	set(0xf1, 7, 1)
	set(0xf2, 7, 1)
	set(0xf3, 2, 0)
	set(0xf4, 6, 1)
	set(0xf5, 4, 1)
	set(0xfa, 6, 1)
	set(0xfd, 2, 0)
	set(0xfe, 0, 0)
	set(0xff, 1, 0)
	return m
}()

// eprIOArr/eprIOKnown are eprIO as fixed arrays (the per-opcode lookup is on the hot path).
var eprIOArr, eprIOKnown = func() (a [256][2]int, k [256]bool) {
	for op, io := range eprIO {
		a[op], k[op] = io, true
	}
	return a, k
}()

// escapes: inputs become externally observable (memory, logs, calls, hashing, copies, return data).
var eprESC = map[byte]bool{0x20: true, 0x37: true, 0x39: true, 0x3c: true, 0x3e: true, 0x51: true, 0x52: true,
	0x53: true, 0x56: true, 0x5d: true, 0x5e: true, 0xa0: true, 0xa1: true, 0xa2: true, 0xa3: true, 0xa4: true,
	0xf0: true, 0xf1: true, 0xf2: true, 0xf3: true, 0xf4: true, 0xf5: true, 0xfa: true, 0xfd: true, 0xff: true}

func (t *EPRTracer) slot(s string) *eprSlot {
	st := t.slots[s]
	if st == nil {
		st = &eprSlot{lo: big.NewInt(0)}
		t.slots[s] = st
	}
	return st
}

func eprDeps(l *eprLabel) []string {
	if l == nil {
		return nil
	}
	r := append([]string(nil), l.dep...)
	if l.s != "" {
		r = append(r, l.s)
	}
	return r
}

func eprHas(l *eprLabel, s string) bool {
	for _, d := range eprDeps(l) {
		if d == s {
			return true
		}
	}
	return false
}

func eprUnion(a, b []string) []string {
	r := append([]string(nil), a...)
	for _, x := range b {
		found := false
		for _, y := range r {
			if x == y {
				found = true
				break
			}
		}
		if !found {
			r = append(r, x)
		}
	}
	return r
}

func (t *EPRTracer) leak(l *eprLabel) {
	for _, s := range eprDeps(l) {
		t.slot(s).leaked = true
	}
}

// slotKey returns the StateDB key of a storage slot, caching it per (address, slot): building it hashes
// the address for its checksum, which is costly on the per-opcode path.
func (t *EPRTracer) slotKey(addr common.Address, slot *uint256.Int) string {
	var k [52]byte
	copy(k[:20], addr[:])
	h := slot.Bytes32()
	copy(k[20:], h[:])
	if s, ok := t.keys[k]; ok {
		return s
	}
	s := storeKey(addr, common.Hash(h))
	if t.keys == nil {
		t.keys = map[[52]byte]string{}
	}
	t.keys[k] = s
	return s
}

// offset returns c = v - O(s) as a signed integer (v and O are uint256).
func (t *EPRTracer) offset(s string, v *uint256.Int) (*big.Int, bool) {
	st := t.slot(s)
	if st.orig == nil {
		return nil, false
	}
	c := new(big.Int).Sub(v.ToBig(), st.orig.ToBig())
	c.Mod(c, two256)
	if c.Cmp(two255) >= 0 {
		c.Sub(c, two256)
	}
	return c, true
}

// noWrap records that x + c stays within [0, 2^63) for the base value x (int64 domain, no wrap-around).
func (t *EPRTracer) noWrap(s string, c *big.Int) {
	st := t.slot(s)
	lo := new(big.Int).Neg(c)
	if lo.Cmp(st.lo) > 0 {
		st.lo = lo
	}
	hi := new(big.Int).Sub(maxInt64B, c)
	if st.hi == nil || hi.Cmp(st.hi) < 0 {
		st.hi = hi
	}
}

func (t *EPRTracer) bound(s string, lo, hi *big.Int) {
	st := t.slot(s)
	if lo != nil && lo.Cmp(st.lo) > 0 {
		st.lo = lo
	}
	if hi != nil && (st.hi == nil || hi.Cmp(st.hi) < 0) {
		st.hi = hi
	}
}

// compare records the constraint for "labeled (x + c) <op> b" whose endorsed outcome is r.
// labeledFirst says whether the labeled operand is the first (top) input. op: 'l' (<), 'g' (>), 'e' (==).
func (t *EPRTracer) compare(s string, c, b *big.Int, op byte, labeledFirst, r bool) {
	v := new(big.Int).Sub(b, c) // x <op> v  (labeled first) or v <op> x (labeled second)
	one := big.NewInt(1)
	if op == 'e' {
		if r {
			t.bound(s, v, new(big.Int).Set(v))
		} else {
			st := t.slot(s)
			st.neq = append(st.neq, v)
		}
		return
	}
	// normalise to "x < v", "x > v" with the labeled operand on the left
	less := op == 'l'
	if !labeledFirst {
		less = !less
	}
	switch {
	case less && r: // x < v
		t.bound(s, nil, new(big.Int).Sub(v, one))
	case less && !r: // x >= v
		t.bound(s, v, nil)
	case !less && r: // x > v
		t.bound(s, new(big.Int).Add(v, one), nil)
	default: // x <= v
		t.bound(s, nil, v)
	}
}

// onExitGas: a frame that halts for lack of gas after a value-dependent cost breaks the gas rule.
func (t *EPRTracer) onExitGas(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
	t.gas.exit(depth, err)
}

func (t *EPRTracer) onOpcode(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
	t.gas.step(pc, op, gas, cost, depth, false, err != nil)
	if t.lost {
		return
	}
	stack := scope.StackData()
	n := len(stack)
	for len(t.stacks) <= depth {
		t.stacks = append(t.stacks, nil)
		t.pend = append(t.pend, eprPend{})
	}
	if depth > t.last {
		// values entering a new frame come through memory/calldata, already untainted
		t.stacks[depth] = t.stacks[depth][:0]
		t.pend[depth].active = false
	}
	if depth < t.last {
		for x := depth + 1; x <= t.last && x < len(t.stacks); x++ {
			t.stacks[x] = t.stacks[x][:0]
			t.pend[x].active = false
		}
	}
	t.last = depth
	S := t.stacks[depth]
	if p := &t.pend[depth]; p.active {
		pushes := n - p.base
		for i := 0; i < pushes; i++ {
			var l *eprLabel
			if i < len(p.outs) {
				l = p.outs[i]
			}
			S = append(S, l)
		}
		if p.sload != "" && n > 0 {
			st := t.slot(p.sload)
			if st.orig == nil && !st.written {
				v := stack[n-1]
				st.orig = &v
			}
		}
		p.active, p.outs, p.sload = false, nil, ""
	}
	// Any mismatch between the shadow stack and the real one means we lost track: give up on this
	// transaction (all its writes stay plain).
	if len(S) != n {
		t.lost = true
		return
	}
	t.stacks[depth] = S
	top := func(i int) *uint256.Int { return &stack[n-1-i] }

	switch {
	case op >= 0x80 && op <= 0x8f: // DUPn
		k := int(op-0x80) + 1
		if l := S[n-k]; l != nil {
			t.pend[depth] = eprPend{active: true, base: n, outs: []*eprLabel{l}}
		} else {
			t.pend[depth] = eprPend{active: true, base: n}
		}
		return
	case op >= 0x90 && op <= 0x9f: // SWAPn
		k := int(op-0x90) + 1
		S[n-1], S[n-1-k] = S[n-1-k], S[n-1]
		t.pend[depth] = eprPend{active: true, base: n}
		return
	case op >= 0x60 && op <= 0x7f: // PUSHn
		t.pend[depth] = eprPend{active: true, base: n}
		return
	}
	if !eprIOKnown[op] || eprIOArr[op][0] > n {
		t.lost = true
		return
	}
	io := eprIOArr[op]
	var insBuf [7]*eprLabel
	ins := insBuf[:io[0]]
	tainted := false
	for j := 0; j < io[0]; j++ {
		ins[j] = S[n-1-j] // ins[0] is the top of the stack
		tainted = tainted || ins[j] != nil
	}
	S = S[:n-io[0]]
	t.stacks[depth] = S
	// Fast path: no tainted input and an opcode without storage/control-flow side effects pushes only
	// untainted outputs; no labels or constraints are involved.
	if !tainted && op != 0x54 && op != 0x55 && op != 0x5a {
		t.pend[depth] = eprPend{active: true, base: len(S)}
		return
	}
	var outs []*eprLabel
	var sload string
	a0, a1 := (*eprLabel)(nil), (*eprLabel)(nil)
	if len(ins) > 0 {
		a0 = ins[0]
	}
	if len(ins) > 1 {
		a1 = ins[1]
	}
	addr := scope.Address()
	isSD := func(l *eprLabel) bool { return l != nil && (l.k == 'O' || l.k == 'D') }

	switch op {
	case 0x54: // SLOAD
		if a0 != nil {
			t.leak(a0)
		}
		s := t.slotKey(addr, top(0))
		st := t.slot(s)
		if st.written {
			st.multi = true
			outs = []*eprLabel{{k: 'X', dep: []string{s}}}
		} else {
			outs = []*eprLabel{{k: 'O', s: s}}
			sload = s
		}
	case 0x01, 0x03: // ADD, SUB (SUB: a0 - a1)
		var base, other *eprLabel
		if isSD(a0) && !eprHas(a1, a0.s) {
			base, other = a0, a1
		} else if op == 0x01 && isSD(a1) && !eprHas(a0, a1.s) {
			base, other = a1, a0
		}
		if base != nil {
			x, y := top(0), top(1)
			res := new(uint256.Int)
			if op == 0x01 {
				res.Add(x, y)
			} else {
				res.Sub(x, y)
			}
			if c, ok := t.offset(base.s, res); ok {
				t.noWrap(base.s, c)
				t.addDeps(base.s, eprUnion(base.dep, eprDeps(other)))
				outs = []*eprLabel{{k: 'D', s: base.s, dep: eprUnion(base.dep, eprDeps(other))}}
			} else {
				t.slot(base.s).bad = true
				outs = []*eprLabel{{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}}
			}
		} else if a0 != nil || a1 != nil {
			outs = []*eprLabel{{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}}
		} else {
			outs = []*eprLabel{nil}
		}
	case 0x16: // AND with a mask keeping the low 63 bits is the identity on [0, 2^63)
		var lab *eprLabel
		var mask *uint256.Int
		if isSD(a0) && a1 == nil {
			lab, mask = a0, top(1)
		} else if isSD(a1) && a0 == nil {
			lab, mask = a1, top(0)
		}
		if lab != nil && new(uint256.Int).And(mask, low63).Eq(low63) {
			v := top(0)
			if lab == a1 {
				v = top(1)
			}
			if c, ok := t.offset(lab.s, v); ok {
				t.noWrap(lab.s, c)
				outs = []*eprLabel{lab}
				break
			}
		}
		if a0 != nil || a1 != nil {
			outs = []*eprLabel{{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}}
		} else {
			outs = []*eprLabel{nil}
		}
	case 0x1c: // SHR (a0 = shift, a1 = value): shifting a value in [0, 2^63) right by >= 63 gives 0
		if a0 == nil && isSD(a1) && top(0).IsUint64() && top(0).Uint64() >= 63 {
			if c, ok := t.offset(a1.s, top(1)); ok {
				t.noWrap(a1.s, c)
				outs = []*eprLabel{nil}
				break
			}
		}
		if a0 != nil || a1 != nil {
			outs = []*eprLabel{{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}}
		} else {
			outs = []*eprLabel{nil}
		}
	case 0x10, 0x11, 0x12, 0x13, 0x14: // LT GT SLT SGT EQ
		outs = []*eprLabel{t.onCompare(op, a0, a1, top(0), top(1))}
	case 0x15: // ISZERO
		switch {
		case a0 == nil:
			outs = []*eprLabel{nil}
		case a0.k == 'G':
			outs = []*eprLabel{a0}
		case isSD(a0):
			zero := new(uint256.Int)
			outs = []*eprLabel{t.onCompare(0x14, a0, nil, top(0), zero)}
		default:
			outs = []*eprLabel{{k: 'X', dep: eprDeps(a0)}}
		}
	case 0x57: // JUMPI (a0 = dest, a1 = cond): guards may steer control flow, nothing else may
		if a0 != nil {
			t.leak(a0)
		}
		if a1 != nil && a1.k != 'G' {
			t.leak(a1)
		}
	case 0x55: // SSTORE (a0 = slot, a1 = value)
		s := t.slotKey(addr, top(0))
		st := t.slot(s)
		if a0 != nil {
			t.leak(a0)
		}
		if st.written {
			st.multi = true
		}
		st.written = true
		t.stored = true
		switch {
		case isSD(a1) && a1.s == s:
			if c, ok := t.offset(s, top(1)); ok {
				st.kind, st.delta = 'd', c
				t.addDeps(s, a1.dep)
			} else {
				st.kind = 'p'
			}
		default:
			st.kind = 'p'
			if a1 != nil {
				t.leak(a1)
			}
		}
	case 0x50: // POP
	case 0x5a: // GAS: after an SSTORE the remaining gas depends on stored values (zero / non-zero)
		if t.stored {
			for _, st := range t.slots {
				if st.written {
					st.bad = true
				}
			}
		}
		outs = []*eprLabel{nil}
	default:
		if op == 0x0a && a1 != nil { // EXP: its gas cost depends on the exponent's byte length
			t.leak(a1)
		}
		if eprESC[op] {
			for _, l := range ins {
				if l != nil {
					t.leak(l)
				}
			}
			for r := 0; r < io[1]; r++ {
				outs = append(outs, nil)
			}
		} else {
			var ds []string
			for _, l := range ins {
				ds = eprUnion(ds, eprDeps(l))
			}
			for r := 0; r < io[1]; r++ {
				if len(ds) > 0 {
					outs = append(outs, &eprLabel{k: 'X', dep: ds})
				} else {
					outs = append(outs, nil)
				}
			}
		}
	}
	t.pend[depth] = eprPend{active: true, base: len(S), outs: outs, sload: sload}
}

// onCompare handles LT/GT/SLT/SGT/EQ with top-of-stack operands a0 (value x0) and a1 (value x1).
func (t *EPRTracer) onCompare(op byte, a0, a1 *eprLabel, x0, x1 *uint256.Int) *eprLabel {
	isSD := func(l *eprLabel) bool { return l != nil && (l.k == 'O' || l.k == 'D') }
	var r bool
	switch op {
	case 0x10:
		r = x0.Lt(x1)
	case 0x11:
		r = x0.Gt(x1)
	case 0x12:
		r = x0.Slt(x1)
	case 0x13:
		r = x0.Sgt(x1)
	case 0x14:
		r = x0.Eq(x1)
	}
	// both sides offsets of the same slot: the outcome does not depend on x (no-wrap constraints hold)
	if isSD(a0) && isSD(a1) && a0.s == a1.s {
		c0, ok0 := t.offset(a0.s, x0)
		c1, ok1 := t.offset(a1.s, x1)
		if ok0 && ok1 {
			t.noWrap(a0.s, c0)
			t.noWrap(a0.s, c1)
			t.addDeps(a0.s, eprUnion(a0.dep, a1.dep))
			return &eprLabel{k: 'G', s: a0.s, dep: eprUnion(a0.dep, a1.dep)}
		}
	}
	var lab, other *eprLabel
	var lv, ov *uint256.Int
	labeledFirst := false
	switch {
	case isSD(a0) && !eprHas(a1, a0.s) && !isSD(a1):
		lab, other, lv, ov, labeledFirst = a0, a1, x0, x1, true
	case isSD(a1) && !eprHas(a0, a1.s) && !isSD(a0):
		lab, other, lv, ov = a1, a0, x1, x0
	}
	if lab == nil {
		if a0 != nil || a1 != nil {
			return &eprLabel{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}
		}
		return nil
	}
	if (op == 0x12 || op == 0x13) && ov.Sign() < 0 { // signed comparison against a negative constant
		t.slot(lab.s).bad = true
	}
	c, ok := t.offset(lab.s, lv)
	if !ok {
		t.slot(lab.s).bad = true
		return &eprLabel{k: 'X', dep: eprUnion(eprDeps(a0), eprDeps(a1))}
	}
	t.noWrap(lab.s, c)
	t.addDeps(lab.s, eprUnion(lab.dep, eprDeps(other)))
	kind := byte('e')
	switch op {
	case 0x10, 0x12:
		kind = 'l'
	case 0x11, 0x13:
		kind = 'g'
	}
	t.compare(lab.s, c, ov.ToBig(), kind, labeledFirst, r)
	return &eprLabel{k: 'G', s: lab.s, dep: eprUnion(lab.dep, eprDeps(other))}
}

// EPRMagic2 prefixes the interval-guarded delta encoding understood by the EPR committer:
// "EPRD2" | delta int64 BE | lower int64 BE | upper int64 BE | width byte.
var EPRMagic2 = []byte("EPRD2")

// EncodeEPRD2 encodes "key <- key + delta, provided lower <= key + delta <= upper".
func EncodeEPRD2(delta, lower, upper int64, width byte) []byte {
	b := make([]byte, 0, 30)
	b = append(b, EPRMagic2...)
	b = binary.BigEndian.AppendUint64(b, uint64(delta)) //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, uint64(lower)) //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, uint64(upper)) //nolint:gosec
	return append(b, width)
}

// Rewrite turns extractable storage writes of rws into guarded deltas. A write qualifies when its slot
// was read from the ledger (not primed: the read must carry a version), its value is D(slot) with the
// slot never escaping, its dependencies are stable reads (not written by this transaction), and the
// endorsed base value satisfies every recorded constraint inside the int64 domain.
func (t *EPRTracer) Rewrite(rws blocks.ReadWriteSet) blocks.ReadWriteSet {
	eprTxs.Add(1)
	if t.lost {
		eprTxsLost.Add(1)
		return rws
	}
	readVersioned := map[string]bool{}
	for _, r := range rws.Reads {
		readVersioned[r.Key] = r.Version != nil
	}
	written := map[string]bool{}
	for s, st := range t.slots {
		if st.written {
			written[s] = true
		}
	}
	if t.gas.short {
		for range written {
			eprPlainWrites.Add(1)
			eprCountReason("gas_headroom")
		}
		return rws
	}
	// A slot whose endorsed value entered another slot's constraint or delta is fixed there as a constant,
	// so its own write must stay plain (its read stays version-checked).
	depOf := map[string]bool{}
	for s, st := range t.slots {
		for _, d := range st.deps {
			if d != s {
				depOf[d] = true
			}
		}
	}
	for i, w := range rws.Writes {
		st, ok := t.slots[w.Key]
		if !ok || !st.written {
			continue
		}
		d, lo, hi, why := st.extract(written, readVersioned[w.Key])
		if why == "" && depOf[w.Key] {
			why = "dep_of_other"
		}
		if why == "" {
			rws.Writes[i] = blocks.KVWrite{Key: w.Key, Value: EncodeEPRD2(d, lo, hi, 32)}
			eprDeltaWrites.Add(1)
		} else {
			eprPlainWrites.Add(1)
			eprCountReason(why)
		}
	}
	return rws
}

// eprReasons counts why storage writes stayed plain.
var (
	eprReasonsMu sync.Mutex
	eprReasons   = map[string]int64{}
)

func eprCountReason(why string) {
	eprReasonsMu.Lock()
	eprReasons[why]++
	eprReasonsMu.Unlock()
}

// EPRPlainReasons returns a copy of the plain-write reason counters.
func EPRPlainReasons() map[string]int64 {
	eprReasonsMu.Lock()
	defer eprReasonsMu.Unlock()
	out := make(map[string]int64, len(eprReasons))
	for k, v := range eprReasons {
		out[k] = v
	}
	return out
}

// extract returns the guarded delta of the slot's write, or a non-empty reason why it stays plain.
func (st *eprSlot) extract(written map[string]bool, versioned bool) (delta, lower, upper int64, why string) {
	switch {
	case !versioned && !eprAllowUnversioned:
		return 0, 0, 0, "unversioned_read"
	case st.kind != 'd':
		return 0, 0, 0, "not_delta_value"
	case st.leaked:
		return 0, 0, 0, "leaked"
	case st.multi:
		return 0, 0, 0, "multi_access"
	case st.bad:
		return 0, 0, 0, "unsupported"
	case st.orig == nil || st.delta == nil:
		return 0, 0, 0, "no_base"
	}
	for _, d := range st.deps {
		if written[d] {
			return 0, 0, 0, "unstable_dep"
		}
	}
	o := st.orig.ToBig()
	if o.Cmp(maxInt64B) > 0 || !st.delta.IsInt64() {
		return 0, 0, 0, "out_of_int64"
	}
	lo := new(big.Int).Set(st.lo)
	hi := new(big.Int).Set(maxInt64B)
	if st.hi != nil && st.hi.Cmp(hi) < 0 {
		hi.Set(st.hi)
	}
	for _, v := range st.neq { // x != v: only representable at an interval end
		switch {
		case v.Cmp(lo) < 0 || v.Cmp(hi) > 0:
		case v.Cmp(lo) == 0:
			lo.Add(lo, big.NewInt(1))
		case v.Cmp(hi) == 0:
			hi.Sub(hi, big.NewInt(1))
		default:
			return 0, 0, 0, "neq_inside"
		}
	}
	if o.Cmp(lo) < 0 || o.Cmp(hi) > 0 { // the endorsed run itself must satisfy the constraints
		return 0, 0, 0, "base_violates"
	}
	d := st.delta.Int64()
	pl := new(big.Int).Add(lo, st.delta)
	ph := new(big.Int).Add(hi, st.delta)
	if pl.Sign() < 0 {
		pl.SetInt64(0)
	}
	// A base upper bound at the int64 domain limit is implied by the committer's value parsing (bases
	// above it abort), so it constrains nothing: report the post value as unbounded. This keeps lower-only
	// debits recognisable as escrow debits; overflow is still checked by the VC.
	if ph.Cmp(maxInt64B) > 0 || hi.Cmp(maxInt64B) >= 0 {
		ph.Set(maxInt64B)
	}
	if !pl.IsInt64() || !ph.IsInt64() || pl.Cmp(ph) > 0 {
		return 0, 0, 0, "empty_guard"
	}
	return d, pl.Int64(), ph.Int64(), ""
}

// ApplyEPRD2 applies an EPRD2-encoded value to the previous stored value (state replicas: endorsers).
// It reports ok = false when v is not an EPRD2 value. The committer only marks a transaction valid if
// the guard held, so replicas apply the delta unconditionally, in block order.
func ApplyEPRD2(prev, v []byte) ([]byte, bool) {
	if len(v) != 30 || string(v[:5]) != string(EPRMagic2) {
		return nil, false
	}
	delta := int64(binary.BigEndian.Uint64(v[5:13])) //nolint:gosec
	width := int(v[29])
	var base int64
	switch len(prev) {
	case 8:
		base = int64(binary.BigEndian.Uint64(prev)) //nolint:gosec
	case 32:
		base = int64(binary.BigEndian.Uint64(prev[24:])) //nolint:gosec
	}
	out := make([]byte, width)
	binary.BigEndian.PutUint64(out[width-8:], uint64(base+delta)) //nolint:gosec
	return out, true
}
