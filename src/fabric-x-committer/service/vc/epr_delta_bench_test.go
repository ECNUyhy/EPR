package vc

import (
	"math"
	"testing"
)

// Committer-side cost of repairing one stale guarded delta on an EVM word, as the VC does it:
// decode the signed content, read the current balance, check the guard on the running value, encode the result.
//   DB_DEPLOYMENT=local go test -run XXX -bench EPRDeltaEval ./service/vc/
func BenchmarkEPRDeltaEval(b *testing.B) {
	enc := append([]byte{}, eprMagic2...)
	for _, x := range []int64{-5, 0, math.MaxInt64} {
		enc = append(enc, eprEncodeBalance(x)...)
	}
	enc = append(enc, 32)
	base := eprEncodeWidth(1000000, 32)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d, l, u, w, ok := eprDecode2(enc)
		x, okb := eprBalanceOK(base)
		v := x + d
		if !ok || !okb || v < l || v > u {
			b.Fatal("guard")
		}
		_ = eprEncodeWidth(v, w)
	}
}
