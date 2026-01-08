package internal

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/go-jet/jet/v2/internal/utils/datetime"
)

var (
	errCastOverFlow = fmt.Errorf("cannot cast a negative value to an unsigned value, buffer overflow error")
)

// float64Result is used to extract float64 from types that implement Float64Value() method,
// like pgtype.Numeric in pgx v5.8.0+
type float64Result struct {
	Float64 float64
	Valid   bool
}

// NullFloat64 struct - handles sql.NullFloat64 with additional support for pgtype.Numeric
type NullFloat64 struct {
	sql.NullFloat64
}

// Scan implements the Scanner interface with support for pgtype.Numeric and similar types
func (nf *NullFloat64) Scan(value interface{}) error {
	if value == nil {
		nf.Valid = false
		return nil
	}

	// Try to use Float64Value() method if available (pgtype.Numeric, etc.)
	if f64Result, ok := tryFloat64Value(value); ok {
		nf.Float64 = f64Result.Float64
		nf.Valid = f64Result.Valid
		return nil
	}

	// Fall back to standard sql.NullFloat64 scanning
	return nf.NullFloat64.Scan(value)
}

// tryFloat64Value attempts to call Float64Value() method on value using reflection
// This handles pgtype.Numeric and similar types from pgx v5.8.0+ without importing pgtype
func tryFloat64Value(value interface{}) (float64Result, bool) {
	v := reflect.ValueOf(value)

	// Look for Float64Value() method
	method := v.MethodByName("Float64Value")
	if !method.IsValid() {
		return float64Result{}, false
	}

	// Call the method
	results := method.Call(nil)
	if len(results) != 2 {
		return float64Result{}, false
	}

	// Check for error (second return value)
	if !results[1].IsNil() {
		return float64Result{}, false
	}

	// Extract the Float8/float64 result struct (first return value)
	// pgtype.Float8 has Float64 and Valid fields
	resultVal := results[0]
	float64Field := resultVal.FieldByName("Float64")
	validField := resultVal.FieldByName("Valid")

	if !float64Field.IsValid() || !validField.IsValid() {
		return float64Result{}, false
	}

	return float64Result{
		Float64: float64Field.Float(),
		Valid:   validField.Bool(),
	}, true
}

// NullBool struct
type NullBool struct {
	sql.NullBool
}

// Scan implements the Scanner interface.
func (nb *NullBool) Scan(value interface{}) error {
	switch v := value.(type) {
	case bool:
		nb.Bool, nb.Valid = v, true
	case int8, int16, int32, int64, int:
		intVal := reflect.ValueOf(v).Int()

		if intVal != 0 && intVal != 1 {
			return fmt.Errorf("can't assign %T(%d) to bool", value, value)
		}

		nb.Bool = intVal == 1
		nb.Valid = true
	case uint8, uint16, uint32, uint64, uint:
		uintVal := reflect.ValueOf(v).Uint()

		if uintVal != 0 && uintVal != 1 {
			return fmt.Errorf("can't assign %T(%d) to bool", value, value)
		}

		nb.Bool = uintVal == 1
		nb.Valid = true
	default:
		return nb.NullBool.Scan(value)
	}

	return nil
}

// NullTime struct
type NullTime struct {
	sql.NullTime
}

// Scan implements the Scanner interface.
func (nt *NullTime) Scan(value interface{}) error {
	if value == nil {
		nt.Valid = false
		return nil
	}

	// Try to extract time.Time from pgtype types that have a Time field with Valid bool
	// This handles pgtype.Timestamp, pgtype.Timestamptz, pgtype.Date
	if t, valid, ok := tryExtractTimeFromPgType(value); ok {
		if valid {
			nt.Time = t
			nt.Valid = true
		} else {
			nt.Valid = false
		}
		return nil
	}

	// Try standard sql.NullTime scanning
	err := nt.NullTime.Scan(value)

	if err == nil {
		return nil
	}

	// Some of the drivers (pgx, mysql) are not parsing all of the time formats(date, time with time zone,...) and are just forwarding string value.
	// At this point we try to parse those values using some of the predefined formats
	nt.Time, nt.Valid = datetime.TryParseAsTime(value, []string{
		"2006-01-02 15:04:05-07:00",  // sqlite
		"2006-01-02 15:04:05.999999", // go-sql-driver/mysql
		"15:04:05-07",                // pgx
		"15:04:05.999999",            // pgx
	})

	if !nt.Valid {
		return fmt.Errorf("can't scan time.Time from %q", value)
	}

	return nil
}

// tryExtractTimeFromPgType attempts to extract time.Time from pgtype structs
// This handles pgtype.Timestamp, pgtype.Timestamptz, pgtype.Date which have Time and Valid fields
// And also handles pgtype.Time which has Microseconds and Valid fields
func tryExtractTimeFromPgType(value interface{}) (time.Time, bool, bool) {
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return time.Time{}, false, true
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return time.Time{}, false, false
	}

	// Check for Valid field
	validField := v.FieldByName("Valid")
	if !validField.IsValid() || validField.Kind() != reflect.Bool {
		return time.Time{}, false, false
	}

	valid := validField.Bool()

	// Try Time field (pgtype.Timestamp, pgtype.Timestamptz, pgtype.Date)
	timeField := v.FieldByName("Time")
	if timeField.IsValid() {
		if t, ok := timeField.Interface().(time.Time); ok {
			return t, valid, true
		}
	}

	// Try Microseconds field (pgtype.Time)
	microField := v.FieldByName("Microseconds")
	if microField.IsValid() && microField.Kind() == reflect.Int64 {
		if !valid {
			return time.Time{}, false, true
		}
		// Convert microseconds since midnight to time.Time (using 2000-01-01 as base date, same as pgx)
		usec := microField.Int()
		const microsecondsPerHour = 60 * 60 * 1_000_000
		const microsecondsPerMinute = 60 * 1_000_000
		const microsecondsPerSecond = 1_000_000

		hours := usec / microsecondsPerHour
		usec -= hours * microsecondsPerHour
		minutes := usec / microsecondsPerMinute
		usec -= minutes * microsecondsPerMinute
		seconds := usec / microsecondsPerSecond
		usec -= seconds * microsecondsPerSecond
		ns := usec * 1000

		t := time.Date(2000, 1, 1, int(hours), int(minutes), int(seconds), int(ns), time.UTC)
		return t, true, true
	}

	return time.Time{}, false, false
}

// NullUInt64 struct
type NullUInt64 struct {
	UInt64 uint64
	Valid  bool
}

// Scan implements the Scanner interface.
func (n *NullUInt64) Scan(value interface{}) error {
	var stringValue string
	switch v := value.(type) {
	case nil:
		n.Valid = false
		return nil
	case int64:
		if v < 0 {
			return errCastOverFlow
		}
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case int32:
		if v < 0 {
			return errCastOverFlow
		}
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case int16:
		if v < 0 {
			return errCastOverFlow
		}
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case int8:
		if v < 0 {
			return errCastOverFlow
		}
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case int:
		if v < 0 {
			return errCastOverFlow
		}
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case uint64:
		n.UInt64, n.Valid = v, true
		return nil
	case uint32:
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case uint16:
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case uint8:
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case uint:
		n.UInt64, n.Valid = uint64(v), true
		return nil
	case []byte:
		stringValue = string(v)
	case string:
		stringValue = v
	default:
		return fmt.Errorf("can't scan uint64 from %v", value)
	}

	uintV, err := strconv.ParseUint(stringValue, 10, 64)
	if err != nil {
		return err
	}
	n.UInt64 = uintV
	n.Valid = true

	return nil
}

// Value implements the driver Valuer interface.
func (n NullUInt64) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	return n.UInt64, nil
}

// NullString struct - handles sql.NullString with additional support for pgtype values
type NullString struct {
	sql.NullString
}

// Scan implements the Scanner interface with support for pgtype values
func (ns *NullString) Scan(value interface{}) error {
	if value == nil {
		ns.Valid = false
		return nil
	}

	// Try standard sql.NullString scanning first
	err := ns.NullString.Scan(value)
	if err == nil {
		return nil
	}

	// For types that implement driver.Valuer (like pgtype.Interval), try to get string from Value()
	if valuer, ok := value.(driver.Valuer); ok {
		driverVal, valErr := valuer.Value()
		if valErr == nil && driverVal != nil {
			if str, ok := driverVal.(string); ok {
				ns.String = str
				ns.Valid = true
				return nil
			}
		}
	}

	// For types that implement fmt.Stringer, use String() method
	if stringer, ok := value.(fmt.Stringer); ok {
		ns.String = stringer.String()
		ns.Valid = true
		return nil
	}

	// For maps and slices (e.g., parsed JSON from pgx), serialize to JSON
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map, reflect.Slice:
		data, jsonErr := json.Marshal(value)
		if jsonErr == nil {
			ns.String = string(data)
			ns.Valid = true
			return nil
		}
	}

	// Last resort: use fmt.Sprintf to convert to string
	ns.String = fmt.Sprintf("%v", value)
	ns.Valid = true
	return nil
}

// TryConvertInterfaceSlice attempts to convert a []interface{} to the destination slice type.
// This is needed when pgx returns arrays as []interface{} but the destination is a typed slice
// like pq.Int32Array (which is []int32).
// Returns true if conversion was successful and the destination was set.
func TryConvertInterfaceSlice(source interface{}, destValue reflect.Value) bool {
	sourceSlice, ok := source.([]interface{})
	if !ok {
		return false
	}

	// Get the destination type - it should be a slice or ptr to slice
	destType := destValue.Type()
	if destValue.Kind() == reflect.Ptr {
		if destValue.IsNil() {
			destValue.Set(reflect.New(destType.Elem()))
		}
		destValue = destValue.Elem()
		destType = destType.Elem()
	}

	if destType.Kind() != reflect.Slice {
		return false
	}

	elemType := destType.Elem()

	// Create a new slice of the destination type
	newSlice := reflect.MakeSlice(destType, len(sourceSlice), len(sourceSlice))

	for i, elem := range sourceSlice {
		if elem == nil {
			continue // leave as zero value
		}

		elemValue := reflect.ValueOf(elem)
		destElemValue := newSlice.Index(i)

		// Try direct assignment first
		if elemValue.Type().AssignableTo(elemType) {
			destElemValue.Set(elemValue)
			continue
		}

		// Try conversion
		if elemValue.Type().ConvertibleTo(elemType) {
			destElemValue.Set(elemValue.Convert(elemType))
			continue
		}

		// For numeric types, try converting through int64/float64
		if tryConvertNumeric(elemValue, destElemValue, elemType) {
			continue
		}

		// Special case: when destination is string and source is a map or other complex type,
		// try to serialize to JSON. This handles cases like jsonb[] -> pq.StringArray
		if elemType.Kind() == reflect.String {
			if jsonBytes, ok := TryMarshalToJSONBytes(elem); ok {
				destElemValue.SetString(string(jsonBytes))
				continue
			}
		}

		// Special case: when destination is []byte and source is a map or other complex type,
		// try to serialize to JSON. This handles cases like jsonb[] -> pq.ByteaArray
		if elemType.Kind() == reflect.Slice && elemType.Elem().Kind() == reflect.Uint8 {
			if jsonBytes, ok := TryMarshalToJSONBytes(elem); ok {
				destElemValue.SetBytes(jsonBytes)
				continue
			}
		}

		// If we can't convert this element, give up
		return false
	}

	destValue.Set(newSlice)
	return true
}

// tryConvertNumeric handles numeric type conversions
func tryConvertNumeric(src reflect.Value, dest reflect.Value, destType reflect.Type) bool {
	switch destType.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch src.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			dest.SetInt(src.Int())
			return true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			dest.SetInt(int64(src.Uint()))
			return true
		case reflect.Float32, reflect.Float64:
			dest.SetInt(int64(src.Float()))
			return true
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch src.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			dest.SetUint(uint64(src.Int()))
			return true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			dest.SetUint(src.Uint())
			return true
		case reflect.Float32, reflect.Float64:
			dest.SetUint(uint64(src.Float()))
			return true
		}
	case reflect.Float32, reflect.Float64:
		switch src.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			dest.SetFloat(float64(src.Int()))
			return true
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			dest.SetFloat(float64(src.Uint()))
			return true
		case reflect.Float32, reflect.Float64:
			dest.SetFloat(src.Float())
			return true
		}
	case reflect.String:
		if src.Kind() == reflect.String {
			dest.SetString(src.String())
			return true
		}
	}
	return false
}

// TryMarshalToJSONBytes attempts to serialize a value to JSON bytes.
// This is useful when pgx parses JSON/JSONB values into Go maps/slices but
// the destination expects []byte.
func TryMarshalToJSONBytes(value interface{}) ([]byte, bool) {
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Struct:
		data, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		return data, true
	}
	return nil, false
}
