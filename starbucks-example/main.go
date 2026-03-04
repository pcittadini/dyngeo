package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/crolly/dyngeo"
	"github.com/gofrs/uuid"
)

var (
	dbClient *dynamodb.Client
	dg       *dyngeo.DynGeo
)

const BATCH_SIZE = 25

type Starbucks struct {
	Position Position `json:"position"`
	Name     string   `json:"name" dynamodbav:"name"`
	Address  string   `json:"address" dynamodbav:"address"`
	Phone    string   `json:"phone" dynamodbav:"phone"`
}

type Position struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lng"`
}

func main() {
	ctx := context.Background()

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("eu-central-1"),
	)
	if err != nil {
		panic(err)
	}

	dbClient = dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String("http://localhost:8000")
	})

	dg, err = dyngeo.New(dyngeo.DynGeoConfig{
		DynamoDBClient: dbClient,
		HashKeyLength:  5,
		TableName:      "coffee-shops",
	})
	if err != nil {
		panic(err)
	}

	setupTable(ctx)
	loadData(ctx)
	queryData(ctx)
}

func setupTable(ctx context.Context) {
	createTableInput := dyngeo.GetCreateTableRequest(dg.Config)
	createTableInput.ProvisionedThroughput.ReadCapacityUnits = aws.Int64(5)
	createTableOutput, err := dbClient.CreateTable(ctx, createTableInput)
	if err != nil {
		panic(err)
	}
	fmt.Println("Table created")
	fmt.Println(createTableOutput)
}

func loadData(ctx context.Context) {
	f, err := os.ReadFile("starbucks_us_locations.json")
	if err != nil {
		panic(err)
	}
	coffeeShops := []Starbucks{}
	err = json.Unmarshal(f, &coffeeShops)
	if err != nil {
		panic(err)
	}

	batchInput := []dyngeo.PutPointInput{}
	for _, s := range coffeeShops {
		id, err := uuid.NewV4()
		if err != nil {
			panic(err)
		}
		input := dyngeo.PutPointInput{
			PutItemInput: dynamodb.PutItemInput{
				Item: map[string]types.AttributeValue{
					"name":    &types.AttributeValueMemberS{Value: s.Name},
					"address": &types.AttributeValueMemberS{Value: s.Address},
				},
			},
		}
		input.RangeKeyValue = id
		input.GeoPoint = dyngeo.GeoPoint{
			Latitude:  s.Position.Latitude,
			Longitude: s.Position.Longitude,
		}
		batchInput = append(batchInput, input)
	}

	batches := [][]dyngeo.PutPointInput{}
	for BATCH_SIZE < len(batchInput) {
		batchInput, batches = batchInput[BATCH_SIZE:], append(batches, batchInput[0:BATCH_SIZE:BATCH_SIZE])
	}
	batches = append(batches, batchInput)

	for count, batch := range batches {
		output, err := dg.BatchWritePoints(ctx, batch)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Batch %d written: %v\n", count, output)
	}
}

func queryData(ctx context.Context) {
	start := time.Now()
	sbs := []Starbucks{}

	result, err := dg.QueryRadius(ctx, dyngeo.QueryRadiusInput{
		CenterPoint: dyngeo.GeoPoint{
			Latitude:  40.7769099,
			Longitude: -73.9822532,
		},
		RadiusInMeter: 5000,
	}, &sbs)
	if err != nil {
		panic(err)
	}

	for _, sb := range sbs {
		fmt.Println(sb)
	}

	fmt.Printf("Found %d results\n", result.Count)
	fmt.Print("Executed in: ")
	fmt.Println(time.Since(start))
}
