// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package messenger

import (
	"context"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/conversion"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/matrix"
	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"
	"maunium.net/go/mautrix/event"
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
		message = &chunkedMessage{chunks: make(map[uint32][]byte, eventContent.ChunksCount)}
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
		message = &chunkedMessage{chunks: make(map[uint32][]byte)}
		m.chunkedMessages[eventContent.MessageID] = message
	}

	return m.addMessageChunk(message, &eventContent.ChunkData, eventContent.ChunkIndex)
}

func (m *messenger) addMessageChunk(message *chunkedMessage, chunkData *matrix.ChunkData, chunkIndex uint32) (*chunkedMessage, bool) {
	// chunksCount == 0 means chunk 0 has not arrived yet, so the range is not
	// yet knowable; assembleEncodedMessage is the backstop for that case.
	if message.chunksCount != 0 && chunkIndex >= message.chunksCount {
		m.logger.Warnf("dropping chunk %d of message (id %s): index out of range for a %d-chunk message",
			chunkIndex, chunkData.MessageID, message.chunksCount)
		return nil, false
	}

	// Keying by index makes a redelivery idempotent: it overwrites its own
	// entry rather than adding to the count that decides completion below.
	message.chunks[chunkIndex] = chunkData.Data

	if message.chunksCount == 0 || conversion.MustIntToUInt32(len(message.chunks)) < message.chunksCount {
		return nil, false
	}

	delete(m.chunkedMessages, chunkData.MessageID)

	return message, true
}

// assembleEncodedMessage joins the chunks in index order. It returns false if
// the index set is not exactly 0..chunksCount-1, which the callers treat as a
// dropped message: handing a payload with a hole in it to the verifier would
// surface as a signature mismatch and blame the crypto for a transport bug.
func (m *messenger) assembleEncodedMessage(message *chunkedMessage) (messaging.EncodedSignedMessageWithSender, bool) {
	chunkedData := make([][]byte, 0, message.chunksCount)
	for i := uint32(0); i < message.chunksCount; i++ {
		chunk, ok := message.chunks[i]
		if !ok {
			return messaging.EncodedSignedMessageWithSender{}, false
		}
		chunkedData = append(chunkedData, chunk)
	}

	return messaging.EncodedSignedMessageWithSender{
		Message: messaging.EncodedSignedMessage{
			ChunkedEncodedMessage: chunkedData,
			Signature:             message.signature,
		},
		SenderTTMAccountAddress: message.fromTTMAccount,
	}, true
}
