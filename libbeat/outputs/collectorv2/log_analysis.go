package collectorv2

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
)

// send to log analysis
// todo remove
func (c *client) sendOutputEvents(events []publisher.Event) {
	sendMap := make(map[string][]publisher.Event)
	for _, event := range events {
		if v, err := event.Content.GetValue("terminus.output.collector"); err == nil {
			if addr, ok := v.(string); ok && addr != "" {
				sendMap[addr] = append(sendMap[addr], event)
			}
		}
	}
	if len(sendMap) == 0 {
		return
	}

	// 根据collector地址全部发送
	for addr, send := range sendMap {
		c.sendOutputAddrEvents(addr, send)
	}
	return
}

func (c *client) sendOutputAddrEvents(addr string, events []publisher.Event) {
	enc, err := newGzipEncoder(c.output.Level)
	if err != nil {
		logp.Err("fail to create encoder: %s", err)
	}

	body, err := enc.encode(events)
	if err != nil {
		logp.Err("fail to encode output %s events: %s", addr, err)
		return
	}
	now := time.Now().UnixNano()

	req, err := newRequest(addr, "", c.output.Method, c.output.Params, c.output.Headers)
	if err != nil {
		logp.Err("fail to create output request %s", addr)
		return
	}
	enc.addHeader(&req.Header)
	var requestID string
	if key, err := uuid.NewV4(); err == nil {
		requestID = key.String()
	}
	req.Header.Set("terminus-request-id", requestID)

	req.Body = ioutil.NopCloser(body)
	resp, err := c.output.Client.Do(req)
	if err != nil {
		logp.Err("fail to send %s output: %s", addr, err)
		return
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logp.Err("output %s response status is not success, code is %v", addr, resp.StatusCode)
		return
	}

	logp.Info("send output %s request %s success, count: %v, cost: %.3fs",
		addr, requestID, len(events), float64(time.Now().UnixNano()-now)/float64(time.Second))
}

type encoder interface {
	addHeader(*http.Header)
	encode([]publisher.Event) (*bytes.Buffer, error)
}

type gzipEncoder struct {
	buf    *bytes.Buffer
	writer *gzip.Writer
}

func newGzipEncoder(level int) (*gzipEncoder, error) {
	buf := bytes.NewBuffer(nil)
	writer, err := gzip.NewWriterLevel(buf, level)
	if err != nil {
		return nil, errors.Wrapf(err, "fail to create gzip level %v writer", level)
	}

	return &gzipEncoder{
		buf:    buf,
		writer: writer,
	}, nil
}

func (ge *gzipEncoder) addHeader(header *http.Header) {
	header.Add("Content-Type", "application/json; charset=UTF-8")
	header.Add("Content-Encoding", "gzip")
	header.Add("Custom-Content-Encoding", "base64")
}

func (ge *gzipEncoder) encode(obj []publisher.Event) (*bytes.Buffer, error) {
	var (
		data []byte
		err  error
	)
	buffer := &bytes.Buffer{}
	defer func() {
		ge.buf.Reset()
		ge.writer.Reset(ge.buf)
	}()
	defer func() {
		if r := recover(); r != nil {
			logp.Err("Panic %v: json: %s, base64: %s", r, string(data), buffer.String())
		}
	}()

	events := []map[string]interface{}{}
	for _, o := range obj {
		m, err := transformMap(o)
		if err != nil {
			logp.Err("Fail to transform map with err: %s;\nEvent.Content: %s", err, marshalMap(o.Content))
			continue
		}

		if m == nil { // ignore
			continue
		}

		events = append(events, m)
	}

	data, err = json.Marshal(events)
	if err != nil {
		return nil, errors.Wrap(err, "fail to json marshal events")
	}

	encoder := base64.NewEncoder(base64.StdEncoding, buffer)
	if _, err := encoder.Write(data); err != nil {
		return nil, errors.Wrap(err, "fail to base64 encode data")
	}
	encoder.Close()

	if _, err := ge.writer.Write(buffer.Bytes()); err != nil {
		return nil, errors.Wrap(err, "fail to gzip write data")
	}
	if err := ge.writer.Flush(); err != nil {
		return nil, errors.Wrap(err, "fail to gzip flush")
	}
	if err := ge.writer.Close(); err != nil {
		return nil, errors.Wrap(err, "fail to gzip close")
	}

	b := bytes.NewBuffer(nil)
	if _, err := io.Copy(b, ge.buf); err != nil {
		return nil, errors.Wrap(err, "fail to copy buf")
	}
	return b, nil
}

func transformMap(event publisher.Event) (map[string]interface{}, error) {
	source, err := event.Content.GetValue("terminus.source")
	if err != nil {
		source = "container"
	}
	id, err := event.Content.GetValue("terminus.id")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get id value")
	}
	offset, err := event.Content.GetValue("log.offset")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get offset value")
	}
	stream, err := event.Content.GetValue("stream")
	if err != nil {
		stream = "stdout"
	}
	message, err := event.Content.GetValue("message")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get message value")
	}
	tags := make(map[string]string)
	if d, err := event.Content.GetValue("terminus.tags"); err == nil {
		if v, err := convert(d); err == nil {
			tags = v
		}
	}
	labels := make(map[string]string)
	if d, err := event.Content.GetValue("terminus.labels"); err == nil {
		if v, err := convert(d); err == nil {
			labels = v
		}
	}
	m := make(map[string]interface{})
	m["source"] = source
	m["id"] = id
	m["offset"] = offset
	m["timestamp"] = event.Content.Timestamp.UnixNano()
	m["stream"] = stream
	m["content"] = message
	m["tags"] = tags
	m["labels"] = labels
	logp.Debug(selector, "transformMap get final message: %+v", marshalMap(m))
	return m, nil
}

func marshalMap(m interface{}) string {
	d, _ := json.Marshal(m)
	return string(d)
}
