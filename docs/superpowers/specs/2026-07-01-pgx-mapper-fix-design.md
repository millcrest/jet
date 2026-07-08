# PGX Mapper Fix Design

## Context

The `pgx` branch adds a `pgxV5` query path that scans rows into `interface{}` values and then reuses Jet's reflection mapper. Two gaps have shown up in product use:

- PostgreSQL `uuid` values returned by pgx arrive as `[16]byte`, which does not scan cleanly into `github.com/google/uuid.NullUUID`.
- Destination fields such as `map[string][]string` are classified as nested complex destinations and can panic with `jet: unsupported dest type` instead of being mapped from a single JSON/JSONB column.

## Goals

- Support `uuid.UUID`, `*uuid.UUID`, and `uuid.NullUUID` in the pgx row mapper.
- Treat map fields with a matching selected column as single-column JSON destinations without requiring a `json_column` tag.
- Preserve `[]byte` destination support for JSON/JSONB expressions that pgx decodes into Go map or slice values when rows are first scanned into `interface{}`.
- Preserve existing nested struct and slice mapping behavior.
- Return normal query mapping errors for unsupported values instead of panicking for map fields.
- Add focused tests that cover pgx-specific UUID, JSON map, and byte-slice behavior.

## Non-Goals

- Rewrite pgx scanning to scan directly into destination field types.
- Change SQL generation or generator output.
- Change existing `json_column` behavior.
- Broaden nested relation mapping semantics for slices or structs.

## Approach

Add a targeted mapper change in `qrm`.

For UUIDs, normalize pgx's `[16]byte` UUID value before invoking `sql.Scanner`. The normalized value should be a `[]byte` containing the 16 UUID bytes, which is already accepted by `google/uuid.UUID.Scan` and therefore by `uuid.NullUUID.Scan`.

For maps, extend field classification so a map field with a matching row column is treated as a JSON single-column mapping. This should apply without `json_column` tags. Existing `json_column` tags continue to force JSON unmarshalling for the tagged field.

For byte slices, keep direct `[]byte` assignment for normal `bytea` values. If the source is a pgx-decoded JSON value such as `map[string]any`, `[]any`, or a scalar JSON value, marshal that value back to JSON bytes before assigning it to the destination field.

## Data Flow

1. `pgxV5.Query` scans row values into the existing `ScanContext.row`.
2. `ScanContext.getTypeInfo` classifies each destination field.
3. If a field is a map and has a matching row column, it is classified as JSON-backed rather than nested complex.
4. During assignment:
   - `nil` sets the map to its zero value.
   - `[]byte`, `string`, or `json.RawMessage` is unmarshaled with `encoding/json`.
   - A driver value that is directly assignable to the destination map is assigned.
   - Other values return a mapping error with field context.
5. If a destination field is `[]byte`, direct byte values are cloned. Non-byte JSON-compatible values are marshaled to JSON and assigned as the raw JSON bytes.

## Error Handling

Map field failures should return errors through the normal `qrmAssignError` path. Invalid JSON should include `invalid json` in the wrapped error. Non-JSON values should report that the value is not convertible to JSON bytes or assignable to the destination map. The previous unsupported-destination panic should no longer occur for map fields with matching columns.

## Testing

Add tests for:

- pgx scan into `uuid.NullUUID` for non-null and null UUID columns.
- pgx scan from JSON/JSONB into `map[string][]string` with no tags.
- pgx scan from JSON/JSONB into `[]byte` fields when pgx returns decoded map or slice values.
- existing `json_column` behavior to confirm it still works.
- unsupported map source values to confirm they return errors instead of panicking.

Verification should include the root module test suite and the pgx-related postgres tests from the nested `tests` module.
