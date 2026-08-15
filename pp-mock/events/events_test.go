// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package events

import (
	"testing"

	notificationv3 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/notification/v3"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/proto/pb/events"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestSendProtoEventStampsTheTypeName(t *testing.T) {
	_, sender := NewServer()

	es, ok := sender.(*eventSender)
	require.True(t, ok, "NewServer should hand back the concrete sender")

	// eventChan is unbuffered, so take delivery on another goroutine.
	received := make(chan *events.SubscribeResponse, 1)
	go func() { received <- <-es.eventChan }()

	require.NoError(t, sender.SendProtoEvent(&notificationv3.TokenBought{TokenId: 42}))

	got := <-received
	require.Equal(t, "ttm.services.notification.v3.TokenBought", got.TypeName)

	roundTripped := &notificationv3.TokenBought{}
	require.NoError(t, proto.Unmarshal(got.Data, roundTripped))
	require.Equal(t, uint64(42), roundTripped.TokenId)
}
