// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package encoding

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicMetadataWireFormat locks the JSON wire key of
// publicMetadata.SenderTTMAccount now that the pre-rebrand "sender_cm_account"
// freeze has been lifted (safe under the hard Camino->Base cutover; see
// Phase 7).
func TestPublicMetadataWireFormat(t *testing.T) {
	md := &publicMetadata{
		RequestID:        "request-id",
		ExpiresAt:        1234,
		SenderTTMAccount: "0x1234567890123456789012345678901234567890",
	}

	data, err := json.Marshal(md)
	require.NoError(t, err)

	require.Contains(t, string(data), `"sender_ttm_account"`)
	require.NotContains(t, string(data), `"sender_cm_account"`)
}
