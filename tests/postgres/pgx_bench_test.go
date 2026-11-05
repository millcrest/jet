package postgres

import (
	"database/sql"
	"testing"

	"github.com/go-jet/jet/v2/pgxV5"
	. "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/tests/.gentestdata/jetdb/dvds/model"
	. "github.com/go-jet/jet/v2/tests/.gentestdata/jetdb/dvds/table"
	"github.com/stretchr/testify/require"
)

// BenchmarkQRMDrivers compares query result mapping of the same statements and destinations across drivers:
// lib/pq (database/sql), pgx stdlib (database/sql) and pgx v5 native (pgxV5.Query).
// Result correctness is asserted once per driver, outside the timed loop.
//
//	go test ./postgres -run '^$' -bench BenchmarkQRMDrivers -benchmem
func BenchmarkQRMDrivers(b *testing.B) {
	pgxStdlibDB, err := sql.Open("pgx", getConnectionString())
	require.NoError(b, err)
	defer pgxStdlibDB.Close()

	drivers := []struct {
		name  string
		query func(stmt Statement, dest any) error
	}{
		{"pq", func(stmt Statement, dest any) error { return stmt.QueryContext(ctx, db, dest) }},
		{"pgx-stdlib", func(stmt Statement, dest any) error { return stmt.QueryContext(ctx, pgxStdlibDB, dest) }},
		{"pgxV5", func(stmt Statement, dest any) error { return pgxV5.Query(ctx, stmt, pgxConn, dest) }},
	}

	cases := []struct {
		name   string
		stmt   Statement
		dest   func() any
		verify func(b *testing.B, dest any)
	}{
		{
			// wide flat rows: text[], tsvector, enum, numeric, timestamp columns, no grouping
			name: "flat-films",
			stmt: SELECT(Film.AllColumns).FROM(Film).ORDER_BY(Film.FilmID),
			dest: func() any { return &[]model.Film{} },
			verify: func(b *testing.B, dest any) {
				require.Len(b, *dest.(*[]model.Film), 1000)
			},
		},
		{
			// nested destination, grouping of a large join
			name: "nested-northwind",
			stmt: northwindJoinEverythingStmt(),
			dest: func() any { return &Dest{} },
			verify: func(b *testing.B, dest any) {
				require.NotEmpty(b, *dest.(*Dest))
			},
		},
	}

	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			for _, d := range drivers {
				b.Run(d.name, func(b *testing.B) {
					dest := c.dest()
					require.NoError(b, d.query(c.stmt, dest))
					c.verify(b, dest)

					b.ReportAllocs()
					b.ResetTimer()

					for i := 0; i < b.N; i++ {
						if err := d.query(c.stmt, c.dest()); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
