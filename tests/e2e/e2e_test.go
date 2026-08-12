// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

//go:build e2e

package e2e

import (
	"crypto/ecdsa"
	"flag"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/runner"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/suite"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/tests"
)

const (
	flagKeyAnvilBinPath         = "anvil"
	flagKeyMatrixBinPath        = "matrix"
	flagKeyASBBinPath           = "asb"
	flagKeyPartnerPluginBinPath = "partner-plugin"
	flagKeyTTMBBinPath          = "ttmb"
	flagKeyDebug                = "debug"
	flagKeyFilter               = "filter"
)

var (
	flagAnvilBinPath            string
	flagMatrixBinPath           string
	flagASBBinPath              string
	flagPartnerPluginBinPath    string
	flagTTMBBinPath             string
	flagTestsDataDir            string
	flagExistingNetworkNodeURI  string
	flagExistingNetworkAdminKey string
	flagFilter                  string
	flagDebug                   bool
)

func init() {
	flag.StringVar(&flagAnvilBinPath, flagKeyAnvilBinPath, "", "Path to anvil binary.")
	flag.StringVar(&flagExistingNetworkNodeURI, "existing-network-node-uri", "", "URI of existing network node.")
	flag.StringVar(&flagExistingNetworkAdminKey, "existing-network-admin-key", "", "Admin key of existing network.")
	flag.StringVar(&flagMatrixBinPath, flagKeyMatrixBinPath, "", "Path to matrix binary.")
	flag.StringVar(&flagASBBinPath, flagKeyASBBinPath, "", "Path to ASB binary.")
	flag.StringVar(&flagPartnerPluginBinPath, flagKeyPartnerPluginBinPath, "", "Path to partner plugin binary.")
	flag.StringVar(&flagTTMBBinPath, flagKeyTTMBBinPath, "", "Path to TTMB binary.")
	flag.StringVar(&flagTestsDataDir, "tests-data-dir", "/tmp/ttm-e2e", "Path to dir with temp tests data.")
	flag.StringVar(&flagFilter, flagKeyFilter, "", "Filter for (comma separated) top level test names e.g. PingV1,AccommodationV3.")
	flag.BoolVar(&flagDebug, flagKeyDebug, false, "Debug mode")
}

func TestE2E(t *testing.T) {
	flag.Parse()
	var err error

	require.NotEmptyf(t, flagAnvilBinPath, "flag -%s (anvil binary path) is required", flagKeyAnvilBinPath)
	require.NotEmpty(t, flagMatrixBinPath, "flag -%s (matrix binary path) is required", flagKeyMatrixBinPath)
	require.NotEmpty(t, flagASBBinPath, "flag -%s (ASB binary path) is required", flagKeyASBBinPath)
	require.NotEmpty(t, flagTTMBBinPath, "flag -%s (TTMB binary path) is required", flagKeyTTMBBinPath)
	require.NotEmpty(t, flagPartnerPluginBinPath, "flag -%s (partner plugin binary path) is required", flagKeyPartnerPluginBinPath)

	flagAnvilBinPath, err = filepath.Abs(flagAnvilBinPath)
	require.NoError(t, err)
	flagMatrixBinPath, err = filepath.Abs(flagMatrixBinPath)
	require.NoError(t, err)
	flagASBBinPath, err = filepath.Abs(flagASBBinPath)
	require.NoError(t, err)
	flagTTMBBinPath, err = filepath.Abs(flagTTMBBinPath)
	require.NoError(t, err)
	flagPartnerPluginBinPath, err = filepath.Abs(flagPartnerPluginBinPath)
	require.NoError(t, err)

	checkFileExist(t, flagAnvilBinPath)
	checkFileExist(t, flagMatrixBinPath)
	checkFileExist(t, flagASBBinPath)
	checkFileExist(t, flagTTMBBinPath)
	checkFileExist(t, flagPartnerPluginBinPath)

	flagTestsDataDir = path.Join(flagTestsDataDir, time.Now().Format("2006-01-02_15-04-05"))

	os.RemoveAll(flagTestsDataDir)
	require.NoError(t, os.MkdirAll(flagTestsDataDir, 0o755))

	var existingNetworkDeployerKey *ecdsa.PrivateKey
	if len(flagExistingNetworkNodeURI) > 0 {
		// The deployer key is not optional for an existing chain: it signs every
		// setup transaction (CreateTTMAccount, RegisterCMService, ...). Without it
		// the run starts and then dies at the first transaction, far from the cause.
		require.NotEmptyf(t, flagExistingNetworkAdminKey,
			"flag -existing-network-admin-key is required when -%s is set", "existing-network-node-uri")
	}
	if len(flagExistingNetworkAdminKey) > 0 {
		var err error
		existingNetworkDeployerKey, err = crypto.HexToECDSA(strings.TrimPrefix(flagExistingNetworkAdminKey, "0x"))
		require.NoError(t, err, "failed to parse existing network deployer key")
	}

	suite, err := suite.New(
		flagAnvilBinPath,
		flagMatrixBinPath,
		flagASBBinPath,
		flagPartnerPluginBinPath,
		flagTTMBBinPath,
		flagTestsDataDir,
		flagExistingNetworkNodeURI,
		existingNetworkDeployerKey,
		flagDebug,
		flagFilter,
	)
	require.NoError(t, err)

	testsRunner := runner.New(
		suite.SetupEnvironment,
		suite.Cleanup,
	)

	for name, test := range tests.Tests {
		if len(suite.TestFilter) > 0 && !slices.Contains(suite.TestFilter, name) {
			continue
		}
		testsRunner.Register(t, name, test)
	}

	maxParallelRuns := 0
	flagTestParallel := flag.Lookup("test.parallel")
	if flagTestParallel != nil {
		maxParallelRuns, err = strconv.Atoi(flagTestParallel.Value.String())
		require.NoError(t, err)
	}
	if maxParallelRuns > 1 {
		testsRunner.RunParallel(t)
	} else {
		testsRunner.Run(t)
	}
}

func checkFileExist(t *testing.T, path string) {
	_, err := os.Stat(path)
	require.NoErrorf(t, err, "file %s does not exist", path)
}
