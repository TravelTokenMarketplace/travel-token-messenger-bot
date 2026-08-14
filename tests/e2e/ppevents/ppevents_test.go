// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package ppevents

import (
	"io"
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
}

func newFakeSubscription() *fakeSubscription {
	return &fakeSubscription{events: make(chan *events.SubscribeResponse, 32)}
}

func (f *fakeSubscription) Recv() (*events.SubscribeResponse, error) {
	resp, ok := <-f.events
	if !ok {
		return nil, f.closed
	}
	return resp, nil
}

func (f *fakeSubscription) send(t *testing.T, messages ...proto.Message) {
	t.Helper()
	for _, message := range messages {
		data, err := proto.Marshal(message)
		require.NoError(t, err)
		f.events <- &events.SubscribeResponse{
			Data:     data,
			TypeName: string(message.ProtoReflect().Descriptor().FullName()),
		}
	}
}

func (f *fakeSubscription) end(err error) {
	f.closed = err
	close(f.events)
}

// The regression test. This is the exact interleaving that turned CI run
// 31816964056 red: the chain took long enough over TokenBought that the next
// loop iteration's search request overtook it.
func TestAwaitFindsALateNotification(t *testing.T) {
	sub := newFakeSubscription()
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

// Unlike TestAwaitFindsALateNotification, which pushes every event into the
// subscription's buffered channel before Await is even called, this test
// sends the matching event from a separate goroutine only after await is
// already parked waiting on it. Passing promptly (well inside the 30s
// timeout) proves the add() wakeup actually fired; a lost wakeup - e.g. a
// future edit that dropped s.wake() from add - would hang for the full
// timeout instead of failing cleanly.
func TestAwaitWakesWhenTheMatchingEventArrivesAfterItParks(t *testing.T) {
	sub := newFakeSubscription()
	stream := Record(sub)
	sub.send(t, &accommodationv5.AccommodationSearchRequest{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		sub.send(t, &notificationv3.TokenBought{TokenId: 7})
	}()

	// A long timeout: passing proves the waiter woke on the new event
	// arriving, not on the clock.
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

	sub := newFakeSubscription()
	stream := Record(sub)
	sub.send(t, expired)

	_, err = await[*notificationv3.TokenBought](stream, 50*time.Millisecond)
	require.Error(t, err, "a TokenReservationExpired must not satisfy an Await for TokenBought")
}

func TestAwaitLeavesUnmatchedEventsForLaterCalls(t *testing.T) {
	sub := newFakeSubscription()
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
	sub := newFakeSubscription()
	stream := Record(sub)

	sub.send(t,
		&notificationv3.TokenReservationExpired{TokenId: 1},
		&notificationv3.TokenReservationExpired{TokenId: 2},
	)

	first := Await[*notificationv3.TokenReservationExpired](t, stream)
	second := Await[*notificationv3.TokenReservationExpired](t, stream)

	require.Equal(t, uint64(1), first.TokenId)
	require.Equal(t, uint64(2), second.TokenId, "the second Await must not re-return the first event")
	require.NotSame(t, first, second)
}

func TestAwaitTimesOutReportingTheStreamContents(t *testing.T) {
	sub := newFakeSubscription()
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

func TestAwaitFailsWhenTheSubscriptionEnds(t *testing.T) {
	sub := newFakeSubscription()
	stream := Record(sub)
	sub.send(t, &bookv5.MintRequest{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		sub.end(io.EOF)
	}()

	// A long timeout: passing proves the waiter woke on the stream ending, not
	// on the clock.
	_, err := await[*notificationv3.TokenBought](stream, 30*time.Second)
	require.ErrorIs(t, err, io.EOF)
	require.Contains(t, err.Error(), "ttm.services.book.v5.MintRequest")
}
