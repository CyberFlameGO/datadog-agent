package stats

import (
	"github.com/DataDog/datadog-agent/pkg/trace/config"
	"github.com/DataDog/datadog-agent/pkg/trace/pb"
	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestPipelineStatsAggregator(t *testing.T) {
	out := make(chan pb.PipelineStatsPayload, 10)
	a := NewPipelineStatsAggregator(&config.AgentConfig{}, out)
	bucketStart := time.Now().Truncate(time.Second*10)
	summary, _ := ddsketch.NewDefaultDDSketch(0.01)
	summary.Add(5)
	summaryBytes, err := proto.Marshal(summary.ToProto())
	assert.Nil(t, err)
	in := pb.ClientPipelineStatsPayload{
		Stats: []pb.ClientPipelineStatsBucket{
			{
				Start: uint64(bucketStart.UnixNano()),
				Duration: uint64(time.Second*10),
				Stats: []pb.ClientGroupedPipelineStats{
					{
						Service: "a",
						ReceivingPipelineName: "pipeline",
						PipelineHash: 45,
						Summary: summaryBytes,
					},
				},
			},
		},
	}
	a.add(in)
	a.add(in)
	a.flushOnTime(bucketStart)
	assert.Len(t, out, 0)
	a.flushOnTime(bucketStart.Add(time.Second*11))
	assert.Len(t, out, 1)
}
