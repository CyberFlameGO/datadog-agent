// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package generic

import (
	"github.com/DataDog/datadog-agent/pkg/aggregator/mocksender"
	"github.com/DataDog/datadog-agent/pkg/util/containers/v2/metrics"
	"github.com/DataDog/datadog-agent/pkg/util/containers/v2/metrics/provider"
	"github.com/DataDog/datadog-agent/pkg/workloadmeta"
)

// MockContainerAccessor is a dummy ContainerLister for tests
type MockContainerAccessor struct {
	containers []*workloadmeta.Container
	err        error
}

func (l *MockContainerAccessor) List() ([]*workloadmeta.Container, error) {
	return l.containers, l.err
}

func CreateTestProcessor(listerContainers []*workloadmeta.Container,
	listerError error,
	metricsContainers map[string]metrics.MockContainerEntry,
	metricsAdapter MetricsAdapter,
	containerFilter ContainerFilter) (*mocksender.MockSender, *Processor, ContainerAccessor) {
	mockProvider := metrics.NewMockMetricsProvider()
	mockCollector := metrics.NewMockCollector("testCollector")
	for _, runtime := range provider.AllLinuxRuntimes {
		mockProvider.RegisterConcreteCollector(runtime, mockCollector)
	}
	for cID, entry := range metricsContainers {
		mockCollector.SetContainerEntry(cID, entry)
	}

	mockAccessor := MockContainerAccessor{
		containers: listerContainers,
		err:        listerError,
	}

	mockedSender := mocksender.NewMockSender("generic-container")
	mockedSender.SetupAcceptAll()

	p := &Processor{
		metricsProvider: mockProvider,
		ctrLister:       &mockAccessor,
		metricsAdapter:  metricsAdapter,
		ctrFilter:       containerFilter,
	}

	return mockedSender, p, &mockAccessor
}
