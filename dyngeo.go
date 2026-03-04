package dyngeo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/golang/geo/s2"
)

// DynGeo manages geospatial data in Amazon DynamoDB using geohash-based indexing.
type DynGeo struct {
	Config DynGeoConfig
	db     db
}

// New returns a new DynGeo instance. DynamoDBClient and TableName are required in config.
func New(config DynGeoConfig) (*DynGeo, error) {
	if config.DynamoDBClient == nil {
		return nil, errors.New("DynamoDBClient is required")
	}

	if config.TableName == "" {
		return nil, errors.New("TableName is required")
	}

	// Apply defaults (replaces mergo.Merge)
	if config.HashKeyAttributeName == "" {
		config.HashKeyAttributeName = "hashKey"
	}
	if config.RangeKeyAttributeName == "" {
		config.RangeKeyAttributeName = "rangeKey"
	}
	if config.GeoHashAttributeName == "" {
		config.GeoHashAttributeName = "geohash"
	}
	if config.GeoJSONAttributeName == "" {
		config.GeoJSONAttributeName = "geoJson"
	}
	if config.GeoHashIndexName == "" {
		config.GeoHashIndexName = "geohash-index"
	}
	if config.HashKeyLength == 0 {
		config.HashKeyLength = 2
	}
	if config.LongitudeFirst == nil {
		config.LongitudeFirst = aws.Bool(true)
	}

	config.s2RegionCoverer = s2.RegionCoverer{
		MinLevel: 10,
		MaxLevel: 10,
		MaxCells: 10,
		LevelMod: 0,
	}

	return &DynGeo{
		Config: config,
		db:     newDB(config),
	}, nil
}

// PutPoint inserts a point into DynamoDB. Once put, you cannot update the hash key,
// range key, geohash, or geoJson attributes. Insert a new record and delete the old one instead.
func (dg *DynGeo) PutPoint(ctx context.Context, input PutPointInput) (*PutPointOutput, error) {
	return dg.db.putPoint(ctx, input)
}

// BatchWritePoints inserts a batch of points into DynamoDB.
// DynamoDB limits batch writes to 25 items; the caller is responsible for chunking.
func (dg *DynGeo) BatchWritePoints(ctx context.Context, inputs []PutPointInput) (*BatchWritePointOutput, error) {
	return dg.db.batchWritePoints(ctx, inputs)
}

// GetPoint retrieves a point from DynamoDB.
func (dg *DynGeo) GetPoint(ctx context.Context, input GetPointInput) (*GetPointOutput, error) {
	return dg.db.getPoint(ctx, input)
}

// UpdatePoint updates a point's non-geo attributes in DynamoDB.
// Geohash and geoJson attributes are automatically excluded from updates.
func (dg *DynGeo) UpdatePoint(ctx context.Context, input UpdatePointInput) (*UpdatePointOutput, error) {
	return dg.db.updatePoint(ctx, input)
}

// DeletePoint removes a point from DynamoDB.
func (dg *DynGeo) DeletePoint(ctx context.Context, input DeletePointInput) (*DeletePointOutput, error) {
	return dg.db.deletePoint(ctx, input)
}

// QueryRadius queries a circular area defined by a center point and radius.
// Results are optionally unmarshalled into out (pass nil to skip).
// Supports pagination via Limit and NextToken on the input.
func (dg *DynGeo) QueryRadius(ctx context.Context, input QueryRadiusInput, out interface{}) (*QueryRadiusOutput, error) {
	latLngRect := boundingLatLngFromQueryRadiusInput(input)
	covering := newCovering(dg.Config.s2RegionCoverer.Covering(s2.Region(latLngRect)))
	results, err := dg.dispatchQueries(ctx, covering, input.GeoQueryInput)
	if err != nil {
		return nil, err
	}

	filtered, err := dg.filterByRadius(results, input)
	if err != nil {
		return nil, err
	}

	items, nextToken, err := paginateResults(filtered, input.Limit, input.NextToken)
	if err != nil {
		return nil, err
	}

	if out != nil && len(items) > 0 {
		if err := dg.unmarshallOutput(items, out); err != nil {
			return nil, err
		}
	}

	return &QueryRadiusOutput{
		Items:     items,
		NextToken: nextToken,
		Count:     len(items),
	}, nil
}

// QueryRectangle queries a rectangular area defined by min and max points.
// Results are optionally unmarshalled into out (pass nil to skip).
// Supports pagination via Limit and NextToken on the input.
func (dg *DynGeo) QueryRectangle(ctx context.Context, input QueryRectangleInput, out interface{}) (*QueryRectangleOutput, error) {
	latLngRect := rectFromQueryRectangleInput(input)
	covering := newCovering(dg.Config.s2RegionCoverer.Covering(s2.Region(latLngRect)))
	results, err := dg.dispatchQueries(ctx, covering, input.GeoQueryInput)
	if err != nil {
		return nil, err
	}

	filtered, err := dg.filterByRect(results, input)
	if err != nil {
		return nil, err
	}

	items, nextToken, err := paginateResults(filtered, input.Limit, input.NextToken)
	if err != nil {
		return nil, err
	}

	if out != nil && len(items) > 0 {
		if err := dg.unmarshallOutput(items, out); err != nil {
			return nil, err
		}
	}

	return &QueryRectangleOutput{
		Items:     items,
		NextToken: nextToken,
		Count:     len(items),
	}, nil
}

func (dg *DynGeo) dispatchQueries(ctx context.Context, covering covering, input GeoQueryInput) ([]map[string]types.AttributeValue, error) {
	type queryResult struct {
		outputs []*dynamodb.QueryOutput
		err     error
	}

	hashRanges := covering.getGeoHashRanges(dg.Config.HashKeyLength)
	resultCh := make(chan queryResult, len(hashRanges))

	for i := 0; i < len(hashRanges); i++ {
		go func(i int) {
			g := hashRanges[i]
			hashKey := generateHashKey(g.rangeMin, dg.Config.HashKeyLength)
			output, err := dg.db.queryGeoHash(ctx, input.QueryInput, hashKey, g)
			resultCh <- queryResult{outputs: output, err: err}
		}(i)
	}

	var mergedResults []map[string]types.AttributeValue
	var firstErr error
	for i := 0; i < len(hashRanges); i++ {
		res := <-resultCh
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		for _, o := range res.outputs {
			mergedResults = append(mergedResults, o.Items...)
		}
	}

	if firstErr != nil {
		return nil, firstErr
	}

	return mergedResults, nil
}

func (dg *DynGeo) filterByRect(list []map[string]types.AttributeValue, input QueryRectangleInput) ([]map[string]types.AttributeValue, error) {
	var filtered []map[string]types.AttributeValue
	latLngRect := rectFromQueryRectangleInput(input)

	for _, item := range list {
		latLng, err := dg.latLngFromItem(item)
		if err != nil {
			return nil, err
		}

		if latLngRect.ContainsLatLng(*latLng) {
			filtered = append(filtered, item)
		}
	}

	return filtered, nil
}

func (dg *DynGeo) filterByRadius(list []map[string]types.AttributeValue, input QueryRadiusInput) ([]map[string]types.AttributeValue, error) {
	var filtered []map[string]types.AttributeValue

	centerLatLng := s2.LatLngFromDegrees(input.CenterPoint.Latitude, input.CenterPoint.Longitude)
	radius := input.RadiusInMeter

	for _, item := range list {
		latLng, err := dg.latLngFromItem(item)
		if err != nil {
			return nil, err
		}

		if getEarthDistance(centerLatLng, *latLng) <= float64(radius) {
			filtered = append(filtered, item)
		}
	}

	return filtered, nil
}

func (dg *DynGeo) latLngFromItem(item map[string]types.AttributeValue) (*s2.LatLng, error) {
	av, ok := item[dg.Config.GeoJSONAttributeName]
	if !ok {
		return nil, fmt.Errorf("item missing %s attribute", dg.Config.GeoJSONAttributeName)
	}
	sVal, ok := av.(*types.AttributeValueMemberS)
	if !ok {
		return nil, fmt.Errorf("%s attribute is not a string", dg.Config.GeoJSONAttributeName)
	}

	geoJSONAttr := GeoJSONAttribute{}
	if err := json.Unmarshal([]byte(sVal.Value), &geoJSONAttr); err != nil {
		return nil, err
	}

	coordinates := geoJSONAttr.Coordinates
	var lng float64
	var lat float64

	lonFirst := aws.ToBool(dg.Config.LongitudeFirst)
	if lonFirst {
		lng = coordinates[0]
		lat = coordinates[1]
	} else {
		lng = coordinates[1]
		lat = coordinates[0]
	}

	latLng := s2.LatLngFromDegrees(lat, lng)

	return &latLng, nil
}

func (dg *DynGeo) unmarshallOutput(output []map[string]types.AttributeValue, out interface{}) error {
	return attributevalue.UnmarshalListOfMaps(output, out)
}
