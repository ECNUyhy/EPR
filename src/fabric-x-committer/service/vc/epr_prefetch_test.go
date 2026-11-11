package vc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The prefetch covers exactly the keys with signed content (here a guarded delta), not plain writes.
func TestEPRSignedKeys(t *testing.T) {
	d2 := append([]byte("EPRD2"), make([]byte, 24)...)
	d2 = append(d2, 8) // width byte
	nb := transactionToWrites{}
	nb.getOrCreate("t1", "basic").append([]byte("str:0xa:0x1"), d2, 1)
	nb.getOrCreate("t1", "basic").append([]byte("str:0xa:0x2"), []byte("plain"), 1)
	nw := transactionToWrites{}
	nw.getOrCreate("t2", "basic").append([]byte("str:0xa:0x1"), d2, 0) // same key twice: listed once
	got := eprSignedKeys(nil, nb, nw)
	require.Equal(t, map[string][][]byte{"basic": {[]byte("str:0xa:0x1")}}, got)
	require.Empty(t, eprSignedKeys(nil, transactionToWrites{}))
}
