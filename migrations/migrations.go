package migrations

import (
	"bytes"
	"context"
	"github.com/bloxapp/ssv/protocol/blockchain/eth1"

	"github.com/pkg/errors"
	"go.uber.org/zap"

	operatorstorage "github.com/bloxapp/ssv/operator/storage"
	validatorstorage "github.com/bloxapp/ssv/operator/validator"
	"github.com/bloxapp/ssv/storage/basedb"
)

var (
	migrationsPrefix   = []byte("migrations/")
	migrationCompleted = []byte("migrationCompleted")

	defaultMigrations = Migrations{
		migrationExample1,
		migrationExample2,
		migrationCleanAllRegistryData,
		migrationCleanOperatorNodeRegistryData,
		migrationCleanExporterRegistryData,
		migrationCleanValidatorRegistryData,
		migrationCleanSyncOffset,
		migrationCleanOperatorRemovalCorruptions,
		migrationCleanShares,
		migrationRemoveChangeRoundSync,
		migrationAddGraffiti,
	}
)

// Run executes the default migrations.
func Run(ctx context.Context, opt Options) error {
	return defaultMigrations.Run(ctx, opt)
}

// MigrationFunc is a function that performs a migration.
type MigrationFunc func(ctx context.Context, opt Options, key []byte) error

// Migration is a named MigrationFunc.
type Migration struct {
	Name string
	Run  MigrationFunc
}

// Migrations is a slice of named migrations, meant to be executed
// from first to last (order is significant).
type Migrations []Migration

// Options are configurations for migrations
type Options struct {
	Db     basedb.IDb
	Logger *zap.Logger
	DbPath string
}

func (o Options) getRegistryStores() []eth1.RegistryStore {
	return []eth1.RegistryStore{o.validatorStorage(), o.nodeStorage()}
}

func (o Options) validatorStorage() validatorstorage.ICollection {
	return validatorstorage.NewCollection(validatorstorage.CollectionOptions{
		DB:     o.Db,
		Logger: o.Logger,
	})
}

func (o Options) nodeStorage() operatorstorage.Storage {
	return operatorstorage.NewNodeStorage(o.Db, o.Logger)
}

// Run executes the migrations.
func (m Migrations) Run(ctx context.Context, opt Options) error {
	opt.Logger.Info("Running migrations:")
	count := 0
	for _, migration := range m {
		// Skip the migration if it's already completed.
		obj, _, err := opt.Db.Get(migrationsPrefix, []byte(migration.Name))
		if err != nil {
			return err
		}
		if bytes.Equal(obj.Value, migrationCompleted) {
			opt.Logger.Debug("migration already applied, skipping", zap.String("name", migration.Name))
			continue
		}

		// Execute the migration.
		err = migration.Run(ctx, opt, []byte(migration.Name))
		if err != nil {
			return errors.Wrapf(err, "migration %q failed", migration.Name)
		}
		count++
		opt.Logger.Info("migration applied successfully", zap.String("name", migration.Name))
	}
	if count == 0 {
		opt.Logger.Info("No migrations to apply.")
	}

	return nil
}
