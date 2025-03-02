// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// +build containerd

package containerd

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/containerd/containerd"
	containerdevents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/api/types"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/events"
	"github.com/containerd/containerd/oci"
	prototypes "github.com/gogo/protobuf/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	containerdutil "github.com/DataDog/datadog-agent/pkg/util/containerd"
)

type mockItf struct {
	mockEvents            func() containerd.EventService
	mockContainers        func() ([]containerd.Container, error)
	mockContainer         func(id string) (containerd.Container, error)
	mockContainerWithCtx  func(ctx context.Context, id string) (containerd.Container, error)
	mockEnvVars           func(ctn containerd.Container) (map[string]string, error)
	mockMetadata          func() (containerd.Version, error)
	mockImage             func(ctn containerd.Container) (containerd.Image, error)
	mockImageSize         func(ctn containerd.Container) (int64, error)
	mockTaskMetrics       func(ctn containerd.Container) (*types.Metric, error)
	mockTaskPids          func(ctn containerd.Container) ([]containerd.ProcessInfo, error)
	mockInfo              func(ctn containerd.Container) (containers.Container, error)
	mockLabels            func(ctn containerd.Container) (map[string]string, error)
	mockLabelsWithContext func(ctx context.Context, ctn containerd.Container) (map[string]string, error)
	mockNamespace         func() string
	mockSpec              func(ctn containerd.Container) (*oci.Spec, error)
	mockSpecWithContext   func(ctx context.Context, ctn containerd.Container) (*oci.Spec, error)
	mockStatus            func(ctn containerd.Container) (containerd.ProcessStatus, error)
}

func (m *mockItf) Image(ctn containerd.Container) (containerd.Image, error) {
	return m.mockImage(ctn)
}

func (m *mockItf) ImageSize(ctn containerd.Container) (int64, error) {
	return m.mockImageSize(ctn)
}

func (m *mockItf) Labels(ctn containerd.Container) (map[string]string, error) {
	return m.mockLabels(ctn)
}

func (m *mockItf) LabelsWithContext(ctx context.Context, ctn containerd.Container) (map[string]string, error) {
	return m.mockLabelsWithContext(ctx, ctn)
}

func (m *mockItf) Info(ctn containerd.Container) (containers.Container, error) {
	return m.mockInfo(ctn)
}

func (m *mockItf) TaskMetrics(ctn containerd.Container) (*types.Metric, error) {
	return m.mockTaskMetrics(ctn)
}

func (m *mockItf) TaskPids(ctn containerd.Container) ([]containerd.ProcessInfo, error) {
	return m.mockTaskPids(ctn)
}

func (m *mockItf) Metadata() (containerd.Version, error) {
	return m.mockMetadata()
}

func (m *mockItf) Namespace() string {
	return m.mockNamespace()
}

func (m *mockItf) Containers() ([]containerd.Container, error) {
	return m.mockContainers()
}

func (m *mockItf) Container(id string) (containerd.Container, error) {
	return m.mockContainer(id)
}

func (m *mockItf) ContainerWithContext(ctx context.Context, id string) (containerd.Container, error) {
	return m.mockContainerWithCtx(ctx, id)
}

func (m *mockItf) GetEvents() containerd.EventService {
	return m.mockEvents()
}

func (m *mockItf) Spec(ctn containerd.Container) (*oci.Spec, error) {
	return m.mockSpec(ctn)
}

func (m *mockItf) SpecWithContext(ctx context.Context, ctn containerd.Container) (*oci.Spec, error) {
	return m.mockSpecWithContext(ctx, ctn)
}

func (m *mockItf) EnvVars(ctn containerd.Container) (map[string]string, error) {
	return m.mockEnvVars(ctn)
}

func (m *mockItf) Status(ctn containerd.Container) (containerd.ProcessStatus, error) {
	return m.mockStatus(ctn)
}

type mockEvt struct {
	events.Publisher
	events.Forwarder
	mockSubscribe func(ctx context.Context, filter ...string) (ch <-chan *events.Envelope, errs <-chan error)
}

func (m *mockEvt) Subscribe(ctx context.Context, filters ...string) (ch <-chan *events.Envelope, errs <-chan error) {
	return m.mockSubscribe(ctx)
}

// TestCheckEvent is an integration test as the underlying logic that we test is the listener for events.
func TestCheckEvents(t *testing.T) {
	cha := make(chan *events.Envelope)
	errorsCh := make(chan error)
	me := &mockEvt{
		mockSubscribe: func(ctx context.Context, filter ...string) (ch <-chan *events.Envelope, errs <-chan error) {
			return cha, errorsCh
		},
	}
	itf := &mockItf{
		mockEvents: func() containerd.EventService {
			return containerd.EventService(me)
		},
	}
	// Test the basic listener
	sub := CreateEventSubscriber("subscriberTest1", "k9s.io", nil)
	sub.CheckEvents(containerdutil.ContainerdItf(itf))

	tp := containerdevents.TaskPaused{
		ContainerID: "42",
	}
	vp, err := tp.Marshal()
	assert.NoError(t, err)

	en := events.Envelope{
		Timestamp: time.Now(),
		Topic:     "/tasks/paused",
		Event: &prototypes.Any{
			Value: vp,
		},
	}
	cha <- &en

	timeout := time.NewTimer(2 * time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	condition := false
	for {
		select {
		case <-ticker.C:
			if !sub.IsRunning() {
				continue
			}
			condition = true
		case <-timeout.C:
			require.FailNow(t, "Timeout waiting event listener to be healthy")
		}
		if condition {
			break
		}
	}

	ev := sub.Flush(time.Now().Unix())
	assert.Len(t, ev, 1)
	assert.Equal(t, ev[0].Topic, "/tasks/paused")
	errorsCh <- fmt.Errorf("chan breaker")
	condition = false
	for {
		select {
		case <-ticker.C:
			if sub.IsRunning() {
				continue
			}
			condition = true
		case <-timeout.C:
			require.FailNow(t, "Timeout waiting for error")
		}
		if condition {
			break
		}
	}

	// Test the multiple events one unsupported
	sub = CreateEventSubscriber("subscriberTest2", "k9s.io", nil)
	sub.CheckEvents(containerdutil.ContainerdItf(itf))

	tk := containerdevents.TaskOOM{
		ContainerID: "42",
	}
	vk, err := tk.Marshal()
	assert.NoError(t, err)

	ek := events.Envelope{
		Timestamp: time.Now(),
		Topic:     "/tasks/oom",
		Event: &prototypes.Any{
			Value: vk,
		},
	}

	nd := containerdevents.NamespaceDelete{
		Name: "k10s.io",
	}
	vnd, err := nd.Marshal()
	assert.NoError(t, err)

	evnd := events.Envelope{
		Timestamp: time.Now(),
		Topic:     "/namespaces/delete",
		Event: &prototypes.Any{
			Value: vnd,
		},
	}

	cha <- &ek
	cha <- &evnd

	condition = false
	for {
		select {
		case <-ticker.C:
			if !sub.IsRunning() {
				continue
			}
			condition = true
		case <-timeout.C:
			require.FailNow(t, "Timeout waiting event listener to be healthy")
		}
		if condition {
			break
		}
	}
	ev2 := sub.Flush(time.Now().Unix())
	fmt.Printf("\n\n 2/ Flush %v\n\n", ev2)
	assert.Len(t, ev2, 1)
	assert.Equal(t, ev2[0].Topic, "/tasks/oom")

}
