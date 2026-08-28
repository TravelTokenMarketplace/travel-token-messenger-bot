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
// which name the renamed column. SQLite validates column names when a statement
// is prepared, which happens during New(), so a database left on the old schema
// cannot even open — that is the failure this migration exists to prevent.
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
		"RENAME COLUMN must carry the composite primary key across, in order")

	exerciseResolvePath(t, storage)
}

// The case this migration exists for: a database created before the rename,
// already stamped as migrated, whose table still has the old column name.
// Renaming the column inside the already-applied migration would leave such a
// database untouched — the tool stores a version number and no checksum, so it
// has no way to notice the file changed — and the bot would then fail at
// startup preparing statements against a column that is not there.
func TestDatabaseCarriedOverFromBeforeTheRenameIsMigrated(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "resolver")
	dbFile := dbPath + ".sqlite"

	// Build exactly what such a database looks like: the pre-rename schema,
	// a row in it, and the migration bookkeeping saying version 1 is done.
	db, err := sqlx.Open("sqlite3", dbFile)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE bots (
			cm_account      VARBINARY(20)  NOT NULL,
			bot             VARBINARY(20)  NOT NULL,
			status          INTEGER        NOT NULL,
			PRIMARY KEY (cm_account, bot)
		);
		CREATE TABLE schema_migrations (version uint64, dirty bool);
		CREATE UNIQUE INDEX version_unique ON schema_migrations (version);
		INSERT INTO schema_migrations (version, dirty) VALUES (1, false);
	`)
	require.NoError(t, err)
	_, err = db.ExecContext(context.Background(), `INSERT INTO bots (cm_account, bot, status) VALUES (?, ?, ?)`,
		testTTMAccount.Bytes(), testBot.Bytes(), int(resolver.BotStatusReachable))
	require.NoError(t, err)
	require.NoError(t, db.Close())

	// Opening the storage runs the outstanding migration and then prepares the
	// statements. Both have to succeed for New() to return.
	storage := openStorage(t, dbPath)

	require.Equal(t, []string{"ttm_account", "bot"}, primaryKeyColumns(t, dbFile, "bots"))

	// The pre-existing row survived the rename and is still resolvable.
	ctx := context.Background()
	session, err := storage.NewSession(ctx)
	require.NoError(t, err)
	defer storage.Abort(session)

	bot, err := storage.GetFirstBotWithStatus(ctx, session, testTTMAccount, resolver.BotStatusReachable)
	require.NoError(t, err)
	require.Equal(t, testBot, bot, "rows written before the rename must still be readable after it")
	require.NoError(t, storage.Commit(session))
}
