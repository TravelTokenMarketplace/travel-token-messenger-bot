// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package tests

import (
	"context"
	"fmt"
	"os"
	"testing"

	pingv1 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/ping/v1"
	typesv1 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v1"
	botGenerated "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/bot"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// deprecatedServicesWarning is the first line of the summary warning emitted by
// NewServiceRegistry for services the TTM Account still supports but the manager
// has unregistered. Kept in sync with internal/messaging/service_registry.go.
const deprecatedServicesWarning = "Deprecated services (supported by this TTM Account, but no longer registered with the manager):"

var _ suite.Test = (*TestDeprecatedService)(nil)

func init() {
	Tests["DeprecatedService"] = &TestDeprecatedService{}
}

// TestDeprecatedService covers the path where the TTM Account still supports a
// service that the manager has unregistered. The bot must keep working — the hot
// path asks the account, not the manager — but it must say so at startup.
type TestDeprecatedService struct {
	*suite.Environment

	supplierPartnerPlugin *partnerplugin.PartnerPlugin
	supplierBot           *bot.Bot
	distributorBot        *bot.Bot
}

func (tt *TestDeprecatedService) Setup(e *suite.Environment) {
	tt.Environment = e
}

func (tt *TestDeprecatedService) Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	tt.prepare(ctx, t)

	t.Run("No warning while the service is still registered", func(t *testing.T) {
		require.NotContains(t, tt.supplierBotLog(t), deprecatedServicesWarning,
			"warning must only appear once the service is unregistered")
	})

	t.Run("Unregister the service and restart the supplier", func(t *testing.T) {
		require.NoError(t, tt.Chain.Client.UnregisterCMService(ctx, botGenerated.PingServiceV1))
		tt.RestartBot(ctx, t, tt.supplierBot)
	})

	t.Run("Deprecated service still works", func(t *testing.T) {
		tt.testPingV1Service(ctx, t)
	})
}

func (tt *TestDeprecatedService) prepare(ctx context.Context, t *testing.T) {
	// The ordering is forced by the contracts: TTMAccount.addService gates on
	// _requireRegisteredService, so the manager must still have the service
	// registered when the account adds it. Unregistering comes later.
	require.NoError(t, tt.Chain.Client.RegisterCMServices(ctx, botGenerated.PingServiceV1))

	// bot with partnerPlugin and with rpc server (supplier)
	tt.supplierPartnerPlugin = tt.CreatePartnerPlugin(ctx, t)
	tt.supplierBot = tt.CreateBot(ctx, t, true, tt.supplierPartnerPlugin,
		bot.WithServices([]bot.CMService{{Name: botGenerated.PingServiceV1}}),
	)

	// bot without partnerPlugin and with rpc server (distributor)
	tt.distributorBot = tt.CreateBot(ctx, t, true, nil)
}

func (tt *TestDeprecatedService) supplierBotLog(t *testing.T) string {
	t.Helper()

	logBytes, err := os.ReadFile(tt.supplierBot.LogPath())
	require.NoError(t, err)
	return string(logBytes)
}

func (tt *TestDeprecatedService) testPingV1Service(ctx context.Context, t *testing.T) {
	expectedResponseMessageSubString := fmt.Sprintf("Ping response to [%s] with request ID:", common.PingMessage)

	req := &pingv1.PingRequest{
		Header:      &typesv1.RequestHeader{BaseHeader: &typesv1.Header{}},
		PingMessage: common.PingMessage,
		Timestamp:   timestamppb.Now(),
	}
	resp, err := tt.distributorBot.PingServiceV1.Ping(
		requestContext(ctx, tt.supplierBot.TTMAccountAddress()),
		req,
	)

	require.NoError(t, err)
	tt.DebugPrintRequestResponse(req, resp)
	require.Equal(t, typesv1.StatusType_STATUS_TYPE_SUCCESS, resp.Header.Status, "unexpected response status")
	require.Empty(t, resp.Header.Alerts, "unexpected response alerts")
	require.Contains(t, resp.PingMessage, expectedResponseMessageSubString, "unexpected response message")
}
