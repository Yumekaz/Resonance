package main

import (
	"net/http"
	"testing"
)

func TestPercentiles(t *testing.T) {
	if got := percentile([]float64{4, 1, 3, 2}, .5); got != 2.5 {
		t.Fatalf("median %v", got)
	}
	if got := percentile([]float64{3, 1, 2}, .5); got != 2 {
		t.Fatalf("median %v", got)
	}
	values := make([]float64, 100)
	for i := range values {
		values[i] = float64(i + 1)
	}
	if got := percentile(values, .95); got != 95 {
		t.Fatalf("p95 %v", got)
	}
}

func TestRejectIncorrectRangeEvidence(t *testing.T) {
	resp := &http.Response{StatusCode: 206, ContentLength: 10, Header: make(http.Header)}
	resp.Header.Set("Content-Range", "bytes 20-29/100")
	if err := validateResponse(resp, 20, 10, 100, true); err != nil {
		t.Fatal(err)
	}
	resp.Header.Set("Content-Range", "bytes 0-9/100")
	if err := validateResponse(resp, 20, 10, 100, true); err == nil {
		t.Fatal("accepted wrong bytes")
	}
	resp.Header.Set("Content-Range", "bytes 20-29/100")
	resp.ContentLength = 9
	if err := validateResponse(resp, 20, 10, 100, true); err == nil {
		t.Fatal("accepted wrong length")
	}
}
