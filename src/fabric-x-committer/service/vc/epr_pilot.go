/*
Extension (not upstream): Endorsement-Preserving Repair (ordered repair) in the VC.

A read-write whose value carries the guarded-delta encoding
    "EPRD1" | delta int64 BE | lower int64 BE
is a signed formula "key <- key + delta, provided key + delta >= lower". The endorsement covers the
encoded bytes, so the committed result is the endorsed formula evaluated on the latest state.
On a version conflict on such keys the transaction is repaired instead of aborted; the guard is
checked on the latest committed value, and a failed guard aborts the transaction as before.
Enabled with FX_EPR=1 in the VC environment. Balances are stored as 8-byte big-endian int64.
*/

package vc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/yugabyte/pgx/v5"
)

var (
	eprMagic   = []byte("EPRD1")
	eprEnabled = os.Getenv("FX_EPR") == "1"
	eprCredits = os.Getenv("FX_EPR2") == "1"
	// eprEscrowVC (escrow debits): every surviving guarded delta is applied as an atomic in-DB increment, because
	// escrow-admitted debits may run concurrently with other deltas on the same key.
	eprEscrowVC = os.Getenv("FX_EPR3") == "1"

	eprRepaired   atomic.Int64
	eprGuardFails atomic.Int64
	eprApplied    atomic.Int64

	// eprNoGuard (unguarded-counter baseline, FabricCRDT-style): deltas are applied without checking their guards, as
	// conflict-free counters would; eprGuardViolations counts deltas committed although their guard failed
	// (e.g. overdrafts).
	eprNoGuard         = os.Getenv("FX_EPR_NOGUARD") == "1"
	eprGuardViolations atomic.Int64

	// eprFastPath (default on; FX_EPR_FAST=0 disables it for comparison): uncontended deltas skip the value
	// fetch and guard evaluation (see pilotRepairDeltas).
	eprFastPath = os.Getenv("FX_EPR_FAST") != "0"
	// eprFastGuarded extends the fast path to guarded deltas (debits) and programs. Its argument (the base is
	// the endorsed one, so the guard holds) relies on the dependency graph being built in ledger order (A1).
	// The in-block reorderings of the baselines (HTFabric's WARD, Fabric++) do not build the graph in ledger
	// order, so with them the fast path is off, as with FX_EPR_FAST_GUARDED=0. The re-execution baselines
	// (FX_REEXEC_URL) keep it off too.
	eprFastGuarded = os.Getenv("FX_EPR_FAST_GUARDED") != "0" &&
		os.Getenv("FX_WARD") != "1" && os.Getenv("FX_REORDER") == "" && os.Getenv("FX_REEXEC_URL") == ""
	eprFastDeltas atomic.Int64
	eprFastProgs  atomic.Int64
)

const eprQueryStateSQLTempl = "SELECT key, value, version FROM ns_${NAMESPACE_ID} WHERE key = ANY($1);"

func eprDecode(v []byte) (delta, lower int64, ok bool) {
	if len(v) != len(eprMagic)+16 || !bytes.HasPrefix(v, eprMagic) {
		return 0, 0, false
	}
	b := v[len(eprMagic):]
	return int64(binary.BigEndian.Uint64(b[:8])), int64(binary.BigEndian.Uint64(b[8:])), true //nolint:gosec
}

// eprMagic2 is the interval-guarded delta used by EVM endorsers:
//
//	"EPRD2" | delta int64 BE | lower int64 BE | upper int64 BE | width byte
//
// meaning "key <- key + delta, provided lower <= key + delta <= upper". width is the stored value
// layout of the key: 8 (int64 BE, e.g. account nonces) or 32 (EVM storage word, uint256 BE). An
// equality guard (lower == upper) expresses an account nonce check; a debit has lower = 0.
var eprMagic2 = []byte("EPRD2")

func eprDecode2(v []byte) (delta, lower, upper int64, width int, ok bool) {
	if len(v) != len(eprMagic2)+25 || !bytes.HasPrefix(v, eprMagic2) {
		return 0, 0, 0, 0, false
	}
	b := v[len(eprMagic2):]
	width = int(b[24])
	if width != 8 && width != 32 {
		return 0, 0, 0, 0, false
	}
	return int64(binary.BigEndian.Uint64(b[:8])), int64(binary.BigEndian.Uint64(b[8:16])), //nolint:gosec
		int64(binary.BigEndian.Uint64(b[16:24])), width, true //nolint:gosec
}

func eprBalance(v []byte) int64 {
	x, _ := eprBalanceOK(v)
	return x
}

// eprBalanceOK parses a stored value as int64: empty (absent or EVM-deleted) is 0, 8 bytes is int64
// BE, 32 bytes is an EVM storage word whose value must fit in a non-negative int64. Any other value
// cannot be repaired (ok = false), so transactions with deltas on it abort (safe fallback).
func eprBalanceOK(v []byte) (int64, bool) {
	switch len(v) {
	case 0:
		return 0, true
	case 8:
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

func eprEncodeBalance(x int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(x)) //nolint:gosec
	return b
}

// eprCanonStorage writes a zero contract-storage word as the endorser does (an EVM SSTORE of zero is a
// delete, which the endorser writes with no value), so that a key's bytes do not depend on whether its value
// was endorsed (fast path) or evaluated at commit time (ordered repair).
func eprCanonStorage(key string, val []byte) []byte {
	if !bytes.HasPrefix([]byte(key), eprStoragePrefix) {
		return val
	}
	for _, c := range val {
		if c != 0 {
			return val
		}
	}
	return []byte{} // stored as an empty value, like an endorsed delete (nil would be stored as NULL)
}

// eprEncodeWidth encodes x in the key's value layout (8: int64 BE, 32: EVM storage word).
func eprEncodeWidth(x int64, width int) []byte {
	if width != 32 {
		return eprEncodeBalance(x)
	}
	b := make([]byte, 32)
	binary.BigEndian.PutUint64(b[24:], uint64(x)) //nolint:gosec
	return b
}

type eprEntry struct {
	tx    TxID
	ns    string
	key   string
	delta int64
	lower int64
	upper int64
	width int
	isNew bool // currently in newWrites (endorsed against a missing key)
	idx   int
}

type eprState struct {
	value   []byte
	version uint64
}

// pilotRepairDeltas evaluates every guarded-delta write in the batch on the latest committed state,
// repairs version conflicts that only involve such keys, and returns the read conflicts that remain.
func (v *validatedTransactions) pilotRepairDeltas(
	ctx context.Context, db *database, conflicts namespaceToReads,
) (namespaceToReads, error) {
	var raws map[TxID][]byte // re-execution baseline: raw transactions carried by endorsers
	if eprReexecURL != "" {
		raws = v.eprStripRawTxs()
	}
	if !eprEnabled {
		return conflicts, nil
	}
	var pre *eprPrefetched // values and decoded programs from the batched read validation (epr_prefetch.go)
	if v.eprPreWait != nil {
		pre = v.eprPreWait()
	}
	var entries []eprEntry
	var credits []eprCreditEntry
	var formulas []eprFEntry
	progs := map[TxID]*eprProgEntry{}
	// deltaKeysOf (tx -> the keys its signed content covers) only decides which conflicting reads are
	// repairable, so a batch without conflicting reads does not build it.
	deltaKeysOf := map[TxID]map[string]bool{}
	needKeys := false
	for _, rc := range conflicts {
		if rc != nil && len(rc.keys) > 0 {
			needKeys = true
			break
		}
	}
	mark := func(tx TxID, ns, k string) {
		if !needKeys {
			return
		}
		if deltaKeysOf[tx] == nil {
			deltaKeysOf[tx] = map[string]bool{}
		}
		deltaKeysOf[tx][ns+"\x00"+k] = true
	}
	collect := func(tw transactionToWrites, isNew bool) {
		for tx, nsw := range tw {
			for ns, w := range nsw {
				for i, val := range w.values {
					// EVM signed content (EPRD2 deltas, EPRP1 programs) is decoded on storage keys only; a value
					// of any other key (e.g. contract code) is a plain write, whatever its bytes.
					storage := bytes.HasPrefix(w.keys[i], eprStoragePrefix)
					var p *EPRProgram
					okp := false
					if storage {
						p, okp = pre.program(val)
					}
					if okp { // guarded program (evaluated once per tx)
						pe := progs[tx]
						if pe == nil {
							pe = &eprProgEntry{ns: ns, p: p, enc: val}
							progs[tx] = pe
							for _, k := range p.Keys { // once: the program is written to each output key
								mark(tx, ns, k)
							}
						} else if pe.ns != ns || !bytes.Equal(pe.enc, val) {
							pe.mismatch = true // a transaction carries one program, written to each of its outputs
						}
						pe.writes = append(pe.writes, string(w.keys[i]))
						continue
					}
					if f, okf := eprDecodeFormula(val); okf {
						formulas = append(formulas, eprFEntry{tx: tx, ns: ns, key: string(w.keys[i]), f: f})
						mark(tx, ns, string(w.keys[i]))
						for _, rk := range f.keys() {
							mark(tx, ns, rk)
						}
						continue
					}
					d, l, ok := eprDecode(val)
					if !ok {
						d2, l2, u2, w2, ok2 := eprDecode2(val)
						if !ok2 || !storage {
							continue
						}
						// An EPRD2 credit (delta >= 0, lower <= delta, no upper bound) can never fail on a
						// non-negative base, so with commutative credits on it commutes like an EPRD1 credit (see eprCommutes).
						// (FX_EPR_NOGUARD baseline: every delta is an unguarded counter update, applied by increment)
						if eprCredits && (eprNoGuard || eprIsCredit2(d2, l2, u2)) {
							credits = append(credits, eprCreditEntry{tx: tx, ns: ns, key: string(w.keys[i]), delta: d2, width: w2})
							mark(tx, ns, string(w.keys[i]))
							continue
						}
						entries = append(entries, eprEntry{tx: tx, ns: ns, key: string(w.keys[i]),
							delta: d2, lower: l2, upper: u2, width: w2, isNew: isNew, idx: i})
						mark(tx, ns, string(w.keys[i]))
						continue
					}
					if eprCredits && l == math.MinInt64 {
						credits = append(credits, eprCreditEntry{tx: tx, ns: ns, key: string(w.keys[i]), delta: d})
						mark(tx, ns, string(w.keys[i]))
						continue
					}
					entries = append(entries, eprEntry{tx: tx, ns: ns, key: string(w.keys[i]),
						delta: d, lower: l, upper: math.MaxInt64, width: 8, isNew: isNew, idx: i})
					mark(tx, ns, string(w.keys[i]))
				}
			}
		}
	}
	collect(v.validTxNonBlindWrites, false)
	collect(v.newWrites, true)
	for _, pe := range progs { // the program's output keys must be exactly the writes that carry it
		pe.mismatch = pe.mismatch || !pe.outputsAreWrites()
	}
	// A credit in the same batch as a program that reads or writes the same key must be evaluated in
	// ledger order with it (e.g. Amalgamate empties the account the credit lands on; a program reading the
	// key must see the credit if it precedes it), so it becomes an ordered entry. The dependency graph
	// guarantees that no other batch updates such a key concurrently.
	if len(progs) > 0 && len(credits) > 0 {
		progWritten := map[string]bool{} // keys a program of the batch reads or writes
		for _, pe := range progs {
			for _, k := range pe.p.Keys {
				progWritten[pe.ns+"\x00"+k] = true
			}
		}
		kept := credits[:0]
		for _, c := range credits {
			if !progWritten[c.ns+"\x00"+c.key] {
				kept = append(kept, c)
				continue
			}
			width := c.width
			if width == 0 {
				width = 8
			}
			entries = append(entries, eprEntry{tx: c.tx, ns: c.ns, key: c.key, delta: c.delta,
				lower: math.MinInt64, upper: math.MaxInt64, width: width})
		}
		credits = kept
	}
	// Fast path for uncontended keys: a delta whose transaction read the key at its current version
	// (no stale read on it) and that is the only signed access to the key in this batch needs neither the
	// current value nor guard evaluation -- the base is the endorsed one, so the guard holds and the result
	// is base+delta -- and is applied as an in-database increment within the commit, saving the value fetch
	// round trip. Committers that take the slow path for the same transaction reach the same value/version.
	// Credits without a stale read likewise skip the range-check fetch (no plain write can have changed the
	// word since endorsement).
	freshCredit := map[string]bool{} // tx \x00 ns \x00 key of credits without a stale read
	if eprFastPath {
		stale := map[string]bool{} // tx \x00 ns \x00 key of every stale read
		for nsID, rc := range conflicts {
			for i, key := range rc.keys {
				for _, tx := range v.readToTxIDs[newCmpRead(nsID, key, rc.versions[i])] {
					stale[string(tx)+"\x00"+nsID+"\x00"+string(key)] = true
				}
			}
		}
		access := map[string]int{}
		for _, e := range entries {
			access[e.ns+"\x00"+e.key]++
		}
		for _, c := range credits {
			access[c.ns+"\x00"+c.key]++
		}
		for _, pe := range progs {
			for _, k := range pe.p.Keys {
				access[pe.ns+"\x00"+k]++
			}
		}
		keptE := entries[:0]
		for _, e := range entries {
			ck := e.ns + "\x00" + e.key
			if eprFastGuarded && access[ck] == 1 && !stale[string(e.tx)+"\x00"+ck] {
				credits = append(credits, eprCreditEntry{tx: e.tx, ns: e.ns, key: e.key, delta: e.delta, width: e.width})
				eprFastDeltas.Add(1)
				continue
			}
			keptE = append(keptE, e)
		}
		entries = keptE
		for _, c := range credits {
			if k := string(c.tx) + "\x00" + c.ns + "\x00" + c.key; !stale[k] {
				freshCredit[k] = true
			}
		}
		// Programs: if none of the program's keys was read stale and no other signed access in the batch
		// touches them, the endorsed outputs are the serial result; write them (the prepared versions are
		// already the next ones) without fetching values or evaluating the program.
		for tx, pe := range progs {
			if !eprFastGuarded || pe.mismatch || len(pe.p.Endorsed) != len(pe.p.Outputs) || len(pe.p.Outputs) == 0 {
				continue
			}
			fast := true
			for _, k := range pe.p.Keys {
				if ck := pe.ns + "\x00" + k; access[ck] != 1 || stale[string(tx)+"\x00"+ck] {
					fast = false
					break
				}
			}
			endorsed := map[string][]byte{}
			for i, o := range pe.p.Outputs {
				endorsed[pe.p.Keys[o.Key]] = pe.p.Endorsed[i]
			}
			for _, k := range pe.writes {
				if _, ok := endorsed[k]; !ok {
					fast = false // a written key without an endorsed value: evaluate
				}
			}
			if !fast {
				continue
			}
			for _, k := range pe.writes {
				if !eprSetValue(v.validTxNonBlindWrites, tx, pe.ns, k, endorsed[k]) {
					eprSetValue(v.newWrites, tx, pe.ns, k, endorsed[k])
				}
			}
			delete(progs, tx)
			eprFastProgs.Add(1)
		}
	}
	// Credits: remove their writes; they are applied as atomic increments at commit time.
	for _, c := range credits {
		eprRemoveKey(v.validTxNonBlindWrites, c.tx, c.ns, c.key)
		eprRemoveKey(v.newWrites, c.tx, c.ns, c.key)
		if v.eprCredits == nil {
			v.eprCredits = map[TxID][]eprCreditEntry{}
		}
		v.eprCredits[c.tx] = append(v.eprCredits[c.tx], c)
		eprApplied.Add(1)
	}
	// Nothing signed left in this batch: return its conflicts, unless queries may still be committed at their
	// snapshot (EPR's per-transaction rule below must also apply to batches with only queries and fast paths)
	if len(entries) == 0 && len(credits) == 0 && len(formulas) == 0 && len(progs) == 0 &&
		!(eprROSnapshot && needKeys && eprReexecURL == "") {
		return conflicts, nil
	}
	// EVM-word credits must land on a value that fits the int64 fast path (e.g. not on a USDC v2.2
	// balance carrying the blacklist bit). Only ordered plain writes can change the high bytes, and they
	// are all committed before this batch (A1), so this check is deterministic.
	rangeChecked := map[string]bool{}
	for _, c := range credits {
		if c.width == 32 && !freshCredit[string(c.tx)+"\x00"+c.ns+"\x00"+c.key] {
			rangeChecked[c.ns+"\x00"+c.key] = true
		}
	}

	// Split conflicts: under EPR each transaction's stale read is decided on its own (repaired, committed at its
	// snapshot, or aborted); the re-execution baselines keep the per-read rule.
	remaining := make(namespaceToReads)
	reexec := map[TxID]bool{}      // transactions a re-executing committer re-runs
	reexecAll := map[TxID]string{} // FX_REEXEC_ALL: unrepairable, committed from their re-execution (-> ns)
	for nsID, rc := range conflicts {
		for i, key := range rc.keys {
			cr := newCmpRead(nsID, key, rc.versions[i])
			if eprReexecURL == "" {
				// EPR decides each transaction's stale read on its own, so that its outcome does not depend on
				// which transactions share its batch (committers of different organizations batch differently):
				// a read its own signed content covers is repaired; a query (no signed content, no storage
				// write) is committed at its snapshot; any other reader keeps the conflict and is aborted.
				nsKey := nsID + "\x00" + string(key)
				storage := bytes.HasPrefix(key, eprStoragePrefix)
				keep, nRep, nSnap := eprStaleKeep(v.readToTxIDs[cr],
					func(tx TxID) bool { return deltaKeysOf[tx][nsKey] },
					func(tx TxID) bool {
						return eprROSnapshot && storage && len(deltaKeysOf[tx]) == 0 && len(v.eprCredits[tx]) == 0 &&
							v.eprNoStorageWrites([]TxID{tx})
					})
				eprRepaired.Add(int64(nRep))
				eprSnapshotReads.Add(int64(nSnap))
				if len(keep) > 0 {
					v.readToTxIDs[cr] = keep // only these readers are aborted for this read
					remaining.getOrCreate(nsID).append(key, rc.versions[i])
				}
				continue
			}
			// re-execution baselines: per read
			repairable := true
			for _, tx := range v.readToTxIDs[cr] {
				if !deltaKeysOf[tx][nsID+"\x00"+string(key)] {
					repairable = false
					break
				}
			}
			if repairable {
				eprRepaired.Add(int64(len(v.readToTxIDs[cr])))
				if eprReexecURL != "" {
					for _, tx := range v.readToTxIDs[cr] {
						reexec[tx] = true
					}
				}
				continue
			}
			if eprROSnapshot && bytes.HasPrefix(key, eprStoragePrefix) && v.eprNoStorageWrites(v.readToTxIDs[cr]) {
				// read-only on contract storage (a query): its endorsed result is that of its snapshot, a
				// committed prefix of the ledger order, so it is serialized there; its only write is the
				// sender nonce, which keeps its ledger position (a nonce read, if the executor makes one, is
				// not skipped here)
				eprSnapshotReads.Add(int64(len(v.readToTxIDs[cr])))
				continue
			}
			if eprReexecAll && eprReexecURL != "" { // XOX/HTFabric: re-execute it instead of aborting
				for _, tx := range v.readToTxIDs[cr] {
					reexec[tx], reexecAll[tx] = true, nsID
				}
				continue
			}
			remaining.getOrCreate(nsID).append(key, rc.versions[i])
		}
	}
	// Re-execution baseline (real): re-run the conflicting transactions with the EVM on the commit path.
	var reexecFinal map[string][]byte
	var reexecOK map[TxID]bool
	var reexecAllOK map[TxID]bool
	var reexecTxW map[TxID]map[string][]byte
	if eprReexecURL != "" && len(reexec) > 0 && eprEarlyGossip {
		// after the valid transactions (early gossip)
		eprLateWork.Store(v, &eprLate{txs: reexec, raws: raws, all: reexecAll})
	} else if eprReexecURL != "" && len(reexec) > 0 {
		f, oks, txw, err := v.eprReexecute(ctx, reexec, raws)
		if err != nil {
			return nil, err
		}
		reexecFinal, reexecOK, reexecTxW = f, oks, txw
		reexecAllOK = make(map[TxID]bool, len(reexecAll))
		for tx := range reexecAll {
			reexecAllOK[tx] = oks[tx]
		}
	}

	// Latest committed value and version of every delta key, per namespace.
	keysByNs := map[string][][]byte{}
	for _, e := range entries {
		keysByNs[e.ns] = append(keysByNs[e.ns], []byte(e.key))
	}
	for _, fe := range formulas {
		keysByNs[fe.ns] = append(keysByNs[fe.ns], []byte(fe.key))
		for _, rk := range fe.f.keys() {
			keysByNs[fe.ns] = append(keysByNs[fe.ns], []byte(rk))
		}
	}
	for _, c := range credits {
		if rangeChecked[c.ns+"\x00"+c.key] {
			keysByNs[c.ns] = append(keysByNs[c.ns], []byte(c.key))
		}
	}
	for _, pe := range progs {
		for _, k := range pe.p.Keys {
			keysByNs[pe.ns] = append(keysByNs[pe.ns], []byte(k))
		}
	}
	state := map[string]eprState{}
	var pf *eprPrefetched
	if len(keysByNs) > 0 { // wait for the concurrently fetched values only when values are needed
		pf = pre.values()
	}
	if pf != nil { // values fetched concurrently with the read validation (epr_prefetch.go)
		for ns, keys := range keysByNs {
			missing := keys[:0:0]
			for _, k := range keys {
				ck := ns + "\x00" + string(k)
				if !pf.queried[ck] {
					missing = append(missing, k)
					continue
				}
				if st, ok := pf.state[ck]; ok {
					state[ck] = st
				}
			}
			eprPrefetchHits.Add(int64(len(keys) - len(missing)))
			eprPrefetchMiss.Add(int64(len(missing)))
			if len(missing) == 0 {
				delete(keysByNs, ns)
			} else {
				keysByNs[ns] = missing
			}
		}
	}
	for ns, keys := range keysByNs {
		rows, err := db.pool.Query(ctx, FmtNsID(eprQueryStateSQLTempl, ns), keys)
		if err != nil {
			return nil, fmt.Errorf("epr: query state: %w", err)
		}
		for rows.Next() {
			var k, val []byte
			var ver int64
			if err := rows.Scan(&k, &val, &ver); err != nil {
				rows.Close()
				return nil, fmt.Errorf("epr: scan state: %w", err)
			}
			state[ns+"\x00"+string(k)] = eprState{value: val, version: uint64(ver)} //nolint:gosec
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("epr: rows: %w", err)
		}
	}

	// Evaluate formulas with exact serial semantics: transactions in commit (height) order; a
	// transaction takes effect only if ALL its guards hold on the running values, otherwise it is
	// aborted and contributes nothing. With the commutativity-aware dependency graph (credits) several
	// transactions may carry deltas on the same key in one batch; only the last effective writer keeps
	// a write (with the aggregated value), the others' writes on that key are dropped.
	byTx := map[TxID][]int{}
	byTxF := map[TxID][]int{}
	var txOrder []TxID
	seenTx := map[TxID]bool{}
	for i, e := range entries {
		if !seenTx[e.tx] {
			seenTx[e.tx] = true
			txOrder = append(txOrder, e.tx)
		}
		byTx[e.tx] = append(byTx[e.tx], i)
	}
	for i, fe := range formulas {
		if !seenTx[fe.tx] {
			seenTx[fe.tx] = true
			txOrder = append(txOrder, fe.tx)
		}
		byTxF[fe.tx] = append(byTxF[fe.tx], i)
	}
	for tx := range progs {
		if !seenTx[tx] {
			seenTx[tx] = true
			txOrder = append(txOrder, tx)
		}
	}
	fval := make([]int64, len(formulas))
	// raw current values for program evaluation (programs are ordered by the dependency graph)
	curRaw := map[string][]byte{}
	for k, st := range state {
		curRaw[k] = st.value
	}
	progOut := map[TxID]map[string][]byte{}
	sort.Slice(txOrder, func(a, b int) bool {
		ha, hb := v.txIDToHeight[txOrder[a]], v.txIDToHeight[txOrder[b]]
		if ha == nil || hb == nil {
			return string(txOrder[a]) < string(txOrder[b])
		}
		if ha.BlockNum != hb.BlockNum {
			return ha.BlockNum < hb.BlockNum
		}
		return ha.TxNum < hb.TxNum
	})
	cur := map[string]int64{}
	bad := map[string]bool{} // keys whose committed value is not a repairable integer
	for k, st := range state {
		x, okb := eprBalanceOK(st.value)
		cur[k] = x
		if !okb {
			bad[k] = true
		}
	}
	lastWriter := map[string]int{} // key -> entry index of the last effective writer
	// Several transactions may write the same key in one batch; only the last
	// effective writer keeps a write, and the version advances by the number of effective writes.
	lastAbs := map[string]TxID{}
	nWrites := map[string]int{}
	progKey := map[string]bool{}
	boundedKey := map[string]bool{}
	var guardFailed []TxID
	creditBad := map[TxID]bool{}
	for _, c := range credits {
		if rangeChecked[c.ns+"\x00"+c.key] && bad[c.ns+"\x00"+c.key] {
			creditBad[c.tx] = true
		}
	}
	for tx := range creditBad {
		if !seenTx[tx] { // credit-only transaction
			guardFailed = append(guardFailed, tx)
		}
	}
	// Readers that keep a stale read are aborted by MVCC after this function: they must not take part in the
	// serial evaluation either, or their deltas would reach the values (and the last writes) of valid
	// transactions of this batch. (The re-execution baselines keep their behavior.)
	doomed := map[TxID]bool{}
	if eprReexecURL == "" {
		for nsID, rc := range remaining {
			for i, key := range rc.keys {
				for _, tx := range v.readToTxIDs[newCmpRead(nsID, key, rc.versions[i])] {
					doomed[tx] = true
				}
			}
		}
	}
	for _, tx := range txOrder {
		if doomed[tx] {
			continue
		}
		ok := !creditBad[tx]
		for _, i := range byTx[tx] {
			e := entries[i]
			k := e.ns + "\x00" + e.key
			if bad[k] || eprOverflow(cur[k], e.delta) {
				ok = false // overflow aborts the transaction (assumption A4)
				break
			}
			if cur[k]+e.delta < e.lower || cur[k]+e.delta > e.upper {
				if eprNoGuard { // CRDT-style baseline: apply the delta anyway, count the violated guard
					eprGuardViolations.Add(1)
					continue
				}
				ok = false
				break
			}
		}
		if ok {
			for _, fi := range byTxF[tx] {
				fe := formulas[fi]
				nv, okf := fe.f.eval(func(k string) int64 { return cur[fe.ns+"\x00"+k] })
				if !okf {
					ok = false
					break
				}
				fval[fi] = nv
			}
		}
		if pe := progs[tx]; ok && pe != nil && pe.mismatch {
			ok = false // outputs other than the writes carrying the program: refuse
		} else if ok && pe != nil {
			outs, okp := pe.p.Eval(func(k string) []byte { return curRaw[pe.ns+"\x00"+k] })
			if !okp {
				ok = false
			} else {
				for _, k := range pe.writes {
					if _, has := outs[k]; !has {
						ok = false // a written key that the program does not produce: refuse
					}
				}
				progOut[tx] = outs
			}
		}
		if !ok {
			guardFailed = append(guardFailed, tx)
			continue
		}
		if pe := progs[tx]; pe != nil {
			for k, val := range progOut[tx] {
				ck := pe.ns + "\x00" + k
				curRaw[ck] = val
				// keep the integer view in sync for deltas on the same key later in the batch
				x, okb := eprBalanceOK(val)
				cur[ck], bad[ck] = x, !okb
				progKey[ck] = true
				lastAbs[ck] = tx
				nWrites[ck]++
			}
			eprApplied.Add(1)
		}
		for _, fi := range byTxF[tx] {
			fe := formulas[fi]
			cur[fe.ns+"\x00"+fe.key] = fval[fi]
			eprApplied.Add(1)
		}
		for _, i := range byTx[tx] {
			e := entries[i]
			k := e.ns + "\x00" + e.key
			cur[k] += e.delta
			curRaw[k] = eprEncodeWidth(cur[k], e.width) // raw view for programs later in the batch
			lastWriter[k] = i
			lastAbs[k] = tx
			nWrites[k]++
			if e.upper != math.MaxInt64 {
				boundedKey[k] = true
			}
			eprApplied.Add(1)
		}
	}
	// A key is written with an absolute value (instead of escrow increments) when the batch also writes it
	// through a program or a bounded delta: those are ordered, so no other batch updates the key concurrently.
	absKey := func(k string) bool { return !eprEscrowVC || progKey[k] || boundedKey[k] }
	nextVersion := func(k string) (uint64, bool) {
		st, exists := state[k]
		if exists {
			return st.version + uint64(nWrites[k]), true //nolint:gosec
		}
		return uint64(nWrites[k] - 1), false //nolint:gosec
	}
	failed := map[TxID]bool{}
	for _, tx := range guardFailed {
		failed[tx] = true
	}
	for i, e := range entries {
		if failed[e.tx] {
			continue // all writes of an aborted tx are removed by updateInvalidTxs below
		}
		k := e.ns + "\x00" + e.key
		if !absKey(k) {
			eprRemoveKey(v.validTxNonBlindWrites, e.tx, e.ns, e.key)
			eprRemoveKey(v.newWrites, e.tx, e.ns, e.key)
			if v.eprCredits == nil {
				v.eprCredits = map[TxID][]eprCreditEntry{}
			}
			v.eprCredits[e.tx] = append(v.eprCredits[e.tx], eprCreditEntry{tx: e.tx, ns: e.ns, key: e.key, delta: e.delta, width: e.width})
			continue
		}
		eprRemoveKey(v.validTxNonBlindWrites, e.tx, e.ns, e.key)
		eprRemoveKey(v.newWrites, e.tx, e.ns, e.key)
		if lastAbs[k] != e.tx || lastWriter[k] != i {
			continue // a later transaction of the batch writes the key
		}
		val := eprCanonStorage(e.key, eprEncodeWidth(cur[k], e.width))
		if ver, exists := nextVersion(k); exists {
			v.validTxNonBlindWrites.getOrCreate(e.tx, e.ns).append([]byte(e.key), val, ver)
		} else {
			v.newWrites.getOrCreate(e.tx, e.ns).append([]byte(e.key), val, ver)
		}
	}
	// Formula writes are ordered by the dependency graph (no concurrent writer on their keys), so the
	// evaluated absolute value is written with the next version (or inserted).
	for fi, fe := range formulas {
		if failed[fe.tx] {
			continue
		}
		k := fe.ns + "\x00" + fe.key
		st, exists := state[k]
		val := eprEncodeBalance(fval[fi])
		eprRemoveKey(v.validTxNonBlindWrites, fe.tx, fe.ns, fe.key)
		eprRemoveKey(v.newWrites, fe.tx, fe.ns, fe.key)
		if exists {
			v.validTxNonBlindWrites.getOrCreate(fe.tx, fe.ns).append([]byte(fe.key), val, st.version+1)
		} else {
			v.newWrites.getOrCreate(fe.tx, fe.ns).append([]byte(fe.key), val, 0)
		}
	}
	// Program outputs replace the encoded program written to each output key (next version or insert).
	for tx, pe := range progs {
		if failed[tx] {
			continue
		}
		for _, key := range pe.writes {
			k := pe.ns + "\x00" + key
			eprRemoveKey(v.validTxNonBlindWrites, tx, pe.ns, key)
			eprRemoveKey(v.newWrites, tx, pe.ns, key)
			if lastAbs[k] != tx {
				continue // a later transaction of the batch writes the key
			}
			out := eprCanonStorage(key, curRaw[k])
			if ver, exists := nextVersion(k); exists {
				v.validTxNonBlindWrites.getOrCreate(tx, pe.ns).append([]byte(key), out, ver)
			} else {
				v.newWrites.getOrCreate(tx, pe.ns).append([]byte(key), out, ver)
			}
		}
	}
	if reexecFinal != nil {
		eprVals := map[string][]byte{}
		for _, e := range entries {
			if !failed[e.tx] && reexec[e.tx] {
				eprVals[e.key] = eprEncodeWidth(cur[e.ns+"\x00"+e.key], e.width)
			}
		}
		for tx, pe := range progs {
			if !failed[tx] && reexec[tx] {
				for _, k := range pe.writes {
					eprVals[k] = progOut[tx][k]
				}
			}
		}
		eprReexecCompare(reexecFinal, eprVals)
		// transactions that also have a non-repairable stale read are aborted by MVCC later, whatever their
		// re-execution yields: leave them out of the outcome comparison
		for nsID, rc := range remaining {
			for i, key := range rc.keys {
				for _, tx := range v.readToTxIDs[newCmpRead(nsID, key, rc.versions[i])] {
					delete(reexecOK, tx)
				}
			}
		}
		eprReexecCompareTxs(reexecOK, failed)
	}
	if len(guardFailed) > 0 {
		eprGuardFails.Add(int64(len(guardFailed)))
		v.updateInvalidTxs(guardFailed, committerpb.Status_ABORTED_MVCC_CONFLICT)
	}
	if len(reexecAll) > 0 && reexecAllOK != nil { // after EPR's evaluation, which it overrides
		v.eprApplyReexecWrites(reexecAll, reexecAllOK, reexecTxW)
	}
	return remaining, nil
}

// eprProgEntry is a guarded program of one transaction (its writes are the program outputs).
type eprProgEntry struct {
	ns     string
	p      *EPRProgram
	writes []string
	enc    []byte // the encoded program, as written to the first output key
	// mismatch: the writes carrying the program differ in bytes or namespace, or are not exactly its output
	// keys; the transaction is refused (a program may change no key that the transaction does not write)
	mismatch bool
}

// outputsAreWrites reports whether the program's output keys are exactly the keys of the writes carrying it.
func (pe *eprProgEntry) outputsAreWrites() bool {
	outs := make(map[string]bool, len(pe.p.Outputs))
	for _, o := range pe.p.Outputs {
		outs[pe.p.Keys[o.Key]] = true
	}
	ws := make(map[string]bool, len(pe.writes))
	for _, k := range pe.writes {
		if !outs[k] {
			return false
		}
		ws[k] = true
	}
	return len(ws) == len(outs)
}

// eprSetValue replaces the value of key in the writes of tx in namespace ns; false if absent.
func eprSetValue(tw transactionToWrites, tx TxID, ns, key string, val []byte) bool {
	nsw, ok := tw[tx]
	if !ok {
		return false
	}
	w, ok := nsw[ns]
	if !ok {
		return false
	}
	for i := range w.keys {
		if string(w.keys[i]) == key {
			w.values[i] = val
			return true
		}
	}
	return false
}

// eprRemoveKey removes key from the writes of tx in namespace ns (no-op if absent).
func eprRemoveKey(tw transactionToWrites, tx TxID, ns, key string) {
	nsw, ok := tw[tx]
	if !ok {
		return
	}
	w, ok := nsw[ns]
	if !ok {
		return
	}
	for i := range w.keys {
		if string(w.keys[i]) == key {
			w.keys = append(w.keys[:i], w.keys[i+1:]...)
			w.values = append(w.values[:i], w.values[i+1:]...)
			w.versions = append(w.versions[:i], w.versions[i+1:]...)
			return
		}
	}
}

// EPRStats reports counters (repaired conflicting reads, applied formulas, guard failures).
func EPRStats() (repaired, applied, guardFails int64) {
	return eprRepaired.Load(), eprApplied.Load(), eprGuardFails.Load()
}

// PilotBalanceStats returns, from ONE SQL statement (one snapshot): the sum of every 8-byte (balance)
// value in the namespace, how many there are, how many are negative, and the committed tx count.
// Used by tests to check deposit accounting (synthetic) and conservation (replay).
func (env *DatabaseTestEnv) PilotBalanceStats(
	ctx context.Context, nsID string,
) (sum int64, n, neg int, committed int64, err error) {
	q := FmtNsID(`SELECT
	  COALESCE(SUM(('x' || encode(value, 'hex'))::bit(64)::bigint), 0)::bigint,
	  COUNT(*),
	  COUNT(*) FILTER (WHERE ('x' || encode(value, 'hex'))::bit(64)::bigint < 0),
	  (SELECT COUNT(*) FROM tx_status WHERE status = 1)
	FROM ns_${NAMESPACE_ID} WHERE length(value) = 8;`, nsID)
	err = env.DB.pool.QueryRow(ctx, q).Scan(&sum, &n, &neg, &committed)
	return sum, n, neg, committed, err
}

type eprCreditEntry struct {
	tx    TxID
	ns    string
	key   string
	delta int64
	width int // stored value layout: 8 (int64 BE; also the default 0) or 32 (EVM storage word)
}

// eprIsCredit2 reports whether an EPRD2 delta is a credit: it never fails on a non-negative base
// (delta >= 0 and lower <= delta) and has no upper bound. Must match the coordinator's eprCommutes.
func eprIsCredit2(delta, lower, upper int64) bool {
	return delta >= 0 && lower <= delta && upper == math.MaxInt64
}

// eprAgg is the aggregated effect of a batch's increments on one key: the summed delta and the number
// of transactions that wrote the key (each committed write advances the version by one, exactly as
// serial execution would, so versions stay identical across committers regardless of batching).
type eprAgg struct {
	delta int64
	count int64
	width int
}

// eprAggregateCredits sums the increments of transactions that are still valid, per namespace and key.
func (v *validatedTransactions) eprAggregateCredits() map[string]map[string]eprAgg {
	if len(v.eprCredits) == 0 {
		return nil
	}
	agg := map[string]map[string]eprAgg{}
	for tx, cs := range v.eprCredits {
		if _, invalid := v.invalidTxStatus[tx]; invalid {
			continue
		}
		for _, c := range cs {
			if agg[c.ns] == nil {
				agg[c.ns] = map[string]eprAgg{}
			}
			a := agg[c.ns][c.key]
			a.delta += c.delta
			a.count++
			if c.width == 32 {
				a.width = 32
			}
			agg[c.ns][c.key] = a
		}
	}
	return agg
}

// A new key written by n transactions in one batch ends at version n-1 (insert = version 0, then one
// increment per further write); an existing key advances by n.
// The value layout is kept per key: 8-byte int64 BE, or a 32-byte EVM word (24 zero bytes + int64 BE).
// The inserted row carries the batch delta in the key's layout; on conflict the stored integer (last 8
// bytes of an 8- or 32-byte value, 0 for anything else) is incremented by it. A 32-byte (contract storage)
// result of zero is stored empty, as the endorser writes it (an SSTORE of zero is a delete); a zero 32-byte
// batch delta (inserted empty) leaves the stored value as it is.
const eprCreditSQLTempl = `INSERT INTO ns_${NAMESPACE_ID} (key, value, version)
SELECT k, CASE WHEN w = 32 THEN (CASE WHEN dl = 0 THEN ''::bytea ELSE decode(repeat('00', 24), 'hex') || int8send(dl) END)
  ELSE int8send(dl) END, cnt - 1
FROM unnest($1::BYTEA[], $2::BIGINT[], $3::BIGINT[], $4::INT[]) WITH ORDINALITY AS t(k, dl, cnt, w, o) ORDER BY o
ON CONFLICT (key) DO UPDATE SET
  value = CASE
    WHEN length(EXCLUDED.value) = 0 THEN ns_${NAMESPACE_ID}.value
    WHEN length(EXCLUDED.value) = 32 AND
      (CASE WHEN length(ns_${NAMESPACE_ID}.value) IN (8, 32)
            THEN ('x' || encode(substring(ns_${NAMESPACE_ID}.value FROM length(ns_${NAMESPACE_ID}.value) - 7 FOR 8), 'hex'))::bit(64)::bigint
            ELSE 0 END)
      + ('x' || encode(substring(EXCLUDED.value FROM length(EXCLUDED.value) - 7 FOR 8), 'hex'))::bit(64)::bigint = 0
      THEN ''::bytea
    ELSE (CASE WHEN length(EXCLUDED.value) = 32 THEN decode(repeat('00', 24), 'hex') ELSE ''::bytea END) || int8send(
    (CASE WHEN length(ns_${NAMESPACE_ID}.value) IN (8, 32)
          THEN ('x' || encode(substring(ns_${NAMESPACE_ID}.value FROM length(ns_${NAMESPACE_ID}.value) - 7 FOR 8), 'hex'))::bit(64)::bigint
          ELSE 0 END)
    + ('x' || encode(substring(EXCLUDED.value FROM length(EXCLUDED.value) - 7 FOR 8), 'hex'))::bit(64)::bigint)
  END,
  version = ns_${NAMESPACE_ID}.version + EXCLUDED.version + 1;`

// eprApplyCredits applies aggregated credits atomically inside the commit transaction.
func (d *database) eprApplyCredits(ctx context.Context, tx pgx.Tx, agg map[string]map[string]eprAgg) error {
	// Lock rows in one global order (namespace, then key bytes) so concurrent batches crediting the
	// same hot keys cannot deadlock.
	nss := make([]string, 0, len(agg))
	for ns := range agg {
		nss = append(nss, ns)
	}
	sort.Strings(nss)
	for _, ns := range nss {
		m := agg[ns]
		ks := make([]string, 0, len(m))
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		keys := make([][]byte, 0, len(m))
		deltas := make([]int64, 0, len(m))
		counts := make([]int64, 0, len(m))
		widths := make([]int32, 0, len(m))
		for _, k := range ks {
			keys = append(keys, []byte(k))
			deltas = append(deltas, m[k].delta)
			counts = append(counts, m[k].count)
			widths = append(widths, int32(m[k].width)) //nolint:gosec
		}
		if _, err := tx.Exec(ctx, FmtNsID(eprCreditSQLTempl, ns), keys, deltas, counts, widths); err != nil {
			return fmt.Errorf("epr: apply credits: %w", err)
		}
	}
	return nil
}

// PilotStateDigest returns md5 over all 8-byte (balance) rows ordered by key, their minimum and the
// committed count, from one SQL statement (one snapshot). Used for determinism comparisons.
func (env *DatabaseTestEnv) PilotStateDigest(ctx context.Context, nsID string) (digest string, minBal, committed int64, err error) {
	q := FmtNsID(`SELECT
	  COALESCE(md5(string_agg(encode(key, 'hex') || ':' || encode(value, 'hex') || ':' || version::text, ',' ORDER BY key)), ''),
	  COALESCE(MIN(('x' || encode(value, 'hex'))::bit(64)::bigint), 0)::bigint,
	  (SELECT COUNT(*) FROM tx_status WHERE status = 1)
	FROM ns_${NAMESPACE_ID} WHERE length(value) = 8;`, nsID)
	err = env.DB.pool.QueryRow(ctx, q).Scan(&digest, &minBal, &committed)
	return digest, minBal, committed, err
}

type eprFEntry struct {
	tx  TxID
	ns  string
	key string
	f   *eprFormula
}

// eprOverflow reports whether a+b overflows int64.
func eprOverflow(a, b int64) bool {
	c := a + b
	return (b > 0 && c < a) || (b < 0 && c > a)
}

// Read-only transactions at their snapshot (FX_EPR_RO=1, EPR only): a transaction that writes no contract
// storage and read storage at an older version is committed instead of aborted. Its contract-storage reads
// come from one committed block state (a prefix of the ledger order) and feed no contract write, and its only
// write, the sender nonce, keeps its ledger position. The harness writes nonces without reading them; an
// executor that reads the nonce leaves that read version-checked, since only "str:" reads are skipped.
// Either way, serializing the reads at the snapshot gives the state of serial ledger-order execution
// (SmallBank's balance queries).
var (
	eprROSnapshot    = os.Getenv("FX_EPR_RO") == "1"
	eprStoragePrefix = []byte("str:")
	eprAccountPrefix = []byte("acc:")
	eprNonceSuffix   = []byte(":nonce")
	eprSnapshotReads atomic.Int64
)

// eprNoStorageWrites reports whether every transaction writes nothing but account nonces ("acc:<addr>:nonce"):
// the reads of such a transaction can be serialized at its endorsement snapshot, since its only write is a
// nonce, which only its sender's transactions write and which is either written blind or read under a
// version check.
func (v *validatedTransactions) eprNoStorageWrites(txs []TxID) bool {
	for _, tx := range txs {
		for _, tw := range []transactionToWrites{v.validTxNonBlindWrites, v.validTxBlindWrites, v.newWrites} {
			for _, w := range tw[tx] {
				for _, k := range w.keys {
					if !bytes.HasPrefix(k, eprAccountPrefix) || !bytes.HasSuffix(k, eprNonceSuffix) {
						return false
					}
				}
			}
		}
	}
	return len(txs) > 0
}

func init() {
	if !eprEnabled {
		return
	}
	go eprStatsLogger()
}

func eprStatsLogger() {
	for range time.Tick(5 * time.Second) {
		r, a, g := EPRStats()
		ph, pm := EPRPrefetchStats()
		logger.Infof("PILOT_EPR repaired_reads=%d applied=%d guard_fails=%d fast_deltas=%d fast_progs=%d snapshot_reads=%d prefetch_hits=%d prefetch_miss=%d", r, a, g, eprFastDeltas.Load(), eprFastProgs.Load(), eprSnapshotReads.Load(), ph, pm)
		if c, n, us, cmp, mm := EPRReexecStats(); c > 0 {
			tc, tm := EPRReexecTxStats()
			ac, ar, an := EPRReexecAllStats()
			logger.Infof("PILOT_REEXEC calls=%d txs=%d micros=%d compared=%d mismatch=%d tx_compared=%d tx_mismatch=%d early_gossip=%d reexec_all_committed=%d reexec_all_reverted=%d reexec_all_new_keys=%d", c, n, us, cmp, mm, tc, tm, eprEarlyGossipBatches.Load(), ac, ar, an)
		}
	}
}

// eprStaleKeep splits the transactions that read one stale version: those whose own signed content covers the
// read (repaired), queries committed at their snapshot, and the rest, which keep the conflict. The decision for
// each transaction depends only on that transaction, so any split of a batch yields the same outcome.
func eprStaleKeep(txs []TxID, repaired, snapshot func(TxID) bool) (keep []TxID, nRepaired, nSnapshot int) {
	for _, tx := range txs {
		switch {
		case repaired(tx):
			nRepaired++
		case snapshot(tx):
			nSnapshot++
		default:
			keep = append(keep, tx)
		}
	}
	return keep, nRepaired, nSnapshot
}
