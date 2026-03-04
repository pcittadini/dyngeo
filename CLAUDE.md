# DynGeo

Unofficial Go port of the [Geo Library for Amazon DynamoDB](https://github.com/amazon-archives/dynamodb-geo). Uses geohash and Google's S2 geometry library to store and query geospatial data in DynamoDB.

## Project Structure

```
dyngeo.go       - Public API: DynGeo struct, New(), CRUD operations, query methods
config.go       - DynGeoConfig struct and constants (MERGE_THRESHOLD)
db.go           - Internal DynamoDB operations (queries, CRUD, table creation helper)
model.go        - All type definitions: GeoPoint, input/output types, geoHashRange, S2 utilities
pagination.go   - Offset-based pagination: token encode/decode, paginateResults()
starbucks-example/  - Example app: loads ~6500 US Starbucks locations, queries by radius
docs/plans/     - Migration and design plans
```

## Architecture

- **DynGeo** is the main entry point. Created via `New(DynGeoConfig)`.
- **db** (unexported) handles all DynamoDB interactions. DynGeo delegates CRUD to it.
- All public methods take `context.Context` as first parameter.
- Geospatial queries use Google S2 `RegionCoverer` to compute cell coverings, then dispatch parallel DynamoDB queries per hash range via buffered channels. Results are filtered client-side by radius or rectangle.
- Items are stored with a numeric `hashKey` (partition key, derived from geohash prefix), a string `rangeKey` (UUID), a numeric `geohash`, and a `geoJson` string attribute.
- A **Local Secondary Index** on `hashKey` + `geohash` enables efficient range queries.
- Query results support **offset-based pagination** via `Limit` and `NextToken` fields.

## Key Dependencies

- `github.com/aws/aws-sdk-go-v2/service/dynamodb` - AWS SDK v2 for DynamoDB
- `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue` - Unmarshalling
- `github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression` - Key condition builder
- `github.com/golang/geo/s2` - Google S2 geometry (cell coverings, lat/lng math)
- `github.com/gofrs/uuid` - UUID generation for range keys

## Build & Run

```bash
go build ./...
go vet ./...

# To run the example (requires local DynamoDB on port 8000)
cd starbucks-example
go run main.go
```

## Conventions

- Package name: `dyngeo`
- Exported types use `PascalCase`; internal helpers are unexported
- Input/output types embed DynamoDB SDK v2 types (e.g., `PutPointInput` embeds `dynamodb.PutItemInput`)
- `PointInput` is the shared base for single-item operations (contains `RangeKeyValue` UUID and `GeoPoint`)
- Config defaults are applied in `New()` via explicit field-by-field defaulting; only `TableName` and `DynamoDBClient` are required
- Concurrent query dispatch uses buffered channels with error propagation
- AttributeValue construction uses v2 member types: `&types.AttributeValueMemberN{Value: "123"}`, `&types.AttributeValueMemberS{Value: "abc"}`

## Important Details

- `HashKeyLength` controls geohash tile granularity (default: 2). See README for tile size table.
- `LongitudeFirst` is `*bool`; nil defaults to true. Determines coordinate order in stored GeoJSON.
- `GetCreateTableRequest()` is a standalone helper (not a method on DynGeo) that returns a `*dynamodb.CreateTableInput` with the required schema and LSI.
- Geohash and geoJson attributes are immutable after insert; `UpdatePoint` explicitly strips them from updates.
- `BatchWritePoints` is limited to DynamoDB's batch size constraint (25 items, handled by caller).
- `queryGeoHash` uses `expression.Key()` builder for key conditions and `dynamodb.NewQueryPaginator` for automatic DynamoDB-level pagination.
- `applyQueryInputOverrides()` in db.go merges caller-provided QueryInput fields onto the default input (replaces the old mergo.Merge approach).
