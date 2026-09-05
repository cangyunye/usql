// Package opengauss defines and registers usql's openGauss driver.
//
// See: https://gitcode.com/opengauss/openGauss-connector-go-pq
// Group: most
package opengauss

import (
	"io"

	_ "gitcode.com/opengauss/openGauss-connector-go-pq" // DRIVER: opengauss
	"github.com/xo/dburl"
	"github.com/xo/usql/drivers"
	"github.com/xo/usql/drivers/metadata"
	pgmeta "github.com/xo/usql/drivers/metadata/postgres"
)

func init() {
	// Register the opengauss:// URL scheme in dburl. openGauss speaks the
	// PostgreSQL wire protocol, so the lib/pq DSN generator is used and the
	// scheme maps to the openGauss-connector-go-pq driver name "opengauss".
	dburl.Register(dburl.Scheme{
		Driver:    "opengauss",
		Generator: dburl.GenPostgres,
		Transport: dburl.TransportTCP | dburl.TransportUnix,
		Aliases:   []string{"og"},
	})

	drivers.Register("opengauss", drivers.Driver{
		Name:                   "opengauss",
		AllowDollar:            true,
		AllowMultilineComments: true,
		AllowCComments:         true,
		AllowHashComments:      true,
		LexerName:              "postgres",
		NewMetadataReader:      pgmeta.NewReader(),
		NewMetadataWriter: func(db drivers.DB, w io.Writer, opts ...metadata.ReaderOption) metadata.Writer {
			return metadata.NewDefaultWriter(pgmeta.NewReader()(db, opts...))(db, w)
		},
	})
}
