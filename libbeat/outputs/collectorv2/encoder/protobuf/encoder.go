package protobuf

import (
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/pb"
)

type Encoder struct {
}

func (e *Encoder) Encode(batch *pb.LogBatch) ([]byte, error) {
	return batch.Marshal()
}

func NewEncoder() *Encoder {
	return &Encoder{}
}
