// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package suite

import (
	"context"
	"crypto/ecdsa"
	"path"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/blockchain"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/bot"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/matrix"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/resources"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/runner"
)

const (
	startupTimeout  = 120 * time.Second
	shutdownTimeout = 30 * time.Second
)

type Test = runner.Test[*Environment]

var (
	_ runner.BeforeRunFunc[*Environment] = (*Suite)(nil).SetupEnvironment
	_ runner.AfterRunFunc[*Environment]  = (*Suite)(nil).Cleanup
)

func New(
	anvilBinPath string,
	matrixBinPath string,
	asbBinPath string,
	partnerPluginBinPath string,
	ttmbBinPath string,
	testsDataDir string,
	existingNetworkNodeURI string,
	existingNetworkDeployerKey *ecdsa.PrivateKey,
	debug bool,
	filter string,
) (*Suite, error) {
	zapConfig := zap.NewDevelopmentConfig()
	zapConfig.Level.SetLevel(zap.InfoLevel)
	if debug {
		zapConfig.Level.SetLevel(zap.DebugLevel)
	}
	logger, err := zapConfig.Build()
	if err != nil {
		return nil, err
	}
	sugaredLogger := logger.Sugar()
	var testFilterElements []string
	if len(filter) > 0 {
		testFilterElements = strings.Split(filter, ",")
		if len(testFilterElements) > 0 {
			sugaredLogger.Debugf("Running only tests with the following names: %v", testFilterElements)
		}
	}

	return &Suite{
		logger:                     sugaredLogger,
		resourcesManager:           resources.NewManager(10000, 50000, 10),
		anvilBinPath:               anvilBinPath,
		matrixBinPath:              matrixBinPath,
		asbBinPath:                 asbBinPath,
		partnerPluginBinPath:       partnerPluginBinPath,
		ttmbBinPath:                ttmbBinPath,
		testsDataDir:               testsDataDir,
		existingNetworkNodeURI:     stripSchemePrefix(existingNetworkNodeURI),
		existingNetworkDeployerKey: existingNetworkDeployerKey,
		TestFilter:                 testFilterElements,
	}, nil
}

// stripSchemePrefix normalizes the -existing-network-node-uri flag into the
// bare host:port form blockchain.UseExistingChain expects. The flag is
// documented as "URI of existing network node", so a caller may reasonably
// pass a scheme (ws://, wss://, http://, https://); UseExistingChain's
// underlying client always prepends "ws://" itself, so leaving a scheme on
// would produce "ws://ws://host:port" and a baffling dial failure.
func stripSchemePrefix(uri string) string {
	for _, prefix := range []string{"wss://", "ws://", "https://", "http://"} {
		if trimmed, ok := strings.CutPrefix(uri, prefix); ok {
			return trimmed
		}
	}
	return uri
}

// Safe for concurrent use.
type Suite struct {
	logger                     *zap.SugaredLogger
	anvilBinPath               string
	matrixBinPath              string
	asbBinPath                 string
	partnerPluginBinPath       string
	ttmbBinPath                string
	testsDataDir               string
	existingNetworkNodeURI     string
	existingNetworkDeployerKey *ecdsa.PrivateKey
	resourcesManager           *resources.Manager
	TestFilter                 []string
}

func (s *Suite) SetupEnvironment(t *testing.T, test Test) *Environment {
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	e := &Environment{
		resourceManagerSession: s.resourcesManager.NewSession(),
		Logger:                 s.logger,
	}
	test.Setup(e)

	dataDir := path.Join(s.testsDataDir, t.Name())

	var err error
	var errChan chan error
	e.networkFeeKey, err = crypto.GenerateKey()
	require.NoError(t, err)

	if len(s.existingNetworkNodeURI) > 0 {
		e.Chain, err = blockchain.UseExistingChain(
			ctx,
			s.logger,
			s.existingNetworkNodeURI,
			s.existingNetworkDeployerKey,
		)
		require.NoError(t, err)
	} else {
		e.Chain, errChan, err = blockchain.StartChain(
			ctx,
			s.logger,
			e.resourceManagerSession,
			dataDir,
			s.anvilBinPath,
		)
		require.NoError(t, err)
		common.ExpectNoErrorAsync(t, errChan)
	}

	e.ASB, errChan, err = matrix.StartNewAppService(
		ctx,
		s.logger,
		e.resourceManagerSession,
		dataDir,
		s.asbBinPath,
		e.networkFeeKey,
		e.Chain.Client,
		e.ASBOptions...,
	)
	require.NoError(t, err)
	common.ExpectNoErrorAsync(t, errChan)

	e.matrix, errChan, err = matrix.StartNewMatrixServer(
		ctx,
		s.logger,
		e.resourceManagerSession,
		dataDir,
		s.matrixBinPath,
		e.ASB,
	)
	require.NoError(t, err)
	common.ExpectNoErrorAsync(t, errChan)

	e.partnerPluginFactory = partnerplugin.NewFactory(
		s.logger,
		e.resourceManagerSession,
		dataDir,
		s.partnerPluginBinPath,
	)

	e.botFactory = bot.NewFactory(
		s.logger,
		e.resourceManagerSession,
		dataDir,
		s.ttmbBinPath,
		e.Chain.Client,
		e.matrix,
		e.ASB,
	)

	return e
}

func (s *Suite) Cleanup(t *testing.T, e *Environment) {
	if e == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var wg sync.WaitGroup

	// Components stopped without respecting the order, so they might stop with errors.
	// That will be most likely reflected in their logs, but we don't care about that at this point.
	// E.g. if network is stopped before bots, while bots have active event subscriptions,
	// bots might log errors about failed subscriptions.

	e.Logger.Debug("Stopping all services")
	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, e.botFactory.StopBots(ctx))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, e.partnerPluginFactory.StopPartnerPlugins(ctx))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, e.ASB.Stop(ctx))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, e.matrix.Stop(ctx))
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, e.Chain.Stop(ctx))
	}()

	wg.Wait()
	e.Logger.Debug("All services stopped")

	e.resourceManagerSession.ReleaseResources()
}
