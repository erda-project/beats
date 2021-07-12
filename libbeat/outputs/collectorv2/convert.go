package collectorv2

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/outputs/collectorv2/pb"
	"github.com/elastic/beats/v7/libbeat/publisher"
	"github.com/pkg/errors"
)

var typeConvertErr = errors.New("type convert failed")

func convertEvents(events []publisher.Event) *pb.LogBatch {
	res := &pb.LogBatch{
		Logs: make([]*pb.Log, 0, len(events)),
	}
	for i := range events {
		log, err := convertEvent(events[i])
		if err != nil {
			logp.Err("convertEvent with err: %s", err)
			continue
		}
		res.Logs = append(res.Logs, log)
	}
	return res
}

func convertEvent(event publisher.Event) (*pb.Log, error) {
	log := &pb.Log{
		Source: "container",
		Stream: "stdout",
	}

	source, _ := event.Content.GetValue("terminus.source")
	if v, ok := source.(string); !ok {
		return nil, typeConvertErr
	} else {
		log.Source = v
	}

	id, err := event.Content.GetValue("terminus.id")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get id value")
	}
	if v, ok := id.(string); !ok {
		return nil, typeConvertErr
	} else {
		log.Id = v
	}

	offset, err := event.Content.GetValue("log.offset")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get offset value")
	}
	switch offset.(type) {
	case int:
		log.Offset = int64(offset.(int))
	case int64:
		log.Offset = offset.(int64)
	default:
		return nil, typeConvertErr
	}

	stream, _ := event.Content.GetValue("stream")
	if v, ok := stream.(string); !ok {
		return nil, typeConvertErr
	} else {
		log.Stream = v
	}

	content, err := event.Content.GetValue("message")
	if err != nil {
		return nil, errors.Wrap(err, "fail to get message value")
	}
	if v, ok := content.(string); !ok {
		return nil, typeConvertErr
	} else {
		log.Content = v
	}

	tags := make(map[string]string)
	if d, err := event.Content.GetValue("terminus.tags"); err == nil {
		if v, err := convert(d); err == nil {
			tags = v
		}
	}
	log.Tags = tags

	labels := make(map[string]string)
	if d, err := event.Content.GetValue("terminus.labels"); err == nil {
		if v, err := convert(d); err == nil {
			labels = v
		}
	}
	log.Labels = labels

	log.Timestamp = event.Content.Timestamp.UnixNano()
	return log, nil
}

func convert(data interface{}) (map[string]string, error) {
	m, err := convertToMap(data)
	if err != nil {
		return nil, err
	}
	m = handle(m)
	return m, nil
}

func convertToMap(data interface{}) (map[string]string, error) {
	res := make(map[string]string)
	switch data.(type) {
	case map[string]string:
		res = data.(map[string]string)
	case common.MapStr:
		tmp := data.(common.MapStr)
		for k, v := range tmp {
			switch val := v.(type) {
			case string:
				res[k] = val
			case uint64:
				res[k] = strconv.Itoa(int(val))
			case uint32:
				res[k] = strconv.Itoa(int(val))
			case int64:
				res[k] = strconv.Itoa(int(val))
			case int32:
				res[k] = strconv.Itoa(int(val))
			case float64:
				res[k] = strconv.Itoa(int(val))
			case float32:
				res[k] = strconv.Itoa(int(val))
			}
			if v, ok := v.(string); ok {
				res[k] = v
			}
		}
	default:
		return nil, errors.New(fmt.Sprintf("no supported type: %s", reflect.TypeOf(data).Name()))
	}
	return res, nil
}

func handle(m map[string]string) map[string]string {
	res := make(map[string]string, len(m))
	for k, v := range m {
		if reflect.DeepEqual(v, reflect.Zero(reflect.TypeOf(v)).Interface()) {
			continue
		}
		res[strings.ToLower(k)] = v
	}
	return res
}
