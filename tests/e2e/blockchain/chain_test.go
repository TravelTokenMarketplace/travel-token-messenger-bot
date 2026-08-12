// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build e2e

package blockchain

import (
	"context"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/resources"
)

// anvilBinPathForTest returns the anvil binary to test against. If the caller
// explicitly pointed at one via TTMB_TEST_ANVIL_BIN (as scripts/e2e.sh does),
// that path must work - it fails the test rather than skipping, so a broken
// wiring between the script and this test cannot silently no-op. When no
// binary is requested at all, it falls back to a provisioned one if present,
// and only skips when nothing is available - so a bare `go test -tags=e2e
// ./...` on a clean checkout still works.
func anvilBinPathForTest(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("TTMB_TEST_ANVIL_BIN"); p != "" {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("TTMB_TEST_ANVIL_BIN=%q is set but unusable: %v", p, err)
		}
		return p
	}
	const provisioned = "build/dependencies/foundry/anvil"
	if _, err := os.Stat("../../../" + provisioned); err == nil {
		return "../../../" + provisioned
	}
	t.Skip("anvil not available: set TTMB_TEST_ANVIL_BIN or run scripts/e2e.sh once to provision it")
	return ""
}

func TestStartChainIsReadyAndFundsKeys(t *testing.T) {
	anvilBin := anvilBinPathForTest(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	logger := zap.NewNop().Sugar()
	// A port range distinct from suite.go's 10000-50000, so this test can run
	// alongside a live e2e suite without fighting over ports.
	rm := resources.NewManager(51000, 52000, 10)
	dataDir := t.TempDir()

	chain, errChan, err := StartChain(ctx, logger, rm.NewSession(), dataDir, anvilBin)
	require.NoError(t, err)
	require.NotNil(t, chain)
	require.NotNil(t, errChan)
	t.Cleanup(func() {
		require.NoError(t, chain.Stop(context.Background()))
	})

	ethClient := chain.Client.ETHClient()

	chainID, err := ethClient.ChainID(ctx)
	require.NoError(t, err)
	require.Equal(t, evmChainID, chainID, "chain id must be the pinned e2e chain id")

	// Cancun must be active, otherwise the contracts' PUSH0 bytecode cannot run.
	header, err := ethClient.HeaderByNumber(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, header.ExcessBlobGas, "Cancun inactive: header has no excessBlobGas")

	keys := chain.Client.PrefundedKeys()
	require.Len(t, keys, prefundedKeysCount)

	for i, key := range keys {
		addr := crypto.PubkeyToAddress(key.PublicKey)
		balance, err := ethClient.BalanceAt(ctx, addr, nil)
		require.NoErrorf(t, err, "prefunded key %d", i)
		require.Zerof(t, balance.Cmp(defaultPrefund), "prefunded key %d has balance %s, want %s", i, balance, defaultPrefund)
	}

	// The deployer key pays real gas to deploy the TTMB contracts below (anvil's
	// default base fee is nonzero), so its balance is defaultPrefund minus
	// deployment gas, not an exact match. The measured deployment cost is about
	// 0.024 ETH against a 1,000,000 ETH prefund, so require the balance sits
	// within one ether of the prefund rather than merely require.Positive,
	// which would pass even if the deployer were funded with 1 wei.
	oneEther := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	minDeployerBalance := new(big.Int).Sub(defaultPrefund, oneEther)

	deployerBalance, err := ethClient.BalanceAt(ctx, crypto.PubkeyToAddress(chain.deployerKey.PublicKey), nil)
	require.NoError(t, err)
	require.Greaterf(t, deployerBalance.Cmp(minDeployerBalance), 0, "deployer balance %s must be within one ether of prefund %s (i.e. > %s)", deployerBalance, defaultPrefund, minDeployerBalance)
	require.LessOrEqualf(t, deployerBalance.Cmp(defaultPrefund), 0, "deployer balance %s must not exceed prefund %s", deployerBalance, defaultPrefund)

	// StartChain deploys the TTMB contracts, which is what the Camino chain
	// could not do: their Cancun bytecode contains PUSH0.
	require.NotNil(t, chain.Client.BookingToken, "BookingToken must be deployed")
}
