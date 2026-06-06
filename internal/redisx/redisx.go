package redisx

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	client    *redis.Client
	enabled   bool
	available bool
	lastError string
}

func ParseURL(url string) (*redis.Options, error) { return redis.ParseURL(url) }

func New(url string) (*Client, error) {
	if url == "" {
		return &Client{}, nil
	}
	opts, err := ParseURL(url)
	if err != nil {
		return &Client{enabled: true, lastError: err.Error()}, err
	}
	rc := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rc.Ping(ctx).Err(); err != nil {
		_ = rc.Close()
		return &Client{enabled: true, available: false, lastError: err.Error()}, err
	}
	return &Client{client: rc, enabled: true, available: true}, nil
}

func (c *Client) Ping(ctx context.Context, timeout time.Duration) error {
	if c == nil || c.client == nil {
		return fmt.Errorf("redis not configured")
	}
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return c.client.Ping(ctx2).Err()
}
func (c *Client) Close() error {
	if c == nil || c.client == nil {
		return nil
	}
	return c.client.Close()
}
func (c *Client) Enabled() bool   { return c != nil && c.enabled }
func (c *Client) Available() bool { return c != nil && c.available }
func (c *Client) LastError() string {
	if c == nil {
		return ""
	}
	return c.lastError
}
func (c *Client) Client() *redis.Client {
	if c == nil {
		return nil
	}
	return c.client
}
