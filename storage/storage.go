// XXX: Currently transactional operations are not used.
package storage

import (
	"context"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	av "github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type itemMap map[string]types.AttributeValue

type Record struct {
	ChannelID   string `dynamodbav:"channel_id"`
	ChannelName string `dynamodbav:"channel_name"`
	Token       string `dynamodbav:"token"`
	Version     int    `dynamodbav:"version"`
	CreatedAt   string `dynamodbav:"created_at"`
}

type Storage struct {
	client    *dynamodb.Client
	tableName *string
}

func NewStorage(ctx context.Context, tableName string) (*Storage, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return &Storage{}, fmt.Errorf("unable to load SDK config: %w", err)
	}
	client := dynamodb.NewFromConfig(cfg)

	return &Storage{client: client, tableName: &tableName}, nil
}

func (s *Storage) Save(ctx context.Context, rec Record) error {
	m, err := av.MarshalMap(rec)
	if err != nil {
		return fmt.Errorf("attributevalue.MarshalMap failed: %w", err)
	}
	input := dynamodb.PutItemInput{
		Item:      m,
		TableName: s.tableName,
	}
	if _, err := s.client.PutItem(ctx, &input); err != nil {
		return fmt.Errorf("dynamodb.PutItem failed: %w", err)
	}
	return nil
}

// QueryByChannelName returns found Records sorted by .Version with descending order.
// https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_Query.html
func (s *Storage) QueryByChannelName(ctx context.Context, channelName string) ([]Record, error) {
	input := dynamodb.QueryInput{
		TableName:                 s.tableName,
		KeyConditionExpression:    aws.String("channel_name = :channel_name"),
		ExpressionAttributeValues: itemMap{":channel_name": &types.AttributeValueMemberS{Value: channelName}},
		ScanIndexForward:          aws.Bool(true),
	}
	out, err := s.client.Query(ctx, &input)
	if err != nil {
		return []Record{}, fmt.Errorf("dynamodb.Query failed: %w", err)
	}

	recs := make([]Record, len(out.Items))
	for i, item := range out.Items {
		rec := Record{}
		if err := av.UnmarshalMap(item, &rec); err != nil {
			return []Record{}, fmt.Errorf("attributevalue.UnmarshalMap failed: %w", err)
		}
		recs[i] = rec
	}
	return recs, nil
}

// Delete removes a record. The record must be in the table.
func (s *Storage) Delete(ctx context.Context, rec Record) error {
	input := dynamodb.DeleteItemInput{
		TableName: s.tableName,
		Key: itemMap{
			"channel_name": &types.AttributeValueMemberS{Value: rec.ChannelName},
			"version":      &types.AttributeValueMemberN{Value: strconv.Itoa(rec.Version)},
		},
		ConditionExpression:       aws.String("#t = :token"),
		ExpressionAttributeValues: itemMap{":token": &types.AttributeValueMemberS{Value: rec.Token}},
		ExpressionAttributeNames:  map[string]string{"#t": "token"},
		ReturnValues:              types.ReturnValueAllOld,
	}
	out, err := s.client.DeleteItem(ctx, &input)
	if err != nil {
		return fmt.Errorf("dynamodb.DeleteItem failed: %w", err)
	}
	if len(out.Attributes) == 0 {
		return fmt.Errorf("no item deleted: rec=%v", rec)
	}
	// Success.
	return nil
}

func (s *Storage) ScanAll(ctx context.Context) ([]Record, error) {
	var (
		recs              []Record
		exclusiveStartKey itemMap
	)

	for {
		input := dynamodb.ScanInput{
			TableName:         s.tableName,
			ExclusiveStartKey: exclusiveStartKey,
		}
		out, err := s.client.Scan(ctx, &input)
		if err != nil {
			return []Record{}, fmt.Errorf("dynamodb.Scan failed: %w", err)
		}

		for _, item := range out.Items {
			rec := Record{}
			if err := av.UnmarshalMap(item, &rec); err != nil {
				return []Record{}, fmt.Errorf("attributevalue.UnmarshalMap failed: %w", err)
			}
			recs = append(recs, rec)
		}

		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		exclusiveStartKey = out.LastEvaluatedKey
	}

	return recs, nil
}
