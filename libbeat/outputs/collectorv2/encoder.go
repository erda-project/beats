package collectorv2

import (
	"bytes"
	"io"

	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder/json"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder/protobuf"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/pkg/errors"
)

type Encoder interface {
	Encode(batch *encoder.LogBatch) ([]byte, error)
}

type encoderName string

const (
	encoderJson     encoderName = "json"
	encoderProtobuf encoderName = "protobuf"
)

type encoderConfig struct {
	Name string `config:"name"`
}

func createEncoder(name encoderName) Encoder {
	switch name {
	case encoderProtobuf:
		return protobuf.NewEncoder()
	default:
		return json.NewEncoder()
	}
}

func (c *client) serialize(events []publisher.Event) (io.Reader, error) {
	send := convertEvents(events)
	if len(send.Logs) == 0 {
		return nil, errors.New("no data to send")
	}
	buf, err := c.enc.Encode(send)
	if err != nil {
		return nil, errors.Wrap(err, "fail to encode send events")
	}

	var reader *bytes.Buffer
	if c.compressor != nil {
		cbody, err := c.compressor.Compress(buf)
		if err != nil {
			return nil, errors.Wrap(err, "compress failed")
		}
		reader = bytes.NewBuffer(cbody)
	} else {
		reader = bytes.NewBuffer(buf)
	}
	return reader, nil
}
