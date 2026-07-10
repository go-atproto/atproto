// Package atproto is a pure-Go, dependency-free read client for Bluesky and
// the AT Protocol, talking to the XRPC HTTP API.
//
// By default the client targets the public Bluesky AppView at
// https://public.api.bsky.app, which serves read methods without
// authentication. Optionally, Login exchanges credentials for an access token
// so that authenticated methods such as Timeline can be used.
package atproto

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultService is the public Bluesky AppView, which serves read XRPC methods
// without authentication.
const DefaultService = "https://public.api.bsky.app"

// Client is an AT Protocol / Bluesky XRPC read client.
type Client struct {
	// Service is the base URL of the XRPC service (default DefaultService).
	Service string
	// HTTPClient is the underlying HTTP client (default http.DefaultClient).
	HTTPClient *http.Client
	// UserAgent is sent as the User-Agent header on every request.
	UserAgent string

	token string
}

// Option configures a Client.
type Option func(*Client)

// WithService sets the base URL of the XRPC service.
func WithService(u string) Option {
	return func(c *Client) { c.Service = u }
}

// WithHTTPClient sets the underlying HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.HTTPClient = h }
}

// WithUserAgent sets the User-Agent header sent on every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.UserAgent = ua }
}

// New returns a Client configured with the given options.
func New(opts ...Option) *Client {
	c := &Client{
		Service:    DefaultService,
		HTTPClient: http.DefaultClient,
		UserAgent:  "go-atproto/atproto",
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Author is the profile of a post's author.
type Author struct {
	DID         string
	Handle      string
	DisplayName string
	Avatar      string
}

// Image is a single image embedded in a post.
type Image struct {
	Thumb    string
	Fullsize string
	Alt      string
}

// Post is a single Bluesky post.
type Post struct {
	URI         string
	CID         string
	Author      Author
	Text        string
	CreatedAt   time.Time
	LikeCount   int
	RepostCount int
	ReplyCount  int
	Images      []Image
}

// Feed is a page of posts with an optional pagination cursor.
type Feed struct {
	Posts  []Post
	Cursor string
}

// imagesEmbedType is the $type of the hydrated images embed view.
const imagesEmbedType = "app.bsky.embed.images#view"

// wire types mirror the XRPC JSON response shapes.

type wireFeed struct {
	Feed   []wireFeedItem `json:"feed"`
	Cursor string         `json:"cursor"`
}

type wireFeedItem struct {
	Post wirePost `json:"post"`
}

type wirePost struct {
	URI         string     `json:"uri"`
	CID         string     `json:"cid"`
	Author      wireAuthor `json:"author"`
	Record      wireRecord `json:"record"`
	LikeCount   int        `json:"likeCount"`
	RepostCount int        `json:"repostCount"`
	ReplyCount  int        `json:"replyCount"`
	Embed       *wireEmbed `json:"embed"`
}

type wireAuthor struct {
	DID         string `json:"did"`
	Handle      string `json:"handle"`
	DisplayName string `json:"displayName"`
	Avatar      string `json:"avatar"`
}

type wireRecord struct {
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}

type wireEmbed struct {
	Type   string      `json:"$type"`
	Images []wireImage `json:"images"`
}

type wireImage struct {
	Thumb    string `json:"thumb"`
	Fullsize string `json:"fullsize"`
	Alt      string `json:"alt"`
}

func (w wireFeed) toFeed() *Feed {
	f := &Feed{Cursor: w.Cursor}
	for _, it := range w.Feed {
		f.Posts = append(f.Posts, it.Post.toPost())
	}
	return f
}

func (p wirePost) toPost() Post {
	created, _ := time.Parse(time.RFC3339, p.Record.CreatedAt)
	post := Post{
		URI: p.URI,
		CID: p.CID,
		Author: Author{
			DID:         p.Author.DID,
			Handle:      p.Author.Handle,
			DisplayName: p.Author.DisplayName,
			Avatar:      p.Author.Avatar,
		},
		Text:        p.Record.Text,
		CreatedAt:   created,
		LikeCount:   p.LikeCount,
		RepostCount: p.RepostCount,
		ReplyCount:  p.ReplyCount,
	}
	if p.Embed != nil && p.Embed.Type == imagesEmbedType {
		for _, im := range p.Embed.Images {
			post.Images = append(post.Images, Image{
				Thumb:    im.Thumb,
				Fullsize: im.Fullsize,
				Alt:      im.Alt,
			})
		}
	}
	return post
}

// xrpcError is the standard XRPC error body.
type xrpcError struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Login exchanges credentials for an access token via
// com.atproto.server.createSession and stores it for subsequent authenticated
// calls. Note that the default public AppView does not host createSession; set
// a PDS URL (e.g. https://bsky.social) with WithService to authenticate.
func (c *Client) Login(ctx context.Context, identifier, password string) error {
	// A map[string]string always marshals successfully.
	body, _ := json.Marshal(map[string]string{
		"identifier": identifier,
		"password":   password,
	})
	endpoint := strings.TrimRight(c.Service, "/") + "/xrpc/com.atproto.server.createSession"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return httpError(resp.StatusCode, data)
	}

	var session struct {
		AccessJwt string `json:"accessJwt"`
		DID       string `json:"did"`
	}
	if err := json.Unmarshal(data, &session); err != nil {
		return err
	}
	c.token = session.AccessJwt
	return nil
}

// AuthorFeed returns the feed of posts authored by actor (a handle or DID) via
// app.bsky.feed.getAuthorFeed.
func (c *Client) AuthorFeed(ctx context.Context, actor string, limit int, cursor string) (*Feed, error) {
	q := url.Values{}
	q.Set("actor", actor)
	setLimitCursor(q, limit, cursor)
	return c.getFeed(ctx, "app.bsky.feed.getAuthorFeed", q)
}

// SearchPosts returns posts matching the query q via app.bsky.feed.searchPosts.
func (c *Client) SearchPosts(ctx context.Context, q string, limit int, cursor string) (*Feed, error) {
	v := url.Values{}
	v.Set("q", q)
	setLimitCursor(v, limit, cursor)
	return c.getFeed(ctx, "app.bsky.feed.searchPosts", v)
}

// Timeline returns the authenticated user's home timeline via
// app.bsky.feed.getTimeline. It requires a prior successful Login.
func (c *Client) Timeline(ctx context.Context, limit int, cursor string) (*Feed, error) {
	if c.token == "" {
		return nil, fmt.Errorf("atproto: Timeline requires authentication; call Login first")
	}
	q := url.Values{}
	setLimitCursor(q, limit, cursor)
	return c.getFeed(ctx, "app.bsky.feed.getTimeline", q)
}

func setLimitCursor(q url.Values, limit int, cursor string) {
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
}

// getFeed performs a GET XRPC call returning a feed shape.
func (c *Client) getFeed(ctx context.Context, method string, q url.Values) (*Feed, error) {
	endpoint := strings.TrimRight(c.Service, "/") + "/xrpc/" + method
	if enc := q.Encode(); enc != "" {
		endpoint += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, httpError(resp.StatusCode, data)
	}

	var wf wireFeed
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, err
	}
	return wf.toFeed(), nil
}

// httpError builds an error from a non-2xx response, including the XRPC error
// body ({error,message}) when present.
func httpError(status int, body []byte) error {
	var xe xrpcError
	if err := json.Unmarshal(body, &xe); err == nil && xe.Error != "" {
		if xe.Message != "" {
			return fmt.Errorf("atproto: xrpc error %d: %s: %s", status, xe.Error, xe.Message)
		}
		return fmt.Errorf("atproto: xrpc error %d: %s", status, xe.Error)
	}
	return fmt.Errorf("atproto: unexpected status %d", status)
}
