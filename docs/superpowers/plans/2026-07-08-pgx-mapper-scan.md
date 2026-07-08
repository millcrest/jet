# PGX Mapper Scan Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix pgx row mapping for `uuid.NullUUID`, JSON/JSONB map fields, and JSON/JSONB output scanned into `[]byte` fields.

**Architecture:** Keep the existing pgx `interface{}` row scan path and improve Jet's mapper normalization and assignment logic. Map fields are treated as JSON-backed scalar fields, pgx UUID byte arrays are normalized before scanner calls, and `[]byte` destinations can receive pgx-decoded JSON values by marshaling them back to raw JSON.

**Tech Stack:** Go, Jet `qrm`, pgx v5, `encoding/json`, `github.com/google/uuid`, existing postgres integration tests.

---

### Task 1: Add Focused Tests

**Files:**
- Modify: `qrm/utill_test.go`
- Modify: `tests/postgres/pgx_test.go`

- [x] **Step 1: Add unit tests for JSON byte assignment helpers**

Add tests in `qrm/utill_test.go` that verify assigning `map[string]any` and `[]any` into `[]byte` produces valid raw JSON bytes, while direct `[]byte` assignment is cloned.

- [x] **Step 2: Add pgx integration tests**

Add tests in `tests/postgres/pgx_test.go` for non-null and null `uuid.NullUUID`, untagged `map[string][]string`, and `[]byte` JSON outputs.

- [x] **Step 3: Run targeted tests**

Run `go test ./qrm -run Test -count=1` from the repo root and `go test ./postgres -run TestUUIDTypePGX -count=1` when the local Go toolchain supports the standard library. Run `go test ./postgres -run 'TestPGX.*JSON|TestUUIDTypePGX' -count=1` from `tests/` when the postgres test database is available.

### Task 2: Implement Mapper Fixes

**Files:**
- Modify: `qrm/scan_context.go`
- Modify: `qrm/qrm.go`
- Modify: `qrm/utill.go`

- [x] **Step 1: Classify map fields as JSON-backed scalar fields**

Update `ScanContext.getTypeInfo` so `map[...]...` fields use the JSON assignment path instead of `complexType`, even when no matching column exists.

- [x] **Step 2: Normalize pgx UUID byte arrays**

Replace the narrow `pgxUUIDPatch` shortcut with a scanner-input normalization helper that converts `[16]byte` values into `[]byte` before calling `sql.Scanner`.

- [x] **Step 3: Add JSON assignment support**

Update JSON assignment so `[]byte`, `string`, and `json.RawMessage` are treated as raw JSON and other Go JSON values are marshaled before unmarshalling into maps. Update simple `[]byte` assignment so pgx-decoded JSON maps and slices marshal back to raw JSON bytes.

- [x] **Step 4: Run targeted tests again**

Run the same targeted test commands from Task 1 and record any environment blockers.

### Task 3: Review and Commit

**Files:**
- Modified files from Tasks 1 and 2
- Docs: `docs/superpowers/specs/2026-07-01-pgx-mapper-fix-design.md`
- Docs: `docs/superpowers/plans/2026-07-08-pgx-mapper-scan.md`

- [x] **Step 1: Review diff**

Run `git diff --stat` and inspect the changed mapper code for unintended behavior changes outside the pgx compatibility path.

- [x] **Step 2: Commit**

Commit the branch with a message such as `fix: support pgx json and nullable uuid scans`.
