package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealthz(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"db ok", nil, http.StatusOK},
		{"db down", errors.New("boom"), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(fakePinger{c.err}).ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d", rec.Code, c.want)
			}
		})
	}
}
