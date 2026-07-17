// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package v5

import (
	"context"

	"buf.build/gen/go/ttm/messenger-protocol/grpc/go/ttm/services/transport/v5/transportv5grpc"
	transportv5 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/transport/v5"
	typesv4 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v4"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/common"
	mockdata "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/services/data"
)

var _ transportv5grpc.TransportProductListServiceServer = (*transportProductListV5Server)(nil)

type transportProductListV5Server struct{}

func NewTransportProductListServer() transportv5grpc.TransportProductListServiceServer {
	return &transportProductListV5Server{}
}

func (s *transportProductListV5Server) TransportProductList(_ context.Context, req *transportv5.TransportProductListRequest) (*transportv5.TransportProductListResponse, error) {
	filteredTrips := mockdata.TripsBasicV5

	if req.ModifiedAfter != nil {
		filteredTrips = filterTripsBasicByModifiedAfter(filteredTrips, req.ModifiedAfter.AsTime())
	}

	response := &transportv5.TransportProductListResponse{
		Response: &transportv5.TransportProductListResponse_SuccessResponse{
			SuccessResponse: &transportv5.TransportProductListSuccessResponse{
				Header: common.SuccessHeaderV4(),
				Trips:  filteredTrips,
			},
		},
	}

	if len(filteredTrips) == 0 {
		common.AddHeaderAlertV4(response.GetSuccessResponse().Header, typesv4.AlertCode_ALERT_CODE_NO_CONTENT, "No trips found")
	}

	return response, nil
}
