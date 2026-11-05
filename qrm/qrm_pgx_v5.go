package qrm

import (
	"context"
	"database/sql/driver"
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

	scanContext := newScanContextPgxV5(rows)

	if len(scanContext.row) == 0 {
		return
	}

	slicePtrValue := reflect.ValueOf(slicePtr)
	scanTargets := newPgxScanTargets(scanContext.row, rows.FieldDescriptions())

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

		if scanContext.rowNum == 1 {
			scanContext.ensureStrictness()
		}
	}

	rowsProcessed = scanContext.rowNum
	return
}

func newScanContextPgxV5(rows pgx.Rows) *ScanContext {
	fields := rows.FieldDescriptions()
	aliases := make([]string, len(fields))
	for i, field := range fields {
		aliases[i] = field.Name
	}

	return newScanContext(aliases)
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
// column with Codec.DecodeDatabaseSQLValue, producing the same database/sql compatible values (int64, float64, bool,
// time.Time, []byte) as the pgx stdlib driver, instead of pgx native Go types (map[string]any for json, [16]byte for
// uuid, []any for arrays, pgtype.Numeric, ...). pgx codecs return []byte values that are already copied out of the
// row buffer, so the value can be retained after the next row is read.
type pgxDriverValue struct {
	dest *interface{}
}

func (v *pgxDriverValue) Scan(src any) error {
	*v.dest = src
	return nil
}

// pgxTextValue is a pgx scan target that implements pgtype.TextScanner. pgx passes text format values to it
// undecoded, so non-intrinsic types (numeric, uuid, interval, arrays, enums, extension types...) reach QRM as the
// exact text postgres sent, the same as the pgx stdlib and lib/pq drivers, without being parsed and re-formatted.
type pgxTextValue struct {
	pgxDriverValue
}

func (v *pgxTextValue) ScanText(text pgtype.Text) error {
	if !text.Valid {
		*v.dest = nil
		return nil
	}
	*v.dest = text.String
	return nil
}

// pgxIntValue, pgxFloatValue, pgxBoolValue, pgxTimestampValue, pgxTimestamptzValue and pgxDateValue receive
// intrinsic types through pgx typed scanner interfaces, the same way the pgx stdlib driver scans them, and produce
// the same database/sql values. They avoid the generic Codec.DecodeDatabaseSQLValue path, which re-plans and boxes
// every value.
//
// All scan targets embed pgxDriverValue, so they are also sql.Scanner. pgx always prefers the typed scanner
// interfaces, but pgx.Rows implementations that only understand sql.Scanner (test doubles, wrappers) keep working.
type pgxIntValue struct{ pgxDriverValue }

func (v *pgxIntValue) ScanInt64(n pgtype.Int8) error {
	return setPgxValue(v.dest, n.Int64, n.Valid)
}

type pgxFloatValue struct{ pgxDriverValue }

func (v *pgxFloatValue) ScanFloat64(f pgtype.Float8) error {
	return setPgxValue(v.dest, f.Float64, f.Valid)
}

type pgxBoolValue struct{ pgxDriverValue }

func (v *pgxBoolValue) ScanBool(b pgtype.Bool) error {
	return setPgxValue(v.dest, b.Bool, b.Valid)
}

type pgxTimestampValue struct{ pgxDriverValue }

func (v *pgxTimestampValue) ScanTimestamp(ts pgtype.Timestamp) error {
	return setPgxValuer(v.dest, ts)
}

type pgxTimestamptzValue struct{ pgxDriverValue }

func (v *pgxTimestamptzValue) ScanTimestamptz(ts pgtype.Timestamptz) error {
	return setPgxValuer(v.dest, ts)
}

type pgxDateValue struct{ pgxDriverValue }

func (v *pgxDateValue) ScanDate(d pgtype.Date) error {
	return setPgxValuer(v.dest, d)
}

func setPgxValue[T any](dest *interface{}, value T, valid bool) error {
	if !valid {
		*dest = nil
		return nil
	}
	*dest = value
	return nil
}

func setPgxValuer(dest *interface{}, valuer driver.Valuer) (err error) {
	*dest, err = valuer.Value() // nil for NULL, string for infinity, otherwise time.Time
	return err
}

// newPgxScanTargets creates, once per result set, a scan target for each column of the row.
func newPgxScanTargets(row []interface{}, fields []pgconn.FieldDescription) []interface{} {
	targets := make([]interface{}, len(row))
	for i := range row {
		dest := row[i].(*interface{})

		if i >= len(fields) {
			targets[i] = &pgxDriverValue{dest: dest}
			continue
		}

		switch oid := fields[i].DataTypeOID; {
		case oid == pgtype.Int2OID || oid == pgtype.Int4OID || oid == pgtype.Int8OID:
			targets[i] = &pgxIntValue{pgxDriverValue{dest: dest}}
		case oid == pgtype.Float4OID || oid == pgtype.Float8OID:
			targets[i] = &pgxFloatValue{pgxDriverValue{dest: dest}}
		case oid == pgtype.BoolOID:
			targets[i] = &pgxBoolValue{pgxDriverValue{dest: dest}}
		case oid == pgtype.TimestampOID:
			targets[i] = &pgxTimestampValue{pgxDriverValue{dest: dest}}
		case oid == pgtype.TimestamptzOID:
			targets[i] = &pgxTimestamptzValue{pgxDriverValue{dest: dest}}
		case oid == pgtype.DateOID:
			targets[i] = &pgxDateValue{pgxDriverValue{dest: dest}}
		case fields[i].Format == pgtype.TextFormatCode && !pgxDecodedOIDs[oid]:
			targets[i] = &pgxTextValue{pgxDriverValue{dest: dest}}
		default:
			targets[i] = &pgxDriverValue{dest: dest}
		}
	}
	return targets
}

// pgxDecodedOIDs are types decoded by pgx codecs even when received in text format (for instance when the simple
// protocol is used): intrinsic types become int64, float64, bool, time.Time or []byte, and json becomes []byte.
var pgxDecodedOIDs = func() map[uint32]bool {
	oids := map[uint32]bool{pgtype.JSONOID: true, pgtype.JSONBOID: true}
	for oid := range pgxResultFormats {
		oids[oid] = true
	}
	return oids
}()

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
