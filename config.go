package dyngeo

import (
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/golang/geo/s2"
)

// MERGE_THRESHOLD defines the maximum gap between two geohash ranges
// for them to be merged into a single range.
const MERGE_THRESHOLD = 2

// DynGeoConfig defines how DynGeo manages geospatial data in DynamoDB.
// TableName and DynamoDBClient are required. All other fields have sensible defaults.
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
	s2RegionCoverer s2.RegionCoverer
}
