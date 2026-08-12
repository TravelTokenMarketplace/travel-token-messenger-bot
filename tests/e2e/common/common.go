// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package common //nolint:revive

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	MinBuyableUntilInContract = 1 // seconds -- overrides the bookingtoken default of 1 minute when deployed for the e2e test
	PingMessage               = "ping"
)

var (
	// Ether is 1e18 wei. It replaces the old CAM constant, which evaluated to
	// exactly the same value (units.Avax 1e9 * X2CRateBig 1e9).
	Ether                       = new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	DefaultTTMAccountOwnerFunds = big.NewInt(0).Mul(Ether, big.NewInt(100))
)

func AwaitError(t *testing.T, errChan chan error, errContent string, timeout time.Duration) {
	t.Helper()

	select {
	case err := <-errChan:
		require.Error(t, err)
		require.Contains(t, err.Error(), errContent)
	case <-time.After(timeout):
		require.Fail(t, "timeout waiting for channel error with content: "+errContent)
	}
}

func ExpectNoErrorAsync(t *testing.T, errChan chan error) {
	t.Helper()
	go func() {
		require.NoError(t, <-errChan)
	}()
}
