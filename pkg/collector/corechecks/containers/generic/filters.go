// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package generic

import (
	"github.com/DataDog/datadog-agent/pkg/util/containers"
	"github.com/DataDog/datadog-agent/pkg/workloadmeta"
)

// ContainerFilter defines an interface to exclude containers based on Metadata
type ContainerFilter interface {
	IsExcluded(container *workloadmeta.Container) bool
}

// GenericContainerFilter allows any function to be used as a ContainerFilter
type GenericContainerFilter func(container *workloadmeta.Container) bool

// IsExcluded returns if a container should be excluded or not
func (f GenericContainerFilter) IsExcluded(container *workloadmeta.Container) bool {
	return f(container)
}

// ANDContainerFilter implements a logical AND between given filters
type ANDContainerFilter struct {
	Filters []ContainerFilter
}

// IsExcluded returns if a container should be excluded or not
func (f ANDContainerFilter) IsExcluded(container *workloadmeta.Container) bool {
	for _, filter := range f.Filters {
		if filter.IsExcluded(container) {
			return true
		}
	}

	return false
}

// LegacyContainerFilter allows to use old containers.Filter within this new framework
type LegacyContainerFilter struct {
	OldFilter *containers.Filter
}

// IsExcluded returns if a container should be excluded or not
func (f LegacyContainerFilter) IsExcluded(container *workloadmeta.Container) bool {
	if f.OldFilter == nil {
		return false
	}

	return f.OldFilter.IsExcluded(container.Name, container.Image.Name, container.Labels["io.kubernetes.pod.namespace"])
}

type RuntimeContainerFilter struct {
	Runtime workloadmeta.ContainerRuntime
}

func (f RuntimeContainerFilter) IsExcluded(container *workloadmeta.Container) bool {
	return container.Runtime != f.Runtime
}
