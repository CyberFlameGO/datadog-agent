package network

import (
	netebpf "github.com/DataDog/datadog-agent/pkg/network/ebpf"
)

type TagsSet struct {
	set          map[string]uint32
	nextTagValue uint32
}

// GetStaticTags() return the string list of static tags from network.ConnectionStats.Tags
func GetStaticTags(staticTags uint64) (tags []string) {
	for tag, str := range netebpf.StaticTags {
		if (staticTags & tag) > 0 {
			tags = append(tags, str)
		}
	}
	return tags
}

// NewTagsSet() create a new set of Tags
func NewTagsSet() *TagsSet {
	return &TagsSet{
		set:          make(map[string]uint32),
		nextTagValue: uint32(0),
	}
}

// Size return the numbers of unique tag
func (ts *TagsSet) Size() int {
	return len(ts.set)
}

// Add a tag to the set and return his index
func (ts *TagsSet) Add(tag string) (v uint32) {
	if v, found := ts.set[tag]; found {
		return v
	}
	v = ts.nextTagValue
	ts.set[tag] = v
	ts.nextTagValue++
	return v
}

// GetStrings() return in order the tags
func (ts *TagsSet) GetStrings() []string {
	strs := make([]string, len(ts.set))
	for k, v := range ts.set {
		strs[v] = k
	}
	return strs
}
