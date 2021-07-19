package collectorv2

import (
	"context"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	netUrl "net/url"
	"time"

	"github.com/elastic/beats/v7/libbeat/common/transport"
	"github.com/elastic/beats/v7/libbeat/common/transport/tlscommon"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/outputs"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/encoder/protobuf"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/pb"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
)

type client struct {
	enc        Encoder
	compressor *gziper
	security   Secret

	client       *http.Client
	jobReq       *http.Request
	containerReq *http.Request

	observer outputs.Observer

	output *outputLogAnalysis // todo remove

	bulkMaxSizeBytes int
}

type outputLogAnalysis struct {
	Client     *http.Client
	Params     map[string]string
	Headers    map[string]string
	Method     string
	Compressor *gziper
}

func newClient(host string, cfg config, observer outputs.Observer) (*client, error) {
	enc := createEncoder(encoderName(cfg.Encoder))

	jobReq, err := newRequest(host, cfg.JobPath, cfg.Method, cfg.Params, cfg.Headers)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create job request")
	}

	containerReq, err := newRequest(host, cfg.ContainerPath, cfg.Method, cfg.Params, cfg.Headers)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create container request")
	}

	var compressor *gziper
	if cfg.CompressLevel > 0 && cfg.CompressLevel <= 9 {
		compressor, err = NewGziper(cfg.CompressLevel)
		if err != nil {
			return nil, err
		}
	}

	tls, err := tlscommon.LoadTLSConfig(cfg.TLS)
	if err != nil {
		return nil, errors.Wrap(err, "fail to load tls")
	}
	httpClient, err := newHTTPClient(cfg.Timeout, cfg.KeepAlive, tls, observer)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create http client")
	}

	outputTLS, err := tlscommon.LoadTLSConfig(cfg.Output.TLS)
	if err != nil {
		return nil, errors.Wrap(err, "fail to load output tls")
	}
	outputClient, err := newHTTPClient(cfg.Output.Timeout, cfg.Output.KeepAlive, outputTLS, observer)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create output client")
	}
	var outputCompressor *gziper
	if cfg.Output.CompressLevel > 0 && cfg.Output.CompressLevel <= 9 {
		outputCompressor, err = NewGziper(cfg.CompressLevel)
		if err != nil {
			return nil, err
		}
	}

	return &client{
		enc:              enc,
		client:           httpClient,
		jobReq:           jobReq,
		containerReq:     containerReq,
		observer:         observer,
		compressor:       compressor,
		security:         createSecret(cfg.Auth),
		bulkMaxSizeBytes: cfg.BulkMaxSizeBytes,

		output: &outputLogAnalysis{
			Client:     outputClient,
			Params:     cfg.Output.Params,
			Headers:    cfg.Output.Headers,
			Method:     cfg.Output.Method,
			Compressor: outputCompressor,
		},
	}, nil
}

func newRequest(url, path, method string, params, headers map[string]string) (*http.Request, error) {
	values := netUrl.Values{}
	for key, value := range params {
		values.Add(key, value)
	}
	v := values.Encode()
	url = url + path + v

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create request")
	}

	splitHost, _, err := net.SplitHostPort(req.Host)
	if err == nil {
		req.Host = splitHost
	}

	for key, value := range headers {
		req.Header.Add(key, value)
	}
	req.Header.Add("Accept", "application/json")
	return req, nil
}

func newHTTPClient(
	timeout, keepAlive time.Duration,
	tls *tlscommon.TLSConfig,
	observer outputs.Observer,
) (*http.Client, error) {
	dialer := transport.NetDialer(timeout)
	tlsDialer, err := transport.TLSDialer(dialer, tls, timeout)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create tls dialer")
	}

	dialer = transport.StatsDialer(dialer, observer)
	tlsDialer = transport.StatsDialer(tlsDialer, observer)

	client := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   timeout,
				KeepAlive: keepAlive,
				DualStack: true,
			}).DialContext,
			Dial:    dialer.Dial,
			DialTLS: tlsDialer.Dial,
		},
	}
	return client, nil
}

func (c *client) Connect() error {
	return nil
}

func (c *client) Close() error {
	return nil
}

func (c *client) Publish(_ context.Context, batch publisher.Batch) error {
	events := batch.Events()
	rest, err := c.publishEvents(events)
	c.observer.NewBatch(len(events))
	if len(rest) == 0 {
		batch.ACK()
	} else {
		c.observer.Failed(len(rest))
		batch.RetryEvents(rest)
	}
	return err
}

func (c *client) publishEvents(events []publisher.Event) ([]publisher.Event, error) {
	if len(events) == 0 {
		return nil, nil
	}

	jobs, containers, err := c.splitEvents(events)
	if err != nil {
		return events, errors.Wrap(err, "fail to split events")
	}

	jobRest, err := c.sendEvents(jobs, true)
	if err != nil {
		return events, errors.Wrap(err, "fail to send job events")
	}
	if len(jobRest) > 0 {
		jobRest = append(jobRest, containers...)
		return jobRest, nil
	}

	containerRest, err := c.sendEvents(containers, false)
	if err != nil {
		return events, errors.Wrap(err, "fail to send container events")
	}
	// todo to log analysis 要兼容
	go c.sendOutputEvents(containers)
	return containerRest, nil
}

func (c *client) sendEvents(events []publisher.Event, isJob bool) ([]publisher.Event, error) {
	for len(events) != 0 {
		pivot, err := sizeLimit(events, c.bulkMaxSizeBytes)
		if err != nil {
			return events, err
		}
		send, rest := events[:pivot], events[pivot:]

		// do request
		var req *http.Request
		if isJob {
			req = c.jobReq
		} else {
			req = c.containerReq
		}
		c.populateRequest(req)

		reader, err := c.serialize(send)
		if err != nil {
			return events, err
		}

		req.Body = ioutil.NopCloser(reader)
		if c.compressor != nil {
			req.Header.Add("Content-Encoding", "gzip")
		}

		c.security.Secure(req)

		resp, err := c.client.Do(req)
		if err != nil {
			return events, errors.Errorf("fail to send request, err: %s", err)
		}
		closeResponseBody(resp.Body)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return events, errors.Errorf("response status code %v is not success", resp.StatusCode)
		}

		events = rest
	}
	return events, nil
}

func sizeLimit(events []publisher.Event, limit int) (int, error) {
	sum := 0
	i := 0
	for ; i < len(events); i++ {
		if sum > limit {
			break
		}
		content, err := events[i].Content.GetValue("message")
		if err != nil {
			return 0, errors.Wrap(err, "event must have a message")
		}
		if v, ok := content.(string); !ok {
			return 0, errors.New("event message must be a string")
		} else {
			sum += len(v)
		}
	}
	return i, nil
}

func (c *client) populateRequest(req *http.Request) {
	switch c.enc.(type) {
	case *protobuf.Encoder:
		req.Header.Add("Content-Type", "application/x-protobuf")
	default:
		req.Header.Add("Content-Type", "application/json")
	}

	var requestID string
	if key, err := uuid.NewV4(); err == nil {
		requestID = key.String()
	}
	req.Header.Set("terminus-request-id", requestID)

}

func (c *client) splitEvents(events []publisher.Event) (jobs, containers []publisher.Event, err error) {
	for _, e := range events {
		source, err := e.Content.GetValue("terminus.source")
		if err != nil || source == "container" {
			containers = append(containers, e)
		} else {
			jobs = append(jobs, e)
		}
	}
	return
}

func closeResponseBody(body io.ReadCloser) {
	if err := body.Close(); err != nil {
		logp.Warn("fail to close response body. err: %s", err)
	}
}

func (c *client) String() string {
	return selector
}

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
	batch := &pb.LogBatch{Logs: make([]*pb.Log, 0, len(events))}
	for _, event := range events {
		line, err := convertEvent(event)
		if err != nil {
			logp.Err("convertEvent failed, error: %s", err)
		}
		batch.Logs = append(batch.Logs, line)
	}

	req, err := newRequest(addr, "", c.output.Method, c.output.Params, c.output.Headers)
	if err != nil {
		logp.Err("fail to create output request %s", addr)
		return
	}
	c.populateRequest(req)

	reader, err := c.serialize(events)
	if err != nil {
		logp.Err("fail to encode output %s events: %s", addr, err)
		return
	}
	req.Body = ioutil.NopCloser(reader)

	if c.output.Compressor != nil {
		req.Header.Add("Content-Encoding", "gzip")
	}

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
}
