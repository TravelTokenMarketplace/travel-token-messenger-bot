// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package ppevents

import (
	"io"
	"sync"
	"testing"
	"time"

	accommodationv5 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/accommodation/v5"
	bookv5 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/book/v5"
	notificationv3 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/notification/v3"
	typesv4 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v4"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/proto/pb/events"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// fakeSubscription stands in for events.EventsService_SubscribeClient so a
// test can dictate an interleaving that a real chain cannot be asked for.
type fakeSubscription struct {
	events chan *events.SubscribeResponse
	closed error
	endOne sync.Once
}

// newFakeSubscription registers a cleanup that ends the subscription, so the
// background drain goroutine started by Record never outlives the test that
// started it. Tests that end the subscription themselves (to control when a
// waiter observes end-of-stream) may still do so - end is idempotent, so the
// cleanup's call is a harmless no-op in that case.
func newFakeSubscription(t *testing.T) *fakeSubscription {
	f := &fakeSubscription{events: make(chan *events.SubscribeResponse, 32)}
	t.Cleanup(func() { f.end(io.EOF) })
	return f
}

func (f *fakeSubscription) Recv() (*events.SubscribeResponse, error) {
	resp, ok := <-f.events
	if !ok {
		return nil, f.closed
	}
	return resp, nil
}

// newSubscribeResponse builds the wire representation of message without
// sending it, so a test can prepare it on the test goroutine and hand the
// finished value to a spawned goroutine that only pushes it onto the channel.
func newSubscribeResponse(t *testing.T, message proto.Message) *events.SubscribeResponse {
	t.Helper()
	data, err := proto.Marshal(message)
	require.NoError(t, err)
	return &events.SubscribeResponse{
		Data:     data,
		TypeName: string(message.ProtoReflect().Descriptor().FullName()),
	}
}

func (f *fakeSubscription) send(t *testing.T, messages ...proto.Message) {
	t.Helper()
	for _, message := range messages {
		f.events <- newSubscribeResponse(t, message)
	}
}

// end is idempotent (guarded by endOne): a double close of f.events would
// panic, and both a test's own explicit call and the newFakeSubscription
// cleanup call end.
func (f *fakeSubscription) end(err error) {
	f.endOne.Do(func() {
		f.closed = err
		close(f.events)
	})
}

// The regression test. This is the exact interleaving that turned CI run
// 31816964056 red: the chain took long enough over TokenBought that the next
// loop iteration's search request overtook it.
func TestAwaitFindsALateNotification(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t,
		&accommodationv5.AccommodationSearchRequest{},
		&bookv5.ValidationRequest{},
		&bookv5.MintRequest{},
		&accommodationv5.AccommodationSearchRequest{}, // next iteration, overtaking
		&notificationv3.TokenBought{
			TokenId: 7,
			MintId:  &typesv4.UUID{Value: "mint-7"},
		},
	)

	got := Await[*notificationv3.TokenBought](t, stream)
	require.Equal(t, uint64(7), got.TokenId)
	require.NotNil(t, got.MintId)
	require.Equal(t, "mint-7", got.MintId.Value)
}

// TestAwaitFindsALateNotification pushes every event into the subscription's
// buffered channel before Await is called, so it exercises the ordering scan but
// says nothing about the wakeup. This test covers the wakeup itself, and does so
// without a sleep: it captures the very channel a parked await would select on,
// and only then delivers the matching event.
//
// Draining the non-matching event first is load-bearing. take() captures
// whatever s.notify is at that moment, and add() closes it for ANY event - so if
// the search request were still in flight, its own add() would close the
// captured channel and this test would pass without the TokenBought having
// arrived at all.
func TestAwaitWakesWhenTheMatchingEventArrivesAfterItParks(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t, &accommodationv5.AccommodationSearchRequest{})
	drained, err := await[*accommodationv5.AccommodationSearchRequest](stream, 5*time.Second)
	require.NoError(t, err, "the non-matching event must be drained before the wait channel is captured")
	require.NotNil(t, drained)

	want := (*notificationv3.TokenBought)(nil).ProtoReflect().Descriptor().FullName()
	pending, changed, err := stream.take(want)
	require.NoError(t, err)
	require.Nil(t, pending, "no TokenBought should have arrived yet")

	sub.events <- newSubscribeResponse(t, &notificationv3.TokenBought{TokenId: 7})

	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("add() did not wake a waiter parked on the notify channel")
	}

	got, err := await[*notificationv3.TokenBought](stream, 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, uint64(7), got.TokenId)
}

// Content cannot disambiguate: TokenBought and TokenReservationExpired have
// identical field layouts, so either unmarshals cleanly as the other. Only the
// type name separates them.
func TestAwaitDoesNotMistakeOneMessageForAnother(t *testing.T) {
	expired := &notificationv3.TokenReservationExpired{TokenId: 99}
	data, err := proto.Marshal(expired)
	require.NoError(t, err)

	decoyed := &notificationv3.TokenBought{}
	require.NoError(t, proto.Unmarshal(data, decoyed))
	require.Equal(t, uint64(99), decoyed.TokenId,
		"precondition: the two messages are wire-compatible, so bytes alone cannot identify them")

	sub := newFakeSubscription(t)
	stream := Record(sub)
	sub.send(t, expired)

	_, err = await[*notificationv3.TokenBought](stream, 50*time.Millisecond)
	require.Error(t, err, "a TokenReservationExpired must not satisfy an Await for TokenBought")
}

func TestAwaitLeavesUnmatchedEventsForLaterCalls(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t,
		&notificationv3.TokenReservationExpired{TokenId: 1},
		&notificationv3.TokenBought{TokenId: 2},
	)

	// Ask for the second event first; the first must survive.
	bought := Await[*notificationv3.TokenBought](t, stream)
	require.Equal(t, uint64(2), bought.TokenId)

	expired := Await[*notificationv3.TokenReservationExpired](t, stream)
	require.Equal(t, uint64(1), expired.TokenId)
}

func TestAwaitReturnsOneTypeInArrivalOrder(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t,
		&notificationv3.TokenReservationExpired{TokenId: 1},
		&notificationv3.TokenReservationExpired{TokenId: 2},
	)

	first := Await[*notificationv3.TokenReservationExpired](t, stream)
	second := Await[*notificationv3.TokenReservationExpired](t, stream)

	require.Equal(t, uint64(1), first.TokenId)
	require.Equal(t, uint64(2), second.TokenId, "the second Await must not re-return the first event")
}

func TestAwaitTimesOutReportingTheStreamContents(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t,
		&accommodationv5.AccommodationSearchRequest{},
		&notificationv3.TokenReservationExpired{TokenId: 1},
	)
	// Take one, so the report shows both a taken and a pending entry.
	_ = Await[*notificationv3.TokenReservationExpired](t, stream)

	_, err := await[*notificationv3.TokenBought](stream, 50*time.Millisecond)
	require.Error(t, err)
	require.Contains(t, err.Error(), "awaiting ttm.services.notification.v3.TokenBought")
	require.Contains(t, err.Error(), "timed out")
	require.Contains(t, err.Error(), "[pending] ttm.services.accommodation.v5.AccommodationSearchRequest")
	require.Contains(t, err.Error(), "[taken  ] ttm.services.notification.v3.TokenReservationExpired")
}

// The finish() counterpart of the test above, and deterministic for the same
// reason: the non-matching event is drained first, so the captured channel can
// only be closed by the subscription ending.
func TestAwaitFailsWhenTheSubscriptionEnds(t *testing.T) {
	sub := newFakeSubscription(t)
	stream := Record(sub)

	sub.send(t, &bookv5.MintRequest{})
	drained, err := await[*bookv5.MintRequest](stream, 5*time.Second)
	require.NoError(t, err, "the non-matching event must be drained before the wait channel is captured")
	require.NotNil(t, drained)

	want := (*notificationv3.TokenBought)(nil).ProtoReflect().Descriptor().FullName()
	_, changed, err := stream.take(want)
	require.NoError(t, err)

	sub.end(io.EOF)

	select {
	case <-changed:
	case <-time.After(5 * time.Second):
		t.Fatal("finish() did not wake a waiter parked on the notify channel")
	}

	_, err = await[*notificationv3.TokenBought](stream, 30*time.Second)
	require.ErrorIs(t, err, io.EOF)
	require.Contains(t, err.Error(), "ttm.services.book.v5.MintRequest")
}
