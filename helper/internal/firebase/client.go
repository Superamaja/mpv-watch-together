package firebase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL   string
	authToken string
	http      *http.Client
}

type StreamEvent struct {
	Event string
	Data  json.RawMessage
}

func New(baseURL string, authToken string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("firebase database URL is required")
	}
	return &Client{
		baseURL:   baseURL,
		authToken: strings.TrimSpace(authToken),
		http:      &http.Client{Timeout: 15 * time.Second},
	}, nil
}

func (c *Client) Get(ctx context.Context, path string, target any) error {
	return c.doJSON(ctx, http.MethodGet, path, nil, target)
}

func (c *Client) Put(ctx context.Context, path string, body any, target any) error {
	return c.doJSON(ctx, http.MethodPut, path, body, target)
}

func (c *Client) Patch(ctx context.Context, path string, body any, target any) error {
	return c.doJSON(ctx, http.MethodPatch, path, body, target)
}

func (c *Client) Delete(ctx context.Context, path string) error {
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *Client) Stream(ctx context.Context, path string) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		backoff := 500 * time.Millisecond
		for {
			if ctx.Err() != nil {
				return
			}
			if err := c.streamOnce(ctx, path, events); err != nil && ctx.Err() == nil {
				errs <- err
				time.Sleep(backoff)
				if backoff < 8*time.Second {
					backoff *= 2
				}
				continue
			}
			backoff = 500 * time.Millisecond
		}
	}()

	return events, errs
}

func (c *Client) streamOnce(ctx context.Context, path string, events chan<- StreamEvent) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("firebase stream failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventName string
	var data bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() > 0 {
				events <- StreamEvent{Event: eventName, Data: append(json.RawMessage(nil), data.Bytes()...)}
			}
			eventName = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return scanner.Err()
}

func (c *Client) doJSON(ctx context.Context, method string, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("firebase %s %s failed: %s: %s", method, path, resp.Status, strings.TrimSpace(string(respBody)))
	}
	if target == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (c *Client) endpoint(path string) string {
	cleanPath := strings.Trim(path, "/")
	endpoint := c.baseURL + "/" + cleanPath + ".json"
	if c.authToken == "" {
		return endpoint
	}
	values := url.Values{}
	values.Set("auth", c.authToken)
	return endpoint + "?" + values.Encode()
}
