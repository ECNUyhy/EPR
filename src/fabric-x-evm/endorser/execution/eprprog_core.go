package execution

// Signed straight-line programs with path guards (generalised endorsement-preserving repair).
//
// An endorser that cannot express a transaction's storage writes as guarded deltas records instead the
// arithmetic that produced them: every value derived from a storage read is a node of an expression DAG
// over the transaction's input slots, with EVM (uint256, wrap-around) semantics; every point where such a
// value influenced execution (branch condition, jump target, memory/storage address, call target, hash
// input) is a guard. The committer and every state replica evaluate the program on the latest values of
// the input slots; if every guard holds, the EVM would have followed exactly the endorsed path, so the
// program's outputs are exactly the writes of a serial re-execution (Lemma 2 of the paper).
//
// This file depends only on the Go standard library so that the committer and the endorser share it
// verbatim (the committer copy only changes the package clause).
//
// Encoding:
//   "EPRP1" | u16 nKeys | nKeys * (u16 len | key) |
//   u32 nNodes | nNodes * (u8 op | i32 a | i32 b | i32 c | [op==CONST: u8 len | value]) |
//   u16 nGuards | nGuards * (u32 node | u8 kind | [kind==0: u8 len | value]) |
//   u16 nOut | nOut * (u16 key | u32 node)
// op: eprOpVar (a = key index), eprOpConst, or an EVM opcode whose operands a, b, c are node indices in
// EVM pop order (a = top of stack). Guard kind 0: node == value; kind 1: (node != 0) == (value byte != 0).

import (
	"encoding/binary"
	"errors"
	"math/big"
)

const (
	EPROpVar   = 0x00 // reserved: STOP never produces a value
	EPROpConst = 0xfe // reserved: INVALID never produces a value
)

// EPRProgMagic prefixes an encoded program.
var EPRProgMagic = []byte("EPRP1")

type EPRProgNode struct {
	Op      byte
	A, B, C int32
	Const   []byte // big-endian value for EPROpConst
}

type EPRProgGuard struct {
	Node  int32
	Kind  byte   // 0: equality with Value; 1: truthiness equals Value[0] != 0
	Value []byte // big-endian
}

type EPRProgOut struct {
	Key  uint16
	Node int32
}

type EPRProgram struct {
	Keys    []string
	Nodes   []EPRProgNode
	Guards  []EPRProgGuard
	Outputs []EPRProgOut
	// Endorsed holds, per output, the value the endorser computed (optional trailing section). A
	// committer may write these directly when none of the program's keys changed since endorsement.
	Endorsed [][]byte
}

func (p *EPRProgram) Encode() []byte {
	b := append([]byte{}, EPRProgMagic...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(p.Keys))) //nolint:gosec
	for _, k := range p.Keys {
		b = binary.BigEndian.AppendUint16(b, uint16(len(k))) //nolint:gosec
		b = append(b, k...)
	}
	b = binary.BigEndian.AppendUint32(b, uint32(len(p.Nodes))) //nolint:gosec
	for _, n := range p.Nodes {
		b = append(b, n.Op)
		b = binary.BigEndian.AppendUint32(b, uint32(n.A)) //nolint:gosec
		b = binary.BigEndian.AppendUint32(b, uint32(n.B)) //nolint:gosec
		b = binary.BigEndian.AppendUint32(b, uint32(n.C)) //nolint:gosec
		if n.Op == EPROpConst {
			b = append(b, byte(len(n.Const)))
			b = append(b, n.Const...)
		}
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(p.Guards))) //nolint:gosec
	for _, g := range p.Guards {
		b = binary.BigEndian.AppendUint32(b, uint32(g.Node)) //nolint:gosec
		b = append(b, g.Kind)
		if g.Kind == 0 {
			b = append(b, byte(len(g.Value)))
			b = append(b, g.Value...)
		} else {
			v := byte(0)
			if len(g.Value) > 0 && g.Value[0] != 0 {
				v = 1
			}
			b = append(b, v)
		}
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(p.Outputs))) //nolint:gosec
	for _, o := range p.Outputs {
		b = binary.BigEndian.AppendUint16(b, o.Key)
		b = binary.BigEndian.AppendUint32(b, uint32(o.Node)) //nolint:gosec
	}
	if len(p.Endorsed) == len(p.Outputs) && len(p.Endorsed) > 0 {
		b = binary.BigEndian.AppendUint16(b, uint16(len(p.Endorsed))) //nolint:gosec
		for _, v := range p.Endorsed {
			b = append(b, byte(len(v)))
			b = append(b, v...)
		}
	}
	return b
}

var errEPRProg = errors.New("malformed EPR program")

// DecodeEPRProgram parses an encoded program; ok is false if v is not one.
// EPRProgMaxNodes bounds the work a committer spends on one program (evaluation is linear in the nodes);
// endorsers fall back to plain writes for larger programs, committers reject them.
const EPRProgMaxNodes = 4096

// EPRProgMinLen: a plain storage value is a 32-byte word, so a value of at most 32 bytes is never a program,
// whatever its prefix (every program the endorser emits names at least one storage key and is far longer).
const EPRProgMinLen = 33

func DecodeEPRProgram(v []byte) (*EPRProgram, bool) {
	if len(v) < EPRProgMinLen || string(v[:len(EPRProgMagic)]) != string(EPRProgMagic) {
		return nil, false
	}
	r := &eprReader{b: v[len(EPRProgMagic):]}
	p := &EPRProgram{}
	nk := int(r.u16())
	for i := 0; i < nk && r.err == nil; i++ {
		p.Keys = append(p.Keys, string(r.bytes(int(r.u16()))))
	}
	nn := int(r.u32())
	if nn > EPRProgMaxNodes {
		return nil, false
	}
	for i := 0; i < nn && r.err == nil; i++ {
		n := EPRProgNode{Op: r.u8(), A: int32(r.u32()), B: int32(r.u32()), C: int32(r.u32())} //nolint:gosec
		if n.Op == EPROpConst {
			n.Const = r.bytes(int(r.u8()))
		}
		p.Nodes = append(p.Nodes, n)
	}
	ng := int(r.u16())
	for i := 0; i < ng && r.err == nil; i++ {
		g := EPRProgGuard{Node: int32(r.u32()), Kind: r.u8()} //nolint:gosec
		if g.Kind == 0 {
			g.Value = r.bytes(int(r.u8()))
		} else {
			g.Value = []byte{r.u8()}
		}
		p.Guards = append(p.Guards, g)
	}
	no := int(r.u16())
	for i := 0; i < no && r.err == nil; i++ {
		p.Outputs = append(p.Outputs, EPRProgOut{Key: r.u16(), Node: int32(r.u32())}) //nolint:gosec
	}
	if r.err == nil && len(r.b) > 0 { // optional section: endorsed output values
		if ne := int(r.u16()); ne == no {
			for i := 0; i < ne && r.err == nil; i++ {
				p.Endorsed = append(p.Endorsed, r.bytes(int(r.u8())))
			}
		} else {
			return nil, false
		}
	}
	if r.err != nil || len(r.b) != 0 {
		return nil, false
	}
	return p, p.wellFormed()
}

func (p *EPRProgram) wellFormed() bool {
	for i, n := range p.Nodes {
		switch n.Op {
		case EPROpVar:
			if n.A < 0 || int(n.A) >= len(p.Keys) {
				return false
			}
		case EPROpConst:
		default:
			// operands must precede the node (topological order); unused operands are -1
			for _, a := range []int32{n.A, n.B, n.C} {
				if a >= int32(i) || a < -1 {
					return false
				}
			}
		}
	}
	for _, g := range p.Guards {
		if g.Node < 0 || int(g.Node) >= len(p.Nodes) {
			return false
		}
	}
	for _, o := range p.Outputs {
		if int(o.Key) >= len(p.Keys) || o.Node < 0 || int(o.Node) >= len(p.Nodes) {
			return false
		}
	}
	return true
}

type eprReader struct {
	b   []byte
	err error
}

func (r *eprReader) need(n int) bool {
	if r.err != nil || len(r.b) < n {
		r.err = errEPRProg
		return false
	}
	return true
}
func (r *eprReader) u8() byte {
	if !r.need(1) {
		return 0
	}
	x := r.b[0]
	r.b = r.b[1:]
	return x
}
func (r *eprReader) u16() uint16 {
	if !r.need(2) {
		return 0
	}
	x := binary.BigEndian.Uint16(r.b)
	r.b = r.b[2:]
	return x
}
func (r *eprReader) u32() uint32 {
	if !r.need(4) {
		return 0
	}
	x := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return x
}
func (r *eprReader) bytes(n int) []byte {
	if !r.need(n) {
		return nil
	}
	x := append([]byte{}, r.b[:n]...)
	r.b = r.b[n:]
	return x
}

var (
	eprTwo256 = new(big.Int).Lsh(big.NewInt(1), 256)
	eprTwo255 = new(big.Int).Lsh(big.NewInt(1), 255)
	eprMask   = new(big.Int).Sub(eprTwo256, big.NewInt(1))
)

func eprWrap(x *big.Int) *big.Int { return x.And(x, eprMask) }

func eprSigned(x *big.Int) *big.Int {
	if x.Cmp(eprTwo255) >= 0 {
		return new(big.Int).Sub(x, eprTwo256)
	}
	return new(big.Int).Set(x)
}

func eprUnsigned(x *big.Int) *big.Int {
	if x.Sign() < 0 {
		return new(big.Int).Add(x, eprTwo256)
	}
	return x
}

func eprBool(b bool) *big.Int {
	if b {
		return big.NewInt(1)
	}
	return big.NewInt(0)
}

// EPREvalOp applies an EVM opcode to operands in pop order with exact uint256 semantics.
func EPREvalOp(op byte, a, b, c *big.Int) (*big.Int, bool) {
	z := new(big.Int)
	switch op {
	case 0x01: // ADD
		return eprWrap(z.Add(a, b)), true
	case 0x02: // MUL
		return eprWrap(z.Mul(a, b)), true
	case 0x03: // SUB
		return eprWrap(eprUnsigned(z.Sub(a, b))), true
	case 0x04: // DIV
		if b.Sign() == 0 {
			return z, true
		}
		return z.Quo(a, b), true
	case 0x05: // SDIV (truncating; MIN/-1 = MIN)
		sa, sb := eprSigned(a), eprSigned(b)
		if sb.Sign() == 0 {
			return z, true
		}
		return eprWrap(eprUnsigned(z.Quo(sa, sb))), true
	case 0x06: // MOD
		if b.Sign() == 0 {
			return z, true
		}
		return z.Rem(a, b), true
	case 0x07: // SMOD (sign of the dividend)
		sa, sb := eprSigned(a), eprSigned(b)
		if sb.Sign() == 0 {
			return z, true
		}
		return eprWrap(eprUnsigned(z.Rem(sa, sb))), true
	case 0x08: // ADDMOD (no wrap before the modulo)
		if c.Sign() == 0 {
			return z, true
		}
		return z.Rem(new(big.Int).Add(a, b), c), true
	case 0x09: // MULMOD
		if c.Sign() == 0 {
			return z, true
		}
		return z.Rem(new(big.Int).Mul(a, b), c), true
	case 0x0a: // EXP
		return z.Exp(a, b, eprTwo256), true
	case 0x0b: // SIGNEXTEND(byteIndex, x)
		if a.Cmp(big.NewInt(31)) >= 0 {
			return new(big.Int).Set(b), true
		}
		bit := uint(a.Uint64()*8 + 7)
		mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), bit+1), big.NewInt(1))
		v := new(big.Int).And(b, mask)
		if v.Bit(int(bit)) == 1 {
			v.Or(v, new(big.Int).Xor(eprMask, mask))
		}
		return v, true
	case 0x10: // LT
		return eprBool(a.Cmp(b) < 0), true
	case 0x11: // GT
		return eprBool(a.Cmp(b) > 0), true
	case 0x12: // SLT
		return eprBool(eprSigned(a).Cmp(eprSigned(b)) < 0), true
	case 0x13: // SGT
		return eprBool(eprSigned(a).Cmp(eprSigned(b)) > 0), true
	case 0x14: // EQ
		return eprBool(a.Cmp(b) == 0), true
	case 0x15: // ISZERO
		return eprBool(a.Sign() == 0), true
	case 0x16: // AND
		return z.And(a, b), true
	case 0x17: // OR
		return z.Or(a, b), true
	case 0x18: // XOR
		return z.Xor(a, b), true
	case 0x19: // NOT
		return z.Xor(a, eprMask), true
	case 0x1a: // BYTE(index, x)
		if a.Cmp(big.NewInt(32)) >= 0 {
			return z, true
		}
		s := uint(248 - a.Uint64()*8)
		return z.And(new(big.Int).Rsh(b, s), big.NewInt(0xff)), true
	case 0x1b: // SHL(shift, x)
		if a.Cmp(big.NewInt(256)) >= 0 {
			return z, true
		}
		return eprWrap(z.Lsh(b, uint(a.Uint64()))), true
	case 0x1c: // SHR(shift, x)
		if a.Cmp(big.NewInt(256)) >= 0 {
			return z, true
		}
		return z.Rsh(b, uint(a.Uint64())), true
	case 0x1d: // SAR(shift, x)
		sb := eprSigned(b)
		if a.Cmp(big.NewInt(256)) >= 0 {
			if sb.Sign() < 0 {
				return new(big.Int).Set(eprMask), true
			}
			return z, true
		}
		return eprWrap(eprUnsigned(z.Rsh(sb, uint(a.Uint64())))), true
	case 0x1e: // CLZ (Osaka)
		return big.NewInt(int64(256 - a.BitLen())), true
	}
	return nil, false
}

// EPRPureOp reports whether op is an operator the program evaluator supports.
func EPRPureOp(op byte) bool {
	_, ok := EPREvalOp(op, big.NewInt(1), big.NewInt(1), big.NewInt(1))
	return ok
}

// Eval evaluates the program on the latest values of its keys (absent keys read as zero). It returns the
// outputs (32-byte words per output key) and false if any guard fails or the program is malformed.
func (p *EPRProgram) Eval(val func(key string) []byte) (map[string][]byte, bool) {
	vals := make([]*big.Int, len(p.Nodes))
	arg := func(i int32) *big.Int {
		if i < 0 {
			return new(big.Int)
		}
		return vals[i]
	}
	for i, n := range p.Nodes {
		switch n.Op {
		case EPROpVar:
			vals[i] = new(big.Int).SetBytes(val(p.Keys[n.A]))
		case EPROpConst:
			vals[i] = new(big.Int).SetBytes(n.Const)
		default:
			v, ok := EPREvalOp(n.Op, arg(n.A), arg(n.B), arg(n.C))
			if !ok {
				return nil, false
			}
			vals[i] = v
		}
	}
	for _, g := range p.Guards {
		v := vals[g.Node]
		if g.Kind == 0 {
			if v.Cmp(new(big.Int).SetBytes(g.Value)) != 0 {
				return nil, false
			}
		} else if (v.Sign() != 0) != (len(g.Value) > 0 && g.Value[0] != 0) {
			return nil, false
		}
	}
	out := make(map[string][]byte, len(p.Outputs))
	for _, o := range p.Outputs {
		w := make([]byte, 32)
		vals[o.Node].FillBytes(w)
		out[p.Keys[o.Key]] = w
	}
	return out, true
}
