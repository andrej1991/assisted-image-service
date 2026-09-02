package overlay

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/diskfs/go-diskfs/backend"
)

type HTTPReader struct {
	client      *http.Client
	url         string
	headers     map[string]string
	queryParams map[string]string
	length      int64
	offset      int64
	chunkSize   int64
	buffer      []byte
	bufferOff   int64
}

// Interface checks
var _ io.ReadSeekCloser = (*HTTPReader)(nil)
var _ backend.Storage = (*HTTPReader)(nil)

func NewHTTPReader(client *http.Client, url string, headers map[string]string, queryParams map[string]string, chunkSize int64) (*HTTPReader, error) {
	req, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		return nil, err
	}
	
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if len(queryParams) > 0 {
		q := req.URL.Query()
		for k, v := range queryParams {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("failed to get HTTP head: status " + resp.Status)
	}

	cl := resp.Header.Get("Content-Length")
	if cl == "" {
		return nil, errors.New("Content-Length header is missing")
	}

	length, err := strconv.ParseInt(cl, 10, 64)
	if err != nil {
		return nil, err
	}

	return &HTTPReader{
		client:      client,
		url:         url,
		headers:     headers,
		queryParams: queryParams,
		length:      length,
		offset:      0,
		chunkSize:   chunkSize,
		buffer:      nil,
		bufferOff:   -1,
	}, nil
}

func (h *HTTPReader) Read(p []byte) (n int, err error) {
	if h.offset >= h.length {
		return 0, io.EOF
	}

	if h.offset+int64(len(p)) > h.length {
		p = p[:h.length-h.offset]
	}
	if h.buffer == nil || h.offset < h.bufferOff || h.offset >= h.bufferOff+int64(len(h.buffer)) {
		err := h.fetchChunk(h.offset)
		if err != nil {
			return 0, err
		}
	}

	bufStart := h.offset - h.bufferOff
	n = copy(p, h.buffer[bufStart:])
	h.offset += int64(n)
	return n, nil
}

func (h *HTTPReader) ReadAt(p []byte, off int64) (n int, err error) {
	if off >= h.length {
		return 0, io.EOF
	}

	var total int
	for len(p) > 0 && off < h.length {
		if h.buffer == nil || off < h.bufferOff || off >= h.bufferOff+int64(len(h.buffer)) {
			err := h.fetchChunk(off)
			if err != nil {
				return total, err
			}
		}
		bufStart := off - h.bufferOff
		copied := copy(p, h.buffer[bufStart:])
		total += copied
		off += int64(copied)
		p = p[copied:]
	}

	if len(p) > 0 && off >= h.length {
		return total, io.EOF
	}
	return total, nil
}

func (h *HTTPReader) fetchChunk(offset int64) error {
	end := offset + h.chunkSize - 1
	if end >= h.length {
		end = h.length - 1
	}

	req, err := http.NewRequest(http.MethodGet, h.url, nil)
	if err != nil {
		return err
	}
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	if len(h.queryParams) > 0 {
		q := req.URL.Query()
		for k, v := range h.queryParams {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-"+strconv.FormatInt(end, 10))

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return errors.New("unexpected status code fetching chunk: " + resp.Status)
	}

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	h.buffer = buf
	h.bufferOff = offset
	return nil
}

func (h *HTTPReader) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = h.offset + offset
	case io.SeekEnd:
		newOffset = h.length + offset
	default:
		return 0, errors.New("invalid whence")
	}

	if newOffset < 0 {
		return 0, errors.New("negative seek offset")
	}
	h.offset = newOffset
	return newOffset, nil
}

func (h *HTTPReader) Close() error {
	h.buffer = nil
	return nil
}

// Implement backend.Storage for diskfs
func (h *HTTPReader) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{size: h.length}, nil
}

func (h *HTTPReader) Sys() (*os.File, error) {
	return nil, errors.New("Sys() not supported on HTTPReader")
}

func (h *HTTPReader) Writable() (backend.WritableFile, error) {
	return nil, backend.ErrIncorrectOpenMode
}

type mockFileInfo struct {
	size int64
}

func (m *mockFileInfo) Name() string       { return "http_reader" }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0444 }
func (m *mockFileInfo) ModTime() time.Time { return time.Now() }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() interface{}   { return nil }
