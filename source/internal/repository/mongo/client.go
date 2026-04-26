package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Client struct {
	Client   *mongo.Client
	Database *mongo.Database
}

func NewClient(ctx context.Context, uri, database string) (*Client, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := mongo.Connect(timeoutCtx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}

	if err := client.Ping(timeoutCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}

	return &Client{
		Client:   client,
		Database: client.Database(database),
	}, nil
}

func (c *Client) Close(ctx context.Context) error {
	return c.Client.Disconnect(ctx)
}
