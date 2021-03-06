// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package filestream

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	loginp "github.com/elastic/beats/v7/filebeat/input/filestream/internal/input-logfile"
	input "github.com/elastic/beats/v7/filebeat/input/v2"
	v2 "github.com/elastic/beats/v7/filebeat/input/v2"
	"github.com/elastic/beats/v7/libbeat/beat"
	"github.com/elastic/beats/v7/libbeat/common"
	"github.com/elastic/beats/v7/libbeat/logp"
	"github.com/elastic/beats/v7/libbeat/statestore"
	"github.com/elastic/beats/v7/libbeat/statestore/storetest"
)

type inputTestingEnvironment struct {
	t          *testing.T
	workingDir string
	stateStore loginp.StateStore
	pipeline   *mockPipelineConnector

	pluginInitOnce sync.Once
	plugin         v2.Plugin

	wg sync.WaitGroup
}

type registryEntry struct {
	Cursor struct {
		Offset int `json:"offset"`
	} `json:"cursor"`
}

func newInputTestingEnvironment(t *testing.T) *inputTestingEnvironment {
	return &inputTestingEnvironment{
		t:          t,
		workingDir: t.TempDir(),
		stateStore: openTestStatestore(),
		pipeline:   &mockPipelineConnector{},
	}
}

func (e *inputTestingEnvironment) mustCreateInput(config map[string]interface{}) v2.Input {
	manager := e.getManager()
	c := common.MustNewConfigFrom(config)
	inp, err := manager.Create(c)
	if err != nil {
		e.t.Fatalf("failed to create input using manager: %+v", err)
	}
	return inp
}

func (e *inputTestingEnvironment) getManager() v2.InputManager {
	e.pluginInitOnce.Do(func() {
		e.plugin = Plugin(logp.L(), e.stateStore)
	})
	return e.plugin.Manager
}

func (e *inputTestingEnvironment) startInput(ctx context.Context, inp v2.Input) {
	e.wg.Add(1)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()

		inputCtx := input.Context{Logger: logp.L(), Cancelation: ctx}
		inp.Run(inputCtx, e.pipeline)
	}(&e.wg)
}

func (e *inputTestingEnvironment) waitUntilInputStops() {
	e.wg.Wait()
}

func (e *inputTestingEnvironment) mustWriteLinesToFile(filename string, lines []byte) {
	path := e.abspath(filename)
	err := ioutil.WriteFile(path, lines, 0644)
	if err != nil {
		e.t.Fatalf("failed to write file '%s': %+v", path, err)
	}
}

func (e *inputTestingEnvironment) mustRenameFile(oldname, newname string) {
	err := os.Rename(e.abspath(oldname), e.abspath(newname))
	if err != nil {
		e.t.Fatalf("failed to rename file '%s': %+v", oldname, err)
	}
}

func (e *inputTestingEnvironment) abspath(filename string) string {
	return filepath.Join(e.workingDir, filename)
}

// requireOffsetInRegistry checks if the expected offset is set for a file.
func (e *inputTestingEnvironment) requireOffsetInRegistry(filename string, expectedOffset int) {
	filepath := e.abspath(filename)
	fi, err := os.Stat(filepath)
	if err != nil {
		e.t.Fatalf("cannot stat file when cheking for offset: %+v", err)
	}

	identifier, _ := newINodeDeviceIdentifier(nil)
	src := identifier.GetSource(loginp.FSEvent{Info: fi, Op: loginp.OpCreate, NewPath: filepath})
	entry := e.getRegistryState(src.Name())

	require.Equal(e.t, expectedOffset, entry.Cursor.Offset)
}

func (e *inputTestingEnvironment) getRegistryState(key string) registryEntry {
	inputStore, _ := e.stateStore.Access()

	var entry registryEntry
	err := inputStore.Get(key, &entry)
	if err != nil {
		e.t.Fatalf("error when getting expected key '%s' from store: %+v", key, err)
	}

	return entry
}

// waitUntilEventCount waits until total count events arrive to the client.
func (e *inputTestingEnvironment) waitUntilEventCount(count int) {
	for {
		sum := 0
		for _, c := range e.pipeline.clients {
			sum += len(c.GetEvents())
		}
		if sum == count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type testInputStore struct {
	registry *statestore.Registry
}

func openTestStatestore() loginp.StateStore {
	return &testInputStore{
		registry: statestore.NewRegistry(storetest.NewMemoryStoreBackend()),
	}
}

func (s *testInputStore) Close() {
	s.registry.Close()
}

func (s *testInputStore) Access() (*statestore.Store, error) {
	return s.registry.Get("filebeat")
}

func (s *testInputStore) CleanupInterval() time.Duration {
	return 24 * time.Hour
}

type mockClient struct {
	publishes  []beat.Event
	ackHandler beat.ACKer
	closed     bool
	mtx        sync.Mutex
}

// GetEvents returns the published events
func (c *mockClient) GetEvents() []beat.Event {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	return c.publishes
}

// Publish mocks the Client Publish method
func (c *mockClient) Publish(e beat.Event) {
	c.PublishAll([]beat.Event{e})
}

// PublishAll mocks the Client PublishAll method
func (c *mockClient) PublishAll(events []beat.Event) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	for _, event := range events {
		c.publishes = append(c.publishes, event)
		c.ackHandler.AddEvent(event, true)
	}
	c.ackHandler.ACKEvents(len(events))
}

// Close mocks the Client Close method
func (c *mockClient) Close() error {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if c.closed {
		return fmt.Errorf("mock client already closed")
	}

	c.closed = true
	return nil
}

// mockPipelineConnector mocks the PipelineConnector interface
type mockPipelineConnector struct {
	clients []*mockClient
	mtx     sync.Mutex
}

// GetAllEvents returns all events associated with a pipeline
func (pc *mockPipelineConnector) GetAllEvents() []beat.Event {
	var evList []beat.Event
	for _, clientEvents := range pc.clients {
		evList = append(evList, clientEvents.GetEvents()...)
	}

	return evList
}

// Connect mocks the PipelineConnector Connect method
func (pc *mockPipelineConnector) Connect() (beat.Client, error) {
	return pc.ConnectWith(beat.ClientConfig{})
}

// ConnectWith mocks the PipelineConnector ConnectWith method
func (pc *mockPipelineConnector) ConnectWith(config beat.ClientConfig) (beat.Client, error) {
	pc.mtx.Lock()
	defer pc.mtx.Unlock()

	c := &mockClient{
		ackHandler: config.ACKHandler,
	}

	pc.clients = append(pc.clients, c)

	return c, nil
}
