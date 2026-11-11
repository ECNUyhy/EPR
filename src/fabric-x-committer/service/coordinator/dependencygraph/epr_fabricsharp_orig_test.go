package dependencygraph

// Baseline: glue of the authors' FabricSharp scheduler (epr_fabricsharp_orig.go).

import (
	"strconv"
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

// fsSnapTx is fsTx with the endorser's snapshot height carried as in patch_fs_snapshot.py.
func fsSnapTx(block uint64, num uint32, id string, snap int, op fsOp) *servicepb.TxWithRef {
	tx := fsTx(block, num, id, op)
	ns := tx.Content.Namespaces[0]
	ns.BlindWrites = append(ns.BlindWrites, &applicationpb.Write{
		Key: []byte(fsSnapPrefix + id), Value: []byte(strconv.Itoa(snap)),
	})
	return tx
}

func fsOrigReset() {
	fsOrig, fsHist = nil, map[string]*fsVerHist{}
}

func TestFabricSharpOrig(t *testing.T) {
	on, exact := FabricSharpOn, fsExact
	FabricSharpOn, fsExact = true, false
	defer func() { FabricSharpOn, fsExact = on, exact; fsOrigReset() }()

	t.Run("hot-key read-modify-write keeps one per block", func(t *testing.T) {
		fsOrigReset()
		kept, aborted := fsRun(t, 1,
			fsSnapTx(1, 0, "a", 0, fsOp{rmw: map[string]int64{"k": 5}}),
			fsSnapTx(1, 1, "b", 0, fsOp{rmw: map[string]int64{"k": 5}}),
			fsSnapTx(1, 2, "c", 0, fsOp{rmw: map[string]int64{"k": 5}}))
		require.Equal(t, []string{"a"}, kept)
		require.Equal(t, []string{"b", "c"}, aborted)
	})

	t.Run("snapshot older than max_span is dropped", func(t *testing.T) {
		fsOrigReset()
		kept, aborted := fsRun(t, 30,
			fsSnapTx(30, 0, "old", 3, fsOp{read: map[string]int64{"x": 0}, write: []string{"y"}}),
			fsSnapTx(30, 1, "new", 25, fsOp{read: map[string]int64{"x": 0}, write: []string{"z"}}))
		require.Equal(t, []string{"new"}, kept)
		require.Equal(t, []string{"old"}, aborted)
	})

	t.Run("a version committed after the carried height raises the snapshot", func(t *testing.T) {
		fsOrigReset()
		fsRun(t, 1, fsSnapTx(1, 0, "seed", 0, fsOp{rmw: map[string]int64{"k": 0}})) // k: version 1 in block 1
		fsRun(t, 15, fsSnapTx(15, 0, "w", 14, fsOp{rmw: map[string]int64{"k": 1}})) // k: version 2 in block 15
		// r read version 2, so its snapshot is at least block 15 whatever its carried height says
		kept, _ := fsRun(t, 22, fsSnapTx(22, 0, "r", 3, fsOp{read: map[string]int64{"k": 2}, write: []string{"q"}}))
		require.Equal(t, []string{"r"}, kept, "snapshot = block 15 >= 22-10, although the carrier says 3")
	})

	t.Run("validator sees no reads and no snapshot carrier", func(t *testing.T) {
		fsOrigReset()
		tx := fsSnapTx(1, 0, "s", 0, fsOp{read: map[string]int64{"A": 0}, rmw: map[string]int64{"B": 0}})
		kept, _ := fsRun(t, 1, tx)
		require.Equal(t, []string{"s"}, kept)
		ns := fsVCNamespaces(tx)
		require.Empty(t, ns[0].ReadsOnly)
		require.Empty(t, ns[0].ReadWrites)
		require.Len(t, ns[0].BlindWrites, 1)
		require.Equal(t, "B", string(ns[0].BlindWrites[0].Key))
	})

	t.Run("write skew through a committed transaction aborts", func(t *testing.T) {
		fsOrigReset()
		fsRun(t, 1, fsSnapTx(1, 0, "t1", 0, fsOp{read: map[string]int64{"B": 0}, rmw: map[string]int64{"A": 0}}))
		// t2 simulated at block 0 (before t1): reads A (t1 overwrote it) and writes B (t1 read it)
		kept, aborted := fsRun(t, 2, fsSnapTx(2, 0, "t2", 0, fsOp{read: map[string]int64{"A": 0}, rmw: map[string]int64{"B": 0}}))
		require.Empty(t, kept)
		require.Equal(t, []string{"t2"}, aborted)
	})

	t.Run("random hot-key stress: no panic, every transaction kept or aborted", func(t *testing.T) {
		fsOrigReset()
		rnd := uint64(42)
		next := func(n int) int { rnd = rnd*6364136223846793005 + 1442695040888963407; return int(rnd>>33) % n }
		for b := uint64(1); b <= 300; b++ {
			var txs []*servicepb.TxWithRef
			for i := 0; i < 20; i++ {
				op := fsOp{read: map[string]int64{}, rmw: map[string]int64{}}
				for j := 0; j < 2; j++ {
					k := string(rune('A' + next(6)))
					if next(2) == 0 {
						op.read[k] = int64(next(3))
					} else {
						op.rmw[k] = int64(next(3))
					}
				}
				snap := int(b) - 1 - next(3)
				txs = append(txs, fsSnapTx(b, uint32(i), "t"+strconv.Itoa(int(b))+"_"+strconv.Itoa(i), snap, op)) //nolint:gosec
			}
			blk := &servicepb.CoordinatorBatch{Txs: txs}
			require.NotPanics(t, func() { FabricSharpBlock(blk) })
			require.Equal(t, 20, len(blk.Txs)+len(blk.Rejected))
		}
	})
}
