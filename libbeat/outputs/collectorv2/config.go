package collectorv2

import (
	"time"

	"github.com/elastic/beats/v7/libbeat/common/transport/tlscommon"
)

type config struct {
	JobPath       string            `config:"job_path"`
	ContainerPath string            `config:"container_path"`
	Params        map[string]string `config:"params"`
	Headers       map[string]string `config:"headers"`
	Method        string            `config:"method"`
	TLS           *tlscommon.Config `config:"ssl"`
	KeepAlive     time.Duration     `config:"keep_alive"`
	Timeout       time.Duration     `config:"timeout"`
	BulkMaxSize   int               `config:"bulk_max_size"`
	MaxRetries    int               `config:"max_retries"`
	Backoff       backoff           `config:"backoff"`
	LoadBalance   bool              `config:"load_balance"`
	CompressLevel int               `config:"compress_level" validate:"min=0, max=9"`
	Limiter       limiterConfig     `config:"limiter"`
	Output        outputConfig      `config:"output"`

	Encoder      string     `config:"encoder"`
	Auth         authConfig `config:"auth"`
}

type authConfig struct {
	Type     string            `config:"type"`
	Property map[string]string `config:"property"`
}

type backoff struct {
	Init time.Duration `config:"init"`
	Max  time.Duration `config:"max"`
}

type limiterConfig struct {
	Quantity  int64         `config:"quantity"`
	Threshold int64         `config:"threshold"`
	Timeout   time.Duration `config:"timeout"`
}

type outputConfig struct {
	Params        map[string]string `config:"params"`
	Headers       map[string]string `config:"headers"`
	Method        string            `config:"method"`
	TLS           *tlscommon.Config `config:"ssl"`
	KeepAlive     time.Duration     `config:"keep_alive"`
	Timeout       time.Duration     `config:"timeout"`
	CompressLevel int               `config:"compress_level" validate:"min=0, max=9"`
}

var defaultConfig = config{
	JobPath:       "/collect/logs/job",
	ContainerPath: "/collect/logs/container",
	Method:        "POST",
	KeepAlive:     30 * time.Second,
	Timeout:       60 * time.Second,
	MaxRetries:    -1,
	Backoff: backoff{
		Init: 1 * time.Second,
		Max:  60 * time.Second,
	},
	LoadBalance:   true,
	CompressLevel: 9,
	Limiter: limiterConfig{
		Quantity:  1024 * 10,
		Threshold: 1024 * 100,
		Timeout:   50 * time.Millisecond,
	},
	Output: outputConfig{
		Method:        "POST",
		KeepAlive:     30 * time.Second,
		Timeout:       60 * time.Second,
		CompressLevel: 9,
	},
	Encoder: string(encoderJson),
}

func (c *config) Validate() error {
	return nil
}
