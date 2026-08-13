// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package matrix

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/conversion"
	e2eCommon "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/process"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/resources"

	"github.com/TravelTokenMarketplace/travel-token-matrix-app-service/config"
	"go.uber.org/zap"
)

// TODO@ asb config examples and readme in its repo

const (
	hsAccessToken            = "ugw8243igya57aaABGFfgeyu" //nolint:gosec // this is not real credentials
	asAccessToken            = "wfghWEGh3wgWHEf3478sHFWE" //nolint:gosec // this is not real credentials
	asbRequestTickerInterval = 500 * time.Millisecond
	asbPingTimeout           = 5 * time.Second
)

func StartNewAppService(
	ctx context.Context,
	logger *zap.SugaredLogger,
	resourceManagerSession *resources.Session,
	dataDir string,
	asbBinPath string,
) (*AppService, chan error, error) {
	logger.Debug("Starting matrix app-service...")

	asbDir := path.Join(dataDir, "asb")
	if err := os.RemoveAll(asbDir); err != nil {
		return nil, nil, fmt.Errorf("failed to remove asb tmp dir: %w", err)
	}

	port, err := resourceManagerSession.GetNetworkPort()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get free port: %w", err)
	}

	dbDir := path.Join(asbDir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("failed to create asb db dir: %w", err)
	}

	// The app-service is chain-agnostic since its fee/cheque removal: it verifies
	// event content and tracks message chunks, and holds no key, no RPC endpoint
	// and no on-chain account. Its whole config is these three sections.
	config := &config.UnparsedConfig{
		LogLevel: "debug",
		Matrix: config.MatrixConfig{
			HTTPPort:    conversion.MustInt32ToUInt64(port),
			AccessToken: hsAccessToken,
		},
		DB: config.UnparsedSQLiteDBConfig{
			DBPath: dbDir,
		},
	}

	configPath := path.Join(asbDir, "config.yaml")
	if err := e2eCommon.WriteYAMLConfig(config, configPath); err != nil {
		return nil, nil, fmt.Errorf("failed to write bot config file: %w", err)
	}

	cmd := exec.Command(asbBinPath, "--config", configPath) //nolint:noctx

	host := fmt.Sprintf("http://localhost:%d", port)

	logFile, err := os.OpenFile(asbDir+"/asb.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create asb log file: %w", err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start asb (%d): %w", cmd.Process.Pid, err)
	}

	m := &AppService{
		logger:        logger,
		pid:           cmd.Process.Pid,
		asbDir:        asbDir,
		logFile:       logFile,
		hsAccessToken: hsAccessToken,
		asAccessToken: asAccessToken,
		host:          host,
	}

	if err := m.awaitReady(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to wait for asb (%d) to be ready: %w", cmd.Process.Pid, err)
	}

	logger.Debugf("ASB (pid %d) started", cmd.Process.Pid)

	errChan := make(chan error)
	go func() {
		err := <-process.ListenForProcessError(cmd)
		if err != nil {
			errChan <- fmt.Errorf("asb (pid %d) failed: %w", m.pid, err)
		}
		close(errChan)
	}()

	return m, errChan, nil
}

// Not safe for concurrent use.
type AppService struct {
	logger        *zap.SugaredLogger
	pid           int
	asbDir        string
	host          string
	hsAccessToken string
	asAccessToken string
	logFile       *os.File
}

func (a *AppService) Host() string {
	return a.host
}

func (a *AppService) HSAccessToken() string {
	return a.hsAccessToken
}

func (a *AppService) ASAccessToken() string {
	return a.asAccessToken
}

func (a *AppService) Stop(ctx context.Context) error {
	if a == nil {
		return nil
	}

	if err := process.StopProcess(ctx, a.pid); err != nil {
		return fmt.Errorf("failed to stop asb process with pid %d: %w", a.pid, err)
	}
	if err := a.logFile.Close(); err != nil {
		return fmt.Errorf("failed to close asb logFile: %w", err)
	}
	a.logger.Debugf("ASB (pid %d) stopped", a.pid)
	return nil
}

func (a *AppService) awaitReady(ctx context.Context) error {
	ticker := time.NewTicker(asbRequestTickerInterval)
	defer ticker.Stop()

	url := fmt.Sprintf("%s/_matrix/app/v1/ping", a.Host())
	client := &http.Client{Timeout: asbPingTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		a.logger.Debugf("ASB (pid %d) awaitReady: cannot build ping request: %v", a.pid, err)
		return err
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", a.hsAccessToken))

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			resp, err := client.Do(req)
			if err != nil {
				a.logger.Debugf("ASB (pid %d) awaitReady: ping failed: %v", a.pid, err)
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}

			a.logger.Debugf("ASB (pid %d) awaitReady: unexpected status %d", a.pid, resp.StatusCode)
		}
	}
}
