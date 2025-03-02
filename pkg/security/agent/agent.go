// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package agent

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/DataDog/datadog-agent/pkg/compliance/event"
	coreconfig "github.com/DataDog/datadog-agent/pkg/config"
	"github.com/DataDog/datadog-agent/pkg/logs/config"
	"github.com/DataDog/datadog-agent/pkg/security/api"
	"github.com/DataDog/datadog-agent/pkg/util/log"
)

// RuntimeSecurityAgent represents the main wrapper for the Runtime Security product
type RuntimeSecurityAgent struct {
	hostname      string
	reporter      event.Reporter
	conn          *grpc.ClientConn
	running       atomic.Value
	wg            sync.WaitGroup
	connected     atomic.Value
	eventReceived uint64
	telemetry     *telemetry
	endpoints     *config.Endpoints
	cancel        context.CancelFunc
}

// NewRuntimeSecurityAgent instantiates a new RuntimeSecurityAgent
func NewRuntimeSecurityAgent(hostname string, reporter event.Reporter, endpoints *config.Endpoints) (*RuntimeSecurityAgent, error) {
	socketPath := coreconfig.Datadog.GetString("runtime_security_config.socket")
	if socketPath == "" {
		return nil, errors.New("runtime_security_config.socket must be set")
	}

	conn, err := grpc.Dial(socketPath, grpc.WithInsecure(), grpc.WithContextDialer(func(ctx context.Context, url string) (net.Conn, error) {
		return net.Dial("unix", url)
	}))
	if err != nil {
		return nil, err
	}

	tel, err := newTelemetry()
	if err != nil {
		return nil, errors.Errorf("failed to initialize the telemetry reporter")
	}

	return &RuntimeSecurityAgent{
		conn:      conn,
		reporter:  reporter,
		hostname:  hostname,
		telemetry: tel,
		endpoints: endpoints,
	}, nil
}

// Start the runtime security agent
func (rsa *RuntimeSecurityAgent) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	rsa.cancel = cancel

	// Start the system-probe events listener
	go rsa.StartEventListener()
	// Send Runtime Security Agent telemetry
	go rsa.telemetry.run(ctx)
}

// Stop the runtime recurity agent
func (rsa *RuntimeSecurityAgent) Stop() {
	rsa.cancel()
	rsa.running.Store(false)
	rsa.wg.Wait()
	rsa.conn.Close()
}

// StartEventListener starts listening for new events from system-probe
func (rsa *RuntimeSecurityAgent) StartEventListener() {
	rsa.wg.Add(1)
	defer rsa.wg.Done()
	apiClient := api.NewSecurityModuleClient(rsa.conn)

	rsa.connected.Store(false)

	logTicker := newLogBackoffTicker()

	rsa.running.Store(true)
	for rsa.running.Load() == true {
		stream, err := apiClient.GetEvents(context.Background(), &api.GetEventParams{})
		if err != nil {
			rsa.connected.Store(false)

			select {
			case <-logTicker.C:
				log.Warnf("Error while connecting to the runtime security module: %v", err)
			default:
				// do nothing
			}

			// retry in 2 seconds
			time.Sleep(2 * time.Second)
			continue
		}

		if rsa.connected.Load() != true {
			rsa.connected.Store(true)

			log.Info("Successfully connected to the runtime security module")
		}

		for {
			// Get new event from stream
			in, err := stream.Recv()
			if err == io.EOF || in == nil {
				break
			}
			log.Tracef("Got message from rule `%s` for event `%s`", in.RuleID, string(in.Data))

			atomic.AddUint64(&rsa.eventReceived, 1)

			// Dispatch security event
			rsa.DispatchEvent(in)
		}
	}
}

// DispatchEvent dispatches a security event message to the subsytems of the runtime security agent
func (rsa *RuntimeSecurityAgent) DispatchEvent(evt *api.SecurityEventMessage) {
	// For now simply log to Datadog
	rsa.reporter.ReportRaw(evt.GetData(), evt.Service, evt.GetTags()...)
}

// GetStatus returns the current status on the agent
func (rsa *RuntimeSecurityAgent) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"connected":     rsa.connected.Load(),
		"eventReceived": atomic.LoadUint64(&rsa.eventReceived),
		"endpoints":     rsa.endpoints.GetStatus(),
	}
}

// newLogBackoffTicker returns a ticker based on an exponential backoff, used to trigger connect error logs
func newLogBackoffTicker() *backoff.Ticker {
	expBackoff := backoff.NewExponentialBackOff()
	expBackoff.InitialInterval = 2 * time.Second
	expBackoff.MaxInterval = 60 * time.Second
	expBackoff.MaxElapsedTime = 0
	expBackoff.Reset()
	return backoff.NewTicker(expBackoff)
}
