package collector

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
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/gofrs/uuid"
	"github.com/pkg/errors"
)

type client struct {
	enc      encoder
	lmtr     *limiter
	security Secret

	client       *http.Client
	jobReq       *http.Request
	containerReq *http.Request

	outputCompressLevel int
	outputClient        *http.Client
	outputParams        map[string]string
	outputHeaders       map[string]string
	outputMethod        string

	observer outputs.Observer
}

func newClient(host string, cfg config, observer outputs.Observer) (*client, error) {
	var (
		enc encoder
		err error
	)
	if cfg.CompressLevel == 0 {
		enc, err = newJSONEncoder()
	} else {
		enc, err = newGzipEncoder(cfg.CompressLevel)
	}
	if err != nil {
		return nil, errors.Wrap(err, "fail to create encoder")
	}

	jobReq, err := newRequest(host, cfg.JobPath, cfg.Method, cfg.Params, cfg.Headers)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create job request")
	}
	enc.addHeader(&jobReq.Header)

	containerReq, err := newRequest(host, cfg.ContainerPath, cfg.Method, cfg.Params, cfg.Headers)
	if err != nil {
		return nil, errors.Wrap(err, "fail to create container request")
	}
	enc.addHeader(&containerReq.Header)

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

	lmtr := newLimiter(cfg.Limiter.Quantity, cfg.Limiter.Threshold, cfg.Limiter.Timeout)

	return &client{
		enc:                 enc,
		lmtr:                lmtr,
		client:              httpClient,
		jobReq:              jobReq,
		containerReq:        containerReq,
		outputCompressLevel: cfg.Output.CompressLevel,
		outputClient:        outputClient,
		outputMethod:        cfg.Output.Method,
		outputParams:        cfg.Output.Params,
		outputHeaders:       cfg.Output.Headers,
		observer:            observer,
		security:            createSecret(cfg.Auth),
	}, nil
}

func newRequest(host, path, method string, params, headers map[string]string) (*http.Request, error) {
	values := netUrl.Values{}
	for key, value := range params {
		values.Add(key, value)
	}
	v := values.Encode()
	url := host + path
	url += v

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
	return containerRest, nil
}

func (c *client) sendEvents(events []publisher.Event, isJob bool) ([]publisher.Event, error) {
	send, rest, err := c.pickSendEvents(events)
	if err != nil {
		return events, errors.Wrap(err, "fail to pick send events")
	}
	if len(send) == 0 {
		return events, nil
	}

	body, err := c.enc.encode(send)
	if err != nil {
		return events, errors.Wrap(err, "fail to encode send events")
	}

	var req *http.Request
	if isJob {
		req = c.jobReq
	} else {
		req = c.containerReq
	}
	var requestID string
	if key, err := uuid.NewV4(); err == nil {
		requestID = key.String()
	}
	req.Header.Set("terminus-request-id", requestID)
	req.Body = ioutil.NopCloser(body)

	c.security.Secure(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return events, errors.Errorf("fail to send request %s: %s", requestID, err)
	}
	defer closeResponseBody(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return events, errors.Errorf("request %s response status code %v is not success", requestID, resp.StatusCode)
	}
	// TODO need refactor
	go func() {
		c.sendOutputEvents(send)
	}()
	return rest, nil
}

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
	var (
		enc encoder
		err error
	)
	if c.outputCompressLevel == 0 {
		enc, err = newJSONEncoder()
	} else {
		enc, err = newGzipEncoder(c.outputCompressLevel)
	}
	if err != nil {
		logp.Err("fail to create encoder: %s", err)
	}

	body, err := enc.encode(events)
	if err != nil {
		logp.Err("fail to encode output %s events: %s", addr, err)
		return
	}
	now := time.Now().UnixNano()

	req, err := newRequest(addr, "", c.outputMethod, c.outputParams, c.outputHeaders)
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
	resp, err := c.outputClient.Do(req)
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

func (c *client) pickSendEvents(events []publisher.Event) (send, rest []publisher.Event, errs error) {
	// 先尝试发送全部events
	body, err := c.enc.encode(events)
	if err != nil {
		rest, errs = events, errors.Wrap(err, "fail to encode events")
		return
	}
	if c.lmtr.assign(int64(body.Len())) {
		send = events
		return
	}

	// 后尝试减少发送的events数
	send, rest, err = c.balanceEvents(events, rest)
	if err != nil {
		errs = errors.Wrap(err, "fail to balance events")
	}
	return
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

func (c *client) balanceEvents(send, rest []publisher.Event) (newSend, newRest []publisher.Event, errs error) {
	if len(send) <= 1 {
		newRest = append(newRest, send...)
		newRest = append(newRest, rest...)
		return
	}

	half := len(send) / 2
	for i, e := range send {
		if i < half {
			newSend = append(newSend, e)
		} else {
			newRest = append(newRest, e)
		}
	}
	newRest = append(newRest, rest...)

	body, err := c.enc.encode(newSend)
	if err != nil {
		errs = errors.Wrap(err, "fail to encode new send events")
		return
	}
	if c.lmtr.assign(int64(body.Len())) {
		return
	}

	newSend, newRest, err = c.balanceEvents(newSend, newRest)
	if err != nil {
		errs = errors.Wrap(err, "fail to balance events")
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
