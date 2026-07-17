// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package v2

import (
	"context"
	"fmt"

	"buf.build/gen/go/ttm/messenger-protocol/grpc/go/ttm/services/ping/v2/pingv2grpc"
	pingv2 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/ping/v2"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ pingv2grpc.PingServiceServer = (*pingServiceV2Server)(nil)

type pingServiceV2Server struct{}

func NewPingServiceServer() pingv2grpc.PingServiceServer {
	return &pingServiceV2Server{}
}

func (s *pingServiceV2Server) Ping(ctx context.Context, req *pingv2.PingRequest) (*pingv2.PingResponse, error) {
	md := metadata.FromContext(ctx)
	return &pingv2.PingResponse{
		Response: &pingv2.PingResponse_SuccessResponse{
			SuccessResponse: &pingv2.PingSuccessResponse{
				Header:    common.SuccessHeaderV4(),
				Timestamp: timestamppb.Now(),
				Message:   fmt.Sprintf("Ping response to [%s] with request ID: %s", req.Message, md.RequestID),
			},
		},
	}, nil
}
