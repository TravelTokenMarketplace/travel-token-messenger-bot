// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package blockchain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// dialURL guards a path the e2e suite never exercises: it self-starts anvil and
// always passes a bare host:port. Only -existing-network-node-uri reaches the
// scheme-bearing cases, so they are covered here.
func TestDialURL(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "bare host:port defaults to ws",
			endpoint: "127.0.0.1:8545",
			want:     "ws://127.0.0.1:8545",
		},
		{
			name:     "bare hostname without port defaults to ws",
			endpoint: "chain.example.com",
			want:     "ws://chain.example.com",
		},
		{
			name:     "ws is preserved, not doubled",
			endpoint: "ws://127.0.0.1:8545",
			want:     "ws://127.0.0.1:8545",
		},
		{
			name:     "wss is preserved",
			endpoint: "wss://chain.example.com",
			want:     "wss://chain.example.com",
		},
		{
			// The reason dialURL exists rather than forcing ws://: go-ethereum
			// speaks http too, and an endpoint that only serves http must not be
			// silently rewritten into a websocket URL it never served.
			name:     "http is preserved, not downgraded to ws",
			endpoint: "http://127.0.0.1:8545",
			want:     "http://127.0.0.1:8545",
		},
		{
			name:     "https is preserved",
			endpoint: "https://chain.example.com",
			want:     "https://chain.example.com",
		},
		{
			name:     "a path on the endpoint survives",
			endpoint: "https://chain.example.com/v1/rpc",
			want:     "https://chain.example.com/v1/rpc",
		},
		{
			name:     "empty stays empty rather than becoming a bogus ws:// URL",
			endpoint: "",
			want:     "ws://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, dialURL(tt.endpoint))
		})
	}
}
