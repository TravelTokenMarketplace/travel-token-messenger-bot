// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package bot

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strconv"
	"sync"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/config"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pkg/conversion"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/blockchain"
	e2eCommon "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/matrix"
	partnerplugin "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/partner_plugin"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/resources"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"go.uber.org/zap"
)

const CashInPeriodSeconds = 3600 // 1h

func NewFactory(
	logger *zap.SugaredLogger,
	resourceManagerSession *resources.Session,
	e2eTmpDir string,
	binPath string,
	networkClient *blockchain.Client,
	matrix *matrix.ConduitServer,
	asb *matrix.AppService,
) *Factory {
	return &Factory{
		logger:                 logger,
		resourceManagerSession: resourceManagerSession,
		dir:                    path.Join(e2eTmpDir, "ttmb"),
		binPath:                binPath,
		networkClient:          networkClient,
		matrix:                 matrix,
		asb:                    asb,
	}
}

type Factory struct {
	logger                 *zap.SugaredLogger
	resourceManagerSession *resources.Session
	dir                    string
	binPath                string
	networkClient          *blockchain.Client
	matrix                 *matrix.ConduitServer
	asb                    *matrix.AppService
	mutex                  sync.Mutex
	bots                   []*Bot
}

type options struct {
	skips               *Skip
	cashInPeriodSeconds int64
	services            []CMService
}

type Option func(*options)

func WithSkips(skips *Skip) Option {
	return func(o *options) { o.skips = skips }
}

// Intentionally skip some steps in bot creation.
// Only used for very specific testing purposes.
type Skip struct {
	// Skips the creation of the cm-account when setting up the bot.
	TTMAccountCreation bool
	// Skips the transfer of funds to the cm-account owner.
	// Requires TTMAccountCreation to be false.
	PrefundOwner bool
	// Skips the transfer of funds to the bot.
	// Requires TTMAccountCreation to be false.
	PrefundBot bool
	// Skips the registration of the bot in the cm-account.
	// Requires TTMAccountCreation and PrefundOwner to be false.
	BotRegistration bool
	// Skips the registration of services in the cm-account.
	// Requires TTMAccountCreation and PrefundOwner to be false.
	ServiceRegistration bool
}

func WithCashInPeriod(cashInPeriodSeconds int64) Option {
	return func(o *options) { o.cashInPeriodSeconds = cashInPeriodSeconds }
}

func WithServices(services []CMService) Option {
	return func(o *options) { o.services = services }
}

type CMService struct {
	Name string
}

func (f *Factory) CreateBot(
	ctx context.Context,
	enableRPCServer bool,
	partnerPlugin *partnerplugin.PartnerPlugin,
	opts ...Option,
) (*Bot, error) {
	options := &options{
		skips:               &Skip{},
		cashInPeriodSeconds: CashInPeriodSeconds, // 1h
	}
	for _, opt := range opts {
		opt(options)
	}

	ttmAccountOwnerKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	botKey, err := ecdsa.GenerateKey(crypto.S256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate key: %w", err)
	}

	var ttmAccountAddress common.Address
	if !options.skips.TTMAccountCreation {
		botAddr := crypto.PubkeyToAddress(botKey.PublicKey)
		ttmAccountOwnerAddress := crypto.PubkeyToAddress(ttmAccountOwnerKey.PublicKey)
		ttmAccountAddress, _, err = f.networkClient.CreateTTMAccount(ctx, ttmAccountOwnerKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create TTM account: %w", err)
		}

		if !options.skips.PrefundOwner {
			if err := f.networkClient.Transfer(ctx, f.networkClient.PrefundedKeys()[0], ttmAccountOwnerAddress, e2eCommon.DefaultTTMAccountOwnerFunds); err != nil {
				return nil, fmt.Errorf("failed to transfer funds to ttm account owner: %w", err)
			}

			if !options.skips.BotRegistration {
				if err := f.networkClient.AddBotToTTMAccount(ctx, ttmAccountAddress, ttmAccountOwnerKey, botAddr); err != nil {
					return nil, fmt.Errorf("failed to add bot to TTM account: %w", err)
				}
			}

			if !options.skips.ServiceRegistration {
				for _, service := range options.services {
					if err := f.networkClient.AddCMService(ctx, ttmAccountAddress, ttmAccountOwnerKey, service.Name); err != nil {
						return nil, fmt.Errorf("failed to add %s service to TTM account: %w", service.Name, err)
					}
				}
			}
		}

		if !options.skips.PrefundBot {
			if err := f.networkClient.Transfer(ctx, f.networkClient.PrefundedKeys()[0], botAddr, e2eCommon.DefaultTTMAccountOwnerFunds); err != nil {
				return nil, fmt.Errorf("failed to transfer funds to bot: %w", err)
			}
		}
	}

	// Prepare bot config

	port := int32(0)
	if enableRPCServer {
		port, err = f.resourceManagerSession.GetNetworkPort()
		if err != nil {
			return nil, fmt.Errorf("failed to get free port: %w", err)
		}
	}

	f.mutex.Lock()
	defer f.mutex.Unlock()

	botDir := path.Join(f.dir, strconv.Itoa(len(f.bots)))

	config := &config.UnparsedConfig{
		DeveloperMode:       true,
		E2ETestMode:         true,
		BotKey:              hex.EncodeToString(crypto.FromECDSA(botKey)),
		TTMAccountAddress:   ttmAccountAddress.Hex(),
		ChainRPCURL:         f.networkClient.ChainRPCURL(),
		BookingTokenAddress: f.networkClient.BookingTokenContractAddress().Hex(),
		ResponseTimeout:     30000, // 30s
		// Mirror the flag defaults: WriteYAMLConfig serializes every mapstructure
		// field, so leaving these unset would emit token_visible_max_attempts: 0,
		// which parseConfig rejects (must be >= 1). The local single-node network
		// serves the reserved token immediately, so the delay never fires.
		TokenVisibleMaxAttempts: 16,
		TokenVisibleRetryDelay:  1000, // ms
		PartnerPlugin: config.PartnerPluginConfig{
			Enabled:     partnerPlugin != nil,
			Host:        partnerPlugin.RPCClientConnectionString(),
			Unencrypted: true,
		},
		RPCServer: config.RPCServerConfig{
			Enabled:     enableRPCServer,
			Port:        conversion.MustInt32ToUInt64(port),
			Unencrypted: true,
		},
		Matrix: config.UnparsedMatrixConfig{Host: f.matrix.Host().String()},
		DB: config.UnparsedSQLiteDBConfig{
			DBPath: path.Join(botDir, "db"),
		},
	}

	if err := os.RemoveAll(botDir); err != nil {
		return nil, fmt.Errorf("failed to remove bot data dir: %w", err)
	}

	if err := os.MkdirAll(botDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create bot data dir: %w", err)
	}

	configPath := path.Join(botDir, "config.yaml")
	if err := e2eCommon.WriteYAMLConfig(config, configPath); err != nil {
		return nil, fmt.Errorf("failed to write bot config file: %w", err)
	}

	rpcClientConnectionString := ""
	if config.RPCServer.Enabled {
		rpcClientConnectionString = fmt.Sprintf("localhost:%d", config.RPCServer.Port) // rpc client connection string
	}

	bot := newBot(
		f.logger,
		ttmAccountAddress,
		f.binPath,
		configPath,
		path.Join(botDir, "bot.log"), // log file path
		rpcClientConnectionString,
	)
	f.bots = append(f.bots, bot)

	return bot, nil
}

func (f *Factory) StopBots(ctx context.Context) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	var errs []error
	errsMx := sync.Mutex{}
	wg := sync.WaitGroup{}

	for _, bot := range f.bots {
		wg.Add(1)
		go func(bot *Bot) {
			defer wg.Done()
			if err := bot.Stop(ctx); err != nil {
				errsMx.Lock()
				errs = append(errs, fmt.Errorf("failed to stop bot (%d): %w", bot.pid, err))
				errsMx.Unlock()
			}
		}(bot)
	}

	wg.Wait()
	return errors.Join(errs...)
}
