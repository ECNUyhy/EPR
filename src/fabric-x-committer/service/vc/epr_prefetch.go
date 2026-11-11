package vc

// Extension (not upstream): the validator stage runs one worker, so every batch pays its database work in sequence.
// The read validation queries the versions of the read keys; pilotRepairDeltas then queries the values of the
// keys that carry signed content, which depends on nothing the first query returns. eprValidateReads issues the
// value query on a second pooled connection while the version check runs, so the database executes both in
// parallel and the stage waits for the slower one only. The evaluation waits for the values only when it
// needs them (a batch on the endorsed fast path never does). The programs decoded to find the keys are cached
// for the evaluation, so no program is decoded twice.
// Values read concurrently with the version check are as recent as those read after it: the dependency graph
// releases a transaction only after every conflicting predecessor has committed, so no write that the
// evaluation must see can land in between (later credits commute and are range-checked as before).

import (
	"bytes"
	"context"
	"os"
	"sync/atomic"
)

// eprPrefetchOn is on with EPR unless FX_EPR_PREFETCH=0; the re-execution baselines prefetch in their own
// re-execution step instead.
var (
	eprPrefetchOn   = eprEnabled && os.Getenv("FX_EPR_PREFETCH") != "0" && os.Getenv("FX_REEXEC_URL") == ""
	eprPrefetchHits atomic.Int64
	eprPrefetchMiss atomic.Int64
)

// eprPrefetched holds the values of the prefetched keys; queried also lists the keys absent from the
// database (they read as absent without a second query).
type eprPrefetched struct {
	state   map[string]eprState
	queried map[string]bool
	// progs caches the programs decoded to find the keys (filled before the query starts)
	progs  map[*byte]*EPRProgram
	done   chan bool // closed when state and queried are filled; failed reports a query error
	failed bool
}

// program returns the decoded program of a written value, from the cache when present.
func (pf *eprPrefetched) program(val []byte) (*EPRProgram, bool) {
	if pf != nil && len(val) > 0 {
		if p, ok := pf.progs[&val[0]]; ok {
			return p, true
		}
	}
	return DecodeEPRProgram(val)
}

// values waits for the prefetched values; nil when there are none or the query failed.
func (pf *eprPrefetched) values() *eprPrefetched {
	if pf == nil || pf.done == nil {
		return nil
	}
	<-pf.done
	if pf.failed {
		return nil
	}
	return pf
}

// eprProgSeen is the last program decoded for a transaction (an endorser writes the same encoded program to
// each of its output keys; identical copies are decoded once).
type eprProgSeen struct {
	ns  string
	val []byte
	p   *EPRProgram
}

// eprSignedKeys returns, per namespace, every key the batch's signed content may need the value of.
func eprSignedKeys(progs map[*byte]*EPRProgram, tws ...transactionToWrites) map[string][][]byte {
	seen := map[string]bool{}
	out := map[string][][]byte{}
	add := func(ns, k string) {
		if ck := ns + "\x00" + k; !seen[ck] {
			seen[ck] = true
			out[ns] = append(out[ns], []byte(k))
		}
	}
	last := map[TxID]eprProgSeen{}
	for _, tw := range tws {
		for tx, nsw := range tw {
			for ns, w := range nsw {
				for i, val := range w.values {
					// EVM signed content is decoded on storage keys only (as in pilotRepairDeltas)
					storage := bytes.HasPrefix(w.keys[i], eprStoragePrefix)
					if s, ok := last[tx]; ok && storage && s.ns == ns && bytes.Equal(val, s.val) {
						if progs != nil {
							progs[&val[0]] = s.p
						}
						continue // a copy: its keys are listed already
					}
					if p, ok := DecodeEPRProgram(val); ok && storage {
						last[tx] = eprProgSeen{ns: ns, val: val, p: p}
						if progs != nil {
							progs[&val[0]] = p
						}
						for _, k := range p.Keys {
							add(ns, k)
						}
						continue
					}
					if f, ok := eprDecodeFormula(val); ok {
						add(ns, string(w.keys[i]))
						for _, rk := range f.keys() {
							add(ns, rk)
						}
						continue
					}
					if _, _, ok := eprDecode(val); ok {
						add(ns, string(w.keys[i]))
						continue
					}
					if _, _, _, _, ok := eprDecode2(val); ok && storage {
						add(ns, string(w.keys[i]))
					}
				}
			}
		}
	}
	return out
}

// eprValidateReads validates the batch's reads while a second connection fetches the values of its signed
// keys. Without prefetching, or without signed keys, it is the upstream validateReads. The version check is
// issued first and the signed content is decoded while it runs, so the single validator worker spends the
// decoding inside the round trip instead of before it (both only read the prepared batch).
func (v *transactionValidator) eprValidateReads(
	ctx context.Context, db *database, prep *preparedTransactions,
) (namespaceToReads, *eprPrefetched, error) {
	if !eprPrefetchOn {
		c, err := v.validateReads(ctx, db, prep.nsToReads)
		return c, nil, err
	}
	type checked struct {
		c   namespaceToReads
		err error
	}
	vr := make(chan checked, 1)
	go func() {
		c, err := v.validateReads(ctx, db, prep.nsToReads)
		vr <- checked{c, err}
	}()
	pf := &eprPrefetched{progs: map[*byte]*EPRProgram{}}
	keysByNs := eprSignedKeys(pf.progs, prep.txIDToNsNonBlindWrites, prep.txIDToNsNewWrites)
	if len(keysByNs) > 0 {
		pf.done = make(chan bool)
		go func() {
			defer close(pf.done)
			state, queried := map[string]eprState{}, map[string]bool{}
			for ns, keys := range keysByNs {
				rows, err := db.pool.Query(ctx, FmtNsID(eprQueryStateSQLTempl, ns), keys)
				if err != nil {
					pf.failed = true
					return
				}
				for rows.Next() {
					var k, val []byte
					var ver int64
					if err := rows.Scan(&k, &val, &ver); err != nil {
						rows.Close()
						pf.failed = true
						return
					}
					state[ns+"\x00"+string(k)] = eprState{value: val, version: uint64(ver)} //nolint:gosec
				}
				rows.Close()
				if rows.Err() != nil {
					pf.failed = true
					return
				}
				for _, k := range keys {
					queried[ns+"\x00"+string(k)] = true
				}
			}
			pf.state, pf.queried = state, queried
		}()
	}
	r := <-vr
	return r.c, pf, r.err
}

// EPRPrefetchStats reports keys served from the prefetch and keys queried afterwards.
func EPRPrefetchStats() (hits, misses int64) { return eprPrefetchHits.Load(), eprPrefetchMiss.Load() }
