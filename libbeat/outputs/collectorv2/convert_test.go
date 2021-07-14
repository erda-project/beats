// Copyright (c) 2021 Terminus, Inc.

// This program is free software: you can use, redistribute, and/or modify
// it under the terms of the GNU Affero General Public License, version 3
// or later ("AGPL"), as published by the Free Software Foundation.

// This program is distributed in the hope that it will be useful, but WITHOUT
// ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
// FITNESS FOR A PARTICULAR PURPOSE.

// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package collectorv2

import (
	"compress/gzip"
	"io/ioutil"
	"log"
	"reflect"
	"testing"
	"time"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/pb"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/stretchr/testify/assert"
)

func mockClient(enc encoderName) *client {
	cli, err := newClient("localhost", config{
		JobPath:       "/jobs",
		ContainerPath: "/containers",
		Auth: authConfig{
			Type: "basic",
			Property: map[string]string{
				"auth_username": "xxx",
				"auth_password": "yyy",
			},
		},
		Method:        "POST",
		Timeout:       10,
		MaxRetries:    1,
		Backoff:       backoff{},
		CompressLevel: 9,
		Limiter:       limiterConfig{},
		Output: outputConfig{
			CompressLevel: 9,
		},
		Encoder: string(enc)},
		nil)
	if err != nil {
		log.Fatal(err)
	}
	return cli
}

func TestSerializeJson(t *testing.T) {
	c := mockClient(encoderJson)
	r, err := c.serialize(mockEvent(1))
	assert.Nil(t, err)
	gr, err := gzip.NewReader(r)
	assert.Nil(t, err)
	o, err := ioutil.ReadAll(gr)
	assert.Nil(t, err)
	assert.Equal(t, `{"Logs":[{"id":"77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2","source":"container","stream":"stdout","offset":17420730,"timestamp":1415792726371000000,"content":"\u001b[37mDEBU\u001b[0m[2021-04-22 14:18:52.265950181] finished handle request GET /health (took 107.411µs) ","tags":{"container_name":"qa","dice_cluster_name":"terminus-dev","dice_component":"qa","pod_name":"dice-qa-7cb5b7fd4-494zb","pod_namespace":"default"}}]}`, string(o))
}

func TestSerializeProtobuf(t *testing.T) {
	c := mockClient(encoderProtobuf)
	r, err := c.serialize(mockEvent(1))
	assert.Nil(t, err)
	gr, err := gzip.NewReader(r)
	assert.Nil(t, err)
	o, err := ioutil.ReadAll(gr)
	assert.Nil(t, err)
	batch := &pb.LogBatch{}
	err = batch.Unmarshal(o)
	assert.Nil(t, err)
	assert.Equal(t, &pb.LogBatch{
		Logs: []*pb.Log{
			{
				Id:      "77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2",
				Source:  "container",
				Stream:  "stdout",
				Offset:  17420730,
				Content: "\u001B[37mDEBU\u001B[0m[2021-04-22 14:18:52.265950181] finished handle request GET /health (took 107.411µs) ",
				Tags: map[string]string{
					"dice_cluster_name": "terminus-dev",
					"pod_name":          "dice-qa-7cb5b7fd4-494zb",
					"pod_namespace":     "default",
					"container_name":    "qa",
					"dice_component":    "qa",
				},
				Timestamp: 1415792726371000000,
			},
		},
	}, batch)
}

func BenchmarkSerializeJson(b *testing.B) {
	b.ReportAllocs()
	c, events := mockClient(encoderJson), mockEvent(500)
	for i := 0; i < b.N; i++ {
		c.serialize(events)
	}
}

func BenchmarkSerializeProtobuf(b *testing.B) {
	b.ReportAllocs()
	c, events := mockClient(encoderProtobuf), mockEvent(500)
	for i := 0; i < b.N; i++ {
		c.serialize(events)
	}
}

func mockEvent(num int) []publisher.Event {
	t, _ := time.Parse("2006-01-02T15:04:05.000Z", "2014-11-12T11:45:26.371Z")
	event := publisher.Event{
		Content: beat.Event{
			Timestamp: t,
			Meta:      nil,
			Fields: common.MapStr{
				"terminus": common.MapStr{
					"tags": common.MapStr{
						"DICE_CLUSTER_NAME": "terminus-dev",
						"pod_name":          "dice-qa-7cb5b7fd4-494zb",
						"pod_namespace":     "default",
						"container_name":    "qa",
						"DICE_COMPONENT":    "qa",
					},
					"labels": common.MapStr{},
					"id":     "77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2",
					"source": "container",
				},
				"log": common.MapStr{
					"offset": 17420730,
					"file": common.MapStr{
						"path": "/var/lib/docker/containers/77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2/77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2-json.log",
					},
				},
				"message": "\u001b[37mDEBU\u001b[0m[2021-04-22 14:18:52.265950181] finished handle request GET /health (took 107.411µs) ",
				"stream":  "stdout",
			},
			Private:    nil,
			TimeSeries: false,
		},
		Flags: publisher.GuaranteedSend,
		Cache: publisher.EventCache{},
	}
	res := make([]publisher.Event, num)
	for i := range res {
		res[i] = event
	}
	return res
}

func Test_convertEvent(t *testing.T) {
	type args struct {
		event publisher.Event
	}
	tests := []struct {
		name    string
		args    args
		want    *pb.Log
		wantErr bool
	}{
		{
			"test convert event",
			args{event: mockEvent(1)[0]},
			&pb.Log{
				Id:      "77e90e85233cb3ec1bfd7655633248056a3fc03e092596c405a5b158cc8885b2",
				Source:  "container",
				Stream:  "stdout",
				Offset:  17420730,
				Content: "\u001B[37mDEBU\u001B[0m[2021-04-22 14:18:52.265950181] finished handle request GET /health (took 107.411µs) ",
				Tags: map[string]string{
					"dice_cluster_name": "terminus-dev",
					"pod_name":          "dice-qa-7cb5b7fd4-494zb",
					"pod_namespace":     "default",
					"container_name":    "qa",
					"dice_component":    "qa",
				},
				Timestamp: 1415792726371000000,
				Labels:    map[string]string{},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := convertEvent(tt.args.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("convertEvent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("convertEvent() got = %v, want %v", got, tt.want)
			}
		})
	}
}
