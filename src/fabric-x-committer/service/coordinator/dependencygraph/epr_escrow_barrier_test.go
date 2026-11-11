package dependencygraph

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

// eprTestNode builds a coordinator node writing value v to key k of namespace ns; plain writes are
// read-writes of the key, escrow debits are left to the classifier.
func eprTestNode(ns, k string, v []byte, plain bool) *TransactionNode {
	n := &TransactionNode{VCTx: &servicepb.VcTx{Namespaces: []*applicationpb.TxNamespace{{
		NsId:       ns,
		ReadWrites: []*applicationpb.ReadWrite{{Key: []byte(k), Value: v}},
	}}}}
	if plain {
		n.rwKeys.readsAndWrites = []string{constructCompositeKey(ns, []byte(k))}
	}
	return n
}

func eprTestDebit(delta, lower int64) []byte {
	b := append([]byte(nil), "EPRD2"...)
	b = binary.BigEndian.AppendUint64(b, uint64(delta)) //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, uint64(lower)) //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, math.MaxInt64)
	return append(b, 32)
}

// TestEPREscrowProgramBarrier: an integer plain write W, then a program output P on the same key; W
// finishes before P. The key's value is unknown until P finishes, so a debit classified in between must
// take the slow (ordered) path, and so must one classified after P.
func TestEPREscrowProgramBarrier(t *testing.T) {
	old := eprEscrowOn
	eprEscrowOn = true
	eprEscrowMu.Lock()
	eprKeys, eprLastBatchID = map[string]*eprKeyState{}, 0
	eprEscrowMu.Unlock()
	t.Cleanup(func() { eprEscrowOn = old })

	const ns, k = "ns1", "str:k" // signed content is recognized on storage keys only
	ck := constructCompositeKey(ns, []byte(k))
	never := func() bool { return false }

	w := eprTestNode(ns, k, binary.BigEndian.AppendUint64(nil, 100), true)
	p := eprTestNode(ns, k, []byte("EPRP-program-encoding-not-an-integer-value"), true)
	eprClassifyBatch(1, TxNodeBatch{w, p}, never)

	w.eprValid.Store(true)
	eprFinish(TxNodeBatch{w})

	d1 := eprTestNode(ns, k, eprTestDebit(-50, 50), false)
	eprClassifyBatch(2, TxNodeBatch{d1}, never)
	require.Contains(t, d1.rwKeys.readsAndWrites, ck, "debit after a pending program must be ordered")
	require.NotContains(t, d1.rwKeys.deltas, ck)

	p.eprValid.Store(true)
	eprFinish(TxNodeBatch{p})
	d2 := eprTestNode(ns, k, eprTestDebit(-50, 50), false)
	eprClassifyBatch(3, TxNodeBatch{d2}, never)
	require.Contains(t, d2.rwKeys.readsAndWrites, ck, "debit after a finished program must be ordered")

	// a later integer write re-establishes knowledge: once it finishes, debits it covers are fast again
	w2 := eprTestNode(ns, k, binary.BigEndian.AppendUint64(nil, 100), true)
	eprClassifyBatch(4, TxNodeBatch{w2}, never)
	d1.eprValid.Store(true)
	d2.eprValid.Store(true)
	w2.eprValid.Store(true)
	eprFinish(TxNodeBatch{d1, d2, w2})
	d3 := eprTestNode(ns, k, eprTestDebit(-50, 50), false)
	eprClassifyBatch(5, TxNodeBatch{d3}, never)
	require.Contains(t, d3.rwKeys.deltas, ck)
}
