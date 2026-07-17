// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package main

import (
	"os"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/pp-mock/server"
)

func main() {
	if err := server.Run(); err != nil {
		os.Exit(1)
	}
}
