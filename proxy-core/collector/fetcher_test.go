package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("expected User-Agent header")
		}
		_, _ = w.Write([]byte("1.1.1.1:80\n2.2.2.2:443"))
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(body) != "1.1.1.1:80\n2.2.2.2:443" {
		t.Fatalf("body = %q", body)
	}
}

func TestFetchNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), srv.URL)
	if err != ErrFetch {
		t.Fatalf("expected ErrFetch, got %v", err)
	}
}

func TestFetchInvalidURL(t *testing.T) {
	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), "://bad-url")
	if err == nil {
		t.Fatal("expected error for invalid url")
	}
}

func TestFetchCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(ctx, "http://127.0.0.1:1")
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestFetchLargeBodyLimited(t *testing.T) {
	// 超过 8MB 的内容应被截断而不是 OOM
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1024)
		for i := 0; i < 9000; i++ { // ~9MB
			_, _ = w.Write(chunk)
		}
	}))
	defer srv.Close()

	f := NewFetcher(5 * time.Second)
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(body) > 8<<20 {
		t.Fatalf("body too large: %d bytes", len(body))
	}
}
