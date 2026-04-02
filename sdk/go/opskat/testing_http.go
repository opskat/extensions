//go:build !wasip1

package opskat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
)

// testHTTPHandle simulates an HTTP IO handle backed by httptest.
type testHTTPHandle struct {
	handler http.HandlerFunc
	method  string
	url     string
	headers map[string]string
	body    bytes.Buffer
	resp    *http.Response
	flushed bool
}

type testIOEntry struct {
	httpHandle *testHTTPHandle
}

var (
	testIOHandles = map[uint32]*testIOEntry{}
	testIONextID  uint32 = 1
	testIOMu      sync.Mutex
)

func (h *TestHost) IOOpen(params []byte) (uint32, []byte, error) {
	var p struct {
		Type    string            `json:"type"`
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
	}
	json.Unmarshal(params, &p)

	if p.Type != "http" {
		return 0, nil, fmt.Errorf("only http IO is supported in TestHost")
	}

	if h.httpHandler == nil {
		return 0, nil, fmt.Errorf("no HTTP mock configured: use WithMockHTTP")
	}

	testIOMu.Lock()
	id := testIONextID
	testIONextID++
	testIOHandles[id] = &testIOEntry{
		httpHandle: &testHTTPHandle{
			handler: h.httpHandler,
			method:  p.Method,
			url:     p.URL,
			headers: p.Headers,
		},
	}
	testIOMu.Unlock()

	return id, []byte(`{}`), nil
}

func (h *TestHost) IORead(handleID uint32, size int) ([]byte, error) {
	testIOMu.Lock()
	entry, ok := testIOHandles[handleID]
	testIOMu.Unlock()
	if !ok || entry.httpHandle == nil {
		return nil, fmt.Errorf("handle not found")
	}
	hh := entry.httpHandle
	if !hh.flushed || hh.resp == nil {
		return nil, fmt.Errorf("not flushed")
	}
	buf := make([]byte, size)
	n, err := hh.resp.Body.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	return nil, err
}

func (h *TestHost) IOWrite(handleID uint32, data []byte) (int, error) {
	testIOMu.Lock()
	entry, ok := testIOHandles[handleID]
	testIOMu.Unlock()
	if !ok || entry.httpHandle == nil {
		return 0, fmt.Errorf("handle not found")
	}
	return entry.httpHandle.body.Write(data)
}

func (h *TestHost) IOFlush(handleID uint32) ([]byte, error) {
	testIOMu.Lock()
	entry, ok := testIOHandles[handleID]
	testIOMu.Unlock()
	if !ok || entry.httpHandle == nil {
		return nil, fmt.Errorf("handle not found")
	}
	hh := entry.httpHandle

	req, _ := http.NewRequest(hh.method, hh.url, &hh.body)
	for k, v := range hh.headers {
		req.Header.Set(k, v)
	}

	recorder := httptest.NewRecorder()
	hh.handler(recorder, req)
	hh.resp = recorder.Result()
	hh.flushed = true

	meta := IOMeta{
		Status:      hh.resp.StatusCode,
		ContentType: hh.resp.Header.Get("Content-Type"),
		Size:        hh.resp.ContentLength,
		Headers:     make(map[string]string),
	}
	for k := range hh.resp.Header {
		meta.Headers[k] = hh.resp.Header.Get(k)
	}
	metaJSON, _ := json.Marshal(meta)
	return metaJSON, nil
}

func (h *TestHost) IOClose(handleID uint32) error {
	testIOMu.Lock()
	entry, ok := testIOHandles[handleID]
	if ok {
		delete(testIOHandles, handleID)
	}
	testIOMu.Unlock()
	if ok && entry.httpHandle != nil && entry.httpHandle.resp != nil {
		entry.httpHandle.resp.Body.Close()
	}
	return nil
}
