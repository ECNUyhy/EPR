package dependencygraph

// Baseline: glue of the original Fabric++ scheduler (epr_fpp_orig.go).

import (
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

func TestFppOrig(t *testing.T) {
	keys := makeTestKeys(t, 1)
	t.Run("reader is scheduled before the writer", func(t *testing.T) {
		writer := createTxForTest(t, 0, nsID1ForTest, nil, [][]byte{keys[0]}, nil)
		reader := createTxForTest(t, 1, nsID1ForTest, [][]byte{keys[0]}, nil, nil)
		out, aborted := eprFppOrig([]*servicepb.TxWithRef{writer, reader})
		require.Empty(t, aborted)
		require.Equal(t, []*servicepb.TxWithRef{reader, writer}, out)
	})
	t.Run("read-modify-write cycle aborts one transaction", func(t *testing.T) {
		a := createTxForTest(t, 0, nsID1ForTest, nil, [][]byte{keys[0]}, nil)
		b := createTxForTest(t, 1, nsID1ForTest, nil, [][]byte{keys[0]}, nil)
		out, aborted := eprFppOrig([]*servicepb.TxWithRef{a, b})
		require.Len(t, out, 2)
		require.Equal(t, map[int]bool{1: true}, aborted)
	})
	t.Run("different versions of a key read in a block stay for the validator", func(t *testing.T) {
		v4, v5 := uint64(4), uint64(5)
		a := createTxForTest(t, 0, nsID1ForTest, [][]byte{keys[0]}, nil, nil)
		b := createTxForTest(t, 1, nsID1ForTest, [][]byte{keys[0]}, nil, nil)
		a.Content.Namespaces[0].ReadsOnly[0].Version = &v5
		b.Content.Namespaces[0].ReadsOnly[0].Version = &v4
		out, aborted := eprFppOrig([]*servicepb.TxWithRef{a, b})
		require.Empty(t, aborted)
		require.ElementsMatch(t, []*servicepb.TxWithRef{a, b}, out)
	})
	t.Run("system transactions are put ahead", func(t *testing.T) {
		w := createTxForTest(t, 0, nsID1ForTest, nil, [][]byte{keys[0]}, nil)
		r := createTxForTest(t, 1, nsID1ForTest, [][]byte{keys[0]}, nil, nil)
		sys := &servicepb.TxWithRef{Ref: createTxForTest(t, 2, nsID1ForTest, nil, nil, nil).Ref, Content: &applicationpb.Tx{
			Namespaces: []*applicationpb.TxNamespace{{NsId: committerpb.MetaNamespaceID}},
		}}
		out, _ := eprFppOrig([]*servicepb.TxWithRef{w, r, sys})
		require.Equal(t, sys, out[0])
	})
}
