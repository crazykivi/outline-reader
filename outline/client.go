package outline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
}

type Document struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Text         string    `json:"text"`
	URL          string    `json:"url"`
	UrlID        string    `json:"urlId"`
	ParentID     string    `json:"parentDocumentId"`
	CollectionID string    `json:"collectionId"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type Collection struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type NavNode struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Children []NavNode `json:"children"`
}

func NewClient(baseURL, apiKey string) *Client {
	return &Client{baseURL: baseURL, apiKey: apiKey, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) post(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		msg := e.Message
		if msg == "" {
			msg = e.Error
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%s: %s", method, msg)
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("%s: decode: %w", method, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("%s: data: %w", method, err)
	}
	return nil
}

func (c *Client) CheckAuth(ctx context.Context) error {
	return c.post(ctx, "auth.info", map[string]any{}, nil)
}

func (c *Client) Collections(ctx context.Context) ([]Collection, error) {
	var all []Collection
	for offset := 0; ; offset += 100 {
		var page []Collection
		if err := c.post(ctx, "collections.list", map[string]any{"limit": 100, "offset": offset}, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 100 {
			break
		}
	}
	return all, nil
}

func (c *Client) CollectionTree(ctx context.Context, id string) ([]NavNode, error) {
	var out []NavNode
	err := c.post(ctx, "collections.documents", map[string]any{"id": id}, &out)
	return out, err
}

func (c *Client) AllDocuments(ctx context.Context) ([]Document, error) {
	var all []Document
	for offset := 0; ; offset += 100 {
		var page []Document
		if err := c.post(ctx, "documents.list", map[string]any{"limit": 100, "offset": offset}, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 100 {
			break
		}
	}
	return all, nil
}

func (c *Client) UpdatedSince(ctx context.Context, since time.Time) ([]Document, error) {
	var out []Document
	for offset := 0; ; offset += 100 {
		var page []Document
		if err := c.post(ctx, "documents.list", map[string]any{
			"sort": "updatedAt", "direction": "DESC", "limit": 100, "offset": offset,
		}, &page); err != nil {
			return nil, err
		}
		stop := false
		for _, d := range page {
			if d.UpdatedAt.After(since) {
				out = append(out, d)
			} else {
				stop = true
			}
		}
		if stop || len(page) < 100 {
			break
		}
	}
	return out, nil
}

func (c *Client) Drafts(ctx context.Context) ([]Document, error) {
	var all []Document
	for offset := 0; ; offset += 100 {
		var page []Document
		if err := c.post(ctx, "documents.drafts", map[string]any{"limit": 100, "offset": offset}, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < 100 {
			break
		}
	}
	return all, nil
}

func (c *Client) DocumentInfo(ctx context.Context, id string) (*Document, error) {
	var out Document
	if err := c.post(ctx, "documents.info", map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Attachment(ctx context.Context, id string) (*http.Response, error) {
	u := c.baseURL + "/api/attachments.redirect?id=" + url.QueryEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("attachment %s: %w", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("attachment %s: %s", id, resp.Status)
	}
	return resp, nil
}
