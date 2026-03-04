package dyngeo

import (
	"math"
	"strconv"

	"github.com/gofrs/uuid"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/golang/geo/s2"
)

// GeoPoint represents a geographic coordinate.
type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

// GeoJSONAttribute represents a GeoJSON Point.
type GeoJSONAttribute struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"`
}

func newGeoJSONAttribute(p GeoPoint, lonFirst bool) GeoJSONAttribute {
	var coordinates []float64
	if lonFirst {
		coordinates = []float64{p.Longitude, p.Latitude}
	} else {
		coordinates = []float64{p.Latitude, p.Longitude}
	}

	return GeoJSONAttribute{
		Type:        "Point",
		Coordinates: coordinates,
	}
}

// PointInput is the base input for single-item operations.
type PointInput struct {
	RangeKeyValue uuid.UUID
	GeoPoint      GeoPoint
}

// GeoQueryInput wraps a DynamoDB QueryInput for geospatial queries.
type GeoQueryInput struct {
	QueryInput dynamodb.QueryInput
}

// GeoQueryOutput wraps a DynamoDB QueryOutput.
type GeoQueryOutput struct {
	*dynamodb.QueryOutput
}

// BatchWritePointOutput wraps a DynamoDB BatchWriteItemOutput.
type BatchWritePointOutput struct {
	*dynamodb.BatchWriteItemOutput
}

// DeletePointInput is the input for deleting a geo point.
type DeletePointInput struct {
	PointInput
	DeleteItemInput dynamodb.DeleteItemInput
}

// DeletePointOutput wraps a DynamoDB DeleteItemOutput.
type DeletePointOutput struct {
	*dynamodb.DeleteItemOutput
}

// GetPointInput is the input for getting a geo point.
type GetPointInput struct {
	PointInput
	GetItemInput dynamodb.GetItemInput
}

// GetPointOutput wraps a DynamoDB GetItemOutput.
type GetPointOutput struct {
	*dynamodb.GetItemOutput
}

// PutPointInput is the input for inserting a geo point.
type PutPointInput struct {
	PointInput
	PutItemInput dynamodb.PutItemInput
}

// PutPointOutput wraps a DynamoDB PutItemOutput.
type PutPointOutput struct {
	*dynamodb.PutItemOutput
}

// UpdatePointInput is the input for updating a geo point.
type UpdatePointInput struct {
	PointInput
	UpdateItemInput dynamodb.UpdateItemInput
}

// UpdatePointOutput wraps a DynamoDB UpdateItemOutput.
type UpdatePointOutput struct {
	*dynamodb.UpdateItemOutput
}

// QueryRadiusInput defines a circular area query.
type QueryRadiusInput struct {
	GeoQueryInput
	CenterPoint   GeoPoint
	RadiusInMeter int
	Limit         int    // 0 means return all results (no pagination)
	NextToken     string // empty means start from beginning
}

// QueryRadiusOutput holds paginated radius query results.
type QueryRadiusOutput struct {
	Items     []map[string]types.AttributeValue
	NextToken string
	Count     int
}

// QueryRectangleInput defines a rectangular area query.
type QueryRectangleInput struct {
	GeoQueryInput
	MinPoint  *GeoPoint
	MaxPoint  *GeoPoint
	Limit     int
	NextToken string
}

// QueryRectangleOutput holds paginated rectangle query results.
type QueryRectangleOutput struct {
	Items     []map[string]types.AttributeValue
	NextToken string
	Count     int
}

// geoHashRange represents a range of geohash values.
type geoHashRange struct {
	rangeMin uint64
	rangeMax uint64
}

func newGeoHashRange(min uint64, max uint64) geoHashRange {
	return geoHashRange{
		rangeMin: min,
		rangeMax: max,
	}
}

func (g geoHashRange) tryMerge(r geoHashRange) bool {
	if r.rangeMin-g.rangeMax <= MERGE_THRESHOLD && r.rangeMin > g.rangeMax {
		g.rangeMax = r.rangeMax
		return true
	}
	if g.rangeMin-r.rangeMax <= MERGE_THRESHOLD && g.rangeMin > r.rangeMax {
		g.rangeMin = r.rangeMin
		return true
	}

	return false
}

func (g geoHashRange) trySplit(hashKeyLength int8) []geoHashRange {
	result := []geoHashRange{}

	minHashKey := generateHashKey(g.rangeMin, hashKeyLength)
	maxHashKey := generateHashKey(g.rangeMax, hashKeyLength)

	rangeMinHashString := strconv.FormatUint(g.rangeMin, 10)
	minHashKeyString := strconv.FormatUint(minHashKey, 10)
	denominator := uint64(math.Pow10(len(rangeMinHashString) - len(minHashKeyString)))

	if minHashKey == maxHashKey {
		result = append(result, g)
	} else {
		for m := minHashKey; m <= maxHashKey; m++ {
			var min uint64
			var max uint64

			if m > 0 {
				if m == minHashKey {
					min = g.rangeMin
				} else {
					min = m * denominator
				}
				if m == maxHashKey {
					max = g.rangeMax
				} else {
					max = (m+1)*denominator - 1
				}
			} else {
				if m == minHashKey {
					min = g.rangeMin
				} else {
					min = (m-1)*denominator + 1
				}

				if m == maxHashKey {
					max = g.rangeMax
				} else {
					max = m * denominator
				}
			}

			result = append(result, newGeoHashRange(min, max))
		}
	}

	return result
}

// covering wraps a set of S2 CellIDs.
type covering struct {
	cellIDs []s2.CellID
}

func newCovering(cellIDs []s2.CellID) covering {
	return covering{
		cellIDs: cellIDs,
	}
}

func (c covering) getGeoHashRanges(hashKeyLength int8) []geoHashRange {
	ranges := []geoHashRange{}

	for _, cellID := range c.cellIDs {
		minRange := s2.CellID.RangeMin(cellID)
		maxRange := s2.CellID.RangeMax(cellID)
		gh := newGeoHashRange(uint64(minRange), uint64(maxRange))
		ranges = append(ranges, gh.trySplit(hashKeyLength)...)
	}

	return ranges
}

func generateGeoHash(geoPoint GeoPoint) s2.CellID {
	latLng := s2.LatLngFromDegrees(geoPoint.Latitude, geoPoint.Longitude)
	cell := s2.CellFromLatLng(latLng)

	return cell.ID()
}

func generateHashKey(geoHash uint64, hashKeyLength int8) uint64 {
	if geoHash < 0 {
		hashKeyLength++
	}

	geoHashString := strconv.FormatUint(geoHash, 10)
	denominator := math.Pow10(len(geoHashString) - int(hashKeyLength))

	return geoHash / uint64(denominator)
}

func generateHashes(p GeoPoint, hashKeyLength int8) (uint64, uint64) {
	geoHash := uint64(generateGeoHash(p))
	hashKey := generateHashKey(geoHash, hashKeyLength)

	return geoHash, hashKey
}

// S2 Util
const EARTH_RADIUS_METERS = 6367000.0

func rectFromQueryRectangleInput(input QueryRectangleInput) *s2.Rect {
	if input.MinPoint != nil && input.MaxPoint != nil {
		minLatLng := s2.LatLngFromDegrees(input.MinPoint.Latitude, input.MinPoint.Longitude)
		maxLatLng := s2.LatLngFromDegrees(input.MaxPoint.Latitude, input.MaxPoint.Longitude)

		rect := rectFromTwoLatLng(minLatLng, maxLatLng)

		return &rect
	}

	return nil
}

func boundingLatLngFromQueryRadiusInput(input QueryRadiusInput) *s2.Rect {
	centerLatLng := s2.LatLngFromDegrees(input.CenterPoint.Latitude, input.CenterPoint.Longitude)

	latRefUnit := 1.0
	if input.CenterPoint.Latitude > 0 {
		latRefUnit = -1.0
	}
	latRef := s2.LatLngFromDegrees(input.CenterPoint.Latitude+latRefUnit, input.CenterPoint.Longitude)

	lngRefUnit := 1.0
	if input.CenterPoint.Longitude > 0 {
		lngRefUnit = -1.0
	}
	lngRef := s2.LatLngFromDegrees(input.CenterPoint.Latitude, input.CenterPoint.Longitude+lngRefUnit)

	latDistance := getEarthDistance(centerLatLng, latRef)
	lngDistance := getEarthDistance(centerLatLng, lngRef)

	radiusInMeter := float64(input.RadiusInMeter)
	latForRadius := radiusInMeter / latDistance
	lngForRadius := radiusInMeter / lngDistance

	center := s2.LatLngFromDegrees(input.CenterPoint.Latitude, input.CenterPoint.Longitude)
	size := s2.LatLngFromDegrees(latForRadius, lngForRadius)
	rect := s2.RectFromCenterSize(center, size)

	return &rect
}

func getEarthDistance(p1 s2.LatLng, p2 s2.LatLng) float64 {
	return p1.Distance(p2).Radians() * EARTH_RADIUS_METERS
}

func rectFromTwoLatLng(min s2.LatLng, max s2.LatLng) s2.Rect {
	bounder := s2.NewRectBounder()
	bounder.AddPoint(s2.PointFromLatLng(min))
	bounder.AddPoint(s2.PointFromLatLng(max))

	return bounder.RectBound()
}
