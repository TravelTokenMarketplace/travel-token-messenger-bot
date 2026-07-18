// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package matrix

import (
	"encoding/json"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestSignedMessageEventContentWireFormat locks the JSON wire key of
// SignedMessageEventContent.SenderTTMAccountAddress to the Go field name
// now that the pre-rebrand "SenderCMAccountAddress" freeze has been lifted
// (safe under the hard Camino->Base cutover; see Phase 7).
func TestSignedMessageEventContentWireFormat(t *testing.T) {
	content := &SignedMessageEventContent{
		ChunkData: ChunkData{
			MessageID: "message-id",
			Data:      []byte("data"),
		},
		ChunksCount:             1,
		Signature:               []byte("signature"),
		SenderTTMAccountAddress: common.HexToAddress("0x1234567890123456789012345678901234567890"),
	}

	data, err := json.Marshal(content)
	require.NoError(t, err)

	require.Contains(t, string(data), `"SenderTTMAccountAddress"`)
	require.NotContains(t, string(data), `"SenderCMAccountAddress"`)
}
