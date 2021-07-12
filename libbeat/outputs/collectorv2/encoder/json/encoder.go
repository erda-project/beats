package json

import (
	"encoding/json"

	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/pb"
)

type Encoder struct {
}

func (e *Encoder) Encode(batch *pb.LogBatch) ([]byte, error) {
	return json.Marshal(batch)
}

func NewEncoder() *Encoder {
	return &Encoder{}
}
