package vc

// Stale reads are decided per transaction, so a batch and any split of it abort exactly the same readers, and a
// reader that keeps its stale read takes no part in the batch's evaluation. Committed values are served as
// prefetched, so these tests need no database.

import (
	"context"
	"math"
	"slices"
	"sort"
	"testing"

	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"
)

// eprRunStale runs the VC's EPR step on one batch whose transactions read the given keys at a stale version
// (stale: key -> readers). It returns the readers that keep a stale read, and the batch.
func eprRunStale(t *testing.T, nb transactionToWrites, committed map[string]eprState, stale map[string][]TxID,
	keys ...string) ([]TxID, *validatedTransactions) {
	t.Helper()
	oldOn, oldFast, oldRO := eprEnabled, eprFastPath, eprROSnapshot
	eprEnabled, eprFastPath, eprROSnapshot = true, false, true
	t.Cleanup(func() { eprEnabled, eprFastPath, eprROSnapshot = oldOn, oldFast, oldRO })
	pf := &eprPrefetched{state: map[string]eprState{}, queried: map[string]bool{}, done: make(chan bool)}
	close(pf.done)
	for _, k := range keys {
		pf.queried[eprTestNs+"\x00"+k] = true
		if st, ok := committed[k]; ok {
			pf.state[eprTestNs+"\x00"+k] = st
		}
	}
	conflicts := namespaceToReads{}
	rt := readToTransactions{}
	for k, txs := range stale {
		ver := uint64(0)
		conflicts.getOrCreate(eprTestNs).append([]byte(k), &ver)
		rt[newCmpRead(eprTestNs, []byte(k), &ver)] = append([]TxID{}, txs...)
	}
	v := &validatedTransactions{
		validTxNonBlindWrites: nb,
		validTxBlindWrites:    transactionToWrites{},
		newWrites:             transactionToWrites{},
		invalidTxStatus:       map[TxID]committerpb.Status{},
		readToTxIDs:           rt,
		eprPreWait:            func() *eprPrefetched { return pf },
	}
	rem, err := v.pilotRepairDeltas(context.Background(), nil, conflicts)
	require.NoError(t, err)
	var kept []TxID
	for nsID, rc := range rem {
		for i, key := range rc.keys {
			kept = append(kept, v.readToTxIDs[newCmpRead(nsID, key, rc.versions[i])]...)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i] < kept[j] })
	return slices.Compact(kept), v
}

const (
	eprStH = "str:0xa:0x1" // hot balance
	eprStK = "str:0xa:0x2" // another slot
)

// the transactions of the tests: a covered delta on H, a query (only a nonce write), a plain writer, a query
// that also moves an account balance, and a delta on H that also has an uncovered stale read of K
func eprStaleTxs() transactionToWrites {
	nb := transactionToWrites{}
	nb.getOrCreate("d1", eprTestNs).append([]byte(eprStH), eprTestDelta(5, 0, math.MaxInt64), 1)
	nb.getOrCreate("q1", eprTestNs).append([]byte("acc:0xq:nonce"), eprTestWord(1), 1)
	nb.getOrCreate("x1", eprTestNs).append([]byte(eprStK), eprTestWord(7), 1)
	nb.getOrCreate("q2", eprTestNs).append([]byte("acc:0xq:balance"), eprTestWord(3), 1)
	nb.getOrCreate("d2", eprTestNs).append([]byte(eprStH), eprTestDelta(7, 0, math.MaxInt64), 1)
	return nb
}

// eprSubset returns fresh copies of the given transactions (a run rewrites the writes it evaluates)
func eprSubset(_ transactionToWrites, txs ...TxID) transactionToWrites {
	fresh := eprStaleTxs()
	out := transactionToWrites{}
	for _, tx := range txs {
		out[tx] = fresh[tx]
	}
	return out
}

func TestEPRStaleReadsPerTransaction(t *testing.T) {
	committed := map[string]eprState{eprStH: {value: eprTestWord(100), version: 1}, eprStK: {value: eprTestWord(0), version: 1}}
	all := eprStaleTxs()

	// (a) a covered delta, a query and a plain reader share one stale read: only the plain reader keeps it
	kept, v := eprRunStale(t, eprSubset(all, "d1", "q1", "x1"), committed,
		map[string][]TxID{eprStH: {"d1", "q1", "x1"}}, eprStH, eprStK)
	require.Equal(t, []TxID{"x1"}, kept)
	val, _, ok := eprWriteOf(v.validTxNonBlindWrites, "d1", eprStH)
	require.True(t, ok)
	require.Equal(t, eprTestWord(105), val)

	// (b) a query that also writes an account balance is not a query: it keeps its stale read
	kept, _ = eprRunStale(t, eprSubset(all, "q2"), committed, map[string][]TxID{eprStH: {"q2"}}, eprStH)
	require.Equal(t, []TxID{"q2"}, kept)

	// (c) the batch and every single transaction of it keep the same readers; a query alone is committed at
	// its snapshot too (no early return for batches without signed content)
	batch := []TxID{"d1", "q1", "x1", "q2"}
	keptBatch, _ := eprRunStale(t, eprSubset(all, batch...), committed, map[string][]TxID{eprStH: batch}, eprStH, eprStK)
	var keptSingles []TxID
	for _, tx := range batch {
		k, _ := eprRunStale(t, eprSubset(all, tx), committed, map[string][]TxID{eprStH: {tx}}, eprStH, eprStK)
		keptSingles = append(keptSingles, k...)
	}
	sort.Slice(keptSingles, func(i, j int) bool { return keptSingles[i] < keptSingles[j] })
	require.Equal(t, keptBatch, keptSingles)
	require.Equal(t, []TxID{"q2", "x1"}, keptBatch)

	// (d) a delta whose transaction keeps another stale read (K, uncovered) is left out of the evaluation: the
	// valid delta d2 on H lands on the committed 100, not on 100 + 5
	kept, v = eprRunStale(t, eprSubset(all, "d1", "d2"), committed,
		map[string][]TxID{eprStH: {"d1", "d2"}, eprStK: {"d1"}}, eprStH, eprStK)
	require.Equal(t, []TxID{"d1"}, kept)
	val, _, ok = eprWriteOf(v.validTxNonBlindWrites, "d2", eprStH)
	require.True(t, ok)
	require.Equal(t, eprTestWord(107), val)
}

func TestEPRStaleKeepPerTransaction(t *testing.T) {
	kind := map[TxID]string{"d1": "delta", "q1": "query", "x1": "plain"}
	keep, nr, ns := eprStaleKeep([]TxID{"d1", "q1", "x1"},
		func(tx TxID) bool { return kind[tx] == "delta" }, func(tx TxID) bool { return kind[tx] == "query" })
	require.Equal(t, []TxID{"x1"}, keep)
	require.Equal(t, 1, nr)
	require.Equal(t, 1, ns)
}

// A delta that brings a contract-storage balance to zero writes it as the endorser does (no value), so a key's
// bytes do not depend on whether it was endorsed or evaluated; other values keep the 32-byte word.
func TestEPRZeroStorageCanonical(t *testing.T) {
	committed := map[string]eprState{eprStH: {value: eprTestWord(100), version: 1}, eprStK: {value: eprTestWord(40), version: 1}}
	nb := transactionToWrites{}
	nb.getOrCreate("z1", eprTestNs).append([]byte(eprStH), eprTestDelta(-100, 0, math.MaxInt64), 1)
	nb.getOrCreate("z2", eprTestNs).append([]byte(eprStK), eprTestDelta(-30, 0, math.MaxInt64), 1)
	_, v := eprRunStale(t, nb, committed, map[string][]TxID{}, eprStH, eprStK)
	val, _, ok := eprWriteOf(v.validTxNonBlindWrites, "z1", eprStH)
	require.True(t, ok)
	require.NotNil(t, val)
	require.Empty(t, val)
	val, _, ok = eprWriteOf(v.validTxNonBlindWrites, "z2", eprStK)
	require.True(t, ok)
	require.Equal(t, eprTestWord(10), val)
	require.Equal(t, make([]byte, 8), eprCanonStorage("acc:0xq:balance", make([]byte, 8)))
}
