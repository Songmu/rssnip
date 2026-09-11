package rssnip

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	maxConcurrentFetches = 8
	hostFetchInterval    = time.Second
	feedRequestTimeout   = 30 * time.Second
)

type politeTransport struct {
	base  http.RoundTripper
	hosts sync.Map
}

type hostFetchState struct {
	token        chan struct{}
	lastFinished time.Time
}

func newPoliteTransport(base http.RoundTripper) *politeTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &politeTransport{base: base}
}

func (transport *politeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	host := canonicalHost(request.URL.Hostname())
	stateValue, _ := transport.hosts.LoadOrStore(host, &hostFetchState{
		token: make(chan struct{}, 1),
	})
	state := stateValue.(*hostFetchState)
	if err := acquireHostFetch(request.Context(), state); err != nil {
		return nil, err
	}

	requestContext, cancel := context.WithTimeout(request.Context(), feedRequestTimeout)
	request = request.Clone(requestContext)
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		cancel()
		releaseHostFetch(state)
		return nil, err
	}
	if response.Body == nil {
		cancel()
		releaseHostFetch(state)
		return response, nil
	}
	response.Body = &hostFetchBody{ReadCloser: response.Body, release: func() {
		cancel()
		releaseHostFetch(state)
	}}
	return response, nil
}

func canonicalHost(host string) string {
	address, zone, hasZone := strings.Cut(host, "%")
	if parsed, err := netip.ParseAddr(address); err == nil {
		if hasZone {
			return parsed.String() + "%" + zone
		}
		return parsed.String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

func acquireHostFetch(ctx context.Context, state *hostFetchState) error {
	select {
	case state.token <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay := time.Until(state.lastFinished.Add(hostFetchInterval)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			<-state.token
			return ctx.Err()
		}
	}
	return nil
}

func releaseHostFetch(state *hostFetchState) {
	state.lastFinished = time.Now()
	<-state.token
}

type hostFetchBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (body *hostFetchBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(body.release)
	return err
}
