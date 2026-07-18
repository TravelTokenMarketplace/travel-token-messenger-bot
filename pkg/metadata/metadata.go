// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package metadata

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/grpc/metadata"
)

const (
	KeyRequestID           = "request_id"
	KeyRecipientTTMAccount = "recipient_ttm_account"
	KeySenderTTMAccount    = "sender_ttm_account"
	KeyTimestamps          = "timestamps"
)

type Metadata struct {
	RequestID           string
	RecipientTTMAccount common.Address
	SenderTTMAccount    common.Address
	Timestamps          Timestamps // can be nil
}

func FromGRPCContext(ctx context.Context) *Metadata {
	md := &Metadata{}

	mdPairs, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return md
	}

	if requestID := mdPairs["request_id"]; len(requestID) == 1 {
		md.RequestID = requestID[0]
	}

	if recipientTTMAccount := mdPairs[KeyRecipientTTMAccount]; len(recipientTTMAccount) == 1 {
		md.RecipientTTMAccount = common.HexToAddress(recipientTTMAccount[0])
	}

	if senderTTMAccount := mdPairs[KeySenderTTMAccount]; len(senderTTMAccount) == 1 {
		md.SenderTTMAccount = common.HexToAddress(senderTTMAccount[0])
	}

	if timestampsStr := mdPairs["timestamps"]; len(timestampsStr) == 1 {
		md.Timestamps, _ = TimestampsFromString(timestampsStr[0])
	}

	return md
}
