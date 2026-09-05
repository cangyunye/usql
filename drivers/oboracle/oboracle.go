// Package oboracle defines and registers usql's OceanBase Oracle driver.
//
// OceanBase Oracle-compatible tenants reject standard MySQL clients (error
// 1235) and require an OceanBase-aware MySQL-wire connector such as
// obconnector-go, which registers the "oboracle" database/sql driver.
// SQL and metadata are Oracle-style (DUAL, ALL_TABLES, etc.).
//
// See: https://github.com/helingjun/obconnector-go
// Group: most
package oboracle

import (
	"errors"
	"fmt"

	ob "github.com/helingjun/obconnector-go" // DRIVER: oboracle
	"github.com/xo/dburl"
	orameta "github.com/xo/usql/drivers/metadata/oracle"
	"github.com/xo/usql/drivers/oracle/orshared"
)

func init() {
	// obconnector-go accepts go-sql-driver/mysql style DSNs (it is a MySQL
	// wire fork), so the MySQL DSN generator is reused. The tenant is carried
	// in the username as user@tenant (optionally #cluster for OBProxy).
	dburl.Register(dburl.Scheme{
		Driver:    "oboracle",
		Generator: dburl.GenMysql,
		Transport: dburl.TransportTCP | dburl.TransportUnix,
		Aliases:   []string{"ob"},
	})

	orshared.Register(
		"oboracle",
		// unwrap obconnector-go server errors
		func(err error) (string, string) {
			var e *ob.ServerError
			if errors.As(err, &e) {
				return fmt.Sprintf("OBE-%05d", e.Number), e.Message
			}
			return "", err.Error()
		},
		// access denied (bad credentials)
		func(err error) bool {
			var e *ob.ServerError
			if errors.As(err, &e) {
				return e.Number == 1045
			}
			return false
		},
		orameta.NewReaderQ(),
	)
}
