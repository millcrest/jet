package pgx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/go-jet/jet/v2/internal/testutils"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/stmtcache"
	"github.com/go-jet/jet/v2/tests/.gentestdata/jetdb/dvds/model"
	"github.com/go-jet/jet/v2/tests/internal/utils/repo"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/tests/dbconfig"
	_ "github.com/lib/pq"
	"github.com/pkg/profile"
	"github.com/stretchr/testify/require"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var ctx = context.Background()

var db *stmtcache.DB
var pgxConn *pgxpool.Pool
var pgxPool *pgxpool.Pool
var testRoot string

var source string

const CockroachDB = "COCKROACH_DB"

func init() {
	source = os.Getenv("PG_SOURCE")
	testRoot = repo.GetTestsDirPath()
}

func sourceIsCockroachDB() bool {
	return source == CockroachDB
}

func skipForCockroachDB(t *testing.T) {
	if sourceIsCockroachDB() {
		t.SkipNow()
	}
}

func TestMain(m *testing.M) {
	defer profile.Start().Stop()
	qrm.GlobalConfig.StrictScan = true

	for _, driverName := range []string{"postgres"} {
		fmt.Printf("\nRunning postgres tests for driver: %s \n", driverName)
		func() {
			var err error
			config, err := pgxpool.ParseConfig(getConnectionString())
			if err != nil {
				panic(err)
			}
			pgxPool, err = pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				panic(err)
			}
			pgxConn = pgxPool
			defer pgxPool.Close()

			sqlDB := stdlib.OpenDBFromPool(pgxPool)
			db = stmtcache.New(sqlDB)

			ret := m.Run()
			if ret != 0 {
				fmt.Printf("\nFAIL: Running postgres tests failed for driver: %s \n", driverName)
				os.Exit(ret)
			}
		}()
	}
}

func runCount(stmtCaching bool) int {
	if stmtCaching {
		return 2
	}

	return 1
}

func getConnectionString() string {
	if sourceIsCockroachDB() {
		return dbconfig.CockroachConnectString
	}

	return dbconfig.PostgresConnectString
}

func allowUnusedColumns(f func()) {
	defer func() {
		qrm.GlobalConfig.StrictScan = true
	}()

	qrm.GlobalConfig.StrictScan = false

	f()
}

func useJsonUnmarshalFunc(unmarshalJson func(data []byte, v any) error, f func()) {
	defer func() {
		qrm.GlobalConfig.JsonUnmarshalFunc = json.Unmarshal
	}()

	qrm.GlobalConfig.JsonUnmarshalFunc = unmarshalJson

	f()
}

var loggedSQL string
var loggedSQLArgs []interface{}
var loggedDebugSQL string

var queryInfo postgres.QueryInfo
var callerFile string
var callerLine int
var callerFunction string

func init() {
	postgres.SetLogger(func(ctx context.Context, statement postgres.PrintableStatement) {
		loggedSQL, loggedSQLArgs = statement.Sql()
		loggedDebugSQL = statement.DebugSql()
	})

	postgres.SetQueryLogger(func(ctx context.Context, info postgres.QueryInfo) {
		queryInfo = info
		callerFile, callerLine, callerFunction = info.Caller()
	})
}

func requireLogged(t require.TestingT, statement postgres.Statement) {
	if _, ok := t.(*testing.B); ok {
		return // skip assert for benchmarks
	}

	query, args := statement.Sql()
	require.Equal(t, loggedSQL, query)
	require.Equal(t, loggedSQLArgs, args)
	require.Equal(t, loggedDebugSQL, statement.DebugSql())
}

func requireQueryLogged(t require.TestingT, statement postgres.Statement, rowsProcessed int64) {
	if _, ok := t.(*testing.B); ok {
		return // skip assert for benchmarks
	}

	query, args := statement.Sql()
	queryLogged, argsLogged := queryInfo.Statement.Sql()

	require.Equal(t, query, queryLogged)
	require.Equal(t, args, argsLogged)
	require.Equal(t, queryInfo.RowsProcessed, rowsProcessed)

	pc, file, _, _ := runtime.Caller(1)
	funcDetails := runtime.FuncForPC(pc)
	require.Equal(t, file, callerFile)
	require.NotEmpty(t, callerLine)
	require.Equal(t, funcDetails.Name(), callerFunction)
}

func skipForPgxDriver(t *testing.T) {
	if isPgxDriver() {
		t.SkipNow()
	}
}

func isPgxDriver() bool {
	switch db.Driver().(type) {
	case *stdlib.Driver:
		return true
	}

	return false
}

func toT[T any](tm *T, loc *time.Location) *T {
	if tm == nil {
		return nil
	}
	switch t := any(tm).(type) {
	case *pgtype.Time:
		out := &pgtype.Time{
			Microseconds: time.UnixMicro(t.Microseconds).In(loc).UnixMicro(),
			Valid:        t.Valid,
		}
		return any(out).(*T)
	case *pgtype.Timestamptz:
		newTz := t
		newTz.Time = t.Time.In(loc)
		return any(newTz).(*T)
	case *pgtype.Timestamp:
		newTz := t
		newTz.Time = t.Time.In(loc)
		return any(newTz).(*T)
	default:
		return tm
	}
}

func toTimestampTz(t *time.Time) *pgtype.Timestamptz {
	if t == nil {
		return &pgtype.Timestamptz{}
	}
	return &pgtype.Timestamptz{
		Time:  *t,
		Valid: true,
	}
}

func toTimestamp(t *time.Time) *pgtype.Timestamp {
	if t == nil {
		return &pgtype.Timestamp{}
	}
	return &pgtype.Timestamp{
		Time:  *t,
		Valid: true,
	}
}

func toTime(t *time.Time) *pgtype.Time {
	if t == nil {
		return &pgtype.Time{}
	}
	// pgtype.Time.Microseconds is microseconds since midnight, not Unix epoch
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	microsecondsSinceMidnight := t.Sub(midnight).Microseconds()
	return &pgtype.Time{
		Microseconds: microsecondsSinceMidnight,
		Valid:        true,
	}
}

func toDate(t *time.Time) *pgtype.Date {
	if t == nil {
		return &pgtype.Date{}
	}
	return &pgtype.Date{
		Time:  *t,
		Valid: true,
	}
}

func getIntervalVal(s string) pgtype.Interval {
	intervalVal := pgtype.Interval{}
	err := intervalVal.Scan(s)
	if err != nil {
		panic(err)
	}
	return intervalVal
}

var actor2 = model.Actor{
	ActorID:    2,
	FirstName:  "Nick",
	LastName:   "Wahlberg",
	LastUpdate: *testutils.TimestampWithoutTimeZone("2013-05-26 14:47:57.62", 2),
}
