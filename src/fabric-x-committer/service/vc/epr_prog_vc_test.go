package vc

import (
	"math/big"
	"testing"
)

func TestEPRProgramCodecAndEval(t *testing.T) {
	// out = (x * y) / (x + 7), guard x > 3
	p := &EPRProgram{
		Keys: []string{"kx", "ky", "kout"},
		Nodes: []EPRProgNode{
			{Op: EPROpVar, A: 0, B: -1, C: -1},
			{Op: EPROpVar, A: 1, B: -1, C: -1},
			{Op: 0x02, A: 0, B: 1, C: -1},
			{Op: EPROpConst, A: -1, B: -1, C: -1, Const: []byte{7}},
			{Op: 0x01, A: 0, B: 3, C: -1},
			{Op: 0x04, A: 2, B: 4, C: -1},
			{Op: EPROpConst, A: -1, B: -1, C: -1, Const: []byte{3}},
			{Op: 0x11, A: 0, B: 6, C: -1},
		},
		Guards:  []EPRProgGuard{{Node: 7, Kind: 1, Value: []byte{1}}},
		Outputs: []EPRProgOut{{Key: 2, Node: 5}},
	}
	q, ok := DecodeEPRProgram(p.Encode())
	if !ok {
		t.Fatal("decode failed")
	}
	vals := map[string][]byte{"kx": big.NewInt(10).Bytes(), "ky": big.NewInt(34).Bytes()}
	out, ok := q.Eval(func(k string) []byte { return vals[k] })
	if !ok || new(big.Int).SetBytes(out["kout"]).Int64() != 20 { // 340 / 17
		t.Fatalf("eval: ok=%v out=%x", ok, out["kout"])
	}
	vals["kx"] = big.NewInt(2).Bytes()
	if _, ok := q.Eval(func(k string) []byte { return vals[k] }); ok {
		t.Fatal("guard x > 3 should fail")
	}
}
