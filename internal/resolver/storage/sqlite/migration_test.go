// Copyright (C) 2022-2026, Travel Token Marketplace. All rights reserved.
// See the file LICENSE for licensing terms.

package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13/internal/resolver"
	"github.com/ethereum/go-ethereum/common"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

var (
	testTTMAccount = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testBot        = common.HexToAddress("0x2222222222222222222222222222222222222222")
	otherBot       = common.HexToAddress("0x3333333333333333333333333333333333333333")
)

func openStorage(t *testing.T, dbPath string) Storage {
	t.Helper()
	storage, err := New(context.Background(), zap.NewNop().Sugar(), dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, storage.Close()) })
	return storage
}

// primaryKeyColumns reports the primary-key columns of a table, in key order.
func primaryKeyColumns(t *testing.T, dbFile, table string) []string {
	t.Helper()
	db, err := sqlx.Open("sqlite3", dbFile)
	require.NoError(t, err)
	defer db.Close()

	rows, err := db.QueryxContext(context.Background(), "SELECT name, pk FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk", table)
	require.NoError(t, err)
	defer rows.Close()

	columns := []string{}
	for rows.Next() {
		var name string
		var pk int
		require.NoError(t, rows.Scan(&name, &pk))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	return columns
}

// exerciseResolvePath drives the queries that prepareBotsStmts prepares, all of
// which name the account column. SQLite validates column names when a statement
// is prepared, which happens during New(), so a schema that disagrees with the
// queries cannot even open.
func exerciseResolvePath(t *testing.T, storage Storage) {
	t.Helper()
	ctx := context.Background()

	session, err := storage.NewSession(ctx)
	require.NoError(t, err)
	defer storage.Abort(session)

	require.NoError(t, storage.SetBots(ctx, session, testTTMAccount, []common.Address{testBot, otherBot}))
	require.NoError(t, storage.SetBotStatus(ctx, session, testBot, resolver.BotStatusReachable))

	bot, err := storage.GetFirstBotWithStatus(ctx, session, testTTMAccount, resolver.BotStatusReachable)
	require.NoError(t, err)
	require.Equal(t, testBot, bot)

	require.NoError(t, storage.Commit(session))
}

func TestMigrationsProduceAWorkingResolverSchema(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "resolver")
	storage := openStorage(t, dbPath)

	require.Equal(t, []string{"ttm_account", "bot"}, primaryKeyColumns(t, dbPath+".sqlite", "bots"),
		"the composite primary key must be present, in order")

	exerciseResolvePath(t, storage)
}
