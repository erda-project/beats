package collectorv2

import (
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/outputs"
)

const selector = "collectorV2"

func init() {
	outputs.RegisterType(selector, makeClient)
}

func makeClient(
	_ outputs.IndexManager,
	_ beat.Info,
	observer outputs.Observer,
	cfg *common.Config,
) (outputs.Group, error) {
	config := defaultConfig
	if err := cfg.Unpack(&config); err != nil {
		return outputs.Fail(err)
	}

	hosts, err := outputs.ReadHostList(cfg)
	if err != nil {
		return outputs.Fail(err)
	}

	clients := make([]outputs.NetworkClient, len(hosts))
	for i, host := range hosts {
		var client outputs.NetworkClient
		client, err = newClient(host, config, observer)
		if err != nil {
			return outputs.Fail(err)
		}

		client = outputs.WithBackoff(client, config.Backoff.Init, config.Backoff.Max)
		clients[i] = client
	}

	return outputs.SuccessNet(config.LoadBalance, config.BulkMaxSize, config.MaxRetries, clients)
}
