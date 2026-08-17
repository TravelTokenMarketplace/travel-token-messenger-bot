// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package tests

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"testing"

	pingv1 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/services/ping/v1"
	typesv1 "buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go/ttm/types/v1"
	matrix_client "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/matrix/client"
	botGenerated "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/bot"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ suite.Test = (*TestMultiChunk)(nil)

func init() {
	Tests["MultiChunk"] = &TestMultiChunk{}
}

// TestMultiChunk drives a payload large enough to be split across several
// Matrix events, so that messageChunkEventHandler and the app-service's
// processMessageChunkEvent are exercised. Every other suite's messages fit in
// one chunk, so without this the whole reassembly path is untested end to end.
//
// This uses ping v1, not v2: v2's PingRequest/PingSuccessResponse.message is
// capped at 512 bytes by a buf.validate rule
// (proto/ttm/services/ping/v2/ping.proto), which is far below MaxChunkSize
// (30KB) - no string can both exceed one chunk and pass that validation, so
// v2 is structurally unusable for this suite. v1 declares no buf/validate
// constraints on ping_message at all.
//
// This proves the multi-chunk path WORKS. It does not prove the duplicate-chunk
// race is fixed - the local conduit is too fast to redeliver. The unit tests in
// internal/matrix/messenger/messages_test.go prove that.
type TestMultiChunk struct {
	*suite.Environment

	supplierPartnerPlugin *partnerplugin.PartnerPlugin
	supplierBot           *bot.Bot
	distributorBot        *bot.Bot
}

func (tt *TestMultiChunk) Setup(e *suite.Environment) {
	tt.Environment = e
}

func (tt *TestMultiChunk) Run(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()

	tt.prepare(ctx, t)

	t.Run("OversizedPing", func(t *testing.T) {
		tt.testOversizedPing(ctx, t)
	})
}

func (tt *TestMultiChunk) prepare(ctx context.Context, t *testing.T) {
	require.NoError(t, tt.Chain.Client.RegisterCMServices(ctx, botGenerated.PingServiceV1))

	tt.supplierPartnerPlugin = tt.CreatePartnerPlugin(ctx, t)

	tt.supplierBot = tt.CreateBot(ctx, t, true, tt.supplierPartnerPlugin,
		bot.WithServices([]bot.CMService{{Name: botGenerated.PingServiceV1}}),
	)

	tt.distributorBot = tt.CreateBot(ctx, t, true, nil)
}

// oversizedMessage returns a base64 string of random bytes sized to span
// several chunks. It must be INCOMPRESSIBLE: the encoder compresses before
// chunking, so a repetitive string would collapse back into a single chunk and
// this suite would silently stop testing anything.
//
// Four chunks is enough - it exercises the same handler, ordering and
// completion paths as the 46-chunk WAN benchmark (see §5.1 of
// docs/superpowers/specs/2026-08-17-multichunk-reassembly-fix-design.md in the
// workspace repo), without making every CI run carry a megabyte.
func oversizedMessage(t *testing.T) string {
	const chunksWanted = 4

	raw := make([]byte, chunksWanted*matrix_client.MaxChunkSize)
	_, err := rand.Read(raw)
	require.NoError(t, err)

	return base64.StdEncoding.EncodeToString(raw)
}

func (tt *TestMultiChunk) testOversizedPing(ctx context.Context, t *testing.T) {
	message := oversizedMessage(t)
	require.Greater(t, len(message), matrix_client.MaxChunkSize,
		"the payload must exceed one chunk or this suite tests nothing")

	req := &pingv1.PingRequest{
		Header:      &typesv1.RequestHeader{BaseHeader: &typesv1.Header{}},
		PingMessage: message,
		Timestamp:   timestamppb.Now(),
	}
	resp, err := tt.distributorBot.PingServiceV1.Ping(
		requestContext(ctx, tt.supplierBot.TTMAccountAddress()),
		req,
	)

	require.NoError(t, err)
	require.Equal(t, typesv1.StatusType_STATUS_TYPE_SUCCESS, resp.Header.Status, "unexpected response status")
	require.Empty(t, resp.Header.Alerts, "unexpected response alerts")

	// pp-mock echoes req.PingMessage verbatim into the response
	// (pp-mock/handlers/ping/v1/ping_handler.go), so the payload is chunked
	// in BOTH directions and this assertion covers reassembly on both sides.
	// Assert containment, not equality: the handler wraps the echo in
	// "Ping response to [%s] with request ID: %s".
	require.Contains(t, resp.PingMessage, message,
		"the round-tripped payload differs from what was sent - reassembly corrupted it")
}
