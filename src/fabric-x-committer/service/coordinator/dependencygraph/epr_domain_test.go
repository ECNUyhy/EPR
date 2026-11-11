package dependencygraph

// The coordinator recognizes guarded deltas exactly where the VC does: on storage keys ("str:...") only.
// A non-storage key with such bytes is an ordinary (ordered) read-write.

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func eprDomainDelta(d, lower int64) []byte {
	b := append([]byte(nil), "EPRD2"...)
	b = binary.BigEndian.AppendUint64(b, uint64(d))     //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, uint64(lower)) //nolint:gosec
	b = binary.BigEndian.AppendUint64(b, math.MaxInt64)
	return append(b, 32)
}

func TestEPRDomainSeparationDepGraph(t *testing.T) { //nolint:paralleltest // toggles package flags
	oldDG, oldEsc := eprDepGraph, eprEscrowOn
	eprDepGraph, eprEscrowOn = true, true
	t.Cleanup(func() { eprDepGraph, eprEscrowOn = oldDG, oldEsc })

	credit, debit := eprDomainDelta(5, 0), eprDomainDelta(-5, 0)
	storage := []byte("str:0xa:0x1")
	for _, k := range [][]byte{[]byte("acc:0xa:code"), []byte("acc:0xa:nonce"), []byte("k")} {
		require.False(t, eprCommutes(k, credit), "%s", k)
		require.False(t, eprIsEscrowDebit(k, debit), "%s", k)
	}
	require.True(t, eprCommutes(storage, credit))
	require.True(t, eprIsEscrowDebit(storage, debit))

	// a credit-shaped value on a non-storage key keeps an ordinary read-write dependency
	tx := createTxForTest(t, 0, nsID1ForTest, nil, [][]byte{[]byte("acc:0xa:code"), storage}, nil)
	tx.Content.Namespaces[0].ReadWrites[0].Value = credit
	tx.Content.Namespaces[0].ReadWrites[1].Value = credit
	n := newTransactionNode(tx)
	require.Equal(t, []string{constructCompositeKey(nsID1ForTest, []byte("acc:0xa:code"))}, n.rwKeys.readsAndWrites)
	require.Equal(t, []string{constructCompositeKey(nsID1ForTest, storage)}, n.rwKeys.deltas)
}
