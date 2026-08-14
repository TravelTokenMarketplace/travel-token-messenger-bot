// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package tests

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	bookv4 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/book/v4"
	notificationv3 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/notification/v3"
	typesv4 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v4"
	"buf.build/go/protovalidate"
	botGenerated "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/bot"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/ppevents"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/suite"
	"github.com/stretchr/testify/require"
)

var _ suite.Test = (*TestMintV4)(nil)

func init() {
	Tests["MintV4"] = &TestMintV4{}
}

type TestMintV4 struct {
	*suite.Environment

	supplierPartnerPlugin      *partnerplugin.PartnerPlugin
	supplierPPEventStream      *ppevents.Stream
	supplierBot                *bot.Bot
	distributorBot             *bot.Bot
	distributorBotWithoutFunds *bot.Bot
}

func (tt *TestMintV4) Setup(e *suite.Environment) {
	tt.Environment = e
}

func (tt *TestMintV4) Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	tt.prepare(ctx, t)

	t.Run("Search->Validate->Mint->TokenBoughtNotification", func(t *testing.T) {
		// We're doing this > 1 times to make sure that even with multiple
		// mint requests everything is working as expected.
		for range 3 {
			tt.testMintV4FullWorkflow(ctx, t)
		}
	})

	t.Run("Search->Validate->Mint(not enough funds to buy)->TokenReservationExpiredNotification", func(t *testing.T) {
		tt.testMintV4TokenExpiredCase(ctx, t)
	})

	t.Run("Search->Validate->Mint(wrong expected price)->TokenReservationExpiredNotification", func(t *testing.T) {
		tt.testMintV4UnexpectedPrice(ctx, t)
	})
}

func (tt *TestMintV4) prepare(ctx context.Context, t *testing.T) {
	require.NoError(t, tt.Chain.Client.RegisterCMServices(ctx,
		botGenerated.AccommodationSearchServiceV4,
		botGenerated.ValidationServiceV4,
		botGenerated.MintServiceV4,
	))

	tt.supplierPartnerPlugin = tt.CreatePartnerPlugin(ctx, t)

	// bot with partnerPlugin and without rpc server (supplier)
	tt.supplierBot = tt.CreateBot(ctx, t, true, tt.supplierPartnerPlugin,
		bot.WithServices([]bot.CMService{
			{Name: botGenerated.AccommodationSearchServiceV4},
			{Name: botGenerated.ValidationServiceV4},
			{Name: botGenerated.MintServiceV4},
		}),
	)

	// bot without partnerPlugin and with rpc server (distributor)
	tt.distributorBot = tt.CreateBot(ctx, t, true, nil)

	// bot without partnerPlugin and with rpc server (distributor) but with the
	// catch, that the bot account does not have funds to pay for the fees when
	// trying to buy the booking token.
	tt.distributorBotWithoutFunds = tt.CreateBot(ctx, t, true, nil,
		bot.WithSkips(&bot.Skip{PrefundBot: true}),
	)

	var err error
	tt.supplierPPEventStream, err = tt.supplierPartnerPlugin.RecordEvents(ctx)
	require.NoError(t, err)
}

func (tt *TestMintV4) testMintV4FullWorkflow(ctx context.Context, t *testing.T) {
	searchID, resultID, totalPrice := testAccommodationV4SearchService(ctx, t, tt.Environment, tt.distributorBot, tt.supplierBot) // see test_accommodation_v4.go
	validationID := testValidateV4(ctx, t, tt.Environment, tt.distributorBot, tt.supplierBot, searchID, resultID, totalPrice)

	balanceBefore := tt.Balance(ctx, t, tt.distributorBot)

	tokenID, mintID, mintRespPrice := testMintV4(ctx, t, tt.Environment, tt.distributorBot, tt.supplierBot, validationID, common.BookingTokenPriceV4)

	tokenBoughtNotification := ppevents.Await[*notificationv3.TokenBought](t, tt.supplierPPEventStream)
	tt.DebugPrintProtoMessage(tokenBoughtNotification)
	require.NoError(t, protovalidate.Validate(tokenBoughtNotification))
	require.Equal(t, tokenBoughtNotification.TokenId, tokenID)
	require.NotNil(t, tokenBoughtNotification.MintId)
	require.Equal(t, tokenBoughtNotification.MintId.Value, mintID)
	require.NotEmpty(t, tokenBoughtNotification.TxId)

	verifyBookingTokenStateBoughtWithPriceV4(ctx, t, tt.Environment, tt.distributorBot, tokenID, mintRespPrice, balanceBefore)
}

func (tt *TestMintV4) testMintV4TokenExpiredCase(ctx context.Context, t *testing.T) {
	searchID, resultID, totalPrice := testAccommodationV4SearchService(ctx, t, tt.Environment, tt.distributorBotWithoutFunds, tt.supplierBot) // see test_accommodation_v4.go
	validationID1 := testValidateV4(ctx, t, tt.Environment, tt.distributorBotWithoutFunds, tt.supplierBot, searchID, resultID, totalPrice)

	searchID, resultID, totalPrice = testAccommodationV4SearchService(ctx, t, tt.Environment, tt.distributorBotWithoutFunds, tt.supplierBot) // see test_accommodation_v4.go
	validationID2 := testValidateV4(ctx, t, tt.Environment, tt.distributorBotWithoutFunds, tt.supplierBot, searchID, resultID, totalPrice)

	balanceBefore := tt.Balance(ctx, t, tt.distributorBotWithoutFunds)

	tt.testMintV4MintV4ExpectedError(ctx, t, tt.distributorBotWithoutFunds, validationID1, totalPrice)
	tt.testMintV4MintV4ExpectedError(ctx, t, tt.distributorBotWithoutFunds, validationID2, totalPrice)

	// Await returns one type in arrival order, so these are the two expired
	// notifications in the order pp-mock received them. Neither assertion
	// depends on which mint produced which.
	firstExpired := ppevents.Await[*notificationv3.TokenReservationExpired](t, tt.supplierPPEventStream)
	tt.DebugPrintProtoMessage(firstExpired)
	require.NoError(t, protovalidate.Validate(firstExpired))

	secondExpired := ppevents.Await[*notificationv3.TokenReservationExpired](t, tt.supplierPPEventStream)
	tt.DebugPrintProtoMessage(secondExpired)
	require.NoError(t, protovalidate.Validate(secondExpired))

	require.Equal(t, balanceBefore, tt.Balance(ctx, t, tt.distributorBotWithoutFunds), "unexpected balance")
}

func (tt *TestMintV4) testMintV4UnexpectedPrice(ctx context.Context, t *testing.T) {
	searchID, resultID, expectedPrice := testAccommodationV4SearchService(ctx, t, tt.Environment, tt.distributorBot, tt.supplierBot) // see test_accommodation_v4.go
	validationID := testValidateV4(ctx, t, tt.Environment, tt.distributorBot, tt.supplierBot, searchID, resultID, expectedPrice)

	balanceBefore := tt.Balance(ctx, t, tt.distributorBot)

	// modify expected price to be different from the one returned by pp-mock mint response
	expectedPrice = common.CloneProto(common.BookingTokenPriceV4)
	value, err := strconv.ParseInt(common.BookingTokenPriceV4.Value, 10, 64)
	require.NoError(t, err)
	expectedPrice.Value = fmt.Sprintf("%d", value+10)

	tt.testMintV4MintV4ExpectedError(ctx, t, tt.distributorBot, validationID, expectedPrice)

	tokenExpiredNotification := ppevents.Await[*notificationv3.TokenReservationExpired](t, tt.supplierPPEventStream)
	tt.DebugPrintProtoMessage(tokenExpiredNotification)
	require.NoError(t, protovalidate.Validate(tokenExpiredNotification))

	require.Equal(t, balanceBefore, tt.Balance(ctx, t, tt.distributorBot), "unexpected balance")
}

func (tt *TestMintV4) testMintV4MintV4ExpectedError(
	ctx context.Context,
	t *testing.T,
	distributorBot *bot.Bot,
	validationID string,
	expectedPrice *typesv4.Price,
) {
	req := &bookv4.MintRequest{
		Header:        &typesv4.RequestHeader{BaseHeader: &typesv4.Header{Version: &typesv4.Version{}}},
		ValidationId:  &typesv4.UUID{Value: validationID},
		BuyerAddress:  &typesv4.EVMAddress{Address: "0x0000000000000000000000000000000000000000"},
		ExpectedPrice: expectedPrice,
		Travellers: []*typesv4.ExtensiveTraveller{{
			FirstNames: []string{"FirstName"},
			Surnames:   []string{"Surname"},
			Gender:     typesv4.GenderType_GENDER_TYPE_UNSPECIFIED,
		}},
	}
	resp, err := distributorBot.MintServiceV4.Mint(
		requestContext(ctx, tt.supplierBot.TTMAccountAddress()),
		req,
	)
	require.NoError(t, err)
	tt.DebugPrintRequestResponse(req, resp)
	require.NoError(t, protovalidate.Validate(resp))

	require.True(t, resp.HasErrorResponse(), "unexpected response status")
}
