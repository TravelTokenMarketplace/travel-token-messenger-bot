// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging/encryption"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging/message"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/partnerplugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/resolver"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/metadata"
	ttmaccounts "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/ttm_accounts"
	m "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/matchers"
	ethCommon "github.com/ethereum/go-ethereum/common"

	"github.com/stretchr/testify/require"

	"go.uber.org/zap"
)

type messageProcessorArgs struct {
	messenger       *MockMessenger
	serviceRegistry *MockServiceRegistry
	responseHandler *MockResponseHandler
	partnerPlugin   *partnerplugin.MockPartnerPlugin
	ttmAccounts     *ttmaccounts.MockService
	encoderDecoder  *MockEncoderDecoder
	resolver        *resolver.MockResolver
}

func defaultMessageProcessorArgs(c *gomock.Controller) messageProcessorArgs {
	return messageProcessorArgs{
		messenger:       NewMockMessenger(c),
		serviceRegistry: NewMockServiceRegistry(c),
		responseHandler: NewMockResponseHandler(c),
		partnerPlugin:   partnerplugin.NewMockPartnerPlugin(c),
		ttmAccounts:     ttmaccounts.NewMockService(c),
		encoderDecoder:  NewMockEncoderDecoder(c),
		resolver:        resolver.NewMockResolver(c),
	}
}

func TestProcessIncomingMessage(t *testing.T) {
	testErr := errors.New("test error")
	requestID := "requestID"
	testSharedKey := encryption.NopKey{SessionKey: []byte("test key")}

	senderBotAddress := ethCommon.Address{1}
	senderTTMAccount := ethCommon.Address{2}

	ownBot := ethCommon.Address{3}
	ownTTMAccount := ethCommon.Address{4}

	responseMessage := &message.Message{
		Type:       generated.PingServiceV1Response,
		RequestID:  requestID,
		Timestamps: metadata.Timestamps{},
	}
	encodedRespMsg := &EncodedSignedMessage{
		ChunkedEncodedMessage: [][]byte{[]byte("chunk1"), []byte("chunk2")},
		Signature:             []byte("signature"),
	}

	type args struct {
		requestMessage          *message.Message
		senderBotAddress        ethCommon.Address
		senderTTMAccountAddress ethCommon.Address
		sharedKey               encryption.Key
	}

	tests := map[string]struct {
		messageProcessorArgs func(*gomock.Controller, *messageProcessorArgs, args)
		responseChannels     map[string]chan *message.Message
		args                 args
		expectedErr          error
		require              func(*testing.T, *messageProcessor)
	}{
		"Invalid message type": {
			messageProcessorArgs: func(_ *gomock.Controller, pArgs *messageProcessorArgs, a args) {
				pArgs.ttmAccounts.EXPECT().IsBotAllowed(m.Context, a.senderTTMAccountAddress, a.senderBotAddress).Return(true, nil)
			},
			args: args{
				requestMessage:          &message.Message{Type: "invalid"},
				senderBotAddress:        ethCommon.Address{1},
				senderTTMAccountAddress: ethCommon.Address{2},
			},
			expectedErr: ErrUnknownMessageCategory,
		},
		"Not supported service": {
			messageProcessorArgs: func(_ *gomock.Controller, pArgs *messageProcessorArgs, a args) {
				pArgs.ttmAccounts.EXPECT().IsBotAllowed(m.Context, a.senderTTMAccountAddress, a.senderBotAddress).Return(true, nil)
				pArgs.serviceRegistry.EXPECT().GetService(a.requestMessage.Type).Return(nil, false)
			},
			args: args{
				requestMessage: &message.Message{
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				senderBotAddress:        senderBotAddress,
				senderTTMAccountAddress: senderTTMAccount,
			},
			expectedErr: ErrUnsupportedService,
		},
		"Messenger failed to send message": {
			messageProcessorArgs: func(c *gomock.Controller, pArgs *messageProcessorArgs, a args) {
				rpcService := rpc.NewMockService(c)
				pArgs.ttmAccounts.EXPECT().IsBotAllowed(m.Context, a.senderTTMAccountAddress, a.senderBotAddress).Return(true, nil)
				pArgs.serviceRegistry.EXPECT().GetService(a.requestMessage.Type).Return(rpcService, true)
				pArgs.partnerPlugin.EXPECT().DoServiceRequest(m.Context, a.requestMessage, rpcService, a.senderTTMAccountAddress, ownTTMAccount).Return(responseMessage.Content, responseMessage.Type)
				pArgs.responseHandler.EXPECT().PrepareResponseMessage(m.Context, a.requestMessage, equalExceptTimestamps(responseMessage))
				pArgs.encoderDecoder.EXPECT().EncodeMessage(m.Context, equalExceptTimestamps(responseMessage), a.senderBotAddress, a.sharedKey, ownTTMAccount).Return(encodedRespMsg, nil)
				pArgs.messenger.EXPECT().SendMessage(m.Context, encodedRespMsg, senderBotAddress, ownTTMAccount).Return(testErr)
			},
			args: args{
				requestMessage: &message.Message{
					RequestID:  requestID,
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				senderBotAddress:        senderBotAddress,
				senderTTMAccountAddress: senderTTMAccount,
				sharedKey:               testSharedKey,
			},
			expectedErr: testErr,
		},
		"OK: process request message": {
			messageProcessorArgs: func(c *gomock.Controller, pArgs *messageProcessorArgs, a args) {
				rpcService := rpc.NewMockService(c)
				pArgs.ttmAccounts.EXPECT().IsBotAllowed(m.Context, a.senderTTMAccountAddress, a.senderBotAddress).Return(true, nil)
				pArgs.serviceRegistry.EXPECT().GetService(a.requestMessage.Type).Return(rpcService, true)
				pArgs.partnerPlugin.EXPECT().DoServiceRequest(m.Context, a.requestMessage, rpcService, a.senderTTMAccountAddress, ownTTMAccount).Return(responseMessage.Content, responseMessage.Type)
				pArgs.responseHandler.EXPECT().PrepareResponseMessage(m.Context, a.requestMessage, equalExceptTimestamps(responseMessage))
				pArgs.encoderDecoder.EXPECT().EncodeMessage(m.Context, equalExceptTimestamps(responseMessage), a.senderBotAddress, a.sharedKey, ownTTMAccount).Return(encodedRespMsg, nil)
				pArgs.messenger.EXPECT().SendMessage(m.Context, encodedRespMsg, senderBotAddress, ownTTMAccount).Return(nil)
			},
			args: args{
				requestMessage: &message.Message{
					RequestID:  requestID,
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				senderBotAddress:        senderBotAddress,
				senderTTMAccountAddress: senderTTMAccount,
				sharedKey:               testSharedKey,
			},
		},
		"OK: process response message": {
			messageProcessorArgs: func(_ *gomock.Controller, pArgs *messageProcessorArgs, a args) {
				pArgs.ttmAccounts.EXPECT().IsBotAllowed(m.Context, a.senderTTMAccountAddress, a.senderBotAddress).Return(true, nil)
			},
			responseChannels: map[string]chan *message.Message{
				requestID: make(chan *message.Message, 1),
			},
			args: args{
				requestMessage:          responseMessage,
				senderBotAddress:        senderBotAddress,
				senderTTMAccountAddress: senderTTMAccount,
			},
			require: func(t *testing.T, p *messageProcessor) {
				responseChan, ok := p.getResponseChannel(requestID)
				require.True(t, ok)
				msgReceived := <-responseChan
				require.Equal(t, responseMessage, msgReceived)
			},
		},
	}
	for tc, tt := range tests {
		t.Run(tc, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			messageProcessorArgs := defaultMessageProcessorArgs(ctrl)
			if tt.messageProcessorArgs != nil {
				tt.messageProcessorArgs(ctrl, &messageProcessorArgs, tt.args)
			}

			p := NewMessageProcessor(
				messageProcessorArgs.messenger,
				zap.NewNop().Sugar(),
				time.Duration(0),
				ownBot,
				ownTTMAccount,
				messageProcessorArgs.serviceRegistry,
				messageProcessorArgs.responseHandler,
				messageProcessorArgs.partnerPlugin,
				messageProcessorArgs.ttmAccounts,
				messageProcessorArgs.encoderDecoder,
				messageProcessorArgs.resolver,
			)
			messageProcessor := p.(*messageProcessor)
			for requestID, responseChan := range tt.responseChannels {
				messageProcessor.setResponseChannel(requestID, responseChan)
			}
			err := messageProcessor.processIncomingMessage(context.Background(), tt.args.requestMessage, tt.args.senderBotAddress, tt.args.senderTTMAccountAddress, tt.args.sharedKey)
			require.ErrorIs(t, err, tt.expectedErr)

			if tt.require != nil {
				tt.require(t, messageProcessor)
			}
		})
	}
}

func TestSendRequestMessage(t *testing.T) {
	testErr := errors.New("test error")
	requestID := "requestID"

	responseMessage := &message.Message{
		Type:      generated.PingServiceV1Response,
		RequestID: requestID,
	}

	ownBot := ethCommon.Address{1}
	ownTTMAccount := ethCommon.Address{2}
	recipientBot := ethCommon.Address{3}
	recipientTTMAccount := ethCommon.Address{4}

	encodedReqMsg := &EncodedSignedMessage{
		ChunkedEncodedMessage: [][]byte{[]byte("chunk1"), []byte("chunk2")},
		Signature:             []byte("signature"),
	}

	type args struct {
		msg                 *message.Message
		recipientTTMAccount ethCommon.Address
	}

	testSharedKey, err := encryption.NewKey()
	require.NoError(t, err)

	tests := map[string]struct {
		messageProcessorArgs    func(*messageProcessorArgs, args)
		args                    args
		expectedResponseMessage *message.Message
		expectedErr             error
		responses               func(*messageProcessor)
	}{
		"Messenger failed to send message": {
			messageProcessorArgs: func(pArgs *messageProcessorArgs, a args) {
				pArgs.resolver.EXPECT().GetBotAddress(m.Context, recipientTTMAccount).Return(recipientBot, nil)
				pArgs.ttmAccounts.EXPECT().IsServiceSupported(m.Context, recipientTTMAccount, a.msg.Type.ToServiceName()).Return(true, nil)
				pArgs.responseHandler.EXPECT().PrepareRequest(a.msg.Content)
				pArgs.encoderDecoder.EXPECT().EncodeMessage(m.Context, a.msg, recipientBot, gomock.AssignableToTypeOf(testSharedKey), ownTTMAccount).Return(encodedReqMsg, nil)
				pArgs.messenger.EXPECT().SendMessage(m.Context, encodedReqMsg, recipientBot, ownTTMAccount).Return(testErr)
			},
			args: args{
				msg: &message.Message{
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				recipientTTMAccount: recipientTTMAccount,
			},
			expectedErr: testErr,
		},
		"Response timeout": {
			messageProcessorArgs: func(pArgs *messageProcessorArgs, a args) {
				pArgs.resolver.EXPECT().GetBotAddress(m.Context, recipientTTMAccount).Return(recipientBot, nil)
				pArgs.ttmAccounts.EXPECT().IsServiceSupported(m.Context, recipientTTMAccount, a.msg.Type.ToServiceName()).Return(true, nil)
				pArgs.responseHandler.EXPECT().PrepareRequest(a.msg.Content)
				pArgs.encoderDecoder.EXPECT().EncodeMessage(m.Context, a.msg, recipientBot, gomock.AssignableToTypeOf(testSharedKey), ownTTMAccount).Return(encodedReqMsg, nil)
				pArgs.messenger.EXPECT().SendMessage(m.Context, encodedReqMsg, recipientBot, ownTTMAccount).Return(nil)
				pArgs.resolver.EXPECT().SetBotStatus(m.Context, recipientBot, resolver.BotStatusUnreachable).Return(nil)
			},
			args: args{
				msg: &message.Message{
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				recipientTTMAccount: recipientTTMAccount,
			},
			expectedErr: ErrExceededResponseTimeout,
		},
		"OK": {
			messageProcessorArgs: func(pArgs *messageProcessorArgs, a args) {
				pArgs.resolver.EXPECT().GetBotAddress(m.Context, recipientTTMAccount).Return(recipientBot, nil)
				pArgs.ttmAccounts.EXPECT().IsServiceSupported(m.Context, recipientTTMAccount, a.msg.Type.ToServiceName()).Return(true, nil)
				pArgs.responseHandler.EXPECT().PrepareRequest(a.msg.Content)
				pArgs.encoderDecoder.EXPECT().EncodeMessage(m.Context, a.msg, recipientBot, gomock.AssignableToTypeOf(testSharedKey), ownTTMAccount).Return(encodedReqMsg, nil)
				pArgs.messenger.EXPECT().SendMessage(m.Context, encodedReqMsg, recipientBot, ownTTMAccount).Return(nil)
				pArgs.responseHandler.EXPECT().ProcessResponseMessage(m.Context, a.msg, responseMessage)
				pArgs.resolver.EXPECT().SetBotStatus(m.Context, recipientBot, resolver.BotStatusReachable).Return(nil)
			},
			args: args{
				msg: &message.Message{
					Type:       generated.PingServiceV1Request,
					RequestID:  requestID,
					Timestamps: metadata.Timestamps{},
				},
				recipientTTMAccount: recipientTTMAccount,
			},
			responses: func(p *messageProcessor) {
				for {
					responseChan, ok := p.getResponseChannel(requestID)
					if ok {
						responseChan <- responseMessage
						return
					}
					time.Sleep(50 * time.Millisecond)
				}
			},
			expectedResponseMessage: responseMessage,
		},
		"Service not supported on-chain": {
			messageProcessorArgs: func(pArgs *messageProcessorArgs, a args) {
				pArgs.resolver.EXPECT().GetBotAddress(m.Context, recipientTTMAccount).Return(recipientBot, nil)
				pArgs.ttmAccounts.EXPECT().IsServiceSupported(m.Context, recipientTTMAccount, a.msg.Type.ToServiceName()).Return(false, nil)
			},
			args: args{
				msg: &message.Message{
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				recipientTTMAccount: recipientTTMAccount,
			},
			expectedErr: ttmaccounts.ErrServiceNotSupported,
		},
		"Service check blockchain error": {
			messageProcessorArgs: func(pArgs *messageProcessorArgs, a args) {
				pArgs.resolver.EXPECT().GetBotAddress(m.Context, recipientTTMAccount).Return(recipientBot, nil)
				pArgs.ttmAccounts.EXPECT().IsServiceSupported(m.Context, recipientTTMAccount, a.msg.Type.ToServiceName()).Return(false, testErr)
			},
			args: args{
				msg: &message.Message{
					Type:       generated.PingServiceV1Request,
					Timestamps: metadata.Timestamps{},
				},
				recipientTTMAccount: recipientTTMAccount,
			},
			expectedErr: rpc.ErrBlockchain,
		},
	}

	for tc, tt := range tests {
		t.Run(tc, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			messageProcessorArgs := defaultMessageProcessorArgs(ctrl)
			if tt.messageProcessorArgs != nil {
				tt.messageProcessorArgs(&messageProcessorArgs, tt.args)
			}

			p := NewMessageProcessor(
				messageProcessorArgs.messenger,
				zap.NewNop().Sugar(),
				1*time.Second, // response timeout
				ownBot,
				ownTTMAccount,
				messageProcessorArgs.serviceRegistry,
				messageProcessorArgs.responseHandler,
				messageProcessorArgs.partnerPlugin,
				messageProcessorArgs.ttmAccounts,
				messageProcessorArgs.encoderDecoder,
				messageProcessorArgs.resolver,
			)
			if tt.responses != nil {
				go tt.responses(p.(*messageProcessor))
			}
			responseMessage, err := p.SendRequestMessage(context.Background(), tt.args.msg, tt.args.recipientTTMAccount)
			require.ErrorIs(t, err, tt.expectedErr)
			require.Equal(t, tt.expectedResponseMessage, responseMessage)
		})
	}
}

func TestStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := gomock.NewController(t)
	serviceRegistry := NewMockServiceRegistry(c)
	ttmAccounts := ttmaccounts.NewMockService(c)
	partnerPlugin := partnerplugin.NewMockPartnerPlugin(c)
	messenger := NewMockMessenger(c)
	encoderDecoder := NewMockEncoderDecoder(c)
	responseHandler := NewMockResponseHandler(c)
	resolver := resolver.NewMockResolver(c)

	senderBot := ethCommon.Address{1}
	senderTTMAccount := ethCommon.Address{2}

	ownBot := ethCommon.Address{3}
	ownTTMAccount := ethCommon.Address{4}

	requestMsg := &message.Message{
		Type:       generated.PingServiceV1Request,
		RequestID:  "requestID",
		Timestamps: metadata.Timestamps{},
	}
	sharedKey := encryption.NopKey{SessionKey: []byte("test shared key")}

	incomingMessages := []EncodedSignedMessageWithSender{}

	// Received message from itself (bot)

	incomingMessages = append(incomingMessages, EncodedSignedMessageWithSender{
		Message:                 EncodedSignedMessage{Signature: []byte("self-message (bot) signature")},
		SenderBotAddress:        ownBot,
		SenderTTMAccountAddress: senderTTMAccount,
	})

	// Received message from itself (ttm account)

	encodedBadRequestMsg := EncodedSignedMessageWithSender{
		Message:                 EncodedSignedMessage{Signature: []byte("self-message (cm-account) signature")},
		SenderBotAddress:        senderBot,
		SenderTTMAccountAddress: ownTTMAccount,
	}
	incomingMessages = append(incomingMessages, encodedBadRequestMsg)

	// OK message

	encodedRequestMsg := EncodedSignedMessageWithSender{
		Message:                 EncodedSignedMessage{Signature: []byte("message signature")},
		SenderBotAddress:        senderBot,
		SenderTTMAccountAddress: senderTTMAccount,
	}
	incomingMessages = append(incomingMessages, encodedRequestMsg)

	rpcService := rpc.NewMockService(c)

	responseMessage := &message.Message{
		Type:       generated.PingServiceV1Response,
		RequestID:  requestMsg.RequestID,
		Timestamps: metadata.Timestamps{},
	}
	encodedRespMsg := &EncodedSignedMessage{
		ChunkedEncodedMessage: [][]byte{[]byte("chunk1"), []byte("chunk2")},
		Signature:             []byte("response signature"),
	}

	encoderDecoder.EXPECT().DecodeAndVerifyMessage(ctx, &encodedRequestMsg.Message, encodedRequestMsg.SenderBotAddress).Return(requestMsg, sharedKey, senderTTMAccount, nil)
	ttmAccounts.EXPECT().IsBotAllowed(gomock.Any(), senderTTMAccount, senderBot).Return(true, nil)
	serviceRegistry.EXPECT().GetService(requestMsg.Type).Return(rpcService, true)
	responseHandler.EXPECT().PrepareResponseMessage(m.Context, requestMsg, equalExceptTimestamps(responseMessage))
	partnerPlugin.EXPECT().DoServiceRequest(m.Context, requestMsg, rpcService, senderTTMAccount, ownTTMAccount).Return(responseMessage.Content, responseMessage.Type)
	encoderDecoder.EXPECT().EncodeMessage(m.Context, equalExceptTimestamps(responseMessage), senderBot, sharedKey, ownTTMAccount).Return(encodedRespMsg, nil)
	messenger.EXPECT().SendMessage(m.Context, encodedRespMsg, senderBot, ownTTMAccount).Return(nil)

	// set up incoming messages channel

	incomingMessagesChan := make(chan EncodedSignedMessageWithSender, len(incomingMessages))
	for _, msg := range incomingMessages {
		incomingMessagesChan <- msg
	}
	messenger.EXPECT().ReceivedMessageChan().Times(len(incomingMessages) + 1).Return(incomingMessagesChan)

	// set up and start messenger

	NewMessageProcessor(
		messenger,
		zap.NewNop().Sugar(),
		time.Duration(0),
		ownBot,
		ownTTMAccount,
		serviceRegistry,
		responseHandler,
		partnerPlugin,
		ttmAccounts,
		encoderDecoder,
		resolver,
	).Start(ctx)

	time.Sleep(1 * time.Second)
}

func equalExceptTimestamps(expectedMessage *message.Message) gomock.Matcher {
	return gomock.Cond(func(actualMessage *message.Message) bool {
		return expectedMessage.RequestID == actualMessage.RequestID &&
			expectedMessage.Type == actualMessage.Type &&
			proto.Equal(expectedMessage.Content, actualMessage.Content)
	})
}
