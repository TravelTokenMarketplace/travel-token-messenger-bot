// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package messaging

import (
	"errors"
	"fmt"
	"sort"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/messaging/message"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/client"
	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/rpc/generated"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccount"
	"github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/ttmaccountmanager"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"go.uber.org/zap"
)

var errUnsupportedService = errors.New("ttm account support service, which bot doesn't support")

type ServiceRegistry interface {
	GetService(messageType message.Type) (rpc.Service, bool)
}

func NewServiceRegistry(
	ttmAccountAddress common.Address,
	evmClient *ethclient.Client,
	logger *zap.SugaredLogger,
	rpcClient *client.RPCClient,
) (ServiceRegistry, error) {
	ttmAccount, err := ttmaccount.NewTtmaccount(ttmAccountAddress, evmClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TTM account: %w", err)
	}

	supportedServices, err := ttmAccount.GetSupportedServices(&bind.CallOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Registered services: %w", err)
	}

	hasSupportedServices := len(supportedServices.ServiceHashes) > 0
	partnerPluginClientEnabled := rpcClient != nil
	var services map[message.Type]rpc.Service

	switch {
	case hasSupportedServices && !partnerPluginClientEnabled:
		return nil, fmt.Errorf("bot supports some services, but doesn't have partner plugin rpc client enabled")
	case !hasSupportedServices && partnerPluginClientEnabled:
		logger.Warn("Bot doesn't support any services, but has partner plugin rpc client enabled")
	case hasSupportedServices && partnerPluginClientEnabled: // register supported services
		// The TTM Account is hash-native: getSupportedServices returns service
		// hashes, not names. The manager owns the name<->hash registry, so the
		// names come from there. The account knows its own manager.
		manager, err := ttmAccountManager(ttmAccount, evmClient)
		if err != nil {
			return nil, err
		}

		registeredServiceNames, err := manager.GetAllRegisteredServiceNames(&bind.CallOpts{})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch registered service names: %w", err)
		}

		nameByHash := make(map[[32]byte]string, len(registeredServiceNames))
		for _, serviceName := range registeredServiceNames {
			nameByHash[crypto.Keccak256Hash([]byte(serviceName))] = serviceName
		}

		servicesNames := make(map[string]struct{}, len(supportedServices.ServiceHashes))
		var deprecatedServiceNames []string

		logStr := "\nSupported services:\n"
		for _, serviceHash := range supportedServices.ServiceHashes {
			serviceName, found := nameByHash[serviceHash]
			if !found {
				// The account supports a service the manager has since
				// unregistered. Unregistering keeps the name mapping, so this
				// single-hash lookup still resolves it.
				serviceName, err = manager.GetServiceNameByHash(&bind.CallOpts{}, serviceHash)
				if err != nil {
					return nil, fmt.Errorf("failed to resolve service name for hash %s: %w", common.BytesToHash(serviceHash[:]), err)
				}
				if serviceName == "" {
					// Deliberate guard, unreachable through this path: addService
					// only accepts a hash the manager has registered, registering
					// always writes _serviceNameByHash, and unregistering keeps it.
					// A supported hash therefore always resolves to a name. Reaching
					// this branch needs corrupted registry storage, so do not try to
					// write a test for it.
					logger.Warnf("Skipping supported service with unknown hash %s", common.BytesToHash(serviceHash[:]))
					continue
				}

				// Resolved only by the fallback, so the manager has unregistered it.
				// The service keeps working, because IsServiceSupported asks the
				// account rather than the manager, but the operator should know.
				deprecatedServiceNames = append(deprecatedServiceNames, serviceName)
			}

			logStr += serviceName + "\n"
			servicesNames[serviceName] = struct{}{}
		}

		services = generated.RegisterServiceClients(rpcClient.ClientConn, servicesNames)

		logStr += "\n"
		logger.Info(logStr)

		if len(deprecatedServiceNames) > 0 {
			// One summary warning rather than one per service, matching the
			// "Unsupported services" block below. Sorted so the output is stable
			// regardless of the order the account returns hashes in.
			sort.Strings(deprecatedServiceNames)

			logStr := "\nDeprecated services (supported by this TTM Account, but no longer registered with the manager):\n"
			for _, serviceName := range deprecatedServiceNames {
				logStr += serviceName + "\n"
			}
			logStr += "\n"
			logger.Warn(logStr)
		}

		if len(servicesNames) > 0 {
			logger.Error(errUnsupportedService)

			logStr := "\nUnsupported services:\n"
			for serviceName := range servicesNames {
				logStr += serviceName + "\n"
			}
			logStr += "\n"
			logger.Warn(logStr)

			return nil, errUnsupportedService
		}
	}

	return &serviceRegistry{
		logger:    logger,
		services:  services,
		rpcClient: rpcClient,
	}, nil
}

// ttmAccountManager binds the manager that owns the given TTM Account.
func ttmAccountManager(
	ttmAccount *ttmaccount.Ttmaccount,
	evmClient *ethclient.Client,
) (*ttmaccountmanager.Ttmaccountmanager, error) {
	managerAddress, err := ttmAccount.GetManagerAddress(&bind.CallOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TTM account manager address: %w", err)
	}

	manager, err := ttmaccountmanager.NewTtmaccountmanager(managerAddress, evmClient)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch TTM account manager: %w", err)
	}

	return manager, nil
}

type serviceRegistry struct {
	logger    *zap.SugaredLogger
	services  map[message.Type]rpc.Service
	rpcClient *client.RPCClient
}

func (s *serviceRegistry) GetService(requestType message.Type) (rpc.Service, bool) {
	service, ok := s.services[requestType]
	if !ok {
		return nil, false
	}
	return service, true
}
