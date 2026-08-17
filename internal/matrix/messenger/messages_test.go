// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package messenger

import (
	"crypto/ecdsa"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/matrix"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// Plain string assignments (not function-call arguments) trip goconst at 3
// occurrences; both of these are assigned that way in more than one test
// function, so they are hoisted here rather than left as literals.
const (
	testMessageID      = "message-id"
	testOtherMessageID = "other-message-id"
)

var testKey *ecdsa.PrivateKey

func init() {
	var err error
	testKey, err = ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		panic(fmt.Errorf("failed to generate test key: %w", err))
	}
}

// preExistingMessageIDs snapshots the message IDs already present in
// chunkedMessages so that a later call to normalizeNewlyCreatedFirstSeen can
// tell apart entries the production code just constructed (which must have
// gotten a real firstSeen) from entries the test fixture injected directly
// (whose firstSeen is deliberately left as the zero value and must not be
// touched).
func preExistingMessageIDs(chunkedMessages map[string]*chunkedMessage) map[string]bool {
	ids := make(map[string]bool, len(chunkedMessages))
	for id := range chunkedMessages {
		ids[id] = true
	}
	return ids
}

// normalizeNewlyCreatedFirstSeen asserts that every chunkedMessage entry NOT
// present in preExisting - i.e. one the production path constructed during
// this call, not one the test fixture injected - has a non-zero firstSeen,
// then zeroes it so the caller's whole-map require.Equal against a fixture
// literal (whose firstSeen is always the zero time.Time) is exact rather than
// weakened.
func normalizeNewlyCreatedFirstSeen(t *testing.T, preExisting map[string]bool, chunkedMessages map[string]*chunkedMessage) {
	t.Helper()
	for id, message := range chunkedMessages {
		if preExisting[id] {
			continue
		}
		require.False(t, message.firstSeen.IsZero(),
			"production-constructed chunkedMessage %q must have firstSeen set", id)
		message.firstSeen = time.Time{}
	}
}

func TestTryCompleteMessageWithFirstChunk(t *testing.T) {
	logger := zap.NewNop().Sugar()
	botKey := testKey

	messageID := testMessageID
	messageSignature := []byte("signature")

	senderTTMAccount := common.Address{1}

	// we will always expect this message chunk to be present in the map unchanged in addition to case-specific expects
	otherMessageID := testOtherMessageID
	otherChunkedMessage := func() *chunkedMessage { // to make copies, not references
		return &chunkedMessage{
			chunksCount:    4,
			signature:      []byte("other-signature"),
			fromTTMAccount: common.Address{2},
			chunks: map[uint32][]byte{
				0: []byte("other-chunk0"),
				1: []byte("other-chunk1"),
			},
		}
	}

	tests := map[string]struct {
		msgEventContent         *matrix.SignedMessageEventContent
		existingChunkedMessages map[string]*chunkedMessage
		expectedChunkedMessages map[string]*chunkedMessage
		expectedMessage         messaging.EncodedSignedMessageWithSender
		expectedComplete        bool
	}{
		"Single-chunk message": {
			msgEventContent: &matrix.SignedMessageEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("single chunk data"),
				},
				ChunksCount:             1,
				SenderTTMAccountAddress: senderTTMAccount,
				Signature:               messageSignature,
			},
			expectedMessage: messaging.EncodedSignedMessageWithSender{
				Message: messaging.EncodedSignedMessage{
					ChunkedEncodedMessage: [][]byte{[]byte("single chunk data")},
					Signature:             messageSignature,
				},
				SenderTTMAccountAddress: senderTTMAccount,
			},
			expectedComplete: true,
		},
		"First chunk is actually first": {
			msgEventContent: &matrix.SignedMessageEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk0"),
				},
				ChunksCount:             3,
				SenderTTMAccountAddress: senderTTMAccount,
				Signature:               messageSignature,
			},
			expectedChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunksCount:    3,
					signature:      messageSignature,
					fromTTMAccount: senderTTMAccount,
					chunks: map[uint32][]byte{
						0: []byte("chunk0"),
					},
				},
			},
		},
		"First chunk is second": {
			msgEventContent: &matrix.SignedMessageEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk0"),
				},
				ChunksCount:             3,
				SenderTTMAccountAddress: senderTTMAccount,
				Signature:               messageSignature,
			},
			existingChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
					},
				},
			},
			expectedChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunksCount:    3,
					signature:      messageSignature,
					fromTTMAccount: senderTTMAccount,
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
						0: []byte("chunk0"),
					},
				},
			},
		},
		"First chunk is last": {
			msgEventContent: &matrix.SignedMessageEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk0"),
				},
				ChunksCount:             3,
				SenderTTMAccountAddress: senderTTMAccount,
				Signature:               messageSignature,
			},
			existingChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
						2: []byte("chunk2"),
					},
				},
			},
			expectedMessage: messaging.EncodedSignedMessageWithSender{
				Message: messaging.EncodedSignedMessage{
					ChunkedEncodedMessage: [][]byte{[]byte("chunk0"), []byte("chunk1"), []byte("chunk2")},
					Signature:             messageSignature,
				},
				SenderTTMAccountAddress: senderTTMAccount,
			},
			expectedComplete: true,
		},
	}
	for tc, tt := range tests {
		t.Run(tc, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			matrixClient := NewMockClient(ctrl)
			matrixClient.EXPECT().SetEventHandler(matrix.EventTypeSignedMessage, gomock.Any())
			matrixClient.EXPECT().SetEventHandler(matrix.EventTypeMessageChunk, gomock.Any())
			matrixClient.EXPECT().SetEventHandler(event.StateMember, gomock.Any())

			matrixMessenger, err := NewMessenger(
				logger,
				matrixClient,
				botKey,
				id.UserID("botUserID"),
			)
			require.NoError(t, err)
			matrixMessengerImpl := matrixMessenger.(*messenger)

			for msgID, chunkedMessage := range tt.existingChunkedMessages {
				matrixMessengerImpl.chunkedMessages[msgID] = chunkedMessage
			}

			if tt.expectedChunkedMessages == nil {
				tt.expectedChunkedMessages = make(map[string]*chunkedMessage)
			}

			// we expect the other message to be present in the map unchanged
			matrixMessengerImpl.chunkedMessages[otherMessageID] = otherChunkedMessage()
			tt.expectedChunkedMessages[otherMessageID] = otherChunkedMessage()

			preExisting := preExistingMessageIDs(matrixMessengerImpl.chunkedMessages)

			message, completed := matrixMessengerImpl.tryCompleteMessageWithFirstChunk(tt.msgEventContent)
			require.Equal(t, tt.expectedComplete, completed)
			require.Equal(t, tt.expectedMessage, message)

			normalizeNewlyCreatedFirstSeen(t, preExisting, matrixMessengerImpl.chunkedMessages)

			require.Equal(t, tt.expectedChunkedMessages, matrixMessengerImpl.chunkedMessages)
		})
	}
}

func TestTryCompleteMessage(t *testing.T) {
	logger := zap.NewNop().Sugar()
	botKey := testKey

	messageID := testMessageID
	messageSignature := []byte("signature")
	senderTTMAccount := common.Address{1}

	// we will always expect this message chunk to be present in the map unchanged in addition to case-specific expects
	otherMessageID := testOtherMessageID
	otherChunkedMessage := func() *chunkedMessage { // to make copies, not references
		return &chunkedMessage{
			chunksCount:    4,
			signature:      []byte("other-signature"),
			fromTTMAccount: common.Address{2},
			chunks: map[uint32][]byte{
				0: []byte("other-chunk0"),
				1: []byte("other-chunk1"),
			},
		}
	}

	tests := map[string]struct {
		msgEventContent         *matrix.MessageChunkEventContent
		existingChunkedMessages map[string]*chunkedMessage
		expectedChunkedMessages map[string]*chunkedMessage
		expectedMessage         messaging.EncodedSignedMessageWithSender
		expectedComplete        bool
	}{
		"Chunk is first": {
			msgEventContent: &matrix.MessageChunkEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk1"),
				},
				ChunkIndex: 1,
			},
			expectedChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
					},
				},
			},
		},
		"Chunk is second": {
			msgEventContent: &matrix.MessageChunkEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk2"),
				},
				ChunkIndex: 2,
			},
			existingChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
					},
				},
			},
			expectedChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunks: map[uint32][]byte{
						1: []byte("chunk1"),
						2: []byte("chunk2"),
					},
				},
			},
		},
		"Chunk is last": {
			msgEventContent: &matrix.MessageChunkEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      []byte("chunk1"),
				},
				ChunkIndex: 1,
			},
			existingChunkedMessages: map[string]*chunkedMessage{
				messageID: {
					chunksCount:    3,
					signature:      messageSignature,
					fromTTMAccount: senderTTMAccount,
					chunks: map[uint32][]byte{
						2: []byte("chunk2"),
						0: []byte("chunk0"),
					},
				},
			},
			expectedMessage: messaging.EncodedSignedMessageWithSender{
				Message: messaging.EncodedSignedMessage{
					ChunkedEncodedMessage: [][]byte{[]byte("chunk0"), []byte("chunk1"), []byte("chunk2")},
					Signature:             messageSignature,
				},
				SenderTTMAccountAddress: senderTTMAccount,
			},
			expectedComplete: true,
		},
	}
	for tc, tt := range tests {
		t.Run(tc, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			matrixClient := NewMockClient(ctrl)
			matrixClient.EXPECT().SetEventHandler(matrix.EventTypeSignedMessage, gomock.Any())
			matrixClient.EXPECT().SetEventHandler(matrix.EventTypeMessageChunk, gomock.Any())
			matrixClient.EXPECT().SetEventHandler(event.StateMember, gomock.Any())

			matrixMessenger, err := NewMessenger(
				logger,
				matrixClient,
				botKey,
				id.UserID("botUserID"),
			)
			require.NoError(t, err)
			matrixMessengerImpl := matrixMessenger.(*messenger)

			for msgID, chunkedMessage := range tt.existingChunkedMessages {
				matrixMessengerImpl.chunkedMessages[msgID] = chunkedMessage
			}

			if tt.expectedChunkedMessages == nil {
				tt.expectedChunkedMessages = make(map[string]*chunkedMessage)
			}

			// we expect the other message to be present in the map unchanged
			matrixMessengerImpl.chunkedMessages[otherMessageID] = otherChunkedMessage()
			tt.expectedChunkedMessages[otherMessageID] = otherChunkedMessage()

			preExisting := preExistingMessageIDs(matrixMessengerImpl.chunkedMessages)

			message, completed := matrixMessengerImpl.tryCompleteMessage(tt.msgEventContent)
			require.Equal(t, tt.expectedComplete, completed)
			require.Equal(t, tt.expectedMessage, message)

			normalizeNewlyCreatedFirstSeen(t, preExisting, matrixMessengerImpl.chunkedMessages)

			require.Equal(t, tt.expectedChunkedMessages, matrixMessengerImpl.chunkedMessages)
		})
	}
}

// A redelivered chunk must not complete a message that is still missing an
// index. The syncer replays up to an hour of events on Start
// (messenger.go:88), so duplicate delivery is expected, not exotic.
func TestTryCompleteMessageDuplicateChunk(t *testing.T) {
	logger := zap.NewNop().Sugar()

	messageID := testMessageID
	messageSignature := []byte("signature")
	senderTTMAccount := common.Address{1}

	ctrl := gomock.NewController(t)

	matrixClient := NewMockClient(ctrl)
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeSignedMessage, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeMessageChunk, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(event.StateMember, gomock.Any())

	matrixMessenger, err := NewMessenger(logger, matrixClient, testKey, id.UserID("botUserID"))
	require.NoError(t, err)
	m := matrixMessenger.(*messenger)

	// chunk 0 of 3, carrying the signature and the count
	_, completed := m.tryCompleteMessageWithFirstChunk(&matrix.SignedMessageEventContent{
		ChunkData:               matrix.ChunkData{MessageID: messageID, Data: []byte("chunk0")},
		ChunksCount:             3,
		SenderTTMAccountAddress: senderTTMAccount,
		Signature:               messageSignature,
	})
	require.False(t, completed, "a 1-of-3 message must not be complete")

	// chunk 1 of 3
	_, completed = m.tryCompleteMessage(&matrix.MessageChunkEventContent{
		ChunkData:  matrix.ChunkData{MessageID: messageID, Data: []byte("chunk1")},
		ChunkIndex: 1,
	})
	require.False(t, completed, "a 2-of-3 message must not be complete")

	// chunk 1 AGAIN - a redelivery. Index 2 has still never arrived.
	msg, completed := m.tryCompleteMessage(&matrix.MessageChunkEventContent{
		ChunkData:  matrix.ChunkData{MessageID: messageID, Data: []byte("chunk1")},
		ChunkIndex: 1,
	})
	require.False(t, completed,
		"a redelivered chunk completed a message that is still missing index 2; "+
			"assembling it would join a byte stream the sender never signed")
	require.Equal(t, messaging.EncodedSignedMessageWithSender{}, msg)

	// the real chunk 2 arrives and only now completes the message, intact
	msg, completed = m.tryCompleteMessage(&matrix.MessageChunkEventContent{
		ChunkData:  matrix.ChunkData{MessageID: messageID, Data: []byte("chunk2")},
		ChunkIndex: 2,
	})
	require.True(t, completed, "all three distinct chunks have arrived")
	require.Equal(t, messaging.EncodedSignedMessageWithSender{
		Message: messaging.EncodedSignedMessage{
			ChunkedEncodedMessage: [][]byte{[]byte("chunk0"), []byte("chunk1"), []byte("chunk2")},
			Signature:             messageSignature,
		},
		SenderTTMAccountAddress: senderTTMAccount,
	}, msg)

	require.Empty(t, m.chunkedMessages, "a completed message must be evicted from the map")
}

// A chunk index at or beyond chunksCount can never be assembled. Storing it
// would inflate len(chunks) and complete the message with a real index still
// missing - the same defect as a duplicate, through a different door.
func TestTryCompleteMessageOutOfRangeChunk(t *testing.T) {
	logger := zap.NewNop().Sugar()

	messageID := testMessageID
	senderTTMAccount := common.Address{1}

	ctrl := gomock.NewController(t)

	matrixClient := NewMockClient(ctrl)
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeSignedMessage, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeMessageChunk, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(event.StateMember, gomock.Any())

	matrixMessenger, err := NewMessenger(logger, matrixClient, testKey, id.UserID("botUserID"))
	require.NoError(t, err)
	m := matrixMessenger.(*messenger)

	// chunk 0 of 2
	_, completed := m.tryCompleteMessageWithFirstChunk(&matrix.SignedMessageEventContent{
		ChunkData:               matrix.ChunkData{MessageID: messageID, Data: []byte("chunk0")},
		ChunksCount:             2,
		SenderTTMAccountAddress: senderTTMAccount,
		Signature:               []byte("signature"),
	})
	require.False(t, completed)

	// index 7 of a 2-chunk message - nonsense, must be dropped not stored
	_, completed = m.tryCompleteMessage(&matrix.MessageChunkEventContent{
		ChunkData:  matrix.ChunkData{MessageID: messageID, Data: []byte("bogus")},
		ChunkIndex: 7,
	})
	require.False(t, completed, "an out-of-range chunk must never complete a message")
	require.Contains(t, m.chunkedMessages, messageID,
		"a rejected out-of-range chunk must leave the partial message intact")
	require.Len(t, m.chunkedMessages[messageID].chunks, 1,
		"an out-of-range chunk must not be stored")
}

// A partial message whose remaining chunks never arrive must not live forever.
// Before this, chunkedMessages had no eviction and no timeout: one lost first
// chunk leaked its payload for the lifetime of the process.
func TestEvictStalePartialMessages(t *testing.T) {
	logger := zap.NewNop().Sugar()

	ctrl := gomock.NewController(t)

	matrixClient := NewMockClient(ctrl)
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeSignedMessage, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(matrix.EventTypeMessageChunk, gomock.Any())
	matrixClient.EXPECT().SetEventHandler(event.StateMember, gomock.Any())

	matrixMessenger, err := NewMessenger(logger, matrixClient, testKey, id.UserID("botUserID"))
	require.NoError(t, err)
	m := matrixMessenger.(*messenger)

	now := time.Now()

	m.chunkedMessages["stale"] = &chunkedMessage{
		firstSeen: now.Add(-partialMessageTTL - time.Second),
		chunks:    map[uint32][]byte{1: []byte("orphan")},
	}
	m.chunkedMessages["fresh"] = &chunkedMessage{
		firstSeen: now.Add(-time.Second),
		chunks:    map[uint32][]byte{1: []byte("in flight")},
	}

	m.evictStalePartialMessages(now)

	require.NotContains(t, m.chunkedMessages, "stale", "a message older than the TTL must be evicted")
	require.Contains(t, m.chunkedMessages, "fresh", "a message still within the TTL must be kept")
}
