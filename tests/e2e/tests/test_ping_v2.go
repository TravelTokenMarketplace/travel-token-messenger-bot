// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package tests

import (
	"context"
	"fmt"
	"testing"

	pingv2 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/ping/v2"
	typesv4 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v4"
	"buf.build/go/protovalidate"
	botGenerated "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/bot"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ suite.Test = (*TestPingV2)(nil)

func init() {
	Tests["PingV2"] = &TestPingV2{}
}

type TestPingV2 struct {
	*suite.Environment

	supplierPartnerPlugin *partnerplugin.PartnerPlugin
	supplierBot           *bot.Bot
	distributorBot        *bot.Bot
}

func (tt *TestPingV2) Setup(e *suite.Environment) {
	tt.Environment = e
}

func (tt *TestPingV2) Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	tt.prepare(ctx, t)

	t.Run("Ping", func(t *testing.T) {
		tt.testPingV2Service(ctx, t)
	})
}

func (tt *TestPingV2) prepare(ctx context.Context, t *testing.T) {
	require.NoError(t, tt.Chain.Client.RegisterCMServices(ctx, botGenerated.PingServiceV2))

	tt.supplierPartnerPlugin = tt.CreatePartnerPlugin(ctx, t)

	// bot with partnerPlugin and without rpc server (supplier)
	tt.supplierBot = tt.CreateBot(ctx, t, true, tt.supplierPartnerPlugin,
		bot.WithServices([]bot.CMService{{Name: botGenerated.PingServiceV2}}),
	)

	// bot without partnerPlugin and with rpc server (distributor)
	tt.distributorBot = tt.CreateBot(ctx, t, true, nil)
}

func (tt *TestPingV2) testPingV2Service(ctx context.Context, t *testing.T) {
	expectedResponseMessageSubString := fmt.Sprintf("Ping response to [%s] with request ID:", common.PingMessage)

	req := &pingv2.PingRequest{
		Header:    &typesv4.RequestHeader{BaseHeader: &typesv4.Header{Version: &typesv4.Version{}}},
		Message:   common.PingMessage,
		Timestamp: timestamppb.Now(),
	}
	resp, err := tt.distributorBot.PingServiceV2.Ping(
		requestContext(ctx, tt.supplierBot.TTMAccountAddress()),
		req,
	)

	require.NoError(t, err)
	tt.DebugPrintRequestResponse(req, resp)
	require.NoError(t, protovalidate.Validate(resp))

	successResp := resp.GetSuccessResponse()
	require.NotNil(t, successResp, "unexpected response status")
	require.Empty(t, successResp.Header.Alerts, "unexpected response alerts")
	require.Contains(t, successResp.Message, expectedResponseMessageSubString, "unexpected response message")
}
