package execution

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/holiman/uint256"
)

// TestEPREvalOpMatchesEVM cross-checks the program evaluator's opcode semantics against go-ethereum's
// uint256 implementation (the arithmetic the EVM itself uses) on random and boundary operands.
func TestEPREvalOpMatchesEVM(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	edges := []*uint256.Int{
		uint256.NewInt(0), uint256.NewInt(1), uint256.NewInt(2), uint256.NewInt(31), uint256.NewInt(32),
		uint256.NewInt(255), uint256.NewInt(256), uint256.NewInt(300),
		new(uint256.Int).Lsh(uint256.NewInt(1), 255), new(uint256.Int).Not(uint256.NewInt(0)),
		new(uint256.Int).Sub(new(uint256.Int).Lsh(uint256.NewInt(1), 255), uint256.NewInt(1)),
	}
	rnd := func() *uint256.Int {
		switch rng.Intn(4) {
		case 0:
			return new(uint256.Int).Set(edges[rng.Intn(len(edges))])
		case 1:
			return uint256.NewInt(rng.Uint64() % 1000)
		default:
			var b [32]byte
			rng.Read(b[:])
			n := rng.Intn(33)
			for i := 0; i < 32-n; i++ {
				b[i] = 0
			}
			return new(uint256.Int).SetBytes(b[:])
		}
	}
	shift := func(a, x *uint256.Int, f func(z, x *uint256.Int, n uint) *uint256.Int, sar bool) *uint256.Int {
		if a.GtUint64(255) {
			if sar && x.Sign() < 0 {
				return new(uint256.Int).Not(uint256.NewInt(0))
			}
			return uint256.NewInt(0)
		}
		return f(new(uint256.Int), x, uint(a.Uint64()))
	}
	ref := map[byte]func(a, b, c *uint256.Int) *uint256.Int{
		0x01: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Add(a, b) },
		0x02: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Mul(a, b) },
		0x03: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Sub(a, b) },
		0x04: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Div(a, b) },
		0x05: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).SDiv(a, b) },
		0x06: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Mod(a, b) },
		0x07: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).SMod(a, b) },
		0x08: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).AddMod(a, b, c) },
		0x09: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).MulMod(a, b, c) },
		0x0a: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Exp(a, b) },
		0x0b: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).ExtendSign(b, a) },
		0x10: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.Lt(b)) },
		0x11: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.Gt(b)) },
		0x12: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.Slt(b)) },
		0x13: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.Sgt(b)) },
		0x14: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.Eq(b)) },
		0x15: func(a, b, c *uint256.Int) *uint256.Int { return eprU(a.IsZero()) },
		0x16: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).And(a, b) },
		0x17: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Or(a, b) },
		0x18: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Xor(a, b) },
		0x19: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Not(a) },
		0x1a: func(a, b, c *uint256.Int) *uint256.Int { return new(uint256.Int).Set(b).Byte(a) },
		0x1b: func(a, b, c *uint256.Int) *uint256.Int { return shift(a, b, (*uint256.Int).Lsh, false) },
		0x1c: func(a, b, c *uint256.Int) *uint256.Int { return shift(a, b, (*uint256.Int).Rsh, false) },
		0x1d: func(a, b, c *uint256.Int) *uint256.Int { return shift(a, b, (*uint256.Int).SRsh, true) },
	}
	for op, f := range ref {
		for i := 0; i < 3000; i++ {
			a, b, c := rnd(), rnd(), rnd()
			want := f(a, b, c)
			got, ok := EPREvalOp(op, a.ToBig(), b.ToBig(), c.ToBig())
			if !ok || got.Cmp(want.ToBig()) != 0 {
				t.Fatalf("op %#x a=%s b=%s c=%s: got %v want %s", op, a.Hex(), b.Hex(), c.Hex(), got, want.Hex())
			}
		}
	}
}

func eprU(b bool) *uint256.Int {
	if b {
		return uint256.NewInt(1)
	}
	return uint256.NewInt(0)
}

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
