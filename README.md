<p align="center"><img src="https://raw.githubusercontent.com/go-atproto/brand/main/social/go-atproto.png" alt="go-atproto/atproto" width="720"></p>

# atproto

[![CI](https://github.com/go-atproto/atproto/actions/workflows/ci.yml/badge.svg)](https://github.com/go-atproto/atproto/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-atproto/atproto.svg)](https://pkg.go.dev/github.com/go-atproto/atproto)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

A pure-Go, dependency-free read client for **Bluesky** and the **AT Protocol**,
talking to the XRPC HTTP API.

- **CGO-free** (`CGO_ENABLED=0`) and **zero third-party dependencies** — standard
  library only.
- Targets the public Bluesky AppView at `https://public.api.bsky.app`, which
  serves read methods **without authentication**.
- Optional `Login` exchanges credentials for an access token to use
  authenticated methods (such as the home timeline).

## Install

```sh
go get github.com/go-atproto/atproto
```

## Usage

Fetch an author's feed anonymously against the public AppView — no credentials
required:

```go
package main

import (
	"context"
	"fmt"

	"github.com/go-atproto/atproto"
)

func main() {
	c := atproto.New() // defaults to https://public.api.bsky.app

	feed, err := c.AuthorFeed(context.Background(), "bsky.app", 25, "")
	if err != nil {
		panic(err)
	}

	for _, p := range feed.Posts {
		fmt.Printf("@%s (%d likes): %s\n", p.Author.Handle, p.LikeCount, p.Text)
		for _, img := range p.Images {
			fmt.Printf("    image: %s (%s)\n", img.Fullsize, img.Alt)
		}
	}

	if feed.Cursor != "" {
		// Pass feed.Cursor back to AuthorFeed to fetch the next page.
	}
}
```

### Searching posts

```go
feed, err := c.SearchPosts(context.Background(), "golang", 25, "")
```

### Authenticated timeline

`Timeline` requires a token. Point the client at a PDS (for example
`https://bsky.social`) and call `Login` first:

```go
c := atproto.New(atproto.WithService("https://bsky.social"))
if err := c.Login(context.Background(), "you.bsky.social", "app-password"); err != nil {
	panic(err)
}
feed, err := c.Timeline(context.Background(), 25, "")
```

## Configuration

| Option                    | Purpose                                         |
| ------------------------- | ----------------------------------------------- |
| `WithService(url)`        | Override the XRPC base URL / PDS.               |
| `WithHTTPClient(client)`  | Supply a custom `*http.Client` (timeouts, etc). |
| `WithUserAgent(ua)`       | Set the `User-Agent` header.                    |

## License

BSD-3-Clause. See [LICENSE](LICENSE).
