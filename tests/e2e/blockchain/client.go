// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package blockchain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"sync"

	e2eCommon "github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/tests/e2e/common"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/bookingtoken"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/bookingtokenoperator"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/erc1967proxy"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/nullusd"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccount"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccountmanager"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const bookingTokenOperatorLibName = "12bd2f62b73a470fe0f6e02c33045f3191" //nolint:gosec // this is not credentials.

var (
	ttmAccountNativeTokenPrefund = big.NewInt(0).Mul(e2eCommon.Ether, big.NewInt(100))

	ErrorAddServiceTxFailed = errors.New("failed to issue AddService tx")
)

func newClient(
	ctx context.Context,
	hostPort string,
	prefundedKeys []*ecdsa.PrivateKey,
	deployerKey *ecdsa.PrivateKey,
) (*Client, error) {
	chainRPCURL := "ws://" + hostPort
	ethClient, err := ethclient.Dial(chainRPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the Ethereum client: %w", err)
	}

	ethChainID, err := ethClient.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	return &Client{
		chainRPCURL:   chainRPCURL,
		ethClient:     ethClient,
		ethChainID:    ethChainID,
		prefundedKeys: prefundedKeys,
		deployerKey:   deployerKey,
		nonces:        make(map[common.Address]uint64),
	}, nil
}

type Client struct {
	nullUSD      *nullusd.Nullusd
	BookingToken *bookingtoken.Bookingtoken

	prefundedKeys               []*ecdsa.PrivateKey
	deployerKey                 *ecdsa.PrivateKey
	chainRPCURL                 string
	ethClient                   *ethclient.Client
	ethChainID                  *big.Int
	bookingTokenContractAddress common.Address
	ttmAccountManager           *ttmaccountmanager.Ttmaccountmanager
	ttmAccountManagerAddress    common.Address

	nonces      map[common.Address]uint64
	noncesMutex sync.Mutex
}

func (c *Client) ETHClient() *ethclient.Client {
	return c.ethClient
}

func (c *Client) PrefundedKeys() []*ecdsa.PrivateKey {
	return c.prefundedKeys
}

func (c *Client) ChainRPCURL() string {
	return c.chainRPCURL
}

func (c *Client) BookingTokenContractAddress() common.Address {
	return c.bookingTokenContractAddress
}

func (c *Client) CreateTTMAccount(ctx context.Context, owner *ecdsa.PrivateKey) (common.Address, *ttmaccount.Ttmaccount, error) {
	transactor, err := c.transactor(ctx, c.deployerKey, ttmAccountNativeTokenPrefund)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("failed to create transactor: %w", err)
	}

	ownerAddr := crypto.PubkeyToAddress(owner.PublicKey)

	tx, err := c.ttmAccountManager.CreateTTMAccount(
		transactor,
		ownerAddr,
		ownerAddr,
	)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("failed to issue ttmAccountManager.CreateTTMAccount tx: %w", err)
	}

	receipt, err := c.waitTxSucceed(ctx, tx)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("failed to wait for ttmAccountManager.CreateTTMAccount tx to succeed: %w", err)
	}

	for _, log := range receipt.Logs {
		event, err := c.ttmAccountManager.ParseTTMAccountCreated(*log)
		if err == nil {
			ttmAccount, err := ttmaccount.NewTtmaccount(event.Account, c.ethClient)
			if err != nil {
				return common.Address{}, nil, fmt.Errorf("failed to create ttm account binding: %w", err)
			}

			return event.Account, ttmAccount, nil
		}
	}

	return common.Address{}, nil, fmt.Errorf("failed to parse TTMAccountCreated event from receipt logs")
}

func (c *Client) AddBotToTTMAccount(
	ctx context.Context,
	ttmAccountAddress common.Address,
	ttmAccountOwnerKey *ecdsa.PrivateKey,
	botAddr common.Address,
) error {
	transactor, err := c.transactor(ctx, ttmAccountOwnerKey, nil)
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	ttmAccount, err := c.TTMAccount(ttmAccountAddress)
	if err != nil {
		return fmt.Errorf("failed to get ttm account binding: %w", err)
	}

	tx, err := ttmAccount.AddMessengerBot(transactor, botAddr, common.Big0)
	if err != nil {
		return fmt.Errorf("failed to issue AddMessengerBot tx: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, tx); err != nil {
		return fmt.Errorf("failed to wait for AddMessengerBot tx to succeed: %w", err)
	}

	return nil
}

func (c *Client) AddCMService(
	ctx context.Context,
	ttmAccountAddress common.Address,
	ttmAccountOwnerKey *ecdsa.PrivateKey,
	serviceName string,
) error {
	transactor, err := c.transactor(ctx, ttmAccountOwnerKey, nil)
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	ttmAccount, err := c.TTMAccount(ttmAccountAddress)
	if err != nil {
		return fmt.Errorf("failed to get ttm account binding: %w", err)
	}

	tx, err := ttmAccount.AddService(
		transactor,
		serviceName,
		false,
		[]string{},
	)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrorAddServiceTxFailed, err)
	}

	if _, err := c.waitTxSucceed(ctx, tx); err != nil {
		return fmt.Errorf("failed to wait for AddCMService tx to succeed: %w", err)
	}

	return nil
}

func (c *Client) RegisterCMService(
	ctx context.Context,
	serviceName string,
) error {
	transactor, err := c.transactor(ctx, c.deployerKey, nil)
	if err != nil {
		return fmt.Errorf("failed to create admin transactor: %w", err)
	}

	tx, err := c.ttmAccountManager.RegisterService(transactor, serviceName)
	if err != nil {
		return fmt.Errorf("failed to issue RegisterService tx: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, tx); err != nil {
		return fmt.Errorf("failed to wait for RegisterService tx to succeed: %w", err)
	}

	return nil
}

func (c *Client) RegisterCMServices(
	ctx context.Context,
	serviceNames ...string,
) error {
	for _, serviceName := range serviceNames {
		if err := c.RegisterCMService(ctx, serviceName); err != nil {
			return fmt.Errorf("failed to register service %s: %w", serviceName, err)
		}
	}
	return nil
}

func (c *Client) Transfer(
	ctx context.Context,
	from *ecdsa.PrivateKey,
	to common.Address,
	amount *big.Int,
) error {
	gasPrice, err := c.ethClient.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("failed to suggest gas price: %w", err)
	}

	nonce, err := c.nextNonce(ctx, crypto.PubkeyToAddress(from.PublicKey))
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

	unsignedTx := types.NewTransaction(nonce, to, amount, 30000, gasPrice, nil)

	signedTx, err := types.SignTx(unsignedTx, types.NewEIP155Signer(c.ethChainID), from)
	if err != nil {
		return fmt.Errorf("failed to sign transaction: %w", err)
	}

	err = c.ethClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, signedTx); err != nil {
		return fmt.Errorf("failed to wait for transaction to succeed: %w", err)
	}

	return nil
}

func (c *Client) BalanceOf(ctx context.Context, addr common.Address) (*big.Int, error) {
	balance, err := c.ethClient.BalanceAt(ctx, addr, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get balance: %w", err)
	}
	return balance, nil
}

func (c *Client) BalanceNullUSDOf(ctx context.Context, addr common.Address) (*big.Int, error) {
	balance, err := c.nullUSD.BalanceOf(&bind.CallOpts{Context: ctx}, addr)
	if err != nil {
		return nil, fmt.Errorf("failed to get nullUSD balance: %w", err)
	}
	return balance, nil
}

func (c *Client) TTMAccount(addr common.Address) (*ttmaccount.Ttmaccount, error) {
	return ttmaccount.NewTtmaccount(addr, c.ethClient)
}

func (c *Client) transactor(ctx context.Context, key *ecdsa.PrivateKey, value *big.Int) (*bind.TransactOpts, error) {
	nonce, err := c.nextNonce(ctx, crypto.PubkeyToAddress(key.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	transactor, err := bind.NewKeyedTransactorWithChainID(key, c.ethChainID)
	transactor.Context = ctx
	transactor.Value = value
	transactor.Nonce = new(big.Int).SetUint64(nonce)
	return transactor, err
}

func (c *Client) waitTxSucceed(ctx context.Context, tx *types.Transaction) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, c.ethClient, tx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction to be mined: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction failed with status %d", receipt.Status)
	}
	return receipt, nil
}

// TODO @evlekht we can use some kind of snapshotting to skip the process of deploying SCs if we'll use persistent network
func (c *Client) prepareTTMBContracts(ctx context.Context) error {
	c.noncesMutex.Lock()
	defer c.noncesMutex.Unlock()

	adminAddress := crypto.PubkeyToAddress(c.deployerKey.PublicKey)

	// prepare contracts, will be done in 4 blocks

	transactor, err := bind.NewKeyedTransactorWithChainID(c.deployerKey, c.ethChainID)
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}
	transactor.Context = ctx

	// prepare TTM Account Manager proxy initialization data

	ttmAccountManagerABI, err := abi.JSON(strings.NewReader(ttmaccountmanager.TtmaccountmanagerABI))
	if err != nil {
		return fmt.Errorf("failed to parse ttmAccountManager ABI: %w", err)
	}
	ttmAccountManagerInitializeData, err := ttmAccountManagerABI.Pack(
		"initialize",
		adminAddress, // admin
		adminAddress, // pauser
		adminAddress, // upgrader
		adminAddress, // versioner
	)
	if err != nil {
		return fmt.Errorf("failed to pack ttmAccountManager.initialize data: %w", err)
	}

	// block 1 (deploy BookingToken impl, TTM Account Manager impl, BookingTokenOperator, nullUSD)

	bookingTokenImplAddress, bookingTokenImplTx, _, err := bookingtoken.DeployBookingtoken(transactor, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to deploy bookingToken implementation contract: %w", err)
	}
	ttmAccountManagerImplAddress, ttmAccountManagerImplTx, _, err := ttmaccountmanager.DeployTtmaccountmanager(transactor, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to deploy ttmAccountManager implementation contract: %w", err)
	}
	bookingTokenOperatorAddress, bookingTokenOperatorTx, _, err := bookingtokenoperator.DeployBookingtokenoperator(transactor, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to deploy bookingTokenOperator contract: %w", err)
	}
	nullUSDAddress, nullUSDTx, _, err := nullusd.DeployNullusd(transactor, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to deploy nullUSD contract: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, bookingTokenImplTx); err != nil {
		return fmt.Errorf("failed to wait for bookingToken implementation deployment tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, ttmAccountManagerImplTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccountManager implementation deployment tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, bookingTokenOperatorTx); err != nil {
		return fmt.Errorf("failed to wait for bookingTokenOperator deployment tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, nullUSDTx); err != nil {
		return fmt.Errorf("failed to wait for nullUSD deployment tx to succeed: %w", err)
	}

	// prepare ttmAccount implementation bytecode with linked Booking Token Operator contract

	ttmAccountABI, err := abi.JSON(strings.NewReader(ttmaccount.TtmaccountABI))
	if err != nil {
		return fmt.Errorf("failed to parse ttmAccount ABI: %w", err)
	}

	bookingTokenOperatorLinkingRegExp := regexp.MustCompile("__\\$" + bookingTokenOperatorLibName + "\\$__")
	ttmAccountImplBytecode := bookingTokenOperatorLinkingRegExp.ReplaceAllString(
		ttmaccount.TtmaccountBin,
		strings.ToLower(bookingTokenOperatorAddress.String()[2:]),
	)

	// create nullUSD bindings

	c.nullUSD, err = nullusd.NewNullusd(nullUSDAddress, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create nullUSD binding: %w", err)
	}

	// block 2 (deploy TTM Account Manager proxy, TTM Account implementation)

	ttmAccountManagerProxyAddress, ttmAccountManagerProxyTx, _, err := erc1967proxy.DeployErc1967proxy(
		transactor,
		c.ethClient,
		ttmAccountManagerImplAddress,
		ttmAccountManagerInitializeData,
	)
	if err != nil {
		return fmt.Errorf("failed to deploy ttmAccountManager proxy contract: %w", err)
	}

	ttmAccountImplAddress, ttmAccountImplTx, _, err := bind.DeployContract(transactor, ttmAccountABI, common.FromHex(ttmAccountImplBytecode), c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to deploy ttmAccount implementation contract: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, ttmAccountManagerProxyTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccountManager proxy deployment tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, ttmAccountImplTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccount implementation deployment tx to succeed: %w", err)
	}

	// create ttmAccountManager binding

	c.ttmAccountManager, err = ttmaccountmanager.NewTtmaccountmanager(ttmAccountManagerProxyAddress, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create ttmAccountManager binding: %w", err)
	}
	c.ttmAccountManagerAddress = ttmAccountManagerProxyAddress

	// prepare Booking Token proxy initialization data

	bookingTokenABI, err := abi.JSON(strings.NewReader(bookingtoken.BookingtokenABI))
	if err != nil {
		return fmt.Errorf("failed to parse ttmAccountManager ABI: %w", err)
	}
	bookingTokenInitializeData, err := bookingTokenABI.Pack(
		"initialize",
		ttmAccountManagerProxyAddress, // TTM Account Manager address
		adminAddress,                  // defaultAdmin
		adminAddress,                  // upgrader
	)
	if err != nil {
		return fmt.Errorf("failed to pack bookingToken.initialize data: %w", err)
	}

	// block 3 (deploy Booking Token proxy, grant ttmAccountManager role for registering services)

	serviceRegistryAdminRole, err := c.ttmAccountManager.SERVICEREGISTRYADMINROLE(&bind.CallOpts{Context: ctx})
	if err != nil {
		return fmt.Errorf("failed to get role: %w", err)
	}

	grantServiceRegistryAdminRoleTx, err := c.ttmAccountManager.GrantRole(transactor, serviceRegistryAdminRole, adminAddress)
	if err != nil {
		return fmt.Errorf("failed to issue ttmAccountManager.GrantRole (serviceRegistryAdminRole): %w", err)
	}

	bookingTokenProxyAddress, bookingTokenProxyTx, _, err := erc1967proxy.DeployErc1967proxy(
		transactor,
		c.ethClient,
		bookingTokenImplAddress,
		bookingTokenInitializeData,
	)
	if err != nil {
		return fmt.Errorf("failed to deploy bookingToken proxy contract: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, grantServiceRegistryAdminRoleTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccountManager.GrantRole (serviceRegistryAdminRole) tx to succeed: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, bookingTokenProxyTx); err != nil {
		return fmt.Errorf("failed to wait for bookingToken proxy deployment tx to succeed: %w", err)
	}

	// create bookingToken binding

	c.BookingToken, err = bookingtoken.NewBookingtoken(bookingTokenProxyAddress, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create bookingToken binding: %w", err)
	}

	// block 4 (ReinitializeV2 Booking Token, link contracts)

	setBookingTokenAddressTx, err := c.ttmAccountManager.SetBookingTokenAddress(
		transactor,
		bookingTokenProxyAddress,
	)
	if err != nil {
		return fmt.Errorf("failed to issue ttmAccountManager.SetBookingTokenAddress tx: %w", err)
	}

	setAccountImplementationTx, err := c.ttmAccountManager.SetAccountImplementation(
		transactor,
		ttmAccountImplAddress,
	)
	if err != nil {
		return fmt.Errorf("failed to issue ttmAccountManager.SetAccountImplementation tx: %w", err)
	}

	reinitializeV2Tx, err := c.BookingToken.ReinitializeV2(transactor, "BookingToken", "BToken")
	if err != nil {
		return fmt.Errorf("failed to issue bookingToken.ReinitializeV2 tx: %w", err)
	}

	minExpirationTimestampDiffRole, err := c.BookingToken.MINEXPIRATIONADMINROLE(&bind.CallOpts{Context: ctx})
	if err != nil {
		return fmt.Errorf("failed to get role: %w", err)
	}

	grantRoleTx, err := c.BookingToken.GrantRole(transactor, minExpirationTimestampDiffRole, adminAddress)
	if err != nil {
		return fmt.Errorf("failed to issue BookingToken.GrantRole (minExpirationTimestampDiffRole) tx: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, grantRoleTx); err != nil {
		return fmt.Errorf("failed to wait for BookingToken.GrantRole (minExpirationTimestampDiffRole) tx to succeed: %w", err)
	}

	updateExpirationTx, err := c.BookingToken.SetMinExpirationTimestampDiff(transactor, big.NewInt(e2eCommon.MinBuyableUntilInContract))
	if err != nil {
		return fmt.Errorf("failed to issue bookingToken.SetMinExpirationTimestampDiff tx: %w", err)
	}

	if _, err := c.waitTxSucceed(ctx, setBookingTokenAddressTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccountManager.SetBookingTokenAddress tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, setAccountImplementationTx); err != nil {
		return fmt.Errorf("failed to wait for ttmAccountManager.SetAccountImplementation tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, reinitializeV2Tx); err != nil {
		return fmt.Errorf("failed to wait for bookingToken.ReinitializeV2 tx to succeed: %w", err)
	}
	if _, err := c.waitTxSucceed(ctx, updateExpirationTx); err != nil {
		return fmt.Errorf("failed to wait for bookingToken.SetMinExpirationTimestampDiff tx to succeed: %w", err)
	}

	c.bookingTokenContractAddress = bookingTokenProxyAddress

	nonce, err := c.ethClient.PendingNonceAt(ctx, adminAddress)
	if err != nil {
		return fmt.Errorf("failed to get nonce for admin address: %w", err)
	}
	c.nonces[adminAddress] = nonce

	return nil
}

func (c *Client) nextNonce(ctx context.Context, addr common.Address) (uint64, error) {
	c.noncesMutex.Lock()
	defer c.noncesMutex.Unlock()

	nonce, ok := c.nonces[addr]
	if !ok {
		var err error
		nonce, err = c.ethClient.PendingNonceAt(ctx, addr)
		if err != nil {
			return 0, fmt.Errorf("failed to get nonce for address %s: %w", addr.Hex(), err)
		}
	}

	c.nonces[addr] = nonce + 1

	return nonce, nil
}
