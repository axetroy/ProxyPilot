package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// TestFetchLocalFile 从本地文件读取订阅内容（file:// URL）。
func TestFetchLocalFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "list.txt")
	content := "1.1.1.1:80\n2.2.2.2:443\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	f := NewFetcher(5 * time.Second)
	// file:// 形式：跨平台用 filepath.ToSlash 拼路径
	fileURL := "file://" + filepath.ToSlash(path)
	body, err := f.Fetch(context.Background(), fileURL)
	if err != nil {
		t.Fatalf("fetch local file: %v", err)
	}
	if string(body) != content {
		t.Fatalf("body = %q, want %q", body, content)
	}
}

// TestFetchLocalFileMissing 文件不存在时返回错误（不 panic）。
func TestFetchLocalFileMissing(t *testing.T) {
	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), "file://"+filepath.ToSlash(filepath.Join(t.TempDir(), "nope.txt")))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestFetchLocalFileTooLarge 文件超过大小上限时返回错误而不是静默截断。
func TestFetchLocalFileTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, make([]byte, maxBodySize+1), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), "file://"+filepath.ToSlash(path))
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

// TestFetchLocalFileCanceled 取消的 context 应中断本地文件读取。
func TestFetchLocalFileCanceled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(ctx, "file://"+filepath.ToSlash(path))
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// TestFetchUnsupportedScheme 非 http/https/file 的 scheme 返回错误。
func TestFetchUnsupportedScheme(t *testing.T) {
	f := NewFetcher(5 * time.Second)
	_, err := f.Fetch(context.Background(), "ftp://example.com/list")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}
}
