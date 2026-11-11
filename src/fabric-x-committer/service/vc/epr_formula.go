/*
Extension (not upstream): guarded LINEAR-FORMULA endorsement (generalized guarded deltas), VC side.

A read-write value "EPRF1" | formula is a signed formula for the written key:
    value := sum(coef_i * current(key_i)) + c + (guard bit == 0 ? pf : 0)
    guard (optional): sum(coef_j * current(key_j)) + gc >= 0, with the truth value `expect` observed at
    endorsement time (0xFF = no endorsed expectation).
The committer evaluates the formula on the latest committed values (no VM). If the guard's truth value
changed since endorsement, the execution path changed and the transaction aborts. Keys referenced by a
formula are ordinary (ordered) accesses in the dependency graph, so no concurrent update can interleave.
*/

package vc

import (
	"bytes"
	"encoding/binary"
)

var eprFormulaMagic = []byte("EPRF1")

type eprTerm struct {
	key  string
	coef int64
}

type eprFormula struct {
	terms  []eprTerm
	c      int64
	guard  []eprTerm
	gc     int64
	expect byte
	hasG   bool
	pf     int64
}

func eprReadTerms(b []byte) ([]eprTerm, []byte, bool) {
	if len(b) < 1 {
		return nil, nil, false
	}
	n := int(b[0])
	b = b[1:]
	ts := make([]eprTerm, 0, n)
	for i := 0; i < n; i++ {
		if len(b) < 1 {
			return nil, nil, false
		}
		kl := int(b[0])
		if len(b) < 1+kl+8 {
			return nil, nil, false
		}
		ts = append(ts, eprTerm{key: string(b[1 : 1+kl]), coef: int64(binary.BigEndian.Uint64(b[1+kl : 9+kl]))}) //nolint:gosec
		b = b[9+kl:]
	}
	return ts, b, true
}

// eprDecodeFormula parses "EPRF1" | terms | c | ng(0/1) [| guard terms | gc | expect] | pf.
func eprDecodeFormula(v []byte) (*eprFormula, bool) {
	if !bytes.HasPrefix(v, eprFormulaMagic) {
		return nil, false
	}
	b := v[len(eprFormulaMagic):]
	f := &eprFormula{}
	var ok bool
	if f.terms, b, ok = eprReadTerms(b); !ok || len(b) < 9 {
		return nil, false
	}
	f.c = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
	ng := b[8]
	b = b[9:]
	if ng == 1 {
		f.hasG = true
		if f.guard, b, ok = eprReadTerms(b); !ok || len(b) < 9 {
			return nil, false
		}
		f.gc = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
		f.expect = b[8]
		b = b[9:]
	}
	if len(b) < 8 {
		return nil, false
	}
	f.pf = int64(binary.BigEndian.Uint64(b[:8])) //nolint:gosec
	return f, true
}

// keys returns every key the formula references (written key excluded).
func (f *eprFormula) keys() []string {
	var ks []string
	for _, t := range f.terms {
		ks = append(ks, t.key)
	}
	for _, t := range f.guard {
		ks = append(ks, t.key)
	}
	return ks
}

// eval computes the new value from the running values; ok=false if the endorsed path no longer holds.
func (f *eprFormula) eval(cur func(string) int64) (int64, bool) {
	bit := byte(1)
	if f.hasG {
		g := f.gc
		for _, t := range f.guard {
			g += t.coef * cur(t.key)
		}
		if g < 0 {
			bit = 0
		}
		if f.expect != 0xFF && bit != f.expect {
			return 0, false
		}
	}
	v := f.c
	for _, t := range f.terms {
		v += t.coef * cur(t.key)
	}
	if bit == 0 {
		v += f.pf
	}
	return v, true
}
