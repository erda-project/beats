package collectorv2

import (
	"bytes"
	"compress/gzip"
	"io"

	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/pkg/errors"
)

type gziper struct {
	buf    *bytes.Buffer
	writer *gzip.Writer
}

func NewGziper(level int) (*gziper, error) {
	buf := bytes.NewBuffer(nil)
	writer, err := gzip.NewWriterLevel(buf, level)
	if err != nil {
		return nil, errors.Wrapf(err, "fail to create gzip level %v writer", level)
	}

	return &gziper{
		buf:    buf,
		writer: writer,
	}, nil
}

func (g *gziper) Compress(data []byte) ([]byte, error) {
	defer func() {
		g.buf.Reset()
		g.writer.Reset(g.buf)
	}()
	defer func() {
		if r := recover(); r != nil {
			logp.Err("Panic %v: json: %s", r, string(data))
		}
	}()

	if _, err := g.writer.Write(data); err != nil {
		return nil, errors.Wrap(err, "fail to gzip write data")
	}
	if err := g.writer.Flush(); err != nil {
		return nil, errors.Wrap(err, "fail to gzip flush")
	}
	if err := g.writer.Close(); err != nil {
		return nil, errors.Wrap(err, "fail to gzip close")
	}

	b := bytes.NewBuffer(nil)
	if _, err := io.Copy(b, g.buf); err != nil {
		return nil, errors.Wrap(err, "fail to copy buf")
	}
	return b.Bytes(), nil
}
