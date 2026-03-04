# DynG(e)o

Unofficial Go port of the [Geo Library for Amazon DynamoDB](https://github.com/amazon-archives/dynamodb-geo) using [geohash](https://en.wikipedia.org/wiki/Geohash) to easily create and query geospatial data.
The library takes care of managing the geohash indexes and storing item with latitude/longitude pairs.

Uses **AWS SDK for Go v2** and supports **pagination** on geospatial queries.

## Install

```
go get github.com/crolly/dyngeo
```

```go
import "github.com/crolly/dyngeo"
```

## Usage

### DynG(e)o Configuration

```go
type DynGeoConfig struct {
	TableName             string
	ConsistentRead        bool
	HashKeyAttributeName  string
	RangeKeyAttributeName string
	GeoHashAttributeName  string
	GeoJSONAttributeName  string
	GeoHashIndexName      string
	HashKeyLength         int8
	LongitudeFirst        *bool // nil defaults to true

	DynamoDBClient  *dynamodb.Client
}
```

Defines how DynG(e)o manages the geospatial data, e.g. what the db attribute and index names are as well as setting the geohash key length.

Only `DynamoDBClient` and `TableName` are required. All other fields have sensible defaults.

The geohash key length determines the size of the tiles the planet is separated into:

| Length | Tile Size             |
| ------ |-----------------------|
| 1      | 5,009.4km x 4,992.6km |
| 2      | 1,252.3km x 624.1km   |
| 3      | 156.5km x 156km       |
| 4      | 39.1km x 19.5km       |
| 5      | 4.9km x 4.9km         |
| 6      | 1.2km x 609.4m        |
| 7      | 152.9m x 152.4m       |
| 8      | 38.2m x 19m           |
| 9      | 4.8m x 4.8m           |
| 10     | 1.2m x 59.5cm         |
| 11     | 14.9cm x 14.9cm       |
| 12     | 3.7cm x 1.9cm         |

### DynG(e)o Instance

#### func New

```go
func New(config DynGeoConfig) (*DynGeo, error)
```
Returns a new instance of `DynG(e)o` managing the geohashing and geospatial db operations.

#### func PutPoint

```go
func (dg *DynGeo) PutPoint(ctx context.Context, input PutPointInput) (*PutPointOutput, error)
```
Put a point into the Amazon DynamoDB table. Once put, you cannot update attributes specified in GeoDataManagerConfiguration: hash key, range key, geohash and geoJson. If you want to update these columns, you need to insert a new record and delete the old record.

#### func BatchWritePoints

```go
func (dg *DynGeo) BatchWritePoints(ctx context.Context, inputs []PutPointInput) (*BatchWritePointOutput, error)
```
Put a list of points into the Amazon DynamoDB table. DynamoDB limits batch writes to 25 items; the caller is responsible for chunking.

#### func GetPoint

```go
func (dg *DynGeo) GetPoint(ctx context.Context, input GetPointInput) (*GetPointOutput, error)
```
Get a point from the Amazon DynamoDB table.

#### func UpdatePoint

```go
func (dg *DynGeo) UpdatePoint(ctx context.Context, input UpdatePointInput) (*UpdatePointOutput, error)
```
Update a point data in Amazon DynamoDB table. You cannot update attributes specified in GeoDataManagerConfiguration: hash key, range key, geohash and geoJson. If you want to update these columns, you need to insert a new record and delete the old record.

#### func DeletePoint

```go
func (dg *DynGeo) DeletePoint(ctx context.Context, input DeletePointInput) (*DeletePointOutput, error)
```
Delete a point from the Amazon DynamoDB table.

#### func QueryRadius

```go
func (dg *DynGeo) QueryRadius(ctx context.Context, input QueryRadiusInput, out interface{}) (*QueryRadiusOutput, error)
```
Query a circular area constructed by a center point and its radius. Results are optionally unmarshalled into `out` (pass `nil` to skip). Supports pagination via `Limit` and `NextToken`.

#### func QueryRectangle

```go
func (dg *DynGeo) QueryRectangle(ctx context.Context, input QueryRectangleInput, out interface{}) (*QueryRectangleOutput, error)
```
Query a rectangular area constructed by two points and return all points within the area. Two points need to construct a rectangle from minimum and maximum latitudes and longitudes. If minPoint.Longitude > maxPoint.Longitude, the rectangle spans the 180 degree longitude line. Supports pagination via `Limit` and `NextToken`.

### Pagination

Both `QueryRadius` and `QueryRectangle` support pagination:

```go
ctx := context.Background()

// First page
result, err := dg.QueryRadius(ctx, dyngeo.QueryRadiusInput{
    CenterPoint:   dyngeo.GeoPoint{Latitude: 40.77, Longitude: -73.98},
    RadiusInMeter: 5000,
    Limit:         10,
}, nil)

// Next page
if result.NextToken != "" {
    result2, err := dg.QueryRadius(ctx, dyngeo.QueryRadiusInput{
        CenterPoint:   dyngeo.GeoPoint{Latitude: 40.77, Longitude: -73.98},
        RadiusInMeter: 5000,
        Limit:         10,
        NextToken:     result.NextToken,
    }, nil)
}
```

Set `Limit` to 0 (or omit it) to return all results without pagination.

**Note:** Pagination uses offset-based tokens. Data changes between page fetches may cause items to be skipped or duplicated.

## Getting Started Example

This repository contains a Getting Started example in the folder `starbucks-example` inspired by James Beswick's very good blog post about [Location-based search results with DynamoDB and Geohash](https://read.acloud.guru/location-based-search-results-with-dynamodb-and-geohash-267727e5d54f)

It uses the US Starbucks locations, loads them into DynamoDB in batches of 25 and then retrieves the locations of all Starbucks in the radius of 5000 meters surrounding Latitude: 40.7769099, Longitude: -73.9822532.

```bash
# Requires local DynamoDB on port 8000
cd starbucks-example
go run main.go
```

## Migration from v1

This library was migrated from AWS SDK for Go v1 to v2. Key breaking changes:

| What | Before (v1) | After (v2) |
|---|---|---|
| Config client | `*dynamodb.DynamoDB` | `*dynamodb.Client` |
| Attribute values | `*dynamodb.AttributeValue` | `types.AttributeValue` (interface) |
| Attribute maps | `map[string]*dynamodb.AttributeValue` | `map[string]types.AttributeValue` |
| Public methods | No `context.Context` | `ctx context.Context` first param |
| Receiver | `DynGeo` (value) | `*DynGeo` (pointer) |
| `QueryRadius` return | `error` | `(*QueryRadiusOutput, error)` |
| `QueryRectangle` return | `error` | `(*QueryRectangleOutput, error)` |
| `LongitudeFirst` | `bool` | `*bool` (nil defaults to true) |
