# Plan: Migrate dyngeo to AWS SDK v2 + Add Caller-Facing Pagination

## Context

The `dyngeo` library uses AWS SDK for Go **v1** (`github.com/aws/aws-sdk-go`), which is in maintenance mode. It also lacks caller-facing pagination — `QueryRadius` and `QueryRectangle` eagerly return all results. Since the SDK migration already breaks the public API, pagination can be added in the same pass.

Additionally, the project has no `go.mod` (pre-modules era) and depends on `github.com/imdario/mergo` for struct merging, which doesn't work well with v2's interface-based `types.AttributeValue`. Both issues are addressed.

---

## Step 1: Initialize Go Modules

- Run `go mod init github.com/crolly/dyngeo`
- New dependencies:
  - `github.com/aws/aws-sdk-go-v2`
  - `github.com/aws/aws-sdk-go-v2/service/dynamodb`
  - `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue`
  - `github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression`
- Removed dependency: `github.com/imdario/mergo`
- Kept: `github.com/golang/geo`, `github.com/gofrs/uuid`

---

## Step 2: `config.go` — Client Type Migration

- Change `DynamoDBClient` field from `*dynamodb.DynamoDB` to `*dynamodb.Client`
- Update import from `github.com/aws/aws-sdk-go/service/dynamodb` to `github.com/aws/aws-sdk-go-v2/service/dynamodb`
- Remove commented-out dead code (`NewConfig`, `SetHashKeyLength`)

---

## Step 3: `model.go` — Migrate Types + Add Pagination Types

**Migrate existing types:**
- All `map[string]*dynamodb.AttributeValue` → `map[string]types.AttributeValue`
- All embedded v1 input/output types (e.g., `dynamodb.PutItemInput`) → same names from v2 package
- Import `github.com/aws/aws-sdk-go-v2/service/dynamodb/types`

**Add pagination fields to query inputs:**
```go
type QueryRadiusInput struct {
    GeoQueryInput
    CenterPoint   GeoPoint
    RadiusInMeter int
    Limit         int    // 0 = return all (default)
    NextToken     string // empty = start from beginning
}

type QueryRectangleInput struct {
    GeoQueryInput
    MinPoint  *GeoPoint
    MaxPoint  *GeoPoint
    Limit     int
    NextToken string
}
```

**Replace old query output types with paginated versions:**
```go
type QueryRadiusOutput struct {
    Items     []map[string]types.AttributeValue
    NextToken string
    Count     int
}

type QueryRectangleOutput struct {
    Items     []map[string]types.AttributeValue
    NextToken string
    Count     int
}
```

---

## Step 4: `db.go` — Migrate All DynamoDB Operations

This is the largest change. Every DynamoDB API call lives here.

**All methods gain `ctx context.Context` as first parameter.**

**AttributeValue construction changes:**
- `&dynamodb.AttributeValue{N: aws.String("123")}` → `&types.AttributeValueMemberN{Value: "123"}`
- `&dynamodb.AttributeValue{S: aws.String("abc")}` → `&types.AttributeValueMemberS{Value: "abc"}`

**`queryGeoHash` — Major rewrite:**
- Replace deprecated `KeyConditions` map with `expression.Key().Equal()` / `.Between()` from `github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression`
- Replace manual `LastEvaluatedKey` pagination loop with `dynamodb.NewQueryPaginator`
- Remove `mergo.Merge` for query input defaults; use explicit `applyQueryInputOverrides()` helper
- Return `error` instead of swallowing with `fmt.Println`

**Remove `paginateQuery`** — replaced by v2's `QueryPaginator`

**`getPoint`, `putPoint`, `batchWritePoints`, `updatePoint`, `deletePoint`:**
- All API calls gain `ctx` parameter: `client.GetItem(ctx, &input)`
- All attribute value maps use v2 types
- `batchWritePoints`: `[]*dynamodb.WriteRequest` → `[]types.WriteRequest` (value slices)

**`GetCreateTableRequest`:**
- Pointer slices → value slices (`[]*dynamodb.KeySchemaElement` → `[]types.KeySchemaElement`)
- String literals → typed enums (`aws.String("HASH")` → `types.KeyTypeHash`)

---

## Step 5: `dyngeo.go` — Migrate Public API + Pagination Logic

**All public methods gain `ctx context.Context` as first parameter.**

**Change receiver from value to pointer:** `func (dg DynGeo)` → `func (dg *DynGeo)`

**Replace `mergo.Merge` in `New()` with explicit field defaulting:**
```go
if config.HashKeyAttributeName == "" { config.HashKeyAttributeName = "hashKey" }
// ... etc for each field
```

**`LongitudeFirst` default handling:** Change to `*bool`; `nil` defaults to `true` in `New()`.

**`QueryRadius` / `QueryRectangle` — New signatures:**
```go
func (dg *DynGeo) QueryRadius(ctx context.Context, input QueryRadiusInput, out interface{}) (*QueryRadiusOutput, error)
func (dg *DynGeo) QueryRectangle(ctx context.Context, input QueryRectangleInput, out interface{}) (*QueryRectangleOutput, error)
```
- `out interface{}` is still auto-unmarshalled (pass `nil` to skip)
- Return value carries `Items`, `NextToken`, `Count`

**Pagination strategy (offset-based, Phase 1):**
- All hash ranges are queried eagerly (same as today)
- Results are geo-filtered (by radius or rectangle)
- `Limit` and `NextToken` (encoding an offset) slice the filtered results
- Simple, correct, no complex cursor state
- Documented caveat: data changes between pages may cause skips/duplicates

**`dispatchQueries` — Improve concurrency:**
- Replace `sync.WaitGroup` + `sync.Mutex` with a buffered channel
- Propagate errors instead of printing them
- Thread `ctx` to all goroutines

**`latLngFromItem` — AttributeValue access:**
- `*item[key].S` → type assertion: `item[key].(*types.AttributeValueMemberS).Value`

**`unmarshallOutput`:**
- `dynamodbattribute.UnmarshalListOfMaps` → `attributevalue.UnmarshalListOfMaps`

---

## Step 6: New File `pagination.go`

Token encode/decode helpers using JSON + base64:
```go
type paginationState struct {
    Offset int `json:"o"`
}
func encodePaginationState(state paginationState) (string, error)
func decodePaginationState(token string) (paginationState, error)
```

Shared by both `QueryRadius` and `QueryRectangle` via a common `paginateResults()` function.

---

## Step 7: `starbucks-example/main.go` — Update Example

- `session.NewSession` → `config.LoadDefaultConfig(ctx)` + `dynamodb.NewFromConfig(cfg)`
- All attribute values use v2 types
- All method calls gain `ctx`
- `ioutil.ReadFile` → `os.ReadFile`
- Handle new return types from `QueryRadius`
- Add `go.mod` for the example module with `replace` directive to local library

---

## Step 8: Finalize

- Run `go mod tidy` in both root and example
- Update `README.md` with v2 examples, pagination docs, migration notes
- Update `CLAUDE.md` with new architecture/dependency info

---

## Breaking Changes Summary

| What | Before | After |
|---|---|---|
| Config client | `*dynamodb.DynamoDB` | `*dynamodb.Client` |
| Attribute values | `*dynamodb.AttributeValue` (struct ptr) | `types.AttributeValue` (interface) |
| Attribute maps | `map[string]*dynamodb.AttributeValue` | `map[string]types.AttributeValue` |
| Public methods | No `context.Context` | `ctx context.Context` first param |
| Receiver | `DynGeo` (value) | `*DynGeo` (pointer) |
| `QueryRadius` return | `error` | `(*QueryRadiusOutput, error)` |
| `QueryRectangle` return | `error` | `(*QueryRectangleOutput, error)` |
| Query inputs | No pagination | `Limit int`, `NextToken string` |
| `LongitudeFirst` | `bool` | `*bool` (nil → true) |
| `mergo` dep | Required | Removed |

---

## Verification

1. `go build ./...` — compiles without errors
2. `go vet ./...` — no issues
3. Run `starbucks-example` against local DynamoDB: table creation, batch load, radius query all work
4. Test pagination: query with `Limit: 10`, verify 10 results + non-empty `NextToken`; pass token back, verify next page; eventually `NextToken` is empty
5. Verify `out interface{}` auto-unmarshaling still works (pass a `*[]Starbucks`)
6. Verify passing `nil` for `out` returns raw `Items` without error
