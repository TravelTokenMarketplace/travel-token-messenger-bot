// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

// Package ppevents turns a pp-mock event subscription into a retained,
// type-indexed stream.
//
// The subscription is a firehose: pp-mock publishes every gRPC request it
// receives, interleaved with the notification calls the bot makes back into
// it. Some of those events are synchronous with the test's own RPCs; others
// (TokenBought, Cancellation*) originate from on-chain events and arrive
// whenever the chain gets around to them. Reading the subscription
// positionally therefore asserts a total order over events that are only
// partially ordered - and because proto.Unmarshal into the wrong message type
// succeeds and leaves every field zero, getting it wrong looks like a
// malformed notification rather than a misread stream.
//
// Record drains the subscription continuously and never discards. Await takes
// the oldest event of a requested type that no earlier Await has taken.
package ppevents

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/proto/pb/events"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// awaitTimeout bounds a single Await. The suites run under a 300s context
// (tests.defaultTestTimeout), so a miss reports its diagnostic well inside the
// suite's budget instead of taking the whole suite down with it.
const awaitTimeout = 60 * time.Second

// Receiver is the part of events.EventsService_SubscribeClient that Record
// uses. Narrowing it keeps this package's own tests free of gRPC plumbing.
type Receiver interface {
	Recv() (*events.SubscribeResponse, error)
}

type entry struct {
	typeName string
	data     []byte
	taken    bool
}

// Stream is a pp-mock event subscription drained into a retained FIFO.
// It is safe for concurrent use.
type Stream struct {
	mutex   sync.Mutex
	entries []*entry
	err     error         // terminal receive error, once the subscription ends
	notify  chan struct{} // closed and replaced whenever entries or err change
}

// Record starts draining sub in the background. The goroutine runs until the
// subscription ends, which happens when the context passed to Subscribe is
// cancelled. Draining continuously also keeps pp-mock unblocked: it publishes
// events from inside a gRPC interceptor over unbuffered channels, so a
// subscriber that stops reading back-pressures every RPC pp-mock serves.
func Record(sub Receiver) *Stream {
	s := &Stream{notify: make(chan struct{})}
	go s.drain(sub)
	return s
}

func (s *Stream) drain(sub Receiver) {
	for {
		resp, err := sub.Recv()
		if err != nil {
			s.finish(err)
			return
		}
		s.add(&entry{typeName: resp.TypeName, data: resp.Data})
	}
}

func (s *Stream) add(e *entry) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.entries = append(s.entries, e)
	s.wake()
}

func (s *Stream) finish(err error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.err = err
	s.wake()
}

// wake releases every current waiter. The caller must hold s.mutex.
// A channel rather than a sync.Cond, because waiters need a timeout.
func (s *Stream) wake() {
	close(s.notify)
	s.notify = make(chan struct{})
}

// take claims the oldest untaken entry of the wanted type. When there is none
// it returns the channel that closes on the next change, plus the terminal
// error if the subscription has already ended.
func (s *Stream) take(want protoreflect.FullName) (*entry, chan struct{}, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	for _, e := range s.entries {
		if !e.taken && e.typeName == string(want) {
			e.taken = true
			return e, nil, nil
		}
	}
	return nil, s.notify, s.err
}

// report renders every event the stream has seen, in arrival order. This is
// what the type tag buys: a failed Await says what was actually on the stream
// instead of leaving a zeroed message to be puzzled over.
func (s *Stream) report() string {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if len(s.entries) == 0 {
		return "no events were seen on this stream"
	}

	var b strings.Builder
	b.WriteString("events seen on this stream, in order:")
	for i, e := range s.entries {
		state := "pending"
		if e.taken {
			state = "taken  "
		}
		fmt.Fprintf(&b, "\n  %3d [%s] %s", i+1, state, e.typeName)
	}
	return b.String()
}

// Await returns the oldest event of type T that no earlier Await has taken,
// waiting for it if it has not arrived yet. Events of other types are left in
// place for later calls - nothing is discarded, so no assertion depends on the
// order two independently-produced events happen to arrive in.
//
// It fails the test if the event does not arrive in time or if the
// subscription ends first.
func Await[T proto.Message](t *testing.T, s *Stream) T {
	t.Helper()

	message, err := await[T](s, awaitTimeout)
	require.NoError(t, err)
	return message
}

// await is the testable core: it reports failures as errors rather than
// through *testing.T, so the timeout and end-of-stream paths can be asserted.
func await[T proto.Message](s *Stream, timeout time.Duration) (T, error) {
	var zero T
	want := zero.ProtoReflect().Descriptor().FullName()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		e, changed, err := s.take(want)
		if e != nil {
			message := zero.ProtoReflect().New().Interface().(T)
			if err := proto.Unmarshal(e.data, message); err != nil {
				return zero, fmt.Errorf("awaiting %s: the event failed to unmarshal: %w", want, err)
			}
			return message, nil
		}
		if err != nil {
			return zero, fmt.Errorf("awaiting %s: the event stream ended: %w\n%s", want, err, s.report())
		}

		select {
		case <-changed:
		case <-timer.C:
			return zero, fmt.Errorf("awaiting %s: timed out after %s\n%s", want, timeout, s.report())
		}
	}
}
