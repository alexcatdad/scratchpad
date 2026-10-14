package persistence

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	errUnsafeStorage = errors.New("unsafe storage")
	errSchema        = errors.New("unsupported schema")
)

// FailureClass names why Open failed for operators without disclosing the
// database URL, credentials or row contents. PostgreSQL failures include
// their SQLSTATE so privilege and authentication problems are distinguishable.
func FailureClass(err error) string {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		class := "database"
		switch {
		case pgError.Code == "42501":
			class = "permission"
		case strings.HasPrefix(pgError.Code, "28"):
			class = "authentication"
		case strings.HasPrefix(pgError.Code, "08"), pgError.Code == "3D000", pgError.Code == "57P03":
			class = "connection"
		case pgError.Code == "42P01", pgError.Code == "42703", pgError.Code == "42P07", pgError.Code == "42710":
			class = "schema"
		}
		return class + ", SQLSTATE " + pgError.Code
	}
	var connectError *pgconn.ConnectError
	var netError net.Error
	switch {
	case errors.As(err, &connectError), errors.As(err, &netError), errors.Is(err, context.DeadlineExceeded):
		return "connection"
	case errors.Is(err, errUnsafeStorage):
		return "storage"
	case errors.Is(err, errSchema):
		return "schema"
	}
	return "unknown"
}
