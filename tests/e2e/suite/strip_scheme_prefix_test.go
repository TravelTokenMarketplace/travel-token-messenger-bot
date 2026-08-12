// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStripSchemePrefix pins the behavior of stripSchemePrefix, which
// normalizes the -existing-network-node-uri flag into the bare host:port
// form blockchain.UseExistingChain (via newClient) expects. newClient always
// prepends "ws://" itself, so a URI that still carries a scheme here would
// silently become "ws://ws://host:port" and fail to dial with a misleading
// error. This is a pure string function - no anvil, no ports, no e2e tag
// needed.
func TestStripSchemePrefix(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want string
	}{
		{name: "ws scheme stripped", uri: "ws://host:1", want: "host:1"},
		{name: "wss scheme stripped", uri: "wss://host:1", want: "host:1"},
		{name: "http scheme stripped", uri: "http://host:1", want: "host:1"},
		{name: "https scheme stripped", uri: "https://host:1", want: "host:1"},
		{name: "bare host:port passes through unchanged", uri: "host:1", want: "host:1"},
		{name: "empty string passes through unchanged", uri: "", want: ""},
		// Adversarial double scheme: the function checks each prefix once and
		// returns on the first match, it does not loop until no prefix
		// matches. "ws://ws://host:1" has its outer "ws://" stripped and the
		// inner "ws://host:1" is returned as-is - it is NOT fully unwrapped
		// to "host:1". Documented here as the actual (surprising) behavior
		// rather than the behavior one might wish for; see the fix report
		// for discussion of whether that is good enough for the values this
		// flag actually receives in practice.
		{name: "double scheme only unwraps one layer", uri: "ws://ws://host:1", want: "ws://host:1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, stripSchemePrefix(tt.uri))
		})
	}
}
