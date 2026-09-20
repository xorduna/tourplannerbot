// Package database opens the PostgreSQL connection used by the application.
package database

import (
	"context"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open establishes and verifies a GORM connection to PostgreSQL using databaseURL.
func Open(ctx context.Context, databaseURL string) (*gorm.DB, error) {
	databaseConnection, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL connection: %w", err)
	}

	sqlDatabaseConnection, err := databaseConnection.DB()
	if err != nil {
		return nil, fmt.Errorf("access PostgreSQL connection pool: %w", err)
	}

	if err := sqlDatabaseConnection.PingContext(ctx); err != nil {
		_ = sqlDatabaseConnection.Close()
		return nil, fmt.Errorf("ping PostgreSQL database: %w", err)
	}

	return databaseConnection, nil
}
