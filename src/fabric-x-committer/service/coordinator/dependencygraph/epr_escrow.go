/*
Extension (not upstream): escrow-style debit commutativity in the dependency graph.

Guarded deltas "key += delta if key + delta >= lower" are classified in ledger order:
  - credits (lower = MinInt64) never fail and always commute (tracked as deltas);
  - a debit takes the FAST path (commutes, no dependency edges with other deltas on the key) only if
    a conservative lower bound of its serial-prefix balance covers it:
        LB = prefix + sum(finished valid effects after the prefix) + sum(unfinished debits before it)
    (unfinished credits are ignored). Then the debit succeeds in ledger-order serial execution and in
    any interleaving of the other admitted deltas, so its outcome is order-independent;
  - otherwise it takes the SLOW path: an ordinary read-write that waits for every earlier access to
    the key and is evaluated exactly by the VC.
Committers of different organizations may choose different paths (timing), but both paths yield the
serial outcome, so results stay deterministic. `prefix` is the balance after the longest finished
prefix of accesses to the key (fresh database: starts at 0). Enabled with FX_EPR3=1.
*/

package dependencygraph

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
)

var (
	eprEscrowOn = os.Getenv("FX_EPR3") == "1"

	eprEscrowMu    sync.Mutex
	eprEscrowCond  = sync.NewCond(&eprEscrowMu)
	eprLastBatchID uint64
	eprKeys        = map[string]*eprKeyState{}
	eprFastDebits  atomic.Int64
	eprSlowDebits  atomic.Int64
	eprCreditsSeen atomic.Int64
)

type eprItem struct {
	delta int64
	done  bool
	valid bool
	node  *TransactionNode
	// formula writes (linear formulas): the item sets the key to a value the coordinator computes
	// from the finished prefixes when the (ordered) formula transaction completes.
	isSet   bool
	f       *depFormula
	ns      string
	setTo   int64
	unknown bool
}

type eprKeyState struct {
	prefix  int64
	pending []*eprItem
	// unknown is set once a plain (non-delta) write to the key is classified: its value is no longer
	// derivable from deltas, so later debits on it always take the slow (exact, ordered) path.
	unknown bool
	// maxLower is the strictest lower bound seen among debits on the key; fast admission uses it so that
	// any interleaving of admitted debits keeps every admitted debit's own guard (Theorem 3, case 2).
	maxLower int64
	hasLower bool
}

type eprAccess struct {
	ck           string
	delta, lower int64
	f            *depFormula
	ns           string
}

// eprStorageKey reports whether k is a contract storage key: EVM signed content (EPRD2 deltas, EPRP1
// programs) is only recognized on such keys, matching the VC; any other key's value is a plain write.
func eprStorageKey(k []byte) bool { return bytes.HasPrefix(k, []byte("str:")) }

// eprGuardedDelta decodes the guarded-delta encoding of the value v written to key k.
func eprGuardedDelta(k, v []byte) (delta, lower int64, ok bool) {
	if len(v) == 30 && bytes.HasPrefix(v, []byte("EPRD2")) {
		if !eprStorageKey(k) {
			return 0, 0, false
		}
		// EVM interval-guarded delta: only lower-bounded ones are escrow-classified; an EPRD2 credit
		// (lower <= delta, never failing on a non-negative word) is reported as an unconditional credit,
		// matching eprCommutes and the VC's eprIsCredit2.
		d := int64(binary.BigEndian.Uint64(v[5:13]))  //nolint:gosec
		l := int64(binary.BigEndian.Uint64(v[13:21])) //nolint:gosec
		u := int64(binary.BigEndian.Uint64(v[21:29])) //nolint:gosec
		if u != math.MaxInt64 {
			return 0, 0, false
		}
		if d >= 0 && l <= d {
			return d, math.MinInt64, true
		}
		return d, l, true
	}
	if len(v) != 21 || !bytes.HasPrefix(v, []byte("EPRD1")) {
		return 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(v[5:13])), int64(binary.BigEndian.Uint64(v[13:])), true //nolint:gosec
}

// eprIsEscrowDebit reports whether a read-write of key k with value v is a guarded debit handled by the
// escrow classifier.
func eprIsEscrowDebit(k, v []byte) bool {
	if !eprEscrowOn {
		return false
	}
	_, l, ok := eprGuardedDelta(k, v)
	return ok && l != math.MinInt64
}

func eprParse(nss []*applicationpb.TxNamespace) []eprAccess {
	var out []eprAccess
	for _, ns := range nss {
		for _, rw := range ns.ReadWrites {
			if d, l, ok := eprGuardedDelta(rw.Key, rw.Value); ok {
				out = append(out, eprAccess{ck: constructCompositeKey(ns.NsId, rw.Key), delta: d, lower: l})
			} else if f, okf := depDecodeFormula(rw.Value); okf && eprEscrowOn {
				out = append(out, eprAccess{ck: constructCompositeKey(ns.NsId, rw.Key), f: f, ns: ns.NsId})
			}
		}
	}
	return out
}

// eprClassifyBatch classifies the guarded deltas of one batch in ledger order (batch IDs are
// consecutive starting at 1) and completes the nodes' key sets accordingly.
func eprClassifyBatch(id uint64, nodes TxNodeBatch, aborted func() bool) {
	if !eprEscrowOn {
		return
	}
	eprEscrowMu.Lock()
	defer eprEscrowMu.Unlock()
	for eprLastBatchID != id-1 && !aborted() {
		eprEscrowCond.Wait()
	}
	for _, n := range nodes {
		accs := eprParse(n.VCTx.Namespaces)
		formulaKeys := map[string]bool{}
		for _, a := range accs {
			if a.f != nil {
				formulaKeys[a.ck] = true
			}
		}
		// A plain write whose value is an integer (8-byte int64, a 32-byte EVM word that fits int64, or an
		// empty/deleted value = 0) is a constant formula: an ordered set item that re-establishes exact
		// knowledge of the key once it finishes valid. Any other plain write poisons the key's escrow
		// knowledge (safe fallback to the slow path).
		plainVals := eprPlainWriteValues(n.VCTx.Namespaces)
		for _, keys := range [][]string{n.rwKeys.readsAndWrites, n.rwKeys.writesOnly} {
			for _, ck := range keys {
				if formulaKeys[ck] {
					continue
				}
				st := eprKeys[ck]
				if st == nil {
					st = &eprKeyState{}
					eprKeys[ck] = st
				}
				if v, ok := plainVals[ck]; ok {
					it := &eprItem{isSet: true, f: &depFormula{c: v}, node: n}
					st.pending = append(st.pending, it)
					n.eprItems = append(n.eprItems, eprItemRef{ck: ck, item: it})
					continue
				}
				// any other write (a program output or a non-integer value) is an ordered barrier with an
				// unknown result: an earlier set item finishing cannot re-establish knowledge past it
				it := &eprItem{isSet: true, unknown: true, node: n}
				st.pending = append(st.pending, it)
				n.eprItems = append(n.eprItems, eprItemRef{ck: ck, item: it})
				st.unknown = true
			}
		}
		for _, a := range accs {
			st := eprKeys[a.ck]
			if st == nil {
				st = &eprKeyState{}
				eprKeys[a.ck] = st
			}
			if a.f != nil { // ordered formula write: a barrier until it finishes
				it := &eprItem{isSet: true, f: a.f, ns: a.ns, node: n}
				st.pending = append(st.pending, it)
				n.eprItems = append(n.eprItems, eprItemRef{ck: a.ck, item: it})
				continue
			}
			it := &eprItem{delta: a.delta, node: n}
			if a.lower == math.MinInt64 {
				eprCreditsSeen.Add(1)
				st.pending = append(st.pending, it)
				n.eprItems = append(n.eprItems, eprItemRef{ck: a.ck, item: it})
				continue // credits are placed in rwKeys.deltas by readAndWriteKeys (commutative credits)
			}
			lb := st.prefix
			barrier := false
			for _, p := range st.pending {
				if p.isSet { // value unknown until the formula transaction finishes
					barrier = true
					break
				}
				switch {
				case p.done && p.valid:
					lb += p.delta
				case !p.done && p.delta < 0:
					lb += p.delta
				}
			}
			st.pending = append(st.pending, it)
			n.eprItems = append(n.eprItems, eprItemRef{ck: a.ck, item: it})
			if !st.hasLower || a.lower > st.maxLower {
				st.maxLower, st.hasLower = a.lower, true
			}
			bound := st.maxLower
			if !barrier && !st.unknown && lb+a.delta >= bound && !eprAddOverflows(lb, a.delta) {
				eprFastDebits.Add(1)
				n.rwKeys.deltas = append(n.rwKeys.deltas, a.ck)
			} else {
				eprSlowDebits.Add(1)
				n.rwKeys.readsAndWrites = append(n.rwKeys.readsAndWrites, a.ck)
			}
		}
	}
	eprLastBatchID = id
	eprEscrowCond.Broadcast()
}

// eprFinish records validation outcomes and advances each key's finished prefix.
func eprFinish(nodes TxNodeBatch) {
	if !eprEscrowOn {
		return
	}
	eprEscrowMu.Lock()
	defer eprEscrowMu.Unlock()
	touched := map[string]bool{}
	for _, n := range nodes {
		if n.eprValid.Load() {
			// all earlier accesses to the formula's keys have finished (ordered), so the prefixes are the
			// exact pre-transaction values: evaluate every formula of the tx before absorbing any.
			for _, r := range n.eprItems {
				if !r.item.isSet || r.item.f == nil { // a barrier keeps its unknown result
					continue
				}
				v, known := r.item.f.evalOn(func(raw string) (int64, bool) {
					st := eprKeys[constructCompositeKey(r.item.ns, []byte(raw))]
					if st == nil {
						return 0, true // never touched in this run: fresh key
					}
					return st.prefix, !st.unknown
				})
				r.item.setTo, r.item.unknown = v, !known
			}
		}
		for _, r := range n.eprItems {
			r.item.done = true
			r.item.valid = n.eprValid.Load()
			r.item.node = nil
			touched[r.ck] = true
		}
	}
	for ck := range touched {
		st := eprKeys[ck]
		i := 0
		for ; i < len(st.pending) && st.pending[i].done; i++ {
			it := st.pending[i]
			if !it.valid {
				continue
			}
			if it.isSet {
				st.prefix = it.setTo
				st.unknown = it.unknown // an exactly computed formula result re-establishes knowledge
				continue
			}
			st.prefix += it.delta
		}
		st.pending = st.pending[i:]
	}
}

// eprPlainWriteValues returns, per composite key, the integer value of each plain (non-EPR) write of
// the transaction that can be parsed exactly; keys with other values are absent from the map.
func eprPlainWriteValues(nss []*applicationpb.TxNamespace) map[string]int64 {
	out := map[string]int64{}
	parse := func(v []byte) (int64, bool) {
		switch len(v) {
		case 0:
			return 0, true
		case 8:
			if bytes.HasPrefix(v, []byte("EPR")) {
				return 0, false
			}
			return int64(binary.BigEndian.Uint64(v)), true //nolint:gosec
		case 32:
			for _, c := range v[:24] {
				if c != 0 {
					return 0, false
				}
			}
			if v[24]&0x80 != 0 {
				return 0, false
			}
			return int64(binary.BigEndian.Uint64(v[24:])), true //nolint:gosec
		}
		return 0, false
	}
	for _, ns := range nss {
		for _, rw := range ns.ReadWrites {
			if x, ok := parse(rw.Value); ok {
				out[constructCompositeKey(ns.NsId, rw.Key)] = x
			}
		}
		for _, bw := range ns.BlindWrites {
			if x, ok := parse(bw.Value); ok {
				out[constructCompositeKey(ns.NsId, bw.Key)] = x
			}
		}
	}
	return out
}

// EPRSetValid is called by the coordinator when the VC returns the transaction's status.
func (n *TransactionNode) EPRSetValid(valid bool) { n.eprValid.Store(valid) }

// EPREscrowStats reports fast/slow debit counts and credits seen.
func EPREscrowStats() (fast, slow, credits int64) {
	return eprFastDebits.Load(), eprSlowDebits.Load(), eprCreditsSeen.Load()
}

type eprItemRef struct {
	ck   string
	item *eprItem
}

func init() {
	if !eprEscrowOn {
		return
	}
	go func() {
		for range time.Tick(5 * time.Second) {
			f, s, c := EPREscrowStats()
			logger.Infof("PILOT_ESCROW fast_debits=%d slow_debits=%d credits=%d", f, s, c)
		}
	}()
}

// ---- formula decoding (same encoding as the VC) ----
type depTerm struct {
	key  string
	coef int64
}

type depFormula struct {
	terms  []depTerm
	c      int64
	guard  []depTerm
	gc     int64
	hasG   bool
	expect byte
	pf     int64
}

func depReadTerms(b []byte) ([]depTerm, []byte, bool) {
	if len(b) < 1 {
		return nil, nil, false
	}
	n := int(b[0])
	b = b[1:]
	ts := make([]depTerm, 0, n)
	for i := 0; i < n; i++ {
		if len(b) < 1 {
			return nil, nil, false
		}
		kl := int(b[0])
		if len(b) < 1+kl+8 {
			return nil, nil, false
		}
		ts = append(ts, depTerm{key: string(b[1 : 1+kl]), coef: int64(binary.BigEndian.Uint64(b[1+kl : 9+kl]))}) //nolint:gosec
		b = b[9+kl:]
	}
	return ts, b, true
}

func depDecodeFormula(v []byte) (*depFormula, bool) {
	if !bytes.HasPrefix(v, []byte("EPRF1")) {
		return nil, false
	}
	b := v[5:]
	f := &depFormula{}
	var ok bool
	if f.terms, b, ok = depReadTerms(b); !ok || len(b) < 9 {
		return nil, false
	}
	f.c = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
	ng := b[8]
	b = b[9:]
	if ng == 1 {
		f.hasG = true
		if f.guard, b, ok = depReadTerms(b); !ok || len(b) < 9 {
			return nil, false
		}
		f.gc = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
		f.expect = b[8]
		b = b[9:]
	}
	if len(b) < 8 {
		return nil, false
	}
	f.pf = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
	return f, true
}

// evalOn evaluates the formula on prefix values; known=false if any referenced value is unknown.
func (f *depFormula) evalOn(val func(string) (int64, bool)) (int64, bool) {
	known := true
	get := func(k string) int64 {
		x, ok := val(k)
		if !ok {
			known = false
		}
		return x
	}
	bit := byte(1)
	if f.hasG {
		if f.expect != 0xFF {
			bit = f.expect // valid tx: the committer confirmed the endorsed path
		} else {
			g := f.gc
			for _, t := range f.guard {
				g += t.coef * get(t.key)
			}
			if g < 0 {
				bit = 0
			}
		}
	}
	v := f.c
	for _, t := range f.terms {
		v += t.coef * get(t.key)
	}
	if bit == 0 {
		v += f.pf
	}
	return v, known
}

// eprAddOverflows reports whether a+b overflows int64.
func eprAddOverflows(a, b int64) bool {
	c := a + b
	return (b > 0 && c < a) || (b < 0 && c > a)
}
