package storage

import (
	"database/sql"
	"database/sql/driver"
	"time"

	"github.com/mattn/go-sqlite3"
)

// TimeLayout is the canonical on-disk timestamp format for every DATETIME
// column: UTC, second precision, space separated. It is byte-identical to
// what SQLite writes for CURRENT_TIMESTAMP / datetime('now'), so rows written
// from SQL defaults and rows written from Go time.Time values sort and compare
// correctly as plain strings. Sub-second precision is intentionally dropped.
const TimeLayout = "2006-01-02 15:04:05"

// utcDriverName is the database/sql driver name registered by this package.
const utcDriverName = "sqlite3_utc"

// FormatTime renders t in the canonical timestamp format (UTC, TimeLayout).
// Use it when building a timestamp string by hand for a query parameter, for
// example a cutoff compared against a DATETIME column. time.Time values bound
// as query args are converted automatically by the driver wrapper in this file.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeLayout)
}

// utcDriver wraps mattn/go-sqlite3 so that every time.Time bound as a query
// argument is written in the canonical format instead of the stock driver
// format (local offset, fractional seconds). This covers sqlc-generated params
// typed time.Time / *time.Time and any hand-written query, so call sites cannot
// regress to the mixed format.
type utcDriver struct{ inner sqlite3.SQLiteDriver }

// Open implements driver.Driver.
func (d *utcDriver) Open(dsn string) (driver.Conn, error) {
	c, err := d.inner.Open(dsn)
	if err != nil {
		return nil, err
	}
	return &utcConn{SQLiteConn: c.(*sqlite3.SQLiteConn)}, nil
}

// utcConn embeds the real connection (promoting Prepare, ExecContext,
// QueryContext, BeginTx, Ping, Close, ...) and adds argument conversion.
type utcConn struct {
	*sqlite3.SQLiteConn
}

// CheckNamedValue implements driver.NamedValueChecker.
func (c *utcConn) CheckNamedValue(nv *driver.NamedValue) error {
	switch v := nv.Value.(type) {
	case time.Time:
		nv.Value = FormatTime(v)
		return nil
	case *time.Time:
		if v == nil {
			nv.Value = nil
		} else {
			nv.Value = FormatTime(*v)
		}
		return nil
	}
	return driver.ErrSkip
}

func init() {
	sql.Register(utcDriverName, &utcDriver{})
}
