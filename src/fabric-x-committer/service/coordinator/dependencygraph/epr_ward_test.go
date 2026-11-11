package dependencygraph

import (
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

// The example of HTFabric's Figure 4: the WARD order is T6, T4, T5, T1, T3, T2.
func TestWardFigure4(t *testing.T) {
	mk := func(id string, reads, writes []string) *servicepb.TxWithRef {
		ns := &applicationpb.TxNamespace{NsId: "n"}
		w := map[string]bool{}
		for _, k := range writes {
			w[k] = true
		}
		for _, k := range reads {
			if w[k] {
				ns.ReadWrites = append(ns.ReadWrites, &applicationpb.ReadWrite{Key: []byte(k)})
				delete(w, k)
			} else {
				ns.ReadsOnly = append(ns.ReadsOnly, &applicationpb.Read{Key: []byte(k)})
			}
		}
		for _, k := range writes {
			if w[k] {
				ns.BlindWrites = append(ns.BlindWrites, &applicationpb.Write{Key: []byte(k)})
			}
		}
		tx := fsTx(1, 0, id, fsOp{})
		tx.Content = &applicationpb.Tx{Namespaces: []*applicationpb.TxNamespace{ns}}
		return tx
	}
	txs := []*servicepb.TxWithRef{
		mk("T1", nil, []string{"K2"}),
		mk("T2", []string{"K1", "K5"}, []string{"K1", "K3"}),
		mk("T3", []string{"K1"}, []string{"K1"}),
		mk("T4", []string{"K2"}, []string{"K2", "K4"}),
		mk("T5", []string{"K3"}, []string{"K5"}),
		mk("T6", []string{"K1"}, []string{"K4"}),
	}
	var got []string
	for _, tx := range eprWard(txs) {
		got = append(got, tx.Ref.TxId)
	}
	require.Equal(t, []string{"T6", "T4", "T5", "T1", "T3", "T2"}, got)
}
