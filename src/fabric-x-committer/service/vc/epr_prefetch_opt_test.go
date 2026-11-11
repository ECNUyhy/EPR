package vc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A program written to several output keys (as the endorser emits it) is decoded once per transaction; every
// copy resolves to that decoding, and its keys are listed once. Another transaction with the same program
// bytes gets its own decoding.
func TestEPRSignedKeysProgramCopies(t *testing.T) {
	p := &EPRProgram{
		Keys:    []string{"str:0xa:0x1", "str:0xa:0x2"},
		Nodes:   []EPRProgNode{{Op: EPROpVar, A: 0, B: -1, C: -1}, {Op: EPROpVar, A: 1, B: -1, C: -1}},
		Outputs: []EPRProgOut{{Key: 0, Node: 1}, {Key: 1, Node: 0}},
	}
	enc := func() []byte { return p.Encode() } // distinct slices with identical bytes, as unmarshalled
	nb := transactionToWrites{}
	nb.getOrCreate("t1", "basic").append([]byte("str:0xa:0x1"), enc(), 1)
	nb.getOrCreate("t1", "basic").append([]byte("str:0xa:0x2"), enc(), 1)
	nb.getOrCreate("t1", "basic").append([]byte("nonce"), []byte{1}, 1)
	nw := transactionToWrites{}
	nw.getOrCreate("t2", "basic").append([]byte("str:0xa:0x1"), enc(), 0)
	cache := map[*byte]*EPRProgram{}
	got := eprSignedKeys(cache, nb, nw)
	require.ElementsMatch(t, [][]byte{[]byte("str:0xa:0x1"), []byte("str:0xa:0x2")}, got["basic"])
	require.Len(t, got, 1)
	require.Len(t, cache, 3)
	w1 := nb["t1"]["basic"].values
	require.Same(t, cache[&w1[0][0]], cache[&w1[1][0]])
	w2 := nw["t2"]["basic"].values
	require.NotSame(t, cache[&w1[0][0]], cache[&w2[0][0]])
	pf := &eprPrefetched{progs: cache}
	for _, v := range [][]byte{w1[0], w1[1], w2[0]} {
		q, ok := pf.program(v)
		require.True(t, ok)
		require.Equal(t, p.Keys, q.Keys)
	}
}
