package vc

import (
	"math"
	"testing"
)

func TestPilotEPRD2Codec(t *testing.T) {
	enc := func(d, l, u int64, w byte) []byte {
		b := append([]byte{}, eprMagic2...)
		for _, x := range []int64{d, l, u} {
			b = append(b, eprEncodeBalance(x)...)
		}
		return append(b, w)
	}
	d, l, u, w, ok := eprDecode2(enc(-5, 0, math.MaxInt64, 32))
	if !ok || d != -5 || l != 0 || u != math.MaxInt64 || w != 32 {
		t.Fatalf("decode2: %d %d %d %d %v", d, l, u, w, ok)
	}
	if _, _, _, _, ok := eprDecode2(enc(1, 1, 1, 7)); ok {
		t.Fatal("bad width accepted")
	}
	if _, _, ok := eprDecode(enc(1, 1, 1, 8)); ok {
		t.Fatal("EPRD2 decoded as EPRD1")
	}
	word := eprEncodeWidth(123456789, 32)
	if len(word) != 32 {
		t.Fatal("width")
	}
	if x, ok := eprBalanceOK(word); !ok || x != 123456789 {
		t.Fatalf("word parse %d %v", x, ok)
	}
	if x, ok := eprBalanceOK(nil); !ok || x != 0 {
		t.Fatal("empty")
	}
	big := make([]byte, 32)
	big[0] = 1
	if _, ok := eprBalanceOK(big); ok {
		t.Fatal("out-of-range word accepted")
	}
	hi := make([]byte, 32)
	hi[24] = 0x80
	if _, ok := eprBalanceOK(hi); ok {
		t.Fatal("word >= 2^63 accepted")
	}
	if _, ok := eprBalanceOK([]byte{1, 2, 3}); ok {
		t.Fatal("odd length accepted")
	}
}
