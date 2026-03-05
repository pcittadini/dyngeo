package dyngeo

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type db struct {
	config DynGeoConfig
}

func newDB(config DynGeoConfig) db {
	return db{
		config: config,
	}
}

func (db db) queryGeoHash(ctx context.Context, queryInput dynamodb.QueryInput, hashKey uint64, ghr geoHashRange) ([]*dynamodb.QueryOutput, error) {
	// Build key condition using expression builder (replaces deprecated KeyConditions)
	// Pass uint64 directly so expression builder serializes as DynamoDB Number (N),
	// matching the table schema where hashKey and geohash are Number attributes.
	hashKeyCond := expression.Key(db.config.HashKeyAttributeName).
		Equal(expression.Value(hashKey))
	geoHashCond := expression.Key(db.config.GeoHashAttributeName).
		Between(
			expression.Value(ghr.rangeMin),
			expression.Value(ghr.rangeMax),
		)

	expr, err := expression.NewBuilder().
		WithKeyCondition(hashKeyCond.And(geoHashCond)).
		Build()
	if err != nil {
		return nil, err
	}

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(db.config.TableName),
		IndexName:                 aws.String(db.config.GeoHashIndexName),
		KeyConditionExpression:    expr.KeyCondition(),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ConsistentRead:            aws.Bool(db.config.ConsistentRead),
		ReturnConsumedCapacity:    types.ReturnConsumedCapacityTotal,
	}

	// Apply caller-provided overrides from queryInput
	applyQueryInputOverrides(input, &queryInput)

	// Use v2 paginator for automatic pagination
	var outputs []*dynamodb.QueryOutput
	paginator := dynamodb.NewQueryPaginator(db.config.DynamoDBClient, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, page)
	}

	return outputs, nil
}

// applyQueryInputOverrides copies non-zero fields from the caller-provided
// QueryInput onto the default input. This replaces the previous mergo.Merge approach.
func applyQueryInputOverrides(dst *dynamodb.QueryInput, src *dynamodb.QueryInput) {
	if src.ProjectionExpression != nil {
		dst.ProjectionExpression = src.ProjectionExpression
	}
	if src.FilterExpression != nil {
		dst.FilterExpression = src.FilterExpression
	}
	if src.Limit != nil {
		dst.Limit = src.Limit
	}
	if src.ScanIndexForward != nil {
		dst.ScanIndexForward = src.ScanIndexForward
	}
	if src.Select != "" {
		dst.Select = src.Select
	}
	// Merge expression attribute names
	if len(src.ExpressionAttributeNames) > 0 {
		if dst.ExpressionAttributeNames == nil {
			dst.ExpressionAttributeNames = make(map[string]string)
		}
		for k, v := range src.ExpressionAttributeNames {
			dst.ExpressionAttributeNames[k] = v
		}
	}
	// Merge expression attribute values
	if len(src.ExpressionAttributeValues) > 0 {
		if dst.ExpressionAttributeValues == nil {
			dst.ExpressionAttributeValues = make(map[string]types.AttributeValue)
		}
		for k, v := range src.ExpressionAttributeValues {
			dst.ExpressionAttributeValues[k] = v
		}
	}
}

func (db db) getPoint(ctx context.Context, input GetPointInput) (*GetPointOutput, error) {
	_, hashKey := generateHashes(input.GeoPoint, db.config.HashKeyLength)

	getItemInput := input.GetItemInput
	getItemInput.TableName = aws.String(db.config.TableName)
	getItemInput.Key = map[string]types.AttributeValue{
		db.config.HashKeyAttributeName:  &types.AttributeValueMemberN{Value: strconv.FormatUint(hashKey, 10)},
		db.config.RangeKeyAttributeName: &types.AttributeValueMemberS{Value: input.RangeKeyValue.String()},
	}

	out, err := db.config.DynamoDBClient.GetItem(ctx, &getItemInput)

	return &GetPointOutput{out}, err
}

func (db db) putPoint(ctx context.Context, input PutPointInput) (*PutPointOutput, error) {
	geoHash, hashKey := generateHashes(input.GeoPoint, db.config.HashKeyLength)
	putItemInput := input.PutItemInput
	putItemInput.TableName = aws.String(db.config.TableName)
	putItemInput.Item = input.PutItemInput.Item

	putItemInput.Item[db.config.HashKeyAttributeName] = &types.AttributeValueMemberN{Value: strconv.FormatUint(hashKey, 10)}
	putItemInput.Item[db.config.RangeKeyAttributeName] = &types.AttributeValueMemberS{Value: input.RangeKeyValue.String()}
	putItemInput.Item[db.config.GeoHashAttributeName] = &types.AttributeValueMemberN{Value: strconv.FormatUint(geoHash, 10)}

	lonFirst := true
	if db.config.LongitudeFirst != nil {
		lonFirst = *db.config.LongitudeFirst
	}
	jsonAttr, err := json.Marshal(newGeoJSONAttribute(input.GeoPoint, lonFirst))
	if err != nil {
		return nil, err
	}
	putItemInput.Item[db.config.GeoJSONAttributeName] = &types.AttributeValueMemberS{Value: string(jsonAttr)}

	out, err := db.config.DynamoDBClient.PutItem(ctx, &putItemInput)

	return &PutPointOutput{out}, err
}

func (db db) batchWritePoints(ctx context.Context, inputs []PutPointInput) (*BatchWritePointOutput, error) {
	writeInputs := []types.WriteRequest{}
	for _, input := range inputs {
		geoHash, hashKey := generateHashes(input.GeoPoint, db.config.HashKeyLength)
		putItemInput := input.PutItemInput

		putRequest := types.PutRequest{
			Item: putItemInput.Item,
		}
		putRequest.Item[db.config.HashKeyAttributeName] = &types.AttributeValueMemberN{Value: strconv.FormatUint(hashKey, 10)}
		putRequest.Item[db.config.RangeKeyAttributeName] = &types.AttributeValueMemberS{Value: input.RangeKeyValue.String()}
		putRequest.Item[db.config.GeoHashAttributeName] = &types.AttributeValueMemberN{Value: strconv.FormatUint(geoHash, 10)}

		lonFirst := true
		if db.config.LongitudeFirst != nil {
			lonFirst = *db.config.LongitudeFirst
		}
		jsonAttr, err := json.Marshal(newGeoJSONAttribute(input.GeoPoint, lonFirst))
		if err != nil {
			return nil, err
		}
		putRequest.Item[db.config.GeoJSONAttributeName] = &types.AttributeValueMemberS{Value: string(jsonAttr)}

		writeInputs = append(writeInputs, types.WriteRequest{PutRequest: &putRequest})
	}

	out, err := db.config.DynamoDBClient.BatchWriteItem(ctx, &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]types.WriteRequest{
			db.config.TableName: writeInputs,
		},
	})

	return &BatchWritePointOutput{out}, err
}

func (db db) updatePoint(ctx context.Context, input UpdatePointInput) (*UpdatePointOutput, error) {
	_, hashKey := generateHashes(input.GeoPoint, db.config.HashKeyLength)

	input.UpdateItemInput.TableName = aws.String(db.config.TableName)
	if input.UpdateItemInput.Key == nil {
		input.UpdateItemInput.Key = map[string]types.AttributeValue{
			db.config.HashKeyAttributeName:  &types.AttributeValueMemberN{Value: strconv.FormatUint(hashKey, 10)},
			db.config.RangeKeyAttributeName: &types.AttributeValueMemberS{Value: input.RangeKeyValue.String()},
		}
	}

	// geoHash and geoJSON cannot be updated
	if input.UpdateItemInput.AttributeUpdates != nil {
		delete(input.UpdateItemInput.AttributeUpdates, db.config.GeoHashAttributeName)
		delete(input.UpdateItemInput.AttributeUpdates, db.config.GeoJSONAttributeName)
	}

	out, err := db.config.DynamoDBClient.UpdateItem(ctx, &input.UpdateItemInput)

	return &UpdatePointOutput{out}, err
}

func (db db) deletePoint(ctx context.Context, input DeletePointInput) (*DeletePointOutput, error) {
	_, hashKey := generateHashes(input.GeoPoint, db.config.HashKeyLength)

	deleteItemInput := input.DeleteItemInput
	deleteItemInput.TableName = aws.String(db.config.TableName)
	deleteItemInput.Key = map[string]types.AttributeValue{
		db.config.HashKeyAttributeName:  &types.AttributeValueMemberN{Value: strconv.FormatUint(hashKey, 10)},
		db.config.RangeKeyAttributeName: &types.AttributeValueMemberS{Value: input.RangeKeyValue.String()},
	}
	out, err := db.config.DynamoDBClient.DeleteItem(ctx, &deleteItemInput)

	return &DeletePointOutput{out}, err
}

// GetCreateTableRequest returns a CreateTableInput with the required schema
// for a DynGeo table, including the geohash Local Secondary Index.
func GetCreateTableRequest(config DynGeoConfig) *dynamodb.CreateTableInput {
	return &dynamodb.CreateTableInput{
		TableName: aws.String(config.TableName),
		ProvisionedThroughput: &types.ProvisionedThroughput{
			ReadCapacityUnits:  aws.Int64(10),
			WriteCapacityUnits: aws.Int64(5),
		},
		KeySchema: []types.KeySchemaElement{
			{
				KeyType:       types.KeyTypeHash,
				AttributeName: aws.String(config.HashKeyAttributeName),
			},
			{
				KeyType:       types.KeyTypeRange,
				AttributeName: aws.String(config.RangeKeyAttributeName),
			},
		},
		AttributeDefinitions: []types.AttributeDefinition{
			{
				AttributeName: aws.String(config.HashKeyAttributeName),
				AttributeType: types.ScalarAttributeTypeN,
			},
			{
				AttributeName: aws.String(config.RangeKeyAttributeName),
				AttributeType: types.ScalarAttributeTypeS,
			},
			{
				AttributeName: aws.String(config.GeoHashAttributeName),
				AttributeType: types.ScalarAttributeTypeN,
			},
		},
		LocalSecondaryIndexes: []types.LocalSecondaryIndex{
			{
				IndexName: aws.String(config.GeoHashIndexName),
				KeySchema: []types.KeySchemaElement{
					{
						KeyType:       types.KeyTypeHash,
						AttributeName: aws.String(config.HashKeyAttributeName),
					},
					{
						KeyType:       types.KeyTypeRange,
						AttributeName: aws.String(config.GeoHashAttributeName),
					},
				},
				Projection: &types.Projection{
					ProjectionType: types.ProjectionTypeAll,
				},
			},
		},
	}
}
