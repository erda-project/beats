package protobuf

import (
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder"
)

type Encoder struct {
}

func (e *Encoder) Encode(batch *encoder.LogBatch) ([]byte, error) {
	return batch.Marshal()
}

func NewEncoder() *Encoder {
	return &Encoder{}
}
