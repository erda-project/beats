package add_terminus_metadata

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/common/docker"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/processors"
	"github.com/elastic/beats/v7/libbeat/processors/actions"
	"github.com/elastic/gosigar/cgroup"
	"github.com/pkg/errors"
)

const (
	processorName         = "add_terminus_metadata"
	dockerContainerIDKey  = "container.id"
	cgroupCacheExpiration = 5 * time.Minute
)

// processGroupPaths returns the cgroups associated with a process. This enables
// unit testing by allowing us to stub the OS interface.
var processCgroupPaths = cgroup.ProcessCgroupPaths

func init() {
	processors.RegisterPlugin(processorName, New)
}

type addDockerMetadata struct {
	log                     *logp.Logger
	watcher                 docker.Watcher
	fields                  []string
	sourceProcessor         processors.Processor
	allLogAnalyse           bool
	monitorLogCollectorAddr string

	pidFields       []string      // Field names that contain PIDs.
	cgroups         *common.Cache // Cache of PID (int) to cgropus (map[string]string).
	hostFS          string        // Directory where /proc is found
	dedot           bool          // If set to true, replace dots in labels with `_`.
	dockerAvailable bool          // If Docker exists in env, then it is set to true

	jobIDKey           string
	outputCollectorKey string
	defaultTags        []string
	tagKeyRel          []string
	labelKeyRel        []string // TODO remove?
}

func New(cfg *common.Config) (processors.Processor, error) {
	return buildDockerMetadataProcessor(logp.NewLogger(processorName), cfg, docker.NewWatcher)
}

func buildDockerMetadataProcessor(log *logp.Logger, cfg *common.Config, watcherConstructor docker.WatcherConstructor) (processors.Processor, error) {
	config := defaultConfig()
	if err := cfg.Unpack(&config); err != nil {
		return nil, errors.Wrapf(err, "fail to unpack the %v configuration", processorName)
	}

	var dockerAvailable bool

	watcher, err := watcherConstructor(log, config.Host, config.TLS, config.MatchShortID)
	if err != nil {
		dockerAvailable = false
		log.Errorf("%v: docker environment not detected: %+v", processorName, err)
	} else {
		dockerAvailable = true
		log.Infof("%v: docker environment detected", processorName)
		if err = watcher.Start(); err != nil {
			return nil, errors.Wrap(err, "failed to start watcher")
		}
	}

	// Use extract_field processor to get container ID from source file path.
	var sourceProcessor processors.Processor
	if config.MatchSource {
		var procConf, _ = common.NewConfigFrom(map[string]interface{}{
			"field":     "log.file.path",
			"separator": string(os.PathSeparator),
			"index":     config.SourceIndex,
			"target":    dockerContainerIDKey,
		})
		sourceProcessor, err = actions.NewExtractField(procConf)
		if err != nil {
			return nil, err
		}
	}

	return &addDockerMetadata{
		dockerAvailable:         dockerAvailable,
		log:                     logp.NewLogger(processorName),
		watcher:                 watcher,
		fields:                  config.Fields,
		sourceProcessor:         sourceProcessor,
		pidFields:               config.MatchPIDs,
		hostFS:                  config.HostFS,
		jobIDKey:                config.JobIDKey,
		outputCollectorKey:      config.OutputCollectorKey,
		allLogAnalyse:           config.AllLogAnalyse,
		monitorLogCollectorAddr: config.MonitorLogCollectorAddr,
		defaultTags:             strings.Split(config.DefaultTags, ","),
		tagKeyRel:               strings.Split(config.TagKeys, ","),
		labelKeyRel:             strings.Split(config.LabelKeys, ","),
	}, nil
}

func lazyCgroupCacheInit(d *addDockerMetadata) {
	if d.cgroups == nil {
		d.log.Debug("Initializing cgroup cache")
		evictionListener := func(k common.Key, v common.Value) {
			d.log.Debugf("Evicted cached cgroups for PID=%v", k)
		}
		d.cgroups = common.NewCacheWithRemovalListener(cgroupCacheExpiration, 100, evictionListener)
		d.cgroups.StartJanitor(5 * time.Second)
	}
}

func (d *addDockerMetadata) Run(event *beat.Event) (*beat.Event, error) {
	var cid string
	var err error

	if !d.dockerAvailable {
		return event, nil
	}

	// Extract CID from the filepath contained in the "log.file.path" field.
	if d.sourceProcessor != nil {
		lfp, _ := event.Fields.GetValue("log.file.path")
		if lfp != nil {
			event, err = d.sourceProcessor.Run(event)
			if err != nil {
				d.log.Debugf("Error while extracting container ID from source path: %v", err)
				return event, nil
			}

			if v, err := event.GetValue(dockerContainerIDKey); err == nil {
				cid, _ = v.(string)
			}
		}
	}

	// Lookup CID using process cgroup membership data.
	if cid == "" && len(d.pidFields) > 0 {
		if id := d.lookupContainerIDByPID(event); id != "" {
			cid = id
			event.PutValue(dockerContainerIDKey, cid)
		}
	}

	// Lookup CID from the user defined field names.
	if cid == "" && len(d.fields) > 0 {
		for _, field := range d.fields {
			value, err := event.GetValue(field)
			if err != nil {
				continue
			}

			if strValue, ok := value.(string); ok {
				cid = strValue
				break
			}
		}
	}

	if cid == "" {
		return event, nil
	}

	// default
	event.Fields.DeepUpdate(common.MapStr{
		"terminus": common.MapStr{
			"id":     cid,
			"source": "container",
		},
	})

	container := d.watcher.Container(cid)
	if container == nil {
		d.log.Debugf("Container not found: cid=%s", cid)
		return event, nil
	}

	var jobID, outputCollector string
	tags := make(map[string]interface{})
	labelRel := make(map[string]interface{})

	// check is Job and update jobID
	if v, ok := container.LookUpEnv(d.jobIDKey); ok {
		jobID = v
	}
	if v, ok := container.LookUpLabel(d.jobIDKey); ok {
		jobID = v
	}
	// 检查并设置导出地址
	if v, ok := container.LookUpEnv(d.outputCollectorKey); ok {
		outputCollector = v
	}
	if v, ok := container.LookUpLabel(d.outputCollectorKey); ok {
		outputCollector = v
	}
	if d.allLogAnalyse {
		outputCollector = d.monitorLogCollectorAddr
	}

	// update tag from containers labels and envs
	for _, key := range d.tagKeyRel {
		if v, ok := container.LookUpLabel(key); ok {
			key = strings.TrimPrefix(key, "io.kubernetes.")
			key = normalize(key)
			tags[key] = v
		}
		if v, ok := container.LookUpEnv(key); ok {
			tags[normalize(key)] = v
		}
	}

	// update tag from containers labels and envs.
	for _, key := range d.labelKeyRel {
		if v, ok := container.LookUpLabel(key); ok {
			labelRel[key] = v
		}
		if v, ok := container.LookUpEnv(key); ok {
			labelRel[key] = v
		}
	}
	meta := common.MapStr{}

	if jobID == "" {
		meta.Put("terminus.source", "container")
		meta.Put("terminus.id", container.ID)
	} else {
		meta.Put("terminus.source", "job")
		meta.Put("terminus.id", jobID)
	}

	if outputCollector != "" {
		meta.Put("terminus.output.collector", outputCollector)
	}

	d.bindDefaultTags(tags)
	meta.Put("terminus.tags", tags)
	meta.Put("terminus.labels", labelRel)
	event.Fields.DeepUpdate(meta)

	return event, nil
}

func (d *addDockerMetadata) bindDefaultTags(tags map[string]interface{}) {
	for _, key := range d.defaultTags {
		env, ok := os.LookupEnv(key)
		_, ok2 := tags[key]
		if !ok2 && ok {
			tags[key] = env
		}
	}
}

func (d *addDockerMetadata) String() string {
	return fmt.Sprintf("%v=[match_fields=[%v] match_pids=[%v]]",
		processorName, strings.Join(d.fields, ", "), strings.Join(d.pidFields, ", "))
}

// lookupContainerIDByPID finds the container ID based on PID fields contained
// in the event.
func (d *addDockerMetadata) lookupContainerIDByPID(event *beat.Event) string {
	var cgroups map[string]string
	for _, field := range d.pidFields {
		v, err := event.GetValue(field)
		if err != nil {
			continue
		}

		pid, ok := common.TryToInt(v)
		if !ok {
			d.log.Debugf("field %v is not a PID (type=%T, value=%v)", field, v, v)
			continue
		}

		cgroups, err = d.getProcessCgroups(pid)
		if err != nil && os.IsNotExist(errors.Cause(err)) {
			continue
		}
		if err != nil {
			d.log.Debugf("failed to get cgroups for pid=%v: %v", pid, err)
		}

		break
	}

	return getContainerIDFromCgroups(cgroups)
}

// getProcessCgroups returns a mapping of cgroup subsystem name to path. It
// returns an error if it failed to retrieve the cgroup info.
func (d *addDockerMetadata) getProcessCgroups(pid int) (map[string]string, error) {
	// Initialize at time of first use.
	lazyCgroupCacheInit(d)

	cgroups, ok := d.cgroups.Get(pid).(map[string]string)
	if ok {
		d.log.Debugf("Using cached cgroups for pid=%v", pid)
		return cgroups, nil
	}

	cgroups, err := processCgroupPaths(d.hostFS, pid)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read cgroups for pid=%v", pid)
	}

	d.cgroups.Put(pid, cgroups)
	return cgroups, nil
}

// getContainerIDFromCgroups checks all of the processes' paths to see if any
// of them are associated with Docker. Docker uses /docker/<CID> when
// naming cgroups and we use this to determine the container ID. If no container
// ID is found then an empty string is returned.
func getContainerIDFromCgroups(cgroups map[string]string) string {
	for _, path := range cgroups {
		if strings.HasPrefix(path, "/docker") {
			return filepath.Base(path)
		}
	}

	return ""
}

func normalize(name string) string {
	name = strings.Replace(name, ":", "_", -1)
	name = strings.Replace(name, ".", "_", -1)
	name = strings.Replace(name, "+", "_", -1)
	name = strings.Replace(name, "-", "_", -1)
	return name
}
