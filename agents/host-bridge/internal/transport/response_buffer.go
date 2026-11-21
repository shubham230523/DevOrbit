package transport

import (
	"bytes"
	"net/http"
)

type ResponseWriterBuffer struct {
	Code        int
	HeaderMap   http.Header
	Body        bytes.Buffer
	wroteHeader bool
}

func NewResponseWriterBuffer() *ResponseWriterBuffer {
	return &ResponseWriterBuffer{
		Code:      http.StatusOK,
		HeaderMap: make(http.Header),
	}
}

func (rw *ResponseWriterBuffer) Header() http.Header {
	return rw.HeaderMap
}

func (rw *ResponseWriterBuffer) Write(buf []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.Body.Write(buf)
}

func (rw *ResponseWriterBuffer) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.Code = code
	rw.wroteHeader = true
}
