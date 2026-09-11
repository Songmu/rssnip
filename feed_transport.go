package rssnip

import (
	"context"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/idna"
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

type scheduledHostFetch struct {
	state *hostFetchState
	mu    sync.Mutex
	taken bool
}

type scheduledHostFetchKey struct{}

func newPoliteTransport(base http.RoundTripper) *politeTransport {
	if base == nil {
		base = http.DefaultTransport
	}
	return &politeTransport{base: base}
}

func (transport *politeTransport) Do(client *http.Client, request *http.Request) (*http.Response, error) {
	state := transport.hostState(request.URL.Hostname())
	if err := acquireHostFetch(request.Context(), state); err != nil {
		return nil, err
	}
	requestContext, cancel := context.WithTimeout(request.Context(), feedRequestTimeout)
	scheduled := &scheduledHostFetch{state: state}
	request = request.Clone(context.WithValue(requestContext, scheduledHostFetchKey{}, scheduled))
	response, err := client.Do(request)
	if err != nil {
		cancel()
		if scheduled.release() {
			releaseHostFetch(state)
		}
		return nil, err
	}
	if response.Body == nil {
		cancel()
		return response, nil
	}
	response.Body = &timeoutBody{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

func doRequest(client *http.Client, request *http.Request) (*http.Response, error) {
	if transport, ok := client.Transport.(*politeTransport); ok {
		return transport.Do(client, request)
	}
	return client.Do(request)
}

func (transport *politeTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var state *hostFetchState
	var cancel context.CancelFunc
	if scheduled, ok := request.Context().Value(scheduledHostFetchKey{}).(*scheduledHostFetch); ok {
		state = scheduled.take()
	}
	if state == nil {
		state = transport.hostState(request.URL.Hostname())
		if err := acquireHostFetch(request.Context(), state); err != nil {
			return nil, err
		}
		requestContext, requestCancel := context.WithTimeout(request.Context(), feedRequestTimeout)
		cancel = requestCancel
		request = request.Clone(requestContext)
	}

	response, err := transport.base.RoundTrip(request)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		releaseHostFetch(state)
		return nil, err
	}
	if response.Body == nil {
		if cancel != nil {
			cancel()
		}
		releaseHostFetch(state)
		return response, nil
	}
	response.Body = &hostFetchBody{ReadCloser: response.Body, release: func() {
		if cancel != nil {
			cancel()
		}
		releaseHostFetch(state)
	}}
	return response, nil
}

func (scheduled *scheduledHostFetch) take() *hostFetchState {
	scheduled.mu.Lock()
	defer scheduled.mu.Unlock()
	if scheduled.taken {
		return nil
	}
	scheduled.taken = true
	return scheduled.state
}

func (scheduled *scheduledHostFetch) release() bool {
	scheduled.mu.Lock()
	defer scheduled.mu.Unlock()
	if scheduled.taken {
		return false
	}
	scheduled.taken = true
	return true
}

func (transport *politeTransport) hostState(host string) *hostFetchState {
	stateValue, _ := transport.hosts.LoadOrStore(canonicalHost(host), &hostFetchState{
		token: make(chan struct{}, 1),
	})
	return stateValue.(*hostFetchState)
}

func canonicalHost(host string) string {
	address, zone, hasZone := strings.Cut(host, "%")
	if parsed, err := netip.ParseAddr(address); err == nil {
		if hasZone {
			return parsed.String() + "%" + zone
		}
		return parsed.String()
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if ascii, err := idna.Lookup.ToASCII(host); err == nil {
		return ascii
	}
	return host
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

type timeoutBody struct {
	io.ReadCloser
	once   sync.Once
	cancel context.CancelFunc
}

func (body *timeoutBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(body.cancel)
	return err
}

func (body *hostFetchBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(body.release)
	return err
}
