// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package blockchain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/process"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/resources"
)

const (
	// NetworkID is kept for camino-conduit, which is configured with it via
	// CONDUIT_CAMINO_NETWORK_ID (tests/e2e/matrix/conduit.go). It no longer
	// describes the chain the harness runs.
	NetworkID = 1005

	chainReadyTickerInterval = 500 * time.Millisecond
	prefundedKeysCount       = 10
	anvilHardfork            = "cancun"
)

var (
	evmChainID = big.NewInt(502)

	// defaultPrefund is 1e24 wei (1_000_000 ether), the same allocation the
	// Camino genesis gave every prefunded account.
	defaultPrefund = new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
)

// Chain is a single anvil process plus a client bound to it.
// Not safe for concurrent use.
type Chain struct {
	Client *Client

	logger        *zap.SugaredLogger
	pid           int
	rpcURL        string
	logFile       *os.File
	prefundedKeys []*ecdsa.PrivateKey
	deployerKey   *ecdsa.PrivateKey
}

func StartChain(
	ctx context.Context,
	logger *zap.SugaredLogger,
	resourceManagerSession *resources.Session,
	dataDir string,
	anvilBinPath string,
) (*Chain, chan error, error) {
	logger.Debug("Starting anvil chain...")

	port, err := resourceManagerSession.GetNetworkPort()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get free port: %w", err)
	}

	chainDir := path.Join(dataDir, "chain")
	if err := os.RemoveAll(chainDir); err != nil {
		return nil, nil, fmt.Errorf("failed to remove chain tmp dir: %w", err)
	}
	if err := os.MkdirAll(chainDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("failed to create chain directory: %w", err)
	}

	// Keys must be random: pkg/matrix.UserIDFromAddress derives Matrix user IDs
	// from EVM addresses, so anvil's deterministic dev accounts would collide
	// across concurrent runs.
	prefundedKeys := make([]*ecdsa.PrivateKey, prefundedKeysCount)
	for i := range prefundedKeys {
		if prefundedKeys[i], err = crypto.GenerateKey(); err != nil {
			return nil, nil, fmt.Errorf("failed to generate prefunded key: %w", err)
		}
	}

	deployerKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate deployer key: %w", err)
	}

	cmd := exec.Command(anvilBinPath, //nolint:gosec,noctx // this is the anvil binary, not some injection.
		"--port", fmt.Sprintf("%d", port),
		"--chain-id", evmChainID.String(),
		"--hardfork", anvilHardfork,
		"--accounts", "0",
	)

	logFile, err := os.OpenFile(path.Join(chainDir, "anvil.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create chain log file: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start anvil: %w", err)
	}

	c := &Chain{
		logger:        logger,
		pid:           cmd.Process.Pid,
		rpcURL:        fmt.Sprintf("127.0.0.1:%d", port),
		logFile:       logFile,
		prefundedKeys: prefundedKeys,
		deployerKey:   deployerKey,
	}

	// The reaper must exist before anything below can fail. Without it, a
	// process that dies later (e.g. during prepareTTMBContracts) is never
	// Wait()-ed on and stays a zombie: process.StopProcess's getProcess still
	// sees it via Signal(0) forever, so waitKillProcess never observes it
	// stop and spins SIGKILL in a tight loop until the caller's ctx is done
	// (which is never, for context.Background()). It is the sole sender and
	// closer of errChan.
	errChan := make(chan error)
	go func() {
		if err := <-process.ListenForProcessError(cmd); err != nil {
			errChan <- fmt.Errorf("anvil (pid %d) failed: %w", c.pid, err)
		}
		close(errChan)
	}()

	// Until c is handed to the caller - on full success, or on the
	// prepareTTMBContracts failure below where c is returned specifically so
	// the caller can call Stop - StartChain owns cleanup itself. Otherwise a
	// failure here would leak the anvil process, its log file handle, and
	// the port, since the caller only ever sees a nil *Chain to clean up.
	ownedByCaller := false
	defer func() {
		if ownedByCaller {
			return
		}
		if stopErr := c.Stop(context.Background()); stopErr != nil {
			logger.Warnf("failed to clean up anvil (pid %d) after startup failure: %v", c.pid, stopErr)
		}
	}()

	if err := c.awaitReady(ctx, errChan); err != nil {
		return nil, nil, fmt.Errorf("failed to wait for anvil (pid %d) to be ready: %w", c.pid, err)
	}

	client, err := newClient(ctx, c.rpcURL, prefundedKeys, deployerKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create chain client: %w", err)
	}
	c.Client = client

	if err := c.fundKeys(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to fund keys: %w", err)
	}

	logger.Debug("Preparing TTMB contracts...")

	if err := c.Client.prepareTTMBContracts(ctx); err != nil {
		ownedByCaller = true
		return c, nil, fmt.Errorf("failed to prepare TTMB contracts: %w", err)
	}

	ownedByCaller = true
	logger.Debugf("Anvil chain (pid %d, port %d) started", c.pid, port)

	return c, errChan, nil
}

// UseExistingChain attaches to a chain this harness did not start. The caller
// owns its lifecycle, so there is no process to stop and no funding to do.
func UseExistingChain(
	ctx context.Context,
	logger *zap.SugaredLogger,
	rpcURL string,
	deployerKey *ecdsa.PrivateKey,
) (*Chain, error) {
	logger.Info("Connecting to existing chain...")

	client, err := newClient(ctx, rpcURL, nil, deployerKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create chain client: %w", err)
	}

	c := &Chain{
		logger:      logger,
		rpcURL:      rpcURL,
		deployerKey: deployerKey,
		Client:      client,
	}

	logger.Info("Preparing TTMB contracts...")

	if err := c.Client.prepareTTMBContracts(ctx); err != nil {
		return c, fmt.Errorf("failed to prepare TTMB contracts: %w", err)
	}

	logger.Info("Existing chain ready")

	return c, nil
}

// awaitReady polls until the JSON-RPC endpoint answers eth_blockNumber. It
// also watches errChan so that a process which exited immediately (a bad
// flag, or a port resources.Session handed out without checking it was
// actually free - see resources.Manager.getNetworkPort) is reported directly
// instead of only ever timing out against ctx with the real cause sitting
// unread in anvil.log.
func (c *Chain) awaitReady(ctx context.Context, errChan <-chan error) error {
	ticker := time.NewTicker(chainReadyTickerInterval)
	defer ticker.Stop()

	for {
		ethClient, err := ethclient.DialContext(ctx, "ws://"+c.rpcURL)
		if err == nil {
			_, err = ethClient.BlockNumber(ctx)
			ethClient.Close()
			if err == nil {
				return nil
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return ctx.Err()
		case procErr, ok := <-errChan:
			if !ok {
				return errors.New("anvil process exited before becoming ready")
			}
			return fmt.Errorf("anvil process exited before becoming ready: %w", procErr)
		}
	}
}

// fundKeys gives every prefunded key and the deployer key a starting balance.
// anvil cannot prefund arbitrary addresses from the command line, so this is
// done over the anvil_setBalance RPC once the node is up.
func (c *Chain) fundKeys(ctx context.Context) error {
	rpcClient := c.Client.ETHClient().Client()

	keys := append([]*ecdsa.PrivateKey{c.deployerKey}, c.prefundedKeys...)
	for _, key := range keys {
		addr := crypto.PubkeyToAddress(key.PublicKey)
		if err := rpcClient.CallContext(ctx, nil, "anvil_setBalance", addr, hexutil.EncodeBig(defaultPrefund)); err != nil {
			return fmt.Errorf("failed to set balance for %s: %w", addr, err)
		}
	}

	return nil
}

func (c *Chain) Stop(ctx context.Context) error {
	if c == nil || c.pid == 0 {
		return nil
	}

	if err := process.StopProcess(ctx, c.pid); err != nil {
		return fmt.Errorf("failed to stop anvil process with pid %d: %w", c.pid, err)
	}
	c.logger.Debugf("Anvil chain (pid %d) stopped", c.pid)

	if c.logFile != nil {
		if err := c.logFile.Close(); err != nil {
			return fmt.Errorf("failed to close chain logFile: %w", err)
		}
	}

	return nil
}
