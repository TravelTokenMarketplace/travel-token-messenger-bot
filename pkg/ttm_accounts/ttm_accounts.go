// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package ttmaccounts

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccount"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccountmanager"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
)

const (
	// Implementation slot for ERC1967Proxy
	// See: https://eips.ethereum.org/EIPS/eip-1967#logic-contract-address
	managerTTMAccountImplementationSlotString = "0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
	evmExecutionRevertErrorMessage            = "execution reverted"
)

var (
	_ Service = &service{}

	messengerBotRole                    = crypto.Keccak256Hash([]byte("MESSENGER_BOT_ROLE"))
	managerTTMAccountImplementationSlot = common.HexToHash(managerTTMAccountImplementationSlotString)

	ErrServiceNotSupported = errors.New("service is not supported")
)

type Service interface {
	GetAllMessengerBots(ctx context.Context, ttmAccountAddress common.Address) ([]common.Address, error)
	IsBotAllowed(ctx context.Context, ttmAccountAddress common.Address, botAddress common.Address) (bool, error)
	IsServiceSupported(ctx context.Context, ttmAccountAddress common.Address, serviceFullName string) (bool, error)

	MintBookingToken(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		ttmAccountAddress common.Address,
		reservedFor common.Address,
		uri string,
		expirationTimestamp *big.Int,
		price *big.Int,
		paymentToken common.Address,
		isoCurrency *big.Int,
		isCancellable bool,
	) (*types.Receipt, error)

	BuyBookingToken(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		ttmAccountAddr common.Address,
		tokenID *big.Int,
		price *big.Int,
		paymentToken common.Address,
	) (*types.Receipt, error)

	RecordExpiration(
		ctx context.Context,
		transactOpts *bind.TransactOpts,
		ttmAccountAddress common.Address,
		tokenID *big.Int,
	) (*types.Receipt, error)

	IsTTMAccountImplementationUpToDate(ctx context.Context, ttmAccountAddress common.Address) (bool, error)

	TTMAccount(common.Address) (*ttmaccount.Ttmaccount, error)

	Cancellation
}

type botAuthCacheKey struct {
	ttmAccount common.Address
	bot        common.Address
}

type botAuthCacheVal struct {
	allowed   bool
	expiresAt time.Time
}

type service struct {
	ethClient *ethclient.Client
	cache     *lru.Cache[common.Address, *ttmaccount.Ttmaccount]
	logger    *zap.SugaredLogger
	chainID   *big.Int

	botAuthCacheTimeout time.Duration
	botAuthCacheMu      sync.RWMutex
	botAuthCache        map[botAuthCacheKey]botAuthCacheVal
}

func NewService(
	ctx context.Context,
	logger *zap.SugaredLogger,
	cacheSize int,
	ethClient *ethclient.Client,
	botAuthCacheTimeout time.Duration,
) (Service, error) {
	chainID, err := ethClient.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	cache, err := lru.New[common.Address, *ttmaccount.Ttmaccount](cacheSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create ttm account cache: %w", err)
	}

	return &service{
		ethClient:           ethClient,
		cache:               cache,
		logger:              logger,
		chainID:             chainID,
		botAuthCacheTimeout: botAuthCacheTimeout,
		botAuthCache:        make(map[botAuthCacheKey]botAuthCacheVal),
	}, nil
}

func (s *service) GetAllMessengerBots(ctx context.Context, ttmAccountAddress common.Address) ([]common.Address, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get ttm account: %v", err)
		return nil, err
	}

	botsAddresses, err := ttmAccount.GetRoleMembers(&bind.CallOpts{Context: ctx}, messengerBotRole)
	if err != nil {
		s.logger.Errorf("Failed to get messenger bots: %v", err)
		return nil, err
	}

	return botsAddresses, nil
}

func (s *service) IsBotAllowed(ctx context.Context, ttmAccountAddress common.Address, botAddress common.Address) (bool, error) {
	key := botAuthCacheKey{ttmAccount: ttmAccountAddress, bot: botAddress}

	s.botAuthCacheMu.RLock()
	cached, found := s.botAuthCache[key]
	s.botAuthCacheMu.RUnlock()

	if found && time.Now().Before(cached.expiresAt) {
		return cached.allowed, nil
	}

	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get ttm account: %v", err)
		return false, err
	}

	allowed, err := ttmAccount.IsBotAllowed(&bind.CallOpts{Context: ctx}, botAddress)
	if err != nil {
		s.logger.Errorf("Failed to check if bot %s is allowed for TTM account %s: %v", botAddress.Hex(), ttmAccountAddress.Hex(), err)
		return false, fmt.Errorf("failed to check if bot is allowed: %w", err)
	}

	if s.botAuthCacheTimeout > 0 {
		s.botAuthCacheMu.Lock()
		s.botAuthCache[key] = botAuthCacheVal{
			allowed:   allowed,
			expiresAt: time.Now().Add(s.botAuthCacheTimeout),
		}
		s.botAuthCacheMu.Unlock()
	}

	return allowed, nil
}

func (s *service) IsServiceSupported(ctx context.Context, ttmAccountAddress common.Address, serviceFullName string) (bool, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		s.logger.Errorf("Failed to get ttm account: %v", err)
		return false, err
	}

	return ttmAccount.IsServiceSupported(&bind.CallOpts{Context: ctx}, serviceFullName)
}

func (s *service) MintBookingToken(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	ttmAccountAddress common.Address,
	reservedFor common.Address,
	uri string,
	expirationTimestamp *big.Int,
	price *big.Int,
	paymentToken common.Address,
	offChainPaymentCurrency *big.Int,
	isCancellable bool,
) (*types.Receipt, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := ttmAccount.MintBookingToken(
		transactOpts,
		reservedFor,
		uri,
		expirationTimestamp,
		price,
		paymentToken,
		offChainPaymentCurrency,
		isCancellable,
	)
	if err != nil {
		return nil, wrapTxErr("failed to mint booking token", err)
	}

	s.logger.Debugf("Waiting for MintBookingToken transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for MintBookingToken transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("MintBookingToken transaction failed: %v", receipt)
	}

	s.logger.Infof("MintBookingToken transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) BuyBookingToken(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	ttmAccountAddress common.Address,
	tokenID *big.Int,
	price *big.Int,
	paymentToken common.Address,
) (*types.Receipt, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := ttmAccount.BuyBookingToken(
		transactOpts,
		tokenID,
		price,
		paymentToken,
	)
	if err != nil {
		return nil, wrapTxErr("failed to buy booking token", err)
	}

	s.logger.Debugf("Waiting for BuyBookingToken transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for BuyBookingToken transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("BuyBookingToken transaction failed: %v", receipt)
	}

	s.logger.Infof("BuyBookingToken transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) TTMAccount(ttmAccountAddr common.Address) (*ttmaccount.Ttmaccount, error) {
	ttmAccount, ok := s.cache.Get(ttmAccountAddr)
	if ok {
		return ttmAccount, nil
	}

	ttmAccount, err := ttmaccount.NewTtmaccount(ttmAccountAddr, s.ethClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create ttm account contract binding: %w", err)
	}
	s.cache.Add(ttmAccountAddr, ttmAccount)

	return ttmAccount, nil
}

func (s *service) RecordExpiration(
	ctx context.Context,
	transactOpts *bind.TransactOpts,
	ttmAccountAddress common.Address,
	tokenID *big.Int,
) (*types.Receipt, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		return nil, err
	}

	tx, err := ttmAccount.RecordExpiration(transactOpts, tokenID)
	if err != nil {
		return nil, wrapTxErr("failed to record expiration", err)
	}

	s.logger.Debugf("Waiting for RecordExpiration transaction to be mined...")

	receipt, err := bind.WaitMined(ctx, s.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for RecordExpiration transaction to be mined: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction failed: %v", receipt)
	}

	s.logger.Infof("RecordExpiration transaction is mined. Block Nr: %s Gas used: %d", receipt.BlockNumber, receipt.GasUsed)

	return receipt, nil
}

func (s *service) getLatestTTMAccountImplementation(ctx context.Context, ttmAccountAddress common.Address) (common.Address, error) {
	ttmAccount, err := s.TTMAccount(ttmAccountAddress)
	if err != nil {
		return common.Address{}, err
	}
	managerAddress, err := ttmAccount.GetManagerAddress(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to fetch TTM account manager address: %w", err)
	}
	manager, err := ttmaccountmanager.NewTtmaccountmanager(managerAddress, s.ethClient)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to create TTM account manager contract binding: %w", err)
	}
	currentImplOnManager, err := manager.GetAccountImplementation(&bind.CallOpts{Context: ctx})
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get account implementation: %w", err)
	}
	return currentImplOnManager, nil
}

func (s *service) getCurrentImplementationOnProxy(ctx context.Context, ttmAccountAddress common.Address) (common.Address, error) {
	implAddressSlotValue, err := s.ethClient.StorageAt(ctx, ttmAccountAddress, managerTTMAccountImplementationSlot, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("failed to get implementation address from proxy: %w", err)
	}
	return slotValueToAddress(implAddressSlotValue)
}

func (s *service) IsTTMAccountImplementationUpToDate(ctx context.Context, ttmAccountAddress common.Address) (bool, error) {
	currentImplOnManager, err := s.getLatestTTMAccountImplementation(ctx, ttmAccountAddress)
	if err != nil {
		return false, fmt.Errorf("failed to get latest ttm account implementation from ttm account manager: %w", err)
	}

	currentImplOnProxy, err := s.getCurrentImplementationOnProxy(ctx, ttmAccountAddress)
	if err != nil {
		return false, fmt.Errorf("failed to get current ttm account implementation implementation: %w", err)
	}
	s.logger.Info("📜 TTM Account Implementation:")
	s.logger.Info("   - Active:  " + currentImplOnProxy.Hex())
	s.logger.Info("   - Latest:  " + currentImplOnManager.Hex())

	return currentImplOnManager == currentImplOnProxy, nil
}

func slotValueToAddress(slotValue []byte) (common.Address, error) {
	if len(slotValue) < 32 {
		return common.Address{}, fmt.Errorf("slot value storage read returned unexpected size: %d", len(slotValue))
	}
	// We take the last 20 bytes because Ethereum addresses are 20 bytes (40 hex chars) but storage slots are 32 bytes
	// The address is right-aligned in the 32 byte slot
	return common.BytesToAddress(slotValue[12:]), nil
}
