package database

import (
	"context"
	"database/sql"
	"errors"
	"sync"
)

// ErrConnectionUnavailable indicates that PostgreSQL has not connected yet.
var ErrConnectionUnavailable = errors.New("PostgreSQL connection is unavailable")

// Readiness keeps the current PostgreSQL connection available to health checks
// without requiring the HTTP server to know about GORM.
type Readiness struct {
	mutex                 sync.RWMutex
	sqlDatabaseConnection *sql.DB
}

// NewReadiness creates a readiness checker that is initially unavailable.
func NewReadiness() *Readiness {
	return &Readiness{}
}

// SetConnection records the PostgreSQL connection used for future readiness checks.
func (readiness *Readiness) SetConnection(sqlDatabaseConnection *sql.DB) {
	readiness.mutex.Lock()
	defer readiness.mutex.Unlock()

	readiness.sqlDatabaseConnection = sqlDatabaseConnection
}

// Check verifies that a PostgreSQL connection exists and responds to PingContext.
func (readiness *Readiness) Check(applicationContext context.Context) error {
	readiness.mutex.RLock()
	sqlDatabaseConnection := readiness.sqlDatabaseConnection
	readiness.mutex.RUnlock()

	if sqlDatabaseConnection == nil {
		return ErrConnectionUnavailable
	}

	return sqlDatabaseConnection.PingContext(applicationContext)
}
