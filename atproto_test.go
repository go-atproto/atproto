package atproto

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// errTransport always fails the round-trip.
type errTransport struct{}

func (errTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("transport boom")
}

// errBody is a ReadCloser whose Read always fails.
type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("read boom") }
func (errBody) Close() error             { return nil }

// bodyErrTransport returns a 200 response whose body errors on read.
type bodyErrTransport struct{}

func (bodyErrTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       errBody{},
		Header:     make(http.Header),
	}, nil
}

const feedJSON = `{
  "feed": [
    {
      "post": {
        "uri": "at://did:plc:abc/app.bsky.feed.post/1",
        "cid": "cid1",
        "author": {"did": "did:plc:abc", "handle": "alice.test", "displayName": "Alice", "avatar": "https://av/a.jpg"},
        "record": {"text": "hello world", "createdAt": "2026-07-10T12:00:00Z"},
        "likeCount": 3, "repostCount": 1, "replyCount": 2,
        "embed": {"$type": "app.bsky.embed.images#view", "images": [{"thumb": "t.jpg", "fullsize": "f.jpg", "alt": "an image"}]}
      }
    },
    {
      "post": {
        "uri": "at://did:plc:xyz/app.bsky.feed.post/2",
        "cid": "cid2",
        "author": {"did": "did:plc:xyz", "handle": "bob.test"},
        "record": {"text": "no embed", "createdAt": "2026-07-10T13:00:00Z"},
        "likeCount": 0, "repostCount": 0, "replyCount": 0
      }
    },
    {
      "post": {
        "uri": "at://did:plc:xyz/app.bsky.feed.post/3",
        "cid": "cid3",
        "author": {"did": "did:plc:xyz", "handle": "bob.test"},
        "record": {"text": "external embed", "createdAt": "2026-07-10T14:00:00Z"},
        "embed": {"$type": "app.bsky.embed.external#view"}
      }
    }
  ],
  "cursor": "next-cursor"
}`

func TestOptionsAndNew(t *testing.T) {
	hc := &http.Client{Timeout: time.Second}
	c := New(
		WithService("https://example.test"),
		WithHTTPClient(hc),
		WithUserAgent("test-agent/1.0"),
	)
	if c.Service != "https://example.test" {
		t.Fatalf("service = %q", c.Service)
	}
	if c.HTTPClient != hc {
		t.Fatal("http client not set")
	}
	if c.UserAgent != "test-agent/1.0" {
		t.Fatalf("ua = %q", c.UserAgent)
	}

	// Defaults.
	d := New()
	if d.Service != DefaultService || d.HTTPClient != http.DefaultClient || d.UserAgent == "" {
		t.Fatalf("defaults wrong: %+v", d)
	}
}

func TestAuthorFeedSuccess(t *testing.T) {
	var gotUA, gotAuth, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		io.WriteString(w, feedJSON)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL+"/"), WithUserAgent("ua/1"))
	feed, err := c.AuthorFeed(context.Background(), "alice.test", 25, "cur1")
	if err != nil {
		t.Fatal(err)
	}
	if gotUA != "ua/1" {
		t.Fatalf("user-agent = %q", gotUA)
	}
	if gotAuth != "" {
		t.Fatalf("unexpected auth header %q", gotAuth)
	}
	if gotPath != "/xrpc/app.bsky.feed.getAuthorFeed" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotQuery, "actor=alice.test") || !strings.Contains(gotQuery, "limit=25") || !strings.Contains(gotQuery, "cursor=cur1") {
		t.Fatalf("query = %q", gotQuery)
	}
	if feed.Cursor != "next-cursor" {
		t.Fatalf("cursor = %q", feed.Cursor)
	}
	if len(feed.Posts) != 3 {
		t.Fatalf("posts = %d", len(feed.Posts))
	}

	// Post 0: with images embed.
	p0 := feed.Posts[0]
	if p0.URI != "at://did:plc:abc/app.bsky.feed.post/1" || p0.CID != "cid1" {
		t.Fatalf("p0 identity: %+v", p0)
	}
	if p0.Author.Handle != "alice.test" || p0.Author.DisplayName != "Alice" || p0.Author.Avatar != "https://av/a.jpg" || p0.Author.DID != "did:plc:abc" {
		t.Fatalf("p0 author: %+v", p0.Author)
	}
	if p0.Text != "hello world" || p0.LikeCount != 3 || p0.RepostCount != 1 || p0.ReplyCount != 2 {
		t.Fatalf("p0 fields: %+v", p0)
	}
	if !p0.CreatedAt.Equal(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("p0 createdAt: %v", p0.CreatedAt)
	}
	if len(p0.Images) != 1 || p0.Images[0].Thumb != "t.jpg" || p0.Images[0].Fullsize != "f.jpg" || p0.Images[0].Alt != "an image" {
		t.Fatalf("p0 images: %+v", p0.Images)
	}

	// Post 1: no embed.
	if len(feed.Posts[1].Images) != 0 {
		t.Fatalf("p1 images should be empty: %+v", feed.Posts[1].Images)
	}
	// Post 2: non-images embed.
	if len(feed.Posts[2].Images) != 0 {
		t.Fatalf("p2 images should be empty: %+v", feed.Posts[2].Images)
	}
}

func TestSearchPostsSuccessNoLimitNoCursor(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `{"feed":[],"cursor":""}`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	feed, err := c.SearchPosts(context.Background(), "golang", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/xrpc/app.bsky.feed.searchPosts" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "q=golang" {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(feed.Posts) != 0 {
		t.Fatalf("posts = %d", len(feed.Posts))
	}
}

func TestTimelineRequiresAuth(t *testing.T) {
	c := New()
	_, err := c.Timeline(context.Background(), 10, "")
	if err == nil || !strings.Contains(err.Error(), "requires authentication") {
		t.Fatalf("expected auth error, got %v", err)
	}
}

func TestLoginThenTimeline(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/xrpc/com.atproto.server.createSession":
			if r.Method != http.MethodPost {
				t.Errorf("createSession method = %s", r.Method)
			}
			io.WriteString(w, `{"accessJwt":"tok123","did":"did:plc:me"}`)
		case "/xrpc/app.bsky.feed.getTimeline":
			gotAuth = r.Header.Get("Authorization")
			io.WriteString(w, `{"feed":[],"cursor":"c"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	if err := c.Login(context.Background(), "me.test", "pw"); err != nil {
		t.Fatal(err)
	}
	if c.token != "tok123" {
		t.Fatalf("token = %q", c.token)
	}
	feed, err := c.Timeline(context.Background(), 5, "cur")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Fatalf("auth header = %q", gotAuth)
	}
	if feed.Cursor != "c" {
		t.Fatalf("cursor = %q", feed.Cursor)
	}
}

func TestLoginErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":"AuthenticationRequired","message":"invalid identifier or password"}`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	err := c.Login(context.Background(), "x", "y")
	if err == nil || !strings.Contains(err.Error(), "AuthenticationRequired") || !strings.Contains(err.Error(), "invalid identifier") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	if err := c.Login(context.Background(), "x", "y"); err == nil {
		t.Fatal("expected json error")
	}
}

func TestLoginBadRequestBuild(t *testing.T) {
	c := New(WithService("http://\x7f\n"))
	if err := c.Login(context.Background(), "x", "y"); err == nil {
		t.Fatal("expected request-build error")
	}
}

func TestLoginTransportError(t *testing.T) {
	c := New(WithService("http://example.test"), WithHTTPClient(&http.Client{Transport: errTransport{}}))
	if err := c.Login(context.Background(), "x", "y"); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestLoginReadError(t *testing.T) {
	c := New(WithService("http://example.test"), WithHTTPClient(&http.Client{Transport: bodyErrTransport{}}))
	if err := c.Login(context.Background(), "x", "y"); err == nil {
		t.Fatal("expected read error")
	}
}

func TestGetFeedBadRequestBuild(t *testing.T) {
	c := New(WithService("http://\x7f\n"))
	if _, err := c.AuthorFeed(context.Background(), "a", 0, ""); err == nil {
		t.Fatal("expected request-build error")
	}
}

func TestGetFeedTransportError(t *testing.T) {
	c := New(WithService("http://example.test"), WithHTTPClient(&http.Client{Transport: errTransport{}}))
	if _, err := c.AuthorFeed(context.Background(), "a", 0, ""); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestGetFeedReadError(t *testing.T) {
	c := New(WithService("http://example.test"), WithHTTPClient(&http.Client{Transport: bodyErrTransport{}}))
	if _, err := c.AuthorFeed(context.Background(), "a", 0, ""); err == nil {
		t.Fatal("expected read error")
	}
}

func TestGetFeedBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{bad`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	if _, err := c.AuthorFeed(context.Background(), "a", 0, ""); err == nil {
		t.Fatal("expected json error")
	}
}

func TestGetFeedErrorStatusMessageOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"NotFound"}`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	_, err := c.AuthorFeed(context.Background(), "a", 0, "")
	if err == nil || !strings.Contains(err.Error(), "NotFound") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "::") {
		t.Fatalf("message-only error malformed: %v", err)
	}
}

func TestGetFeedErrorStatusNonXRPCBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `Internal Server Error`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	_, err := c.AuthorFeed(context.Background(), "a", 0, "")
	if err == nil || !strings.Contains(err.Error(), "unexpected status 500") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetFeedErrorStatusEmptyErrorField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, `{"error":"","message":"nope"}`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	_, err := c.AuthorFeed(context.Background(), "a", 0, "")
	if err == nil || !strings.Contains(err.Error(), "unexpected status 502") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchActorsSuccess(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		io.WriteString(w, `{"actors":[{"did":"did:plc:abc","handle":"alice.bsky.social","displayName":"Alice","description":"hi there","avatar":"https://cdn/av.jpg"}],"cursor":"c1"}`)
	}))
	defer srv.Close()

	c := New(WithService(srv.URL))
	page, err := c.SearchActors(context.Background(), "alice", 25, "c0")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/xrpc/app.bsky.actor.searchActors" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "cursor=c0&limit=25&q=alice" {
		t.Fatalf("query = %q", gotQuery)
	}
	if page.Cursor != "c1" || len(page.Actors) != 1 {
		t.Fatalf("page = %+v", page)
	}
	a := page.Actors[0]
	if a.DID != "did:plc:abc" || a.Handle != "alice.bsky.social" || a.DisplayName != "Alice" || a.Description != "hi there" || a.Avatar != "https://cdn/av.jpg" {
		t.Fatalf("actor = %+v", a)
	}
}

func TestSearchActorsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"actors":[],"cursor":""}`)
	}))
	defer srv.Close()
	page, err := New(WithService(srv.URL)).SearchActors(context.Background(), "nobody", 0, "")
	if err != nil || len(page.Actors) != 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestSearchActorsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"InvalidRequest","message":"bad q"}`)
	}))
	defer srv.Close()
	if _, err := New(WithService(srv.URL)).SearchActors(context.Background(), "x", 0, ""); err == nil {
		t.Fatal("expected an error on a 400 response")
	}
}

func TestSearchActorsMalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{not json`)
	}))
	defer srv.Close()
	if _, err := New(WithService(srv.URL)).SearchActors(context.Background(), "x", 0, ""); err == nil {
		t.Fatal("expected a JSON decode error")
	}
}
