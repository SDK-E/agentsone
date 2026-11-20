// SPDX-License-Identifier: MIT

// Package httpsource downloads the engine over HTTPS with byte ranges, so a
// download that stopped continues where it stopped. In the machine container
// every connection leaves through the edge, so the download host must be on
// the internet allow list (downloads.claude.ai by default).
package httpsource

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/SDK-E/agentsone/internal/enginefetch/app"
)

// UserAgent names the client without saying anything about the machine.
const UserAgent = "agentsone-enginefetch/1"

// Source is an app.Source over HTTP.
type Source struct{ client *http.Client }

var _ app.Source = (*Source)(nil)

// New returns a source on client, or on a default client when nil. The
// source never follows a redirect to another host.
func New(client *http.Client) *Source {
	c := &http.Client{}
	if client != nil {
		*c = *client
	}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("refused a redirect to another host, %s", req.URL.Host)
		}
		return nil
	}
	return &Source{client: c}
}

// Fetch implements app.Source.
func (s *Source) Fetch(ctx context.Context, url string, offset int64) (app.Body, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return app.Body{}, fmt.Errorf("build the download request: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return app.Body{}, ctx.Err()
		}
		return app.Body{}, fmt.Errorf("could not reach the download server %s (is it on the internet allow list?): %w", req.URL.Host, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return app.Body{ReadCloser: resp.Body, Offset: 0}, nil
	case http.StatusPartialContent:
		start, ok := rangeStart(resp.Header.Get("Content-Range"))
		if !ok {
			_ = resp.Body.Close() //nolint:errcheck // a turned-away read-only body has nothing to flush
			return app.Body{}, fmt.Errorf("the download server sent an unreadable Content-Range %q", resp.Header.Get("Content-Range"))
		}
		return app.Body{ReadCloser: resp.Body, Offset: start}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		_ = resp.Body.Close() //nolint:errcheck // a turned-away read-only body has nothing to flush
		return app.Body{}, app.ErrRangeRefused
	}
	_ = resp.Body.Close() //nolint:errcheck // a turned-away read-only body has nothing to flush
	return app.Body{}, fmt.Errorf("the download server answered %s", resp.Status)
}

// rangeStart reads the first byte of "bytes <start>-<end>/<total>".
func rangeStart(h string) (int64, bool) {
	rest, ok := strings.CutPrefix(h, "bytes ")
	if !ok {
		return 0, false
	}
	first, _, ok := strings.Cut(rest, "-")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(first, 10, 64)
	return n, err == nil && n >= 0
}
