package util

import (
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	httpMaxIdleConns          = 100
	httpMaxIdleConnsPerHost   = 32
	httpMaxConnsPerHost       = 64
	httpIdleConnTimeout       = 50 * time.Second
	httpDialTimeout           = 30 * time.Second
	httpKeepAlive             = 30 * time.Second
	httpTLSHandshakeTimeout   = 10 * time.Second
	httpResponseHeaderTimeout = 6 * time.Minute
	httpExpectContinueTimeout = 1 * time.Second
)

var (
	sharedHTTPClient   *http.Client
	initSharedHTTPOnce sync.Once
)

func initSharedHTTP() {
	dialer := &net.Dialer{Timeout: httpDialTimeout, KeepAlive: httpKeepAlive}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = dialer.DialContext
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = nil
	if tr.TLSClientConfig == nil {
		tr.TLSClientConfig = &tls.Config{}
	}
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
	tr.MaxIdleConns = httpMaxIdleConns
	tr.MaxIdleConnsPerHost = httpMaxIdleConnsPerHost
	tr.MaxConnsPerHost = httpMaxConnsPerHost
	tr.IdleConnTimeout = httpIdleConnTimeout
	tr.TLSHandshakeTimeout = httpTLSHandshakeTimeout
	tr.ResponseHeaderTimeout = httpResponseHeaderTimeout
	tr.ExpectContinueTimeout = httpExpectContinueTimeout

	sharedHTTPClient = &http.Client{Transport: tr, Timeout: 0}
}

// DefaultHTTPClient returns a shared http.Client suitable for SSE streams.
func DefaultHTTPClient() *http.Client {
	initSharedHTTPOnce.Do(initSharedHTTP)
	return sharedHTTPClient
}
