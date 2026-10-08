package services

// Custom CQL marshaling for Cassandra 5.0 vector<float, N>.
//
// gocql v1.7.0 treats vectors as TypeCustom and cannot marshal []float32
// natively. We wrap []float32 in CQLVector which implements the gocql
// Marshaler / Unmarshaler interfaces.
//
// Wire format (CEP-30, fixed-size element):
//   - N float32 values concatenated, big-endian (network byte order)
//   - No per-element length prefix (since float32 is fixed-width)

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/gocql/gocql"
)

// CQLVector wraps []float32 for marshaling into Cassandra vector<float, N>.
type CQLVector []float32

// MarshalCQL implements gocql.Marshaler.
func (v CQLVector) MarshalCQL(_ gocql.TypeInfo) ([]byte, error) {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.BigEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf, nil
}

// UnmarshalCQL implements gocql.Unmarshaler.
func (v *CQLVector) UnmarshalCQL(_ gocql.TypeInfo, data []byte) error {
	if len(data)%4 != 0 {
		return fmt.Errorf("vector: invalid byte length %d (not divisible by 4)", len(data))
	}
	n := len(data) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.BigEndian.Uint32(data[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	*v = out
	return nil
}
