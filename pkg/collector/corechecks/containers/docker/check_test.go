// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package docker

import (
	"testing"
	"time"

	"github.com/DataDog/datadog-agent/pkg/collector/corechecks/containers/generic"
	taggerUtils "github.com/DataDog/datadog-agent/pkg/tagger/utils"
	"github.com/DataDog/datadog-agent/pkg/util/containers/v2/metrics"
	"github.com/DataDog/datadog-agent/pkg/util/docker"
	"github.com/DataDog/datadog-agent/pkg/workloadmeta"
	"github.com/stretchr/testify/assert"
)

func createContainerMeta(runtime, cID string) *workloadmeta.Container {
	return &workloadmeta.Container{
		EntityID: workloadmeta.EntityID{
			Kind: workloadmeta.KindContainer,
			ID:   cID,
		},
		Runtime: workloadmeta.ContainerRuntime(runtime),
		State: workloadmeta.ContainerState{
			Running:   true,
			StartedAt: time.Now(),
		},
	}
}

func TestDockerCheckGenericPart(t *testing.T) {
	// Creating mocks
	containersMeta := []*workloadmeta.Container{
		// Container with full stats
		createContainerMeta("docker", "cID100"),
		// Should never been called as we are in the Docker check
		createContainerMeta("containerd", "cID101"),
	}

	containersStats := map[string]metrics.MockContainerEntry{
		"cID100": metrics.GetFullSampleContainerEntry(),
		"cID101": metrics.GetFullSampleContainerEntry(),
	}

	dockerClient := docker.MockClient{}

	// Inject mock processor in check
	mockSender, processor, accessor := generic.CreateTestProcessor(containersMeta, nil, containersStats, metricsAdapter{}, getProcessorFilter(nil))
	err := processor.Run(mockSender, 0)
	assert.ErrorIs(t, err, nil)

	// Create Docker check
	check := DockerCheck{
		instance: &DockerConfig{
			CollectExitCodes:   true,
			CollectImagesStats: true,
			CollectImageSize:   true,
			CollectDiskStats:   true,
			CollectVolumeCount: true,
			CollectEvent:       true,
		},
		processor:         *processor,
		containerAccessor: accessor,
		dockerHostname:    "testhostname",
	}

	err = check.run(mockSender, &dockerClient)
	assert.NoError(t, err)

	expectedTags := []string{"runtime:docker"}
	// mockSender.AssertNumberOfCalls(t, "Rate", 13)
	// mockSender.AssertNumberOfCalls(t, "Gauge", 13)

	mockSender.AssertMetricInRange(t, "Gauge", "docker.uptime", 0, 600, "", expectedTags)
	mockSender.AssertMetric(t, "Rate", "docker.cpu.usage", 1e-6, "", expectedTags)
	mockSender.AssertMetric(t, "Rate", "docker.cpu.user", 3e-6, "", expectedTags)
	mockSender.AssertMetric(t, "Rate", "docker.cpu.system", 2e-6, "", expectedTags)
	mockSender.AssertMetric(t, "Rate", "docker.cpu.throttled.time", 1e-6, "", expectedTags)
	mockSender.AssertMetric(t, "Rate", "docker.cpu.throttled", 0, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.cpu.limit", 5, "", expectedTags)

	mockSender.AssertMetric(t, "Gauge", "docker.kmem.usage", 40, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.limit", 42000, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.soft_limit", 40000, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.rss", 300, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.cache", 200, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.swap", 0, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.mem.failed_count", 10, "", expectedTags)

	expectedFooTags := taggerUtils.ConcatenateStringTags(expectedTags, "device:/dev/foo", "device_name:/dev/foo")
	mockSender.AssertMetric(t, "Rate", "docker.io.read_bytes", 100, "", expectedFooTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.read_operations", 10, "", expectedFooTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.write_bytes", 200, "", expectedFooTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.write_operations", 20, "", expectedFooTags)
	expectedBarTags := taggerUtils.ConcatenateStringTags(expectedTags, "device:/dev/bar", "device_name:/dev/bar")
	mockSender.AssertMetric(t, "Rate", "docker.io.read_bytes", 100, "", expectedBarTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.read_operations", 10, "", expectedBarTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.write_bytes", 200, "", expectedBarTags)
	mockSender.AssertMetric(t, "Rate", "docker.io.write_operations", 20, "", expectedBarTags)

	mockSender.AssertMetric(t, "Gauge", "docker.thread.count", 10, "", expectedTags)
	mockSender.AssertMetric(t, "Gauge", "docker.thread.limit", 20, "", expectedTags)
}
