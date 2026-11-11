package execution

// Program tracer for generalised endorsement-preserving repair (see eprprog_core.go).
//
// The tracer shadows every EVM stack word, memory word (32-byte, by exact offset), call-data word and
// return-data word with an expression node when the value depends on a storage read of this transaction,
// across call frames. It records a guard whenever such a value influences execution:
//   - JUMPI condition (truthiness) and JUMP target (equality);
//   - storage keys, memory offsets/sizes, call targets/values, BALANCE/EXTCODE* addresses (equality);
//   - KECCAK256 and precompile inputs (equality of the input words, so the output is unchanged).
// Under all guards the opcode sequence, every address and every untainted value are those of the endorsed
// run, and every tainted value is the node's evaluation (EVM semantics replayed exactly), so the storage
// writes are the program outputs. Gas: remaining gas may differ on a different base (SSTORE pricing, EXP
// with a tainted exponent, or a call whose gas operand is tainted, which changes the callee's gas); from the
// first such opcode on, GAS yields a marker that may only feed call gas limits, unchanged or plus or minus
// a constant; extraction requires headroom (the per-opcode gas rule of epr_gas.go).
// Log data derived from repaired values is not recomputed; the committed state does not depend on it.
// Anything outside the supported fragment (partially overlapping tainted memory, unknown opcodes, shadow
// stack mismatch) marks the run lost: the transaction keeps its plain writes and ordinary MVCC.
//
// Enabled with FX_EPR_PROG=1 in the endorser (together with FX_EPR_EVM=1).

import (
	"fmt"
	"math/big"
	"os"
	"sort"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
	"github.com/hyperledger/fabric-x-sdk/blocks"
)

// EPRProgEnabled turns on program extraction for transactions that guarded deltas cannot express.
var EPRProgEnabled = os.Getenv("FX_EPR_PROG") == "1"

var eprProgTxs, eprProgLost, eprProgUsed atomic.Int64

// EPRProgStats reports program-traced transactions, those where tracking was lost, and programs emitted.
func EPRProgStats() (txs, lost, used int64) {
	return eprProgTxs.Load(), eprProgLost.Load(), eprProgUsed.Load()
}

type eprPNode struct {
	op      byte
	a, b, c *eprPNode
	key     string
	val     *big.Int // concrete endorsement-time value
	idx     int32
	gas     bool // remaining gas, plus or minus constants: it may only set a call's gas
}

type eprPGuard struct {
	n    *eprPNode
	kind byte
	want *big.Int
}

type eprPMem map[uint64]*eprPNode // tainted 32-byte words by exact offset

type eprPFrame struct {
	stack    []*eprPNode
	mem      eprPMem
	poison   map[uint64]bool // bytes of partially overwritten tainted words (reading them is lost)
	calldata eprPMem
	retdata  eprPMem // return data of the last completed sub-call
	pend     struct {
		active bool
		base   int
		out    *eprPNode
		sload  string
	}
	// sub-call bookkeeping
	callRetOff, callRetLen uint64
	applyRet               bool
	retLabels              eprPMem // labels of this frame's RETURN/REVERT region
	journalMark            int
	tjournalMark           int
}

type eprPJournal struct {
	slot     string
	prev     *eprPNode
	prevVal  *big.Int
	prevWrit bool
}

// eprPTJournal is a transient-storage write, undone like a storage write when its frame reverts.
type eprPTJournal struct {
	key  string
	prev *eprPNode
	had  bool
}

// EPRProgTracer builds a guarded straight-line program for one transaction. Not safe for concurrent use.
type EPRProgTracer struct {
	frames    []*eprPFrame // by OnOpcode depth (1 = top level)
	last      int
	vars      map[string]*eprPNode
	cur       map[string]*eprPNode // expression of the current value of written slots (nil: constant)
	curVal    map[string]*big.Int  // concrete current value of written slots
	written   map[string]bool
	journal   []eprPJournal
	transient map[string]*eprPNode
	tjournal  []eprPTJournal
	guards    []eprPGuard
	nextCall  eprPMem // calldata labels for the frame about to be entered
	keys      map[[52]byte]string
	lost      bool
	lostAt    string // diagnostics: opcode and reason where tracking was lost
	gasDep    bool   // remaining gas may differ on another base (SSTORE, EXP with a tainted exponent, tainted call gas)
	GasLeft   uint64 // kept for callers; the headroom decision is gasRule (epr_gas.go)
	gasRule   eprGasRule
}

// NewEPRProgTracer returns a fresh program tracer for one transaction.
func NewEPRProgTracer() *EPRProgTracer {
	return &EPRProgTracer{vars: map[string]*eprPNode{}, cur: map[string]*eprPNode{}, curVal: map[string]*big.Int{},
		written: map[string]bool{}, transient: map[string]*eprPNode{}}
}

func (t *EPRProgTracer) frame(d int) *eprPFrame {
	for len(t.frames) <= d {
		t.frames = append(t.frames, nil)
	}
	if t.frames[d] == nil {
		t.frames[d] = &eprPFrame{mem: eprPMem{}, calldata: eprPMem{}, retdata: eprPMem{}}
	}
	return t.frames[d]
}

// OnEnter: a new frame at OnOpcode depth d+1 starts with the call-data labels prepared by the caller.
func (t *EPRProgTracer) onEnter(depth int, typ byte, from, to common.Address, input []byte, gas uint64, value *big.Int) {
	if t.lost {
		return
	}
	f := &eprPFrame{mem: eprPMem{}, calldata: t.nextCall, retdata: eprPMem{}, journalMark: len(t.journal),
		tjournalMark: len(t.tjournal)}
	if f.calldata == nil {
		f.calldata = eprPMem{}
	}
	t.nextCall = nil
	for len(t.frames) <= depth+1 {
		t.frames = append(t.frames, nil)
	}
	t.frames[depth+1] = f
}

// OnExit: the frame at OnOpcode depth d+1 ends; a reverted frame's storage and transient-storage writes are
// rolled back and its RETURN/REVERT labels become the caller's return data (applied at the caller's next
// opcode).
func (t *EPRProgTracer) onExit(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
	t.gasRule.exit(depth, err) // A frame that halts for lack of gas breaks the gas rule
	if t.lost || depth+1 >= len(t.frames) || t.frames[depth+1] == nil {
		return
	}
	f := t.frames[depth+1]
	if reverted {
		for i := len(t.journal) - 1; i >= f.journalMark; i-- {
			j := t.journal[i]
			if j.prevWrit {
				t.cur[j.slot], t.curVal[j.slot] = j.prev, j.prevVal
			} else {
				delete(t.cur, j.slot)
				delete(t.curVal, j.slot)
			}
			t.written[j.slot] = j.prevWrit
		}
		t.journal = t.journal[:f.journalMark]
		for i := len(t.tjournal) - 1; i >= f.tjournalMark; i-- {
			j := t.tjournal[i]
			if j.had {
				t.transient[j.key] = j.prev
			} else {
				delete(t.transient, j.key)
			}
		}
		t.tjournal = t.tjournal[:f.tjournalMark]
	}
	ret := f.retLabels
	if ret == nil {
		ret = eprPMem{}
	}
	t.frames[depth+1] = nil
	if depth >= 1 && depth < len(t.frames) && t.frames[depth] != nil {
		p := t.frames[depth]
		p.retdata = ret
		p.applyRet = true
	}
}

// Hooks returns the tracing hooks of the program tracer.
func (t *EPRProgTracer) Hooks() *tracing.Hooks {
	return &tracing.Hooks{OnOpcode: t.onOpcode, OnEnter: t.onEnter, OnExit: t.onExit}
}

func (t *EPRProgTracer) node(op byte, a, b, c *eprPNode, val *big.Int) *eprPNode {
	for _, x := range []*eprPNode{a, b, c} {
		if x != nil && x.gas { // never reached (markers cannot enter memory or operator nodes); kept as a check
			t.lose(op, 0, 0, 94)
		}
	}
	return &eprPNode{op: op, a: a, b: b, c: c, val: val, idx: -1}
}

func (t *EPRProgTracer) constNode(v *big.Int) *eprPNode {
	return &eprPNode{op: EPROpConst, val: new(big.Int).Set(v), idx: -1}
}

// guardEq records that a tainted value must equal its endorsed value.
func (t *EPRProgTracer) guardEq(n *eprPNode) {
	if n != nil && n.gas {
		t.lose(0x5a, 0, 0, 91) // remaining gas may differ on another base: it may not steer execution
		return
	}
	if n != nil {
		t.guards = append(t.guards, eprPGuard{n: n, kind: 0, want: n.val})
	}
}

func (t *EPRProgTracer) slotKey(addr common.Address, slot *uint256.Int) string {
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

// region helpers: tainted words fully inside [off, off+n); partial overlap with a tainted word is lost.
func (t *EPRProgTracer) regionWords(m eprPMem, off, n uint64) (map[uint64]*eprPNode, bool) {
	if len(m) == 0 || n == 0 {
		return nil, true
	}
	out := map[uint64]*eprPNode{}
	for k, v := range m {
		if k+32 <= off || k >= off+n {
			continue
		}
		if k < off || k+32 > off+n {
			return nil, false
		}
		out[k-off] = v
	}
	return out, true
}

// clearRegion removes tainted words inside [off, off+n); a partial overlap is lost.
func (t *EPRProgTracer) clearRegion(m eprPMem, off, n uint64) bool {
	if len(m) == 0 || n == 0 {
		return true
	}
	for k := range m {
		if k+32 <= off || k >= off+n {
			continue
		}
		if k < off || k+32 > off+n {
			return false
		}
		delete(m, k)
	}
	return true
}

// clearMem prepares [off, off+n) of a frame's memory for a write: tainted words inside are dropped; a
// tainted word only partially overwritten is dropped and its remaining bytes are poisoned (Solidity reuses
// memory with shifted ABI layouts; the old bytes are normally never read again). Poison inside the written
// range is cleared.
func (t *EPRProgTracer) clearMem(f *eprPFrame, off, n uint64) {
	if n == 0 {
		return
	}
	for k := range f.mem {
		if k+32 <= off || k >= off+n {
			continue
		}
		if k < off || k+32 > off+n {
			if f.poison == nil {
				f.poison = map[uint64]bool{}
			}
			for b := k; b < k+32; b++ {
				if b < off || b >= off+n {
					f.poison[b] = true
				}
			}
		}
		delete(f.mem, k)
	}
	for b := range f.poison {
		if b >= off && b < off+n {
			delete(f.poison, b)
		}
	}
}

// readMem returns the tainted words fully inside [off, off+n); a partial tainted word or a poisoned byte
// in the range cannot be represented (lost).
func (t *EPRProgTracer) readMem(f *eprPFrame, off, n uint64) (map[uint64]*eprPNode, bool) {
	if t.poisoned(f, off, n) {
		return nil, false
	}
	return t.regionWords(f.mem, off, n)
}

// callInput returns the tainted words of a call input [off, off+n) relative to off, when the only obstacle to
// readMem is a tainted word that straddles the end of the input: the callee's call data then holds the word's
// leading bytes inside the input followed by zeros (the EVM pads call data), which is the word AND a constant
// mask keeping those bytes. A word straddling the start of the input, or poisoned bytes, still cannot be
// represented (false).
func (t *EPRProgTracer) callInput(f *eprPFrame, off, n uint64) (map[uint64]*eprPNode, bool) {
	if t.poisoned(f, off, n) {
		return nil, false
	}
	out := map[uint64]*eprPNode{}
	for _, k := range memKeys(f.mem) {
		v := f.mem[k]
		if k+32 <= off || k >= off+n {
			continue
		}
		if k < off {
			return nil, false
		}
		if k+32 <= off+n {
			out[k-off] = v
			continue
		}
		keep := off + n - k // bytes of the word inside the input, 1..31
		low := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(8*(32-keep))), big.NewInt(1))
		mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
		mask.Xor(mask, low)
		mv, _ := EPREvalOp(0x16, v.val, mask, nil)
		out[k-off] = t.node(0x16, v, t.constNode(mask), nil, mv)
	}
	return out, true
}

func (t *EPRProgTracer) poisoned(f *eprPFrame, off, n uint64) bool {
	for b := range f.poison {
		if b >= off && b < off+n {
			return true
		}
	}
	return false
}

// composeWord returns the expression of the 32-byte word loaded at off from a shadowed byte area (memory
// or call data) whose tainted words only partially overlap the window (nil if none overlaps): the
// untainted bytes are a constant taken from the concrete area, each overlapping tainted word contributes
// its bytes shifted into place, and the parts are OR-ed. The concrete-value check at the next opcode
// cross-checks the composition against the EVM.
func (t *EPRProgTracer) composeWord(m eprPMem, off uint64, area []byte) *eprPNode {
	var window [32]byte
	for i := uint64(0); i < 32; i++ {
		if off+i < uint64(len(area)) {
			window[i] = area[off+i]
		}
	}
	var acc *eprPNode
	constPart := new(big.Int)
	for _, k := range memKeys(m) {
		v := m[k]
		if k+32 <= off || k >= off+32 {
			continue
		}
		var part *eprPNode
		if k >= off { // tainted word starts inside the window: its top bytes fill [k-off, 32)
			d := k - off
			for i := d; i < 32; i++ {
				window[i] = 0
			}
			sh := big.NewInt(int64(8 * d))
			pv, _ := EPREvalOp(0x1c, sh, v.val, nil)
			part = t.node(0x1c, t.constNode(sh), v, nil, pv)
		} else { // tainted word starts before the window: its low bytes fill [0, 32-(off-k))
			d := off - k
			for i := uint64(0); i < 32-d; i++ {
				window[i] = 0
			}
			sh := big.NewInt(int64(8 * d))
			pv, _ := EPREvalOp(0x1b, sh, v.val, nil)
			part = t.node(0x1b, t.constNode(sh), v, nil, pv)
		}
		if acc == nil {
			acc = part
		} else {
			av, _ := EPREvalOp(0x17, acc.val, part.val, nil)
			acc = t.node(0x17, acc, part, nil, av)
		}
	}
	if acc == nil {
		return nil
	}
	constPart.SetBytes(window[:])
	if constPart.Sign() == 0 {
		return acc
	}
	v, _ := EPREvalOp(0x17, acc.val, constPart, nil)
	return t.node(0x17, acc, t.constNode(constPart), nil, v)
}

func (t *EPRProgTracer) copyInto(dst eprPMem, dstOff uint64, words map[uint64]*eprPNode) {
	for rel, v := range words {
		dst[dstOff+rel] = v
	}
}

// eprIO reused from the delta tracer: [inputs, outputs] per opcode.

func (t *EPRProgTracer) onOpcode(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
	t.gasRule.step(pc, op, gas, cost, depth, true, err != nil)
	if t.lost {
		return
	}
	stack := scope.StackData()
	n := len(stack)
	f := t.frame(depth)
	if depth < t.last {
		for x := depth + 1; x <= t.last && x < len(t.frames); x++ {
			t.frames[x] = nil
		}
	}
	t.last = depth
	S := f.stack
	if p := &f.pend; p.active {
		pushes := n - p.base
		if pushes < 0 {
			t.lose(op, pc, depth, 1)
			return
		}
		for i := 0; i < pushes; i++ {
			var l *eprPNode
			if i == 0 {
				l = p.out
			}
			S = append(S, l)
		}
		if p.sload != "" && n > 0 {
			v := stack[n-1].ToBig()
			if vn := t.vars[p.sload]; vn != nil && vn.val == nil {
				vn.val = v
			}
		}
		if p.out != nil && p.out.gas && p.out.val == nil && n > 0 { // GAS: its value is the pushed word
			p.out.val = stack[n-1].ToBig()
		}
		if p.out != nil && p.out.op != EPROpVar && n > 0 && p.out.val.Cmp(stack[n-1].ToBig()) != 0 {
			t.lose(op, pc, depth, 2) // evaluator disagrees with the EVM: never emit a program from this run
			return
		}
		p.active, p.out, p.sload = false, nil, ""
	}
	if len(S) != n {
		t.lose(op, pc, depth, 3)
		return
	}
	if f.applyRet { // a sub-call just returned: its return-data labels land in [retOff, retOff+retLen)
		f.applyRet = false
		t.clearMem(f, f.callRetOff, f.callRetLen)
		words, ok := t.regionWords(f.retdata, 0, f.callRetLen)
		if !ok {
			t.lose(op, pc, depth, 5)
			return
		}
		t.copyInto(f.mem, f.callRetOff, words)
	}
	f.stack = S
	top := func(i int) *uint256.Int { return &stack[n-1-i] }
	u64 := func(i int) uint64 {
		v := top(i)
		if !v.IsUint64() {
			return 1 << 62
		}
		return v.Uint64()
	}
	setPend := func(out *eprPNode, sload string) {
		f.pend.active, f.pend.base, f.pend.out, f.pend.sload = true, len(f.stack), out, sload
	}
	gasOut := func(l *eprPNode) bool { // a gas-derived value stored or branched on: never emit a program
		if l != nil && l.gas {
			t.lose(op, pc, depth, 91)
			return true
		}
		return false
	}

	switch {
	case op >= 0x80 && op <= 0x8f:
		k := int(op-0x80) + 1
		f.pend.active, f.pend.base, f.pend.out = true, n, S[n-k]
		return
	case op >= 0x90 && op <= 0x9f:
		k := int(op-0x90) + 1
		S[n-1], S[n-1-k] = S[n-1-k], S[n-1]
		f.pend.active, f.pend.base, f.pend.out = true, n, nil
		return
	case op >= 0x60 && op <= 0x7f:
		f.pend.active, f.pend.base, f.pend.out = true, n, nil
		return
	}
	if !eprIOKnown[op] || eprIOArr[op][0] > n {
		t.lose(op, pc, depth, 6)
		return
	}
	io := eprIOArr[op]
	var insBuf [7]*eprPNode
	ins := insBuf[:io[0]]
	tainted := false
	for j := 0; j < io[0]; j++ {
		ins[j] = S[n-1-j]
		tainted = tainted || ins[j] != nil
	}
	f.stack = S[:n-io[0]]
	addr := scope.Address()

	switch op {
	case 0x54: // SLOAD
		t.guardEq(ins[0])
		s := t.slotKey(addr, top(0))
		if t.written[s] {
			out := t.cur[s]
			if out == nil {
				out = nil // constant current value: untainted
			}
			setPend(out, "")
			return
		}
		v := t.vars[s]
		if v == nil {
			v = &eprPNode{op: EPROpVar, key: s, idx: -1}
			t.vars[s] = v
			setPend(v, s)
			return
		}
		setPend(v, "")
		return
	case 0x55: // SSTORE
		t.guardEq(ins[0])
		if gasOut(ins[1]) {
			return
		}
		t.gasDep = true // the cost depends on the slot's original and current values
		s := t.slotKey(addr, top(0))
		t.journal = append(t.journal, eprPJournal{slot: s, prev: t.cur[s], prevVal: t.curVal[s], prevWrit: t.written[s]})
		t.cur[s] = ins[1]
		t.curVal[s] = top(1).ToBig()
		t.written[s] = true
		setPend(nil, "")
		return
	case 0x5c: // TLOAD
		t.guardEq(ins[0])
		setPend(t.transient[addr.Hex()+top(0).Hex()], "")
		return
	case 0x5d: // TSTORE
		t.guardEq(ins[0])
		if gasOut(ins[1]) {
			return
		}
		k := addr.Hex() + top(0).Hex()
		prev, had := t.transient[k]
		t.tjournal = append(t.tjournal, eprPTJournal{key: k, prev: prev, had: had})
		t.transient[k] = ins[1]
		setPend(nil, "")
		return
	case 0x56: // JUMP
		t.guardEq(ins[0])
		setPend(nil, "")
		return
	case 0x57: // JUMPI
		t.guardEq(ins[0])
		if gasOut(ins[1]) {
			return
		}
		if ins[1] != nil {
			t.guards = append(t.guards, eprPGuard{n: ins[1], kind: 1, want: ins[1].val})
		}
		setPend(nil, "")
		return
	case 0x51: // MLOAD
		t.guardEq(ins[0])
		off := u64(0)
		if v, ok := f.mem[off]; ok {
			setPend(v, "")
			return
		}
		if t.poisoned(f, off, 32) {
			t.lose(op, pc, depth, 7)
			return
		}
		setPend(t.composeWord(f.mem, off, scope.MemoryData()), "")
		return
	case 0x52: // MSTORE
		t.guardEq(ins[0])
		if gasOut(ins[1]) {
			return
		}
		off := u64(0)
		t.clearMem(f, off, 32)
		if ins[1] != nil {
			f.mem[off] = ins[1]
		} else {
			delete(f.mem, off)
		}
		setPend(nil, "")
		return
	case 0x53: // MSTORE8
		t.guardEq(ins[0])
		if ins[1] != nil {
			t.lose(op, pc, depth, 9)
			return
		}
		t.clearMem(f, u64(0), 1)
		setPend(nil, "")
		return
	case 0x35: // CALLDATALOAD
		t.guardEq(ins[0])
		off := u64(0)
		if v, ok := f.calldata[off]; ok {
			setPend(v, "")
			return
		}
		setPend(t.composeWord(f.calldata, off, scope.CallInput()), "")
		return
	case 0x37, 0x3e, 0x39, 0x5e: // CALLDATACOPY, RETURNDATACOPY, CODECOPY, MCOPY (dst, src, len)
		for _, l := range ins {
			t.guardEq(l)
		}
		dst, src, ln := u64(0), u64(1), u64(2)
		var srcMem eprPMem
		switch op {
		case 0x37:
			srcMem = f.calldata
		case 0x3e:
			srcMem = f.retdata
		case 0x5e:
			srcMem = f.mem
		}
		var words map[uint64]*eprPNode
		ok := true
		if op == 0x5e {
			words, ok = t.readMem(f, src, ln)
		} else if srcMem != nil {
			words, ok = t.regionWords(srcMem, src, ln)
		}
		if ok {
			t.clearMem(f, dst, ln)
		}
		if !ok {
			t.lose(op, pc, depth, 12)
			return
		}
		t.copyInto(f.mem, dst, words)
		setPend(nil, "")
		return
	case 0x3c: // EXTCODECOPY(addr, dst, src, len)
		for _, l := range ins {
			t.guardEq(l)
		}
		t.clearMem(f, u64(1), u64(3))
		setPend(nil, "")
		return
	case 0x20: // KECCAK256(off, len): the hash is unchanged if its tainted input words are
		t.guardEq(ins[0])
		t.guardEq(ins[1])
		off, ln := u64(0), u64(1)
		if t.poisoned(f, off, ln) {
			t.lose(op, pc, depth, 90)
			return
		}
		for _, k := range memKeys(f.mem) { // sorted: every endorser appends the guards in the same order
			if k+32 > off && k < off+ln {
				t.guardEq(f.mem[k])
			}
		}
		setPend(nil, "")
		return
	case 0xa0, 0xa1, 0xa2, 0xa3, 0xa4: // LOG: offsets guarded; data/topics may stay tainted (see header)
		t.guardEq(ins[0])
		t.guardEq(ins[1])
		setPend(nil, "")
		return
	case 0xf3, 0xfd: // RETURN, REVERT
		t.guardEq(ins[0])
		t.guardEq(ins[1])
		words, ok := t.readMem(f, u64(0), u64(1))
		if !ok {
			t.lose(op, pc, depth, 14)
			return
		}
		f.retLabels = eprPMem(words)
		setPend(nil, "")
		return
	case 0xf1, 0xf2, 0xf4, 0xfa: // CALL, CALLCODE, DELEGATECALL, STATICCALL
		// operands: gas, addr, [value], argsOff, argsLen, retOff, retLen. A gas operand computed from storage
		// reads is guarded: the gas rule of epr_gas.go bounds how much a callee's allowance can shrink
		// only if the operand is a constant, the GAS marker or fixed. From here on GAS is a marker, as after an
		// SSTORE (a marker operand implies gasDep already).
		if ins[0] != nil {
			if !ins[0].gas {
				t.guardEq(ins[0])
			}
			t.gasDep = true
		}
		t.guardEq(ins[1])
		i := 2
		if op == 0xf1 || op == 0xf2 {
			t.guardEq(ins[2])
			i = 3
		}
		for _, l := range ins[i:] {
			t.guardEq(l)
		}
		argsOff, argsLen, retOff, retLen := u64(i), u64(i+1), u64(i+2), u64(i+3)
		words, ok := t.readMem(f, argsOff, argsLen)
		if !ok { // a tainted word may straddle the end of the input (ABI encodings ending inside a word)
			words, ok = t.callInput(f, argsOff, argsLen)
		}
		if !ok {
			t.lose(op, pc, depth, 15)
			return
		}
		to := common.Address(top(1).Bytes20())
		if eprIsPrecompile(to) {
			for _, k := range memKeys(words) {
				t.guardEq(words[k]) // the precompile output is unchanged if its tainted inputs are
			}
			t.nextCall = eprPMem{}
		} else {
			t.nextCall = eprPMem(words)
		}
		f.callRetOff, f.callRetLen = retOff, retLen
		setPend(nil, "")
		return
	case 0xf0, 0xf5: // CREATE, CREATE2: guard everything feeding the init code
		for _, l := range ins {
			t.guardEq(l)
		}
		off, ln := u64(1), u64(2)
		if t.poisoned(f, off, ln) { // init code bytes left over from a partially overwritten word (as MLOAD)
			t.lose(op, pc, depth, 95)
			return
		}
		for _, k := range memKeys(f.mem) { // sorted: every endorser appends the guards in the same order
			if k+32 > off && k < off+ln {
				t.guardEq(f.mem[k])
			}
		}
		t.nextCall = eprPMem{}
		f.callRetOff, f.callRetLen = 0, 0
		setPend(nil, "")
		return
	case 0x31, 0x3b, 0x3f, 0x40, 0xff: // BALANCE, EXTCODESIZE, EXTCODEHASH, BLOCKHASH, SELFDESTRUCT
		for _, l := range ins {
			t.guardEq(l)
		}
		setPend(nil, "")
		return
	case 0x50: // POP
		setPend(nil, "")
		return
	case 0x5a: // GAS: after a value-dependent cost the remaining gas is a marker, allowed only as call gas
		if t.gasDep {
			setPend(&eprPNode{op: EPROpConst, idx: -1, gas: true}, "")
			return
		}
		setPend(nil, "")
		return
	}
	if !tainted || io[1] == 0 {
		for _, l := range ins { // e.g. tainted input of an opcode without outputs: keep it observable
			t.guardEq(l)
		}
		setPend(nil, "")
		return
	}
	if !EPRPureOp(op) {
		for _, l := range ins {
			t.guardEq(l)
		}
		setPend(nil, "")
		return
	}
	if op == 0x0a && ins[1] != nil { // EXP: the cost depends on the exponent's byte length
		t.gasDep = true
	}
	gasIn, otherIn := false, false
	for _, l := range ins {
		gasIn = gasIn || (l != nil && l.gas)
		otherIn = otherIn || (l != nil && !l.gas)
	}
	if gasIn { // a marker combined with storage values is lost
		if otherIn {
			t.lose(op, pc, depth, 92)
			return
		}
		// a marker stays a marker only as the remaining gas plus or
		// minus a constant, without wrapping around on the endorsed run. On another base such a call gas
		// operand is lower by at most the extra cost paid so far, or it wraps around to more than any
		// allowance; either way the callee's allowance shrinks by at most that extra cost (epr_gas.go). Any
		// other operator (AND, MUL, DIV, a second marker, a constant minus the marker, ...) can turn a small
		// difference into a large one, so the program is lost.
		ok := false
		switch op {
		case 0x01: // ADD: the marker plus a constant below 2^64 (the sum never wraps around), or minus one, as
			// compilers encode a subtraction (adding 2^256 - k), with k not above the marker
			if (ins[0] == nil) != (ins[1] == nil) {
				m, c := top(0), top(1)
				if ins[0] == nil {
					m, c = top(1), top(0)
				}
				k := new(uint256.Int).Neg(c)
				ok = c.BitLen() <= 64 || (k.BitLen() <= 64 && !m.Lt(k))
			}
		case 0x03: // SUB: the marker minus a constant that does not exceed it
			ok = ins[0] != nil && ins[1] == nil && !top(0).Lt(top(1))
		}
		if !ok {
			t.lose(op, pc, depth, 93)
			return
		}
		v, _ := EPREvalOp(op, top(0).ToBig(), top(1).ToBig(), new(big.Int))
		setPend(&eprPNode{op: EPROpConst, val: v, idx: -1, gas: true}, "")
		return
	}
	// pure operator with a tainted operand: build a node, its concrete value replays the EVM
	var args [3]*eprPNode
	var vals [3]*big.Int
	for j := 0; j < 3; j++ {
		vals[j] = new(big.Int)
		if j < len(ins) {
			vals[j] = top(j).ToBig()
			if ins[j] != nil {
				args[j] = ins[j]
			} else {
				args[j] = t.constNode(vals[j])
			}
		}
	}
	v, _ := EPREvalOp(op, vals[0], vals[1], vals[2])
	setPend(t.node(op, args[0], args[1], args[2], v), "")
}

// lose marks the run lost and records where (diagnostics).
func (t *EPRProgTracer) lose(op byte, pc uint64, depth, site int) {
	if !t.lost {
		t.lost = true
		t.lostAt = fmt.Sprintf("op=%#x pc=%d depth=%d site=%d", op, pc, depth, site)
	}
}

func eprIsPrecompile(a common.Address) bool {
	for _, b := range a[:19] {
		if b != 0 {
			return false
		}
	}
	return a[19] >= 1 && a[19] <= 0x11
}

// Program builds the guarded program whose outputs are the transaction's storage writes, or nil if the
// run was lost or a write cannot be attributed.
func (t *EPRProgTracer) Program(writes map[string]bool) *EPRProgram {
	if t.lost {
		return nil
	}
	for s := range writes {
		if !t.written[s] {
			return nil
		}
	}
	p := &EPRProgram{}
	keyIdx := map[string]int{}
	key := func(s string) int32 {
		if i, ok := keyIdx[s]; ok {
			return int32(i) //nolint:gosec
		}
		keyIdx[s] = len(p.Keys)
		p.Keys = append(p.Keys, s)
		return int32(len(p.Keys) - 1) //nolint:gosec
	}
	var emit func(n *eprPNode) int32
	emit = func(n *eprPNode) int32 {
		if n == nil {
			return -1
		}
		if n.idx >= 0 {
			return n.idx
		}
		var pn EPRProgNode
		switch n.op {
		case EPROpVar:
			pn = EPRProgNode{Op: EPROpVar, A: key(n.key), B: -1, C: -1}
		case EPROpConst:
			pn = EPRProgNode{Op: EPROpConst, A: -1, B: -1, C: -1, Const: n.val.Bytes()}
		default:
			pn = EPRProgNode{Op: n.op, A: emit(n.a), B: emit(n.b), C: emit(n.c)}
		}
		n.idx = int32(len(p.Nodes)) //nolint:gosec
		p.Nodes = append(p.Nodes, pn)
		return n.idx
	}
	for _, g := range t.guards {
		w := g.want.Bytes()
		if g.kind == 1 {
			w = []byte{0}
			if g.want.Sign() != 0 {
				w = []byte{1}
			}
		}
		p.Guards = append(p.Guards, EPRProgGuard{Node: emit(g.n), Kind: g.kind, Value: w})
	}
	outs := make([]string, 0, len(writes))
	for s := range writes {
		outs = append(outs, s)
	}
	sort.Strings(outs)
	for _, s := range outs {
		n := t.cur[s]
		if n == nil {
			n = t.constNode(t.curVal[s])
		}
		p.Outputs = append(p.Outputs, EPRProgOut{Key: uint16(key(s)), Node: emit(n)}) //nolint:gosec
	}
	// every storage read is a program input; sorted so that all endorsers sign identical bytes
	rest := make([]string, 0, len(t.vars))
	for _, v := range t.vars {
		if v.val != nil {
			rest = append(rest, v.key)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		key(k)
	}
	return p
}

// RewriteProgram replaces every storage write of rws with the encoded program (the committer and the
// replicas evaluate it once per transaction). Storage keys are "str:..."; other keys are left as they are.
func (t *EPRProgTracer) RewriteProgram(rws blocks.ReadWriteSet) (blocks.ReadWriteSet, bool) {
	eprProgTxs.Add(1)
	if t.lost {
		eprProgLost.Add(1)
		return rws, false
	}
	writes := map[string]bool{}
	for _, w := range rws.Writes {
		if len(w.Key) > 4 && w.Key[:4] == "str:" {
			writes[w.Key] = true
		}
	}
	if len(writes) == 0 || t.gasRule.short {
		return rws, false
	}
	p := t.Program(writes)
	if p == nil || len(p.Nodes) > EPRProgMaxNodes {
		eprProgLost.Add(1)
		return rws, false
	}
	// endorsed output values (the plain write set): a committer may write them directly when none of
	// the program's keys changed since endorsement (uncontended fast path)
	orig := make(map[string][]byte, len(rws.Writes))
	for _, w := range rws.Writes {
		orig[w.Key] = w.Value
	}
	for _, o := range p.Outputs {
		p.Endorsed = append(p.Endorsed, orig[p.Keys[o.Key]])
	}
	enc := p.Encode()
	for i, w := range rws.Writes {
		if writes[w.Key] {
			rws.Writes[i] = blocks.KVWrite{Key: w.Key, Value: enc}
		}
	}
	eprProgUsed.Add(1)
	return rws, true
}

var _ = vm.NewGasBudget // keep the vm import for callers in this package

// EPRCombineHooks runs the delta tracer and the program tracer on the same execution.
func EPRCombineHooks(d *EPRTracer, p *EPRProgTracer) *tracing.Hooks {
	ph := p.Hooks()
	return &tracing.Hooks{
		OnOpcode: func(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
			d.onOpcode(pc, op, gas, cost, scope, rData, depth, err)
			ph.OnOpcode(pc, op, gas, cost, scope, rData, depth, err)
		},
		OnEnter: ph.OnEnter,
		OnExit: func(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
			d.onExitGas(depth, output, gasUsed, err, reverted)
			ph.OnExit(depth, output, gasUsed, err, reverted)
		},
	}
}

// EPRRewriteTx chooses the encoding of a transaction's storage writes: guarded deltas when every storage
// write is one (they commute and admit escrow), otherwise, if program extraction succeeded, one guarded
// program for the whole transaction; else writes that are guarded deltas keep that form and the others stay
// plain (ordinary MVCC).
func EPRRewriteTx(r blocks.ReadWriteSet, d *EPRTracer, p *EPRProgTracer) blocks.ReadWriteSet {
	if d == nil {
		return r
	}
	orig := append([]blocks.KVWrite(nil), r.Writes...)
	r = d.Rewrite(r)
	if p == nil || !EPRProgEnabled {
		return r
	}
	plain := false
	for _, w := range r.Writes {
		if len(w.Key) > 4 && w.Key[:4] == "str:" && !(len(w.Value) == 30 && string(w.Value[:5]) == string(EPRMagic2)) {
			plain = true
			break
		}
	}
	if !plain {
		return r
	}
	p.GasLeft = d.GasLeft
	r2 := r
	r2.Writes = orig
	if r3, ok := p.RewriteProgram(r2); ok {
		return r3
	}
	// no program: writes that are guarded deltas keep that form, the others stay plain (their reads and
	// every dependency of a delta are version-checked)
	r.Writes = orig
	return d.Rewrite(r)
}

// memKeys returns the offsets of a shadowed byte area in increasing order. Loops that create nodes or guards
// iterate in this order, so that every endorser derives the same program bytes (Go randomizes map order).
func memKeys(m eprPMem) []uint64 {
	ks := make([]uint64, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}
