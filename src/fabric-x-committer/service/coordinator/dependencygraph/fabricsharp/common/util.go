// Copied from Hyperledger Fabric common/ledger/util (Apache-2.0); proto varints of the size byte (at most 8)
// are written as that single byte, which is what proto.EncodeVarint produces for values below 128.

package common

import (
	"encoding/binary"
	"sync/atomic"
)

func encodeOrderPreservingVarUint64(number uint64) []byte {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, number)
	startingIndex := 0
	size := 0
	for i, b := range bytes {
		if b != 0x00 {
			startingIndex = i
			size = 8 - i
			break
		}
	}
	encodedBytes := make([]byte, size+1)
	encodedBytes[0] = byte(size)
	copy(encodedBytes[1:], bytes[startingIndex:])
	return encodedBytes
}

func decodeOrderPreservingVarUint64(bytes []byte) (uint64, int) {
	size := int(bytes[0])
	decodedBytes := make([]byte, 8)
	copy(decodedBytes[8-size:], bytes[1:size+1])
	numBytesConsumed := size + 1
	return binary.BigEndian.Uint64(decodedBytes), numBytesConsumed
}

// fsLogger replaces the orderer's flogging logger (see port_orig_baselines.py).
type fsLogger struct{}

// Logger is the logger of the ported packages.
var Logger fsLogger

// Warnings and Errors count the scheduler's warnings (DESERT: a false-positive cycle) and errors.
var Warnings, Errors atomic.Int64

func (fsLogger) Infof(string, ...interface{})  {}
func (fsLogger) Debugf(string, ...interface{}) {}
func (fsLogger) Warnf(string, ...interface{})  { Warnings.Add(1) }
func (fsLogger) Errorf(string, ...interface{}) { Errors.Add(1) }
func (fsLogger) Panicf(f string, a ...interface{}) {
	panic(f)
}
