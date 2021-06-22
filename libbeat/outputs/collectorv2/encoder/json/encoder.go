package json

import (
	"encoding/json"

	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder"
)

type Encoder struct {
}

func (e *Encoder) Encode(batch *encoder.LogBatch) ([]byte, error) {
	return json.Marshal(batch)
}

func NewEncoder() *Encoder {
	return &Encoder{}
}
