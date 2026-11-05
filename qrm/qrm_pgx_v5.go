package qrm

import (
	"bytes"
	"context"
	"fmt"
	"github.com/go-jet/jet/v2/internal/utils/must"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"reflect"
)

// QueryablePgxV5 interface for pgx Query method
type QueryablePgxV5 interface {
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)
}

// ExecutablePgxV5 interface for pgx Exec method
type ExecutablePgxV5 interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// QueryJsonObjPgxV5 executes a SQL query that returns a JSON object, unmarshals the result into the provided destination,
// and returns the number of rows processed.
//
// The query must return exactly one row with a single column; otherwise, an error is returned.
//
// Parameters:
//
//	ctx      - The context for managing query execution (timeouts, cancellations).
//	db       - The database connection or transaction that implements the QueryablePGX interface.
//	query    - The SQL query string to be executed.
//	args     - A slice of arguments to be used with the query.
//	destPtr  - A pointer to the variable where the unmarshaled JSON result will be stored.
//	          The destination should be a pointer to a struct or map[string]any.
//
// Returns:
//
//	rowsProcessed - The number of rows processed by the query execution.
//	err           - An error if query execution or unmarshaling fails.
func QueryJsonObjPgxV5(ctx context.Context, db QueryablePgxV5, query string, args []interface{}, destPtr interface{}) (rowsProcessed int64, err error) {
	must.BeInitializedPtr(destPtr, "jet: destination is nil")
	must.BeTypeKind(destPtr, reflect.Ptr, jsonDestObjErr)
	destType := reflect.TypeOf(destPtr).Elem()
	must.BeTrue(destType.Kind() == reflect.Struct || destType.Kind() == reflect.Map, jsonDestObjErr)

	return queryJsonPgxV5(ctx, db, query, args, destPtr)
}

// QueryJsonArrPgxV5 executes a SQL query that returns a JSON array, unmarshals the result into the provided destination,
// and returns the number of rows processed.
//
// The query must return exactly one row with a single column; otherwise, an error is returned.
//
// Parameters:
//
//	ctx      - The context for managing query execution (timeouts, cancellations).
//	db       - The database connection or transaction that implements the QueryablePGX interface.
//	query    - The SQL query string to be executed.
//	args     - A slice of arguments to be used with the query.
//	destPtr  - A pointer to the variable where the unmarshaled JSON array will be stored.
//	          The destination should be a pointer to a slice of structs or []map[string]any.
//
// Returns:
//
//	rowsProcessed - The number of rows processed by the query execution.
//	err           - An error if query execution or unmarshaling fails.
func QueryJsonArrPgxV5(ctx context.Context, db QueryablePgxV5, query string, args []interface{}, destPtr interface{}) (rowsProcessed int64, err error) {
	must.BeInitializedPtr(destPtr, "jet: destination is nil")
	must.BeTypeKind(destPtr, reflect.Ptr, jsonDestArrErr)
	destType := reflect.TypeOf(destPtr).Elem()
	must.BeTrue(destType.Kind() == reflect.Slice, jsonDestArrErr)

	return queryJsonPgxV5(ctx, db, query, args, destPtr)
}

// QueryPgxV5 executes Query Result Mapping (QRM) of `query` with list of parametrized arguments `arg` over database connection `db`
// using context `ctx` into destination `destPtr`.
// Destination can be either pointer to struct or pointer to slice of structs.
// If destination is pointer to struct and query result set is empty, method returns qrm.ErrNoRows.
func QueryPgxV5(ctx context.Context, db QueryablePgxV5, query string, args []interface{}, destPtr interface{}) (rowsProcessed int64, err error) {

	must.BeInitializedPtr(db, "jet: db is nil")
	must.BeInitializedPtr(destPtr, "jet: destination is nil")
	must.BeTypeKind(destPtr, reflect.Ptr, "jet: destination has to be a pointer to slice or pointer to struct")

	destinationPtrType := reflect.TypeOf(destPtr)

	if destinationPtrType.Elem().Kind() == reflect.Slice {
		rowsProcessed, err := queryToSlicePgxV5(ctx, db, query, args, destPtr)
		if err != nil {
			return rowsProcessed, fmt.Errorf("jet: %w", err)
		}
		return rowsProcessed, nil
	} else if destinationPtrType.Elem().Kind() == reflect.Struct {
		tempSlicePtrValue := reflect.New(reflect.SliceOf(destinationPtrType))
		tempSliceValue := tempSlicePtrValue.Elem()

		rowsProcessed, err := queryToSlicePgxV5(ctx, db, query, args, tempSlicePtrValue.Interface())

		if err != nil {
			return rowsProcessed, fmt.Errorf("jet: %w", err)
		}

		if rowsProcessed == 0 {
			return 0, ErrNoRows
		}

		// edge case when row result set contains only NULLs.
		if tempSliceValue.Len() == 0 {
			return rowsProcessed, nil
		}

		structValue := reflect.ValueOf(destPtr).Elem()
		firstTempStruct := tempSliceValue.Index(0).Elem()

		if structValue.Type().AssignableTo(firstTempStruct.Type()) {
			structValue.Set(tempSliceValue.Index(0).Elem())
		}
		return rowsProcessed, nil
	} else {
		panic("jet: destination has to be a pointer to slice or pointer to struct")
	}
}

func queryToSlicePgxV5(ctx context.Context, db QueryablePgxV5, query string, args []interface{}, slicePtr interface{}) (rowsProcessed int64, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	rows, err := db.Query(ctx, query, withPgxResultFormats(args)...)

	if err != nil {
		return
	}
	defer func() {
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
	}()

	scanContext, err := NewScanContextPGXv5(rows)

	if err != nil {
		return
	}

	if len(scanContext.row) == 0 {
		return
	}

	slicePtrValue := reflect.ValueOf(slicePtr)
	scanTargets := newPgxScanTargets(scanContext.row)

	for rows.Next() {
		err = rows.Scan(scanTargets...)

		if err != nil {
			return scanContext.rowNum, err
		}

		scanContext.rowNum++

		_, err = mapRowToSlice(scanContext, "", slicePtrValue, nil)

		if err != nil {
			return scanContext.rowNum, err
		}
	}

	rowsProcessed = scanContext.rowNum
	return
}

func queryJsonPgxV5(ctx context.Context, db QueryablePgxV5, query string, args []interface{}, destPtr interface{}) (rowsProcessed int64, err error) {
	must.BeInitializedPtr(db, "jet: db is nil")

	var rows pgx.Rows
	rows, err = db.Query(ctx, query, args...)

	if err != nil {
		return 0, err
	}

	defer rows.Close()

	if !rows.Next() {
		err = rows.Err()
		if err != nil {
			return 0, err
		}
		return 0, ErrNoRows
	}

	var jsonData []byte
	err = rows.Scan(&jsonData)

	if err != nil {
		return 1, err
	}

	if jsonData == nil {
		return 1, nil
	}

	err = GlobalConfig.JsonUnmarshalFunc(jsonData, &destPtr)

	if err != nil {
		return 1, fmt.Errorf("jet: invalid json, %w", err)
	}

	if rows.Next() {
		return 1, fmt.Errorf("jet: query returned more then one row")
	}

	rows.Close()

	return 1, nil
}

// pgxDriverValue is a pgx scan target that implements sql.Scanner. For sql.Scanner targets pgx decodes the
// column with Codec.DecodeDatabaseSQLValue, producing the same database/sql compatible values (string, []byte,
// int64, float64, bool, time.Time) as the pgx stdlib driver, instead of pgx native Go types
// (map[string]any for json, [16]byte for uuid, []any for arrays, pgtype.Numeric, ...). This allows QRM
// to map pgx rows exactly like database/sql rows.
type pgxDriverValue struct {
	dest *interface{}
}

func (v *pgxDriverValue) Scan(src any) error {
	if b, ok := src.([]byte); ok {
		src = bytes.Clone(b) // pgx reuses row buffers between rows
	}
	*v.dest = src
	return nil
}

func newPgxScanTargets(row []interface{}) []interface{} {
	targets := make([]interface{}, len(row))
	for i := range row {
		targets[i] = &pgxDriverValue{dest: row[i].(*interface{})}
	}
	return targets
}

// pgxResultFormats mirrors pgx stdlib (database/sql) driver: only intrinsic types are received in binary format,
// every other type (arrays, json, uuid, numeric, enums, extension types...) is received in text format, the same way
// lib/pq receives them. Combined with pgxDriverValue, rows scanned through pgx are mapped exactly as database/sql rows.
var pgxResultFormats = pgx.QueryResultFormatsByOID{
	pgtype.BoolOID:        pgtype.BinaryFormatCode,
	pgtype.ByteaOID:       pgtype.BinaryFormatCode,
	pgtype.CIDOID:         pgtype.BinaryFormatCode,
	pgtype.DateOID:        pgtype.BinaryFormatCode,
	pgtype.Float4OID:      pgtype.BinaryFormatCode,
	pgtype.Float8OID:      pgtype.BinaryFormatCode,
	pgtype.Int2OID:        pgtype.BinaryFormatCode,
	pgtype.Int4OID:        pgtype.BinaryFormatCode,
	pgtype.Int8OID:        pgtype.BinaryFormatCode,
	pgtype.OIDOID:         pgtype.BinaryFormatCode,
	pgtype.TimestampOID:   pgtype.BinaryFormatCode,
	pgtype.TimestamptzOID: pgtype.BinaryFormatCode,
	pgtype.XIDOID:         pgtype.BinaryFormatCode,
}

func withPgxResultFormats(args []interface{}) []interface{} {
	return append([]interface{}{pgxResultFormats}, args...)
}
