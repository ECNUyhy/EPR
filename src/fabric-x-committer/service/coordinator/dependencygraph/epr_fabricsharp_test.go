package dependencygraph

import (
	"testing"

	"github.com/hyperledger/fabric-x-common/api/applicationpb"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
	"github.com/stretchr/testify/require"

	"github.com/hyperledger/fabric-x-committer/api/servicepb"
)

type fsOp struct {
	read  map[string]int64 // key -> version read (-1: absent)
	write []string
	rmw   map[string]int64 // read-modify-write
}

func fsTx(block uint64, num uint32, id string, op fsOp) *servicepb.TxWithRef {
	ns := &applicationpb.TxNamespace{NsId: "basic"}
	ver := func(v int64) *uint64 {
		if v < 0 {
			return nil
		}
		u := uint64(v) //nolint:gosec
		return &u
	}
	for k, v := range op.read {
		ns.ReadsOnly = append(ns.ReadsOnly, &applicationpb.Read{Key: []byte(k), Version: ver(v)})
	}
	for k, v := range op.rmw {
		ns.ReadWrites = append(ns.ReadWrites, &applicationpb.ReadWrite{Key: []byte(k), Version: ver(v), Value: []byte("x")})
	}
	for _, k := range op.write {
		ns.BlindWrites = append(ns.BlindWrites, &applicationpb.Write{Key: []byte(k), Value: []byte("x")})
	}
	return &servicepb.TxWithRef{
		Ref:     &committerpb.TxRef{BlockNum: block, TxNum: num, TxId: id},
		Content: &applicationpb.Tx{Namespaces: []*applicationpb.TxNamespace{ns}},
	}
}

func fsRun(t *testing.T, block uint64, txs ...*servicepb.TxWithRef) (kept []string, aborted []string) {
	t.Helper()
	blk := &servicepb.CoordinatorBatch{Txs: txs}
	FabricSharpBlock(blk)
	for _, tx := range blk.Txs {
		kept = append(kept, tx.Ref.TxId)
	}
	for _, r := range blk.Rejected {
		aborted = append(aborted, r.Ref.TxId)
	}
	return kept, aborted
}

func TestFabricSharp(t *testing.T) {
	on, exact := FabricSharpOn, fsExact
	FabricSharpOn, fsExact = true, true // the idealized reimplementation (FX_FABRICSHARP=exact)
	defer func() { FabricSharpOn, fsExact = on, exact }()
	t.Run("hot-key read-modify-write keeps one per version", func(t *testing.T) {
		fsState = newFabricSharp()
		kept, aborted := fsRun(t, 1,
			fsTx(1, 0, "a", fsOp{rmw: map[string]int64{"k": 5}}),
			fsTx(1, 1, "b", fsOp{rmw: map[string]int64{"k": 5}}),
			fsTx(1, 2, "c", fsOp{rmw: map[string]int64{"k": 5}}))
		require.Equal(t, []string{"a"}, kept)
		require.Equal(t, []string{"b", "c"}, aborted)
		// next block: a stale read-modify-write (read 5, a wrote 6) is a lost update
		kept, aborted = fsRun(t, 2, fsTx(2, 0, "d", fsOp{rmw: map[string]int64{"k": 5}}),
			fsTx(2, 1, "e", fsOp{rmw: map[string]int64{"k": 6}}))
		require.Equal(t, []string{"e"}, kept)
		require.Equal(t, []string{"d"}, aborted)
	})

	t.Run("stale read without cycle is serialized before the overwriter", func(t *testing.T) {
		fsState = newFabricSharp()
		_, aborted := fsRun(t, 1, fsTx(1, 0, "w", fsOp{rmw: map[string]int64{"A": 0}})) // A: 0 -> 1
		require.Empty(t, aborted)
		// reads A at the old version 0 and writes B: serializable before w (Fabric/Fabric++ would abort)
		kept, aborted := fsRun(t, 2, fsTx(2, 0, "s", fsOp{read: map[string]int64{"A": 0}, rmw: map[string]int64{"B": 0}}))
		require.Equal(t, []string{"s"}, kept)
		require.Empty(t, aborted)
		ns := fsVCNamespaces(fsTx(2, 0, "s", fsOp{}))
		require.Empty(t, ns[0].ReadsOnly)
		require.Empty(t, ns[0].ReadWrites)
		require.Len(t, ns[0].BlindWrites, 1)
	})

	t.Run("write skew cycle through a committed transaction aborts", func(t *testing.T) {
		fsState = newFabricSharp()
		// t1 reads B@0 and writes A; committed
		fsRun(t, 1, fsTx(1, 0, "t1", fsOp{read: map[string]int64{"B": 0}, rmw: map[string]int64{"A": 0}}))
		// t2 read A@0 (stale: t1 wrote A) and writes B (t1 read B): t2 -> t1 (anti-rw), t1 -> t2 (rw)
		kept, aborted := fsRun(t, 2, fsTx(2, 0, "t2", fsOp{read: map[string]int64{"A": 0}, rmw: map[string]int64{"B": 0}}))
		require.Empty(t, kept)
		require.Equal(t, []string{"t2"}, aborted)
	})

	t.Run("last writer that left the window is not given new edges", func(t *testing.T) {
		fsState = newFabricSharp()
		fsRun(t, 1, fsTx(1, 0, "w", fsOp{rmw: map[string]int64{"k": 0}})) // k: 0 -> 1
		for b := uint64(2); b < 2+fsWindowBlocks+2; b++ {                 // unrelated blocks: w leaves the window
			fsRun(t, b, fsTx(b, 0, "x"+string(rune('a'+b)), fsOp{rmw: map[string]int64{"other": int64(b - 2)}}))
		}
		b := uint64(2 + fsWindowBlocks + 2)
		kept, aborted := fsRun(t, b, fsTx(b, 0, "late", fsOp{rmw: map[string]int64{"k": 1}}))
		require.Equal(t, []string{"late"}, kept)
		require.Empty(t, aborted)
	})

	t.Run("random hot-key stress: no panic, accepted schedules stay acyclic", func(t *testing.T) {
		fsState = newFabricSharp()
		rnd := uint64(42)
		next := func(n int) int { rnd = rnd*6364136223846793005 + 1442695040888963407; return int(rnd>>33) % n }
		ver := map[string]int64{}
		for b := uint64(1); b <= 2000; b++ {
			var txs []*servicepb.TxWithRef
			for i := 0; i < 20; i++ {
				op := fsOp{read: map[string]int64{}, rmw: map[string]int64{}}
				for j := 0; j < 2; j++ {
					k := string(rune('A' + next(4))) // 4 hot keys
					stale := int64(next(3))          // read 0-2 versions behind
					v := ver[k] - stale
					if v < 0 {
						v = 0
					}
					if next(2) == 0 {
						op.read[k] = v
					} else {
						op.rmw[k] = v
					}
				}
				txs = append(txs, fsTx(b, uint32(i), "t"+string(rune(b))+string(rune(i)), op)) //nolint:gosec
			}
			blk := &servicepb.CoordinatorBatch{Txs: txs}
			require.NotPanics(t, func() { FabricSharpBlock(blk) })
			require.Equal(t, 20, len(blk.Txs)+len(blk.Rejected))
			for _, tx := range blk.Txs { // advance the versions the accepted transactions wrote
				for _, rw := range tx.Content.Namespaces[0].ReadWrites {
					ver[string(rw.Key)]++
				}
			}
		}
	})

	t.Run("pending reader is ordered before a pending blind writer", func(t *testing.T) {
		fsState = newFabricSharp()
		kept, aborted := fsRun(t, 1,
			fsTx(1, 0, "w", fsOp{write: []string{"k"}}),
			fsTx(1, 1, "r", fsOp{read: map[string]int64{"k": 3}, write: []string{"j"}}))
		require.Empty(t, aborted)
		require.Equal(t, []string{"r", "w"}, kept)
	})
}
