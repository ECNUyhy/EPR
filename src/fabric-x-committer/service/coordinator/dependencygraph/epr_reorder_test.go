package dependencygraph

// Baseline: Fabric++-style reordering.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

func TestEPRReorderReadersBeforeWriters(t *testing.T) {
	own := eprReorderOwn
	eprReorderOwn = true // the reimplementation (FX_REORDER=own)
	defer func() { eprReorderOwn = own }()
	keys := makeTestKeys(t, 1)
	writer := createTxForTest(t, 0, nsID1ForTest, nil, [][]byte{keys[0]}, nil) // reads and writes k
	reader := createTxForTest(t, 1, nsID1ForTest, [][]byte{keys[0]}, nil, nil) // only reads k
	out, aborted := eprReorder([]*servicepb.TxWithRef{writer, reader})
	require.Empty(t, aborted, "no cycle: nothing aborted")
	require.Equal(t, []*servicepb.TxWithRef{reader, writer}, out, "the reader is moved before the writer")
}

func TestEPRReorderBreaksReadModifyWriteCycles(t *testing.T) {
	own := eprReorderOwn
	eprReorderOwn = true // the reimplementation (FX_REORDER=own)
	defer func() { eprReorderOwn = own }()
	keys := makeTestKeys(t, 1)
	var txs []*servicepb.TxWithRef
	for i := range 5 {
		txs = append(txs, createTxForTest(t, i, nsID1ForTest, nil, [][]byte{keys[0]}, nil))
	}
	out, aborted := eprReorder(txs)
	require.Len(t, out, 5)
	require.Len(t, aborted, 4, "read-modify-writes of one key: only one can commit, as in plain EOV")
}
