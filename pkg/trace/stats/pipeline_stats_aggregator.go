package stats

import (
	"time"

	"github.com/DataDog/datadog-agent/pkg/trace/watchdog"
	"github.com/DataDog/datadog-agent/pkg/util/log"
	"github.com/DataDog/datadog-agent/pkg/trace/config"
	"github.com/DataDog/datadog-agent/pkg/trace/info"
	"github.com/DataDog/datadog-agent/pkg/trace/pb"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
)

const (
	pipelineBucketDuration       = 10 * time.Second
)

var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type pipelineStatsPoint struct {
	service string
	receivingPipelineName string
	parentHash uint64
	summary *ddsketch.DDSketch
}

type pipelineAggregationKey struct {
	env string
	version string
}

type pipelineBucket struct {
	pipelineStats map[pipelineAggregationKey]map[uint64]pipelineStatsPoint
}

func (b *pipelineBucket) add(bucket pb.ClientPipelineStatsBucket, env, version string) {
	key := pipelineAggregationKey{
		env: env,
		version: version,
	}
	points, ok := b.pipelineStats[key]
	if !ok {
		points = make(map[uint64]pipelineStatsPoint)
		b.pipelineStats[key] = points
	}
	for _, p := range bucket.Stats {
		sketch, err := ddsketch.DecodeDDSketch(p.Sketch, store.BufferedPaginatedStoreConstructor, sketchMapping)
		if err != nil {
			log.Errorf("error decoding sketch: %v.", err)
			continue
		}
		if point, ok := points[p.PipelineHash]; ok {
			// todo[piochelepiotr] Add check
			err := point.summary.MergeWith(sketch)
			if err != nil {
				log.Errorf("error merging sketches: %v.", err)
				continue
			}
			continue
		}
		points[p.PipelineHash] = pipelineStatsPoint{
			receivingPipelineName: p.ReceivingPipelineName,
			parentHash: p.ParentHash,
			service: p.Service,
			summary: sketch,
		}
	}
}

func (b *pipelineBucket) export(start uint64, duration uint64) (p []pb.ClientPipelineStatsPayload) {
	for key, bucket := range b.pipelineStats {
		clientBucket := pb.ClientPipelineStatsBucket{
			Start: start,
			Duration: duration,
		}
		for hash, point := range bucket {
			var summary []byte
			pbSummary := point.summary.ToProto()
			summary, err := proto.Marshal(pbSummary)
			if err != nil {
				log.Errorf("error serializing ddsketch: %v", err)
				continue
			}
			clientBucket.Stats = append(clientBucket.Stats, pb.ClientGroupedPipelineStats{
				PipelineHash: hash,
				Service: point.service,
				ReceivingPipelineName: point.receivingPipelineName,
				ParentHash: point.parentHash,
				Sketch: summary,
			})
		}
		if len(clientBucket.Stats) > 0 {
			p = append(p, pb.ClientPipelineStatsPayload{
				Env: key.env,
				Version: key.version,
				Stats: []pb.ClientPipelineStatsBucket{clientBucket},
			})
		}
	}
	return p
}

// PipelineStatsAggregator aggregates pipeline stats
type PipelineStatsAggregator struct {
	In      chan pb.ClientPipelineStatsPayload
	out     chan pb.PipelineStatsPayload
	buckets map[int64]*pipelineBucket

	flushTicker   *time.Ticker
	agentEnv      string
	agentHostname string

	exit chan struct{}
	done chan struct{}
}

// NewPipelineStatsAggregator initializes a new aggregator.
func NewPipelineStatsAggregator(conf *config.AgentConfig, out chan pb.PipelineStatsPayload) *PipelineStatsAggregator {
	return &PipelineStatsAggregator{
		flushTicker:   time.NewTicker(time.Second),
		In:            make(chan pb.ClientPipelineStatsPayload, 10),
		// todo[piochelepiotr] Should we group multiple buckets from the same tracer into the same flushed payload?
		buckets:       make(map[int64]*pipelineBucket, 20),
		out:           out,
		agentEnv:      conf.DefaultEnv,
		agentHostname: conf.Hostname,
		exit:          make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// Start starts the aggregator.
func (a *PipelineStatsAggregator) Start() {
	go func() {
		defer watchdog.LogOnPanic()
		for {
			select {
			case t := <-a.flushTicker.C:
				a.flushOnTime(t)
			case input := <-a.In:
				a.add(input)
			case <-a.exit:
				a.flushAll()
				close(a.done)
				return
			}
		}
	}()
}

// Stop stops the aggregator. Calling Stop twice will panic.
func (a *PipelineStatsAggregator) Stop() {
	close(a.exit)
	<-a.done
}

// flushOnTime flushes all buckets up to flushTs, except the last one.
func (a *PipelineStatsAggregator) flushOnTime(now time.Time) {
	ts := now.Unix()
	duration := int64(bucketDuration.Seconds())
	for start, b := range a.buckets {
		if ts > start + duration {
			log.Info("flushing bucket %d", start)
			a.flush(b.export(uint64(start)*uint64(time.Second), uint64(duration)*uint64(time.Second)))
			delete(a.buckets, start)
		}
	}
}

func (a *PipelineStatsAggregator) flushAll() {
	for start, b := range a.buckets {
		a.flush(b.export(uint64(start), uint64(pipelineBucketDuration.Nanoseconds())))
	}
}

func (a *PipelineStatsAggregator) add(p pb.ClientPipelineStatsPayload) {
	log.Info("calling add")
	for _, clientBucket := range p.Stats {
		log.Info("adding bucket %d", clientBucket.Start)
		clientBucketStart := time.Unix(0, int64(clientBucket.Start)).Truncate(pipelineBucketDuration)
		ts := clientBucketStart.Unix()
		b, ok := a.buckets[ts]
		if !ok {
			b = &pipelineBucket{pipelineStats: make(map[pipelineAggregationKey]map[uint64]pipelineStatsPoint)}
			a.buckets[ts] = b
		}
		b.add(clientBucket, p.Env, p.Version)
	}
}

func (a *PipelineStatsAggregator) flush(p []pb.ClientPipelineStatsPayload) {
	if len(p) == 0 {
		log.Info("nothing to flush")
		return
	}
	a.out <- pb.PipelineStatsPayload{
		Stats:          p,
		AgentEnv:       a.agentEnv,
		AgentHostname:  a.agentHostname,
		AgentVersion:   info.Version,
	}
}
