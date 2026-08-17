// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package messenger

import (
	"context"
	"time"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/conversion"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/matrix"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"maunium.net/go/mautrix/event"
)

const (
	// partialMessageTTL bounds how long an incomplete message is held. It is
	// deliberately far longer than any plausible multi-chunk delivery (the
	// 46-chunk 1 MiB WAN benchmark completed in seconds) because evicting a
	// message that was still in flight turns a slow delivery into a lost one.
	// Its only job is to stop unbounded growth.
	partialMessageTTL = 5 * time.Minute

	// partialMessageSweepInterval is how often the sweeper runs. Eviction is
	// not latency-sensitive, so this is coarse on purpose.
	partialMessageSweepInterval = time.Minute
)

type chunkedMessage struct {
	chunksCount    uint32
	fromTTMAccount common.Address
	signature      []byte

	// chunks is keyed by chunk index so that a redelivered chunk overwrites
	// its own entry. len(chunks) is therefore the number of DISTINCT indices
	// received, which is what the completion check needs - counting arrivals
	// instead let one duplicate complete a message with a missing index, and
	// the joined payload then failed signature verification. See
	// docs/superpowers/specs/2026-08-17-multichunk-reassembly-fix-design.md.
	chunks map[uint32][]byte

	// firstSeen is when the first chunk of this message arrived, used to evict
	// messages whose remaining chunks never do.
	firstSeen time.Time
}

func (m *messenger) SendMessage(ctx context.Context, msg *messaging.EncodedSignedMessage, sendTo common.Address, senderTTMAccount common.Address) error {
	messageID := uuid.New().String()

	m.logger.Debugf("Sending message (id %s) to %s", messageID, sendTo)

	roomID, err := m.getRoomForRecipient(ctx, matrix.UserIDFromAddress(sendTo, m.matrixHost))
	if err != nil {
		return err
	}

	messageEvent := matrix.SignedMessageEventContent{
		ChunkData: matrix.ChunkData{
			MessageID: messageID,
			Data:      msg.ChunkedEncodedMessage[0],
		},
		Signature:               msg.Signature,
		SenderTTMAccountAddress: senderTTMAccount,
		ChunksCount:             conversion.MustIntToUInt32(len(msg.ChunkedEncodedMessage)),
	}

	var chunkEvents []matrix.MessageChunkEventContent
	if messageEvent.ChunksCount > 1 {
		chunkEvents = make([]matrix.MessageChunkEventContent, 0, len(msg.ChunkedEncodedMessage)-1)
		for i, chunk := range msg.ChunkedEncodedMessage[1:] {
			chunkEvents = append(chunkEvents, matrix.MessageChunkEventContent{
				ChunkData: matrix.ChunkData{
					MessageID: messageID,
					Data:      chunk,
				},
				ChunkIndex: conversion.MustIntToUInt32(i + 1),
			})
		}
	}

	// TODO @nikos add retry logic?
	if err := m.client.SendSignedMessageEvent(ctx, roomID, messageEvent); err != nil {
		return err
	}

	for _, chunkEvent := range chunkEvents {
		if err := m.client.SendMessageChunkEvent(ctx, roomID, chunkEvent); err != nil {
			return err
		}
	}
	return nil
}

func (m *messenger) signedMessageEventHandler(_ context.Context, evt *event.Event) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Errorf("failed to process %s event, recovered from panic: %v", &matrix.EventTypeSignedMessage.Type, r)
		}
	}()
	m.logger.Debugf("Received %s event %s from %s in room %s", matrix.EventTypeSignedMessage.Type, evt.ID, evt.Sender, evt.RoomID)

	if evt.Sender == m.botUserID { // ignore own messages
		m.logger.Debugf("Ignoring own message from %s in room %s", evt.Sender, evt.RoomID)
		return
	}

	eventContent := evt.Content.Parsed.(*matrix.SignedMessageEventContent)

	if err := eventContent.Verify(); err != nil {
		m.logger.Warnf("Received invalid %s event: %v", &matrix.EventTypeSignedMessage.Type, err)
		return
	}

	msg, completed := m.tryCompleteMessageWithFirstChunk(eventContent)
	if !completed {
		m.logger.Debugf("Received message (id %s) first chunk, waiting for more chunks", eventContent.MessageID)
		return
	}

	msg.SenderBotAddress = matrix.AddressFromUserID(evt.Sender)
	m.msgChannel <- msg
}

func (m *messenger) messageChunkEventHandler(_ context.Context, evt *event.Event) {
	defer func() {
		if r := recover(); r != nil {
			m.logger.Errorf("failed to process %s event, recovered from panic: %v", &matrix.EventTypeMessageChunk.Type, r)
		}
	}()
	m.logger.Debugf("Received %s event %s from %s in room %s", matrix.EventTypeMessageChunk.Type, evt.ID, evt.Sender, evt.RoomID)

	if evt.Sender == m.botUserID { // ignore own messages
		m.logger.Debugf("Ignoring own message from %s in room %s", evt.Sender, evt.RoomID)
		return
	}

	eventContent := evt.Content.Parsed.(*matrix.MessageChunkEventContent)

	if err := eventContent.Verify(); err != nil {
		m.logger.Warnf("Received invalid %s event: %v", &matrix.EventTypeMessageChunk.Type, err)
		return
	}

	msg, completed := m.tryCompleteMessage(eventContent)
	if !completed {
		m.logger.Debugf("Received message (id %s) chunk %d, waiting for more chunks", eventContent.MessageID, eventContent.ChunkIndex)
		return
	}

	msg.SenderBotAddress = matrix.AddressFromUserID(evt.Sender)
	m.msgChannel <- msg
}

func (m *messenger) tryCompleteMessageWithFirstChunk(eventContent *matrix.SignedMessageEventContent) (messaging.EncodedSignedMessageWithSender, bool) {
	// if the message is not chunked, we can already complete it
	if eventContent.ChunksCount == 1 {
		return messaging.EncodedSignedMessageWithSender{
			Message: messaging.EncodedSignedMessage{
				ChunkedEncodedMessage: [][]byte{eventContent.Data},
				Signature:             eventContent.Signature,
			},
			SenderTTMAccountAddress: eventContent.SenderTTMAccountAddress,
		}, true
	}

	chunkedMessage, complete := m.addMessageFirstChunk(eventContent)
	if !complete {
		return messaging.EncodedSignedMessageWithSender{}, false
	}
	return m.assembleOrDropMessage(chunkedMessage, eventContent.MessageID)
}

func (m *messenger) tryCompleteMessage(eventContent *matrix.MessageChunkEventContent) (messaging.EncodedSignedMessageWithSender, bool) {
	chunkedMessage, complete := m.addMessageNextChunk(eventContent)
	if !complete {
		return messaging.EncodedSignedMessageWithSender{}, false
	}
	return m.assembleOrDropMessage(chunkedMessage, eventContent.MessageID)
}

// assembleOrDropMessage assembles the chunked message, or drops it and logs
// why if the index set turned out incomplete at completion time.
func (m *messenger) assembleOrDropMessage(chunkedMessage *chunkedMessage, messageID string) (messaging.EncodedSignedMessageWithSender, bool) {
	msg, ok := m.assembleEncodedMessage(chunkedMessage)
	if !ok {
		m.logger.Errorf("dropping message (id %s): chunk set incomplete at completion", messageID)
		return messaging.EncodedSignedMessageWithSender{}, false
	}
	return msg, true
}

func (m *messenger) addMessageFirstChunk(eventContent *matrix.SignedMessageEventContent) (*chunkedMessage, bool) {
	m.messagesMutex.Lock()
	defer m.messagesMutex.Unlock()

	message, ok := m.chunkedMessages[eventContent.MessageID]
	if !ok {
		message = &chunkedMessage{
			chunks:    make(map[uint32][]byte, eventContent.ChunksCount),
			firstSeen: time.Now(),
		}
		m.chunkedMessages[eventContent.MessageID] = message
	}

	message.signature = eventContent.Signature
	message.chunksCount = eventContent.ChunksCount
	message.fromTTMAccount = eventContent.SenderTTMAccountAddress

	return m.addMessageChunk(message, &eventContent.ChunkData, 0)
}

func (m *messenger) addMessageNextChunk(eventContent *matrix.MessageChunkEventContent) (*chunkedMessage, bool) {
	m.messagesMutex.Lock()
	defer m.messagesMutex.Unlock()

	message, ok := m.chunkedMessages[eventContent.MessageID]
	if !ok {
		message = &chunkedMessage{
			chunks:    make(map[uint32][]byte),
			firstSeen: time.Now(),
		}
		m.chunkedMessages[eventContent.MessageID] = message
	}

	return m.addMessageChunk(message, &eventContent.ChunkData, eventContent.ChunkIndex)
}

func (m *messenger) addMessageChunk(message *chunkedMessage, chunkData *matrix.ChunkData, chunkIndex uint32) (*chunkedMessage, bool) {
	// chunksCount == 0 means chunk 0 has not arrived yet, so the range is not
	// yet knowable; a bogus index stored during this window is tolerated - see
	// messageIndexSetComplete.
	if message.chunksCount != 0 && chunkIndex >= message.chunksCount {
		m.logger.Warnf("dropping chunk %d of message (id %s): index out of range for a %d-chunk message",
			chunkIndex, chunkData.MessageID, message.chunksCount)
		return nil, false
	}

	// Keying by index makes a redelivery idempotent: it overwrites its own
	// entry rather than adding to the count that decides completion below.
	message.chunks[chunkIndex] = chunkData.Data

	// len(message.chunks) reaching chunksCount is necessary but not sufficient
	// for completion: while chunksCount was still 0 (chunk 0 not yet arrived)
	// an out-of-spec index from a peer cannot be range-checked, so it can sit
	// in the map alongside chunksCount-1 genuine chunks with a real index
	// still missing. messageIndexSetComplete is what actually decides
	// completion; only delete the entry once it agrees, so a message that
	// merely LOOKS complete by count keeps its legitimately-received chunks
	// around for the real trailing chunk to finish later instead of being
	// deleted out from under it.
	if !messageIndexSetComplete(message) {
		return nil, false
	}

	delete(m.chunkedMessages, chunkData.MessageID)

	return message, true
}

// messageIndexSetComplete reports whether message.chunks holds every index in
// 0..chunksCount-1 - i.e. whether the message can actually be assembled, as
// opposed to merely having accumulated chunksCount arrivals (which a bogus
// out-of-spec index received before chunksCount was known can also produce).
// Shared by addMessageChunk (to decide whether to delete the entry) and
// assembleEncodedMessage (to decide whether to build the payload), so the
// walk is written once. It's an O(chunksCount) walk - one map lookup per
// index, ~2k lookups for a 1 MiB / 46-chunk message - negligible next to the
// cost of receiving and joining that many chunks.
func messageIndexSetComplete(message *chunkedMessage) bool {
	if message.chunksCount == 0 {
		return false
	}
	for i := uint32(0); i < message.chunksCount; i++ {
		if _, ok := message.chunks[i]; !ok {
			return false
		}
	}
	return true
}

// assembleEncodedMessage joins the chunks in index order. It returns false if
// the index set is not exactly 0..chunksCount-1. addMessageChunk only ever
// hands back a message for which messageIndexSetComplete is already true, so
// in practice this should never observe a hole - it stays as a defensive
// backstop. If it ever did fire, the caller (assembleOrDropMessage) treats it
// as a dropped message and logs why: handing a payload with a hole in it to
// the verifier would surface as a signature mismatch and blame the crypto for
// a transport bug.
func (m *messenger) assembleEncodedMessage(message *chunkedMessage) (messaging.EncodedSignedMessageWithSender, bool) {
	if !messageIndexSetComplete(message) {
		return messaging.EncodedSignedMessageWithSender{}, false
	}

	chunkedData := make([][]byte, 0, message.chunksCount)
	for i := uint32(0); i < message.chunksCount; i++ {
		chunkedData = append(chunkedData, message.chunks[i])
	}

	return messaging.EncodedSignedMessageWithSender{
		Message: messaging.EncodedSignedMessage{
			ChunkedEncodedMessage: chunkedData,
			Signature:             message.signature,
		},
		SenderTTMAccountAddress: message.fromTTMAccount,
	}, true
}

// evictStalePartialMessages drops incomplete messages older than
// partialMessageTTL. now is a parameter rather than time.Now() so the test can
// drive it.
func (m *messenger) evictStalePartialMessages(now time.Time) {
	m.messagesMutex.Lock()
	defer m.messagesMutex.Unlock()

	for messageID, message := range m.chunkedMessages {
		if now.Sub(message.firstSeen) <= partialMessageTTL {
			continue
		}
		m.logger.Warnf("evicting incomplete message (id %s): %d of %d chunks after %s",
			messageID, len(message.chunks), message.chunksCount, partialMessageTTL)
		delete(m.chunkedMessages, messageID)
	}
}
