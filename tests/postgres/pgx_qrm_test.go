package postgres

// Tests for pgxV5 Query Result Mapping. They mirror the query and destination shapes used by
// production consumers of the pgxV5 package (pgx-typed models, jsonb into []byte, lib/pq arrays,
// uuid.NullUUID, CTE + RETURNING, UPDATE ... MODEL, error propagation) and check that pgx rows
// map identically to database/sql rows.

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-jet/jet/v2/internal/testutils"
	"github.com/go-jet/jet/v2/pgxV5"
	. "github.com/go-jet/jet/v2/postgres"
	"github.com/go-jet/jet/v2/qrm"
	"github.com/go-jet/jet/v2/tests/.gentestdata/jetdb/test_sample/model"
	"github.com/go-jet/jet/v2/tests/.gentestdata/jetdb/test_sample/table"
	"github.com/google/go-cmp/cmp"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

var timeEqual = cmp.Comparer(func(a, b time.Time) bool { return a.Equal(b) })

// Every generated model column type must map the same through pgx as through database/sql.
func TestPgxParityAllTypes(t *testing.T) {
	stmt := SELECT(table.AllTypes.AllColumns).
		FROM(table.AllTypes).
		ORDER_BY(table.AllTypes.Serial)

	var expected []model.AllTypes
	require.NoError(t, stmt.Query(db, &expected))
	require.Len(t, expected, 2)

	for name, conn := range map[string]qrm.QueryablePgxV5{"pgx.Conn": pgxConn, "pgxpool.Pool": pgxPool} {
		t.Run(name, func(t *testing.T) {
			var actual []model.AllTypes
			require.NoError(t, pgxV5.Query(ctx, stmt, conn, &actual))
			testutils.AssertDeepEqual(t, actual, expected, timeEqual)
		})
	}

	t.Run("view", func(t *testing.T) {
		viewStmt := SELECT(table.AllTypes.AllColumns).FROM(table.AllTypes).ORDER_BY(table.AllTypes.Serial).LIMIT(1)
		var expected, actual model.AllTypes
		require.NoError(t, viewStmt.Query(db, &expected))
		require.NoError(t, pgxV5.Query(ctx, viewStmt, pgxPool, &actual))
		testutils.AssertDeepEqual(t, actual, expected, timeEqual)
	})
}

// pgx-typed destinations (as generated with pgx model types) mixed with default types.
func TestPgxScanPgxTypedDestination(t *testing.T) {
	type AllTypes struct { // named like the table so "all_types.*" aliases match
		Serial          int32
		UUID            uuid.UUID
		UUIDPtr         uuid.NullUUID
		Timestampz      pgtype.Timestamptz
		TimestampzPtr   *pgtype.Timestamptz
		Timestamp       pgtype.Timestamp
		TimestampPtr    *pgtype.Timestamp
		Date            pgtype.Date
		Time            pgtype.Time
		Interval        pgtype.Interval
		Numeric         pgtype.Numeric
		Decimal         decimal.Decimal
		DecimalPtr      *decimal.Decimal
		Jsonb           []byte
		JsonbPtr        []byte
		JSON            json.RawMessage
		TextArray       pq.StringArray
		TextArrayPtr    *pq.StringArray
		IntegerArray    pq.Int32Array
		IntegerArrayPtr *pq.Int32Array
		JsonbArray      pq.StringArray
		Mood            model.Mood
		MoodPtr         *model.Mood
	}

	stmt := SELECT(table.AllTypes.AllColumns).
		FROM(table.AllTypes).
		ORDER_BY(table.AllTypes.Serial)

	var dest []AllTypes
	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Len(t, dest, 2)

	id := uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")
	tstz := time.Date(1999, 1, 8, 12, 5, 6, 0, time.UTC)

	row := dest[0]
	require.Equal(t, id, row.UUID)
	require.Equal(t, uuid.NullUUID{UUID: id, Valid: true}, row.UUIDPtr)
	require.True(t, row.Timestampz.Valid)
	require.True(t, row.Timestampz.Time.Equal(tstz), row.Timestampz.Time)
	require.NotNil(t, row.TimestampzPtr)
	require.True(t, row.TimestampzPtr.Time.Equal(tstz))
	require.Equal(t, pgtype.Timestamp{Time: time.Date(1999, 1, 8, 4, 5, 6, 0, time.UTC), Valid: true}, row.Timestamp)
	require.Equal(t, pgtype.Date{Time: time.Date(1999, 1, 8, 0, 0, 0, 0, time.UTC), Valid: true}, row.Date)
	require.Equal(t, pgtype.Time{Microseconds: (4*3600 + 5*60 + 6) * 1_000_000, Valid: true}, row.Time)
	require.Equal(t, pgtype.Interval{Days: 3, Microseconds: (4*3600 + 5*60 + 6) * 1_000_000, Valid: true}, row.Interval)
	numeric, err := row.Numeric.Value()
	require.NoError(t, err)
	require.Equal(t, "2.220", numeric)
	require.Equal(t, "1.11", row.Decimal.String())
	require.Equal(t, "1.11", row.DecimalPtr.String())
	require.JSONEq(t, `{"a": 1, "b": 3}`, string(row.Jsonb))
	require.JSONEq(t, `{"a": 1, "b": 3}`, string(row.JsonbPtr))
	require.JSONEq(t, `{"a": 1, "b": 3}`, string(row.JSON))
	require.Equal(t, pq.StringArray{"breakfast", "consulting"}, row.TextArray)
	require.Equal(t, &pq.StringArray{"breakfast", "consulting"}, row.TextArrayPtr)
	require.Equal(t, pq.Int32Array{1, 2, 3}, row.IntegerArray)
	require.Equal(t, &pq.Int32Array{1, 2, 3}, row.IntegerArrayPtr)
	require.Equal(t, pq.StringArray{`{"a": 1, "b": 2}`, `{"a": 3, "b": 4}`}, row.JsonbArray)
	require.Equal(t, model.Mood_Happy, row.Mood)
	require.Equal(t, model.Mood_Sad, *row.MoodPtr)

	// second row: *_ptr columns are NULL
	row = dest[1]
	require.Equal(t, uuid.NullUUID{}, row.UUIDPtr)
	require.Nil(t, row.TimestampzPtr)
	require.Nil(t, row.TimestampPtr)
	require.Nil(t, row.DecimalPtr)
	require.Nil(t, row.JsonbPtr)
	require.Nil(t, row.TextArrayPtr)
	require.Nil(t, row.IntegerArrayPtr)
	require.Nil(t, row.MoodPtr)
}

// NULL values assigned to value (non-pointer) fields are left at their zero value.
func TestPgxScanNullIntoValueFields(t *testing.T) {
	stmt := SELECT(
		table.AllTypes.Serial.AS("serial"),
		table.AllTypes.TextPtr.AS("text"),
		table.AllTypes.UUIDPtr.AS("uuid"),
		table.AllTypes.IntegerPtr.AS("integer"),
		table.AllTypes.TimestampzPtr.AS("timestampz"),
		CAST(NULL).AS_TIMESTAMPZ().AS("cast_timestampz"),
		RawString("CAST(NULL AS uuid)").AS("cast_uuid"),
		RawString("CAST(NULL AS text[])").AS("cast_text_array"),
	).FROM(table.AllTypes).
		WHERE(table.AllTypes.TextPtr.IS_NULL())

	var dest struct {
		Serial         int32
		Text           string
		UUID           uuid.UUID
		Integer        int32
		Timestampz     pgtype.Timestamptz
		CastTimestampz pgtype.Timestamptz
		CastUUID       uuid.NullUUID
		CastTextArray  pq.StringArray
	}

	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.NotZero(t, dest.Serial)
	require.Empty(t, dest.Text)
	require.Equal(t, uuid.Nil, dest.UUID)
	require.Zero(t, dest.Integer)
	require.False(t, dest.Timestampz.Valid)
	require.False(t, dest.CastTimestampz.Valid)
	require.False(t, dest.CastUUID.Valid)
	require.Empty(t, dest.CastTextArray)
}

func TestPgxScanAllNullRowAndNoRows(t *testing.T) {
	var dest struct {
		Csv  string
		XMin float64
		ID   *uuid.UUID
	}

	t.Run("all NULL row", func(t *testing.T) {
		stmt := SELECT(
			RawString("NULL::text").AS("csv"),
			RawFloat("NULL::float8").AS("x_min"),
			RawString("NULL::uuid").AS("id"),
		)
		require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
		require.Empty(t, dest.Csv)
		require.Nil(t, dest.ID)
	})

	t.Run("no rows", func(t *testing.T) {
		stmt := SELECT(table.AllTypes.Text.AS("csv")).FROM(table.AllTypes).WHERE(Bool(false))
		err := pgxV5.Query(ctx, stmt, pgxPool, &dest)
		require.ErrorIs(t, err, qrm.ErrNoRows)

		var slice []model.AllTypes
		require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &slice))
		require.Empty(t, slice)
	})
}

// json/jsonb returned as raw bytes: formatting, key order and big integers must be preserved.
func TestPgxScanJsonRaw(t *testing.T) {
	stmt := SELECT(
		RawString(`'{"id": 9007199254740993, "b": 1, "a": 2}'::jsonb`).AS("jsonb"),
		RawString(`'{"z": 1,   "a": 12345678901234567890}'::json`).AS("json"),
		RawString(`jsonb_object_agg(t.k, t.v)`).AS("agg"),
	).FROM(SELECT(RawString("chr(119 + generate_series(1, 2))").AS("k"), RawInt("generate_series(1, 2)").AS("v")).AsTable("t"))

	var dest struct {
		Jsonb    []byte
		JSON     json.RawMessage
		Agg      []byte
		JsonbStr *string `alias:"jsonb"`
	}

	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Equal(t, `{"a": 2, "b": 1, "id": 9007199254740993}`, string(dest.Jsonb))
	require.Equal(t, `{"z": 1,   "a": 12345678901234567890}`, string(dest.JSON))
	require.JSONEq(t, `{"x": 1, "y": 2}`, string(dest.Agg))
}

// pgx reuses row buffers, []byte destinations must not alias them.
func TestPgxScanByteSlicesNotShared(t *testing.T) {
	stmt := SELECT(
		RawInt("s.g").AS("id"),
		RawString(`('{"i": ' || s.g || '}')::jsonb`).AS("jsonb"),
		RawString(`convert_to('bytes-' || s.g, 'UTF8')`).AS("bytea"),
	).FROM(SELECT(RawInt("generate_series(1, 50)").AS("g")).AsTable("s"))

	var dest []struct {
		ID    int64 `sql:"primary_key"`
		Jsonb []byte
		Bytea []byte
	}

	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Len(t, dest, 50)
	for i, row := range dest {
		require.Equal(t, fmt.Sprintf(`{"i": %d}`, i+1), string(row.Jsonb))
		require.Equal(t, fmt.Sprintf(`bytes-%d`, i+1), string(row.Bytea))
	}
}

func TestPgxScanArrays(t *testing.T) {
	stmt := SELECT(
		RawString(`ARRAY[1.5, 2.5]::float8[]`).AS("floats"),
		RawString(`NULL::float8[]`).AS("floats_null"),
		RawString(`ARRAY['a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11']::uuid[]`).AS("uuids"),
		RawString(`ARRAY['{"a": 1}']::jsonb[]`).AS("jsons"),
		RawString(`COALESCE(array(SELECT jsonb_array_elements_text('["x", "y"]')), ARRAY[]::text[])`).AS("texts"),
		RawString(`ARRAY[]::int[]`).AS("empty"),
		RawString(`ARRAY[true, false]`).AS("bools"),
		RawString(`ARRAY[1, 2]::bigint[]`).AS("bigints"),
	)

	var quoted struct{ Quoted pq.StringArray }

	var res struct {
		Floats     pq.Float64Array
		FloatsNull *pq.Float64Array
		Uuids      pq.StringArray
		Jsons      pq.StringArray
		Texts      pq.StringArray
		Empty      pq.Int32Array
		Bools      pq.BoolArray
		Bigints    pq.Int64Array
	}
	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &res))
	require.Equal(t, pq.Float64Array{1.5, 2.5}, res.Floats)
	require.Nil(t, res.FloatsNull)
	require.Equal(t, pq.StringArray{"a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"}, res.Uuids)
	require.Equal(t, pq.StringArray{`{"a": 1}`}, res.Jsons)
	require.Equal(t, pq.StringArray{"x", "y"}, res.Texts)
	require.Equal(t, pq.Int32Array{}, res.Empty)
	require.Equal(t, pq.BoolArray{true, false}, res.Bools)
	require.Equal(t, pq.Int64Array{1, 2}, res.Bigints)

	require.NoError(t, pgxV5.Query(ctx, SELECT(RawString(`ARRAY['a,b', 'c"d']::text[]`).AS("quoted")), pgxPool, &quoted))
	require.Equal(t, pq.StringArray{"a,b", `c"d`}, quoted.Quoted)
}

// Aggregates and casts mapped into narrower or differently typed Go fields.
func TestPgxScanAggregatesIntoCustomTypes(t *testing.T) {
	type runtimeStatus int32

	stmt := SELECT(
		COUNT(STAR).AS("count32"),
		COUNT(STAR).AS("count64"),
		SUM(table.AllTypes.BigInt).AS("sum_big_int"),
		SUM(table.AllTypes.Numeric).AS("sum_numeric"),
		MAX(table.AllTypes.Integer).AS("status"),
		CAST(MAX(table.AllTypes.BigInt)).AS_INTEGER().AS("max_int_ptr"),
		MAX(table.AllTypes.Timestampz).AS("max_timestampz"),
		RawString("(array_agg(all_types.uuid))[1]").AS("max_uuid_text"),
		RawBool("COUNT(*) FILTER (WHERE all_types.boolean_ptr IS TRUE) = 0").AS("none_true"),
	).FROM(table.AllTypes)

	var dest struct {
		Count32       int32
		Count64       int64
		SumBigInt     float64
		SumNumeric    float64
		Status        runtimeStatus
		MaxIntPtr     *int32
		MaxTimestampz pgtype.Timestamptz
		MaxUUIDText   string
		NoneTrue      bool
	}

	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Equal(t, int32(2), dest.Count32)
	require.Equal(t, int64(2), dest.Count64)
	require.Equal(t, float64(10000), dest.SumBigInt)
	require.Equal(t, 4.44, dest.SumNumeric)
	require.Equal(t, runtimeStatus(300), dest.Status)
	require.Equal(t, int32(5000), *dest.MaxIntPtr)
	require.True(t, dest.MaxTimestampz.Valid)
	require.Equal(t, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", dest.MaxUUIDText)
	require.False(t, dest.NoneTrue)
}

// uuid columns scanned into string and *string without a cast.
func TestPgxScanUUIDIntoString(t *testing.T) {
	stmt := SELECT(table.AllTypes.UUID.AS("id"), table.AllTypes.UUIDPtr.AS("id_ptr")).
		FROM(table.AllTypes).
		ORDER_BY(table.AllTypes.Serial)

	var dest []struct {
		ID    string
		IDPtr *string
	}

	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Len(t, dest, 2)
	require.Equal(t, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", dest[0].ID)
	require.Equal(t, "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11", *dest[0].IDPtr)
	require.Nil(t, dest[1].IDPtr)
}

// "TypeName.column" aliases into named struct types, alias tags and case-insensitive type names.
func TestPgxScanNamedTypeAliases(t *testing.T) {
	type LinkRow struct {
		LinkID      int64 `alias:"l.id"`
		URL         string
		Name        string
		Description *string
		ExternalID  uuid.NullUUID
		CreatedAt   pgtype.Timestamptz
	}

	stmt := SELECT(
		table.Link.ID.AS("l.id"),
		table.Link.URL.AS("LinkRow.url"),
		table.Link.Name.AS("linkrow.name"),
		table.Link.Description.AS("LinkRow.description"),
		RawString("CAST(NULL AS uuid)").AS("LinkRow.external_id"),
		RawTimestampz("'2020-01-01 00:00:00+00'::timestamptz").AS("LinkRow.created_at"),
	).FROM(table.Link).
		WHERE(table.Link.ID.IN(Int(0), Int(1))).
		ORDER_BY(table.Link.ID)

	var dest []LinkRow
	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Len(t, dest, 2)
	require.Equal(t, int64(0), dest[0].LinkID)
	require.Equal(t, "http://www.youtube.com", dest[0].URL)
	require.Equal(t, "Youtube", dest[0].Name)
	require.Equal(t, "", *dest[0].Description)
	require.False(t, dest[0].ExternalID.Valid)
	require.True(t, dest[0].CreatedAt.Time.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))
	require.Equal(t, "Gmail", dest[1].Name)
	require.NotNil(t, dest[1].Description)
}

// lib/pq array values and []byte passed as query arguments through pgx.
func TestPgxQueryArrayArguments(t *testing.T) {
	ids := []uuid.UUID{uuid.MustParse("a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11")}

	stmt := SELECT(COUNT(STAR).AS("count")).
		FROM(table.AllTypes).
		WHERE(AND(
			RawBool("all_types.uuid = ANY(CAST(#1 AS uuid[]))", RawArgs{"#1": pq.Array(ids)}),
			RawBool("all_types.integer = ANY(CAST(#2 AS int[]))", RawArgs{"#2": pq.Array([]int32{300})}),
			RawBool("all_types.text <> ALL(CAST(#3 AS text[]))", RawArgs{"#3": pq.StringArray{"nope"}}),
			RawBool("all_types.bytea = CAST(#4 AS bytea)", RawArgs{"#4": []byte("bytea")}),
		))

	var dest struct{ Count int64 }
	require.NoError(t, pgxV5.Query(ctx, stmt, pgxPool, &dest))
	require.Equal(t, int64(2), dest.Count)
}

// Database errors must be returned, and must not be reported as qrm.ErrNoRows.
func TestPgxQueryErrors(t *testing.T) {
	t.Run("error before first row", func(t *testing.T) {
		stmt := SELECT(table.AllTypes.UUID.AS("id")).
			FROM(table.AllTypes).
			WHERE(RawBool("all_types.uuid = ANY(#1)", RawArgs{"#1": pq.StringArray{"not-a-uuid"}}))

		var single struct{ ID uuid.UUID }
		err := pgxV5.Query(ctx, stmt, pgxPool, &single)
		require.Error(t, err)
		require.False(t, errors.Is(err, qrm.ErrNoRows))
		require.Contains(t, err.Error(), "invalid input syntax for type uuid")

		var slice []struct{ ID uuid.UUID }
		require.ErrorContains(t, pgxV5.Query(ctx, stmt, pgxPool, &slice), "invalid input syntax for type uuid")
	})

	t.Run("error after first row", func(t *testing.T) {
		stmt := SELECT(RawInt("10 / (3 - s.g)").AS("value")).
			FROM(SELECT(RawInt("generate_series(1, 5)").AS("g")).AsTable("s"))

		var slice []struct{ Value int64 }
		require.ErrorContains(t, pgxV5.Query(ctx, stmt, pgxPool, &slice), "division by zero")

		var single struct{ Value int64 }
		err := pgxV5.Query(ctx, stmt, pgxPool, &single)
		require.ErrorContains(t, err, "division by zero")
		require.False(t, errors.Is(err, qrm.ErrNoRows))
	})
}

// Data-modifying CTEs with RETURNING, executed through pgxV5.Query inside a pgx.Tx.
func TestPgxCteInsertDeleteReturning(t *testing.T) {
	testutils.ExecuteInTxAndRollbackPgxV5(t, pgxConn, func(tx pgx.Tx) {
		source := CTE("source")
		inserted := CTE("inserted")
		deleted := CTE("deleted")

		insertedID := table.Link.ID.From(inserted)

		stmt := WITH(
			source.AS(
				SELECT(
					RawInt("s.g + 1000").AS("id"),
					RawString("'http://example.com/' || s.g").AS("url"),
				).FROM(SELECT(RawInt("generate_series(1, 3)").AS("g")).AsTable("s")),
			),
			inserted.AS(
				table.Link.INSERT(table.Link.ID, table.Link.URL, table.Link.Name).
					QUERY(
						SELECT(IntegerColumn("id").From(source), StringColumn("url").From(source), String("cte")).
							FROM(source),
					).
					RETURNING(table.Link.ID),
			),
			deleted.AS(
				table.Link.DELETE().
					USING(inserted).
					WHERE(table.Link.ID.EQ(Int(2))).
					RETURNING(table.Link.ID),
			),
		)(
			SELECT(
				insertedID.AS("new_link_id"),
				IntExp(SELECT(table.Link.ID.From(deleted)).FROM(deleted)).AS("deleted_link_id"),
			).FROM(inserted).
				ORDER_BY(insertedID),
		)

		var dest []struct {
			NewLinkID     int64
			DeletedLinkID *int64
		}
		require.NoError(t, pgxV5.Query(ctx, stmt, tx, &dest))
		require.Len(t, dest, 3)
		for i, r := range dest {
			require.Equal(t, int64(1001+i), r.NewLinkID)
			require.Equal(t, int64(2), *r.DeletedLinkID)
		}
	})
}

// UPDATE ... MODEL with pgx typed values and jsonb []byte, executed with pgxV5.Exec, then read back.
func TestPgxUpdateModelWithPgxTypes(t *testing.T) {
	testutils.ExecuteInTxAndRollbackPgxV5(t, pgxConn, func(tx pgx.Tx) {
		ts := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
		id := uuid.MustParse("11111111-2222-3333-4444-555555555555")

		update := struct {
			TimestampzPtr *pgtype.Timestamptz
			Timestampz    pgtype.Timestamptz
			JsonbPtr      []byte
			UUIDPtr       uuid.NullUUID
			TextArrayPtr  pq.StringArray
			IntegerPtr    *int32
		}{
			TimestampzPtr: &pgtype.Timestamptz{Time: ts, Valid: true},
			Timestampz:    pgtype.Timestamptz{Time: ts, Valid: true},
			JsonbPtr:      []byte(`{"k": [1, 2]}`),
			UUIDPtr:       uuid.NullUUID{UUID: id, Valid: true},
			TextArrayPtr:  pq.StringArray{"x", "y"},
			IntegerPtr:    nil,
		}

		stmt := table.AllTypes.UPDATE(
			table.AllTypes.TimestampzPtr,
			table.AllTypes.Timestampz,
			table.AllTypes.JsonbPtr,
			table.AllTypes.UUIDPtr,
			table.AllTypes.TextArrayPtr,
			table.AllTypes.IntegerPtr,
		).MODEL(update).
			WHERE(table.AllTypes.Serial.EQ(IntExp(SELECT(MIN(table.AllTypes.Serial)).FROM(table.AllTypes))))

		result, err := pgxV5.Exec(ctx, stmt, tx)
		require.NoError(t, err)
		require.Equal(t, int64(1), result.RowsAffected())

		var readBack struct {
			TimestampzPtr *pgtype.Timestamptz
			JsonbPtr      []byte
			UUIDPtr       uuid.NullUUID
			TextArrayPtr  pq.StringArray
			IntegerPtr    *int32
		}
		err = pgxV5.Query(ctx,
			SELECT(
				table.AllTypes.TimestampzPtr.AS("timestampz_ptr"),
				table.AllTypes.JsonbPtr.AS("jsonb_ptr"),
				table.AllTypes.UUIDPtr.AS("uuid_ptr"),
				table.AllTypes.TextArrayPtr.AS("text_array_ptr"),
				table.AllTypes.IntegerPtr.AS("integer_ptr"),
			).FROM(table.AllTypes).
				WHERE(table.AllTypes.UUIDPtr.EQ(UUID(id))),
			tx, &readBack)
		require.NoError(t, err)
		require.True(t, readBack.TimestampzPtr.Time.Equal(ts))
		require.JSONEq(t, `{"k": [1, 2]}`, string(readBack.JsonbPtr))
		require.Equal(t, uuid.NullUUID{UUID: id, Valid: true}, readBack.UUIDPtr)
		require.Equal(t, pq.StringArray{"x", "y"}, readBack.TextArrayPtr)
		require.Nil(t, readBack.IntegerPtr)

		t.Run("no rows affected", func(t *testing.T) {
			result, err := pgxV5.Exec(ctx, stmt.WHERE(Bool(false)), tx)
			require.NoError(t, err)
			require.Equal(t, int64(0), result.RowsAffected())
		})
	})
}
