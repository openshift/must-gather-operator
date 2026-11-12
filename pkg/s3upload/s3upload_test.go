package s3upload

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestObjectKey(t *testing.T) {
	tests := []struct {
		prefix, file, want string
	}{
		{"", "/must-gather-upload/mg.tar.gz", "mg.tar.gz"},
		{"cluster-a", "/must-gather-upload/mg.tar.gz", "cluster-a/mg.tar.gz"},
		{"cluster-a/", "mg.tar.gz", "cluster-a/mg.tar.gz"},
		{"/a/b/", "x/mg.tar.gz", "a/b/mg.tar.gz"},
	}
	for _, tt := range tests {
		if got := ObjectKey(tt.prefix, tt.file); got != tt.want {
			t.Errorf("ObjectKey(%q, %q) = %q, want %q", tt.prefix, tt.file, got, tt.want)
		}
	}
}

// isolateAWSEnv keeps the developer's ~/.aws files and env out of the test.
func isolateAWSEnv(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", empty)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", empty)
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
}

func TestUploadPathStyleToCustomEndpoint(t *testing.T) {
	isolateAWSEnv(t)

	var mu sync.Mutex
	var gotMethod, gotPath, gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotMethod, gotPath, gotAuth, gotBody = r.Method, r.URL.Path, r.Header.Get("Authorization"), b
		mu.Unlock()
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "must-gather.local.123.tar.gz")
	content := []byte("must-gather archive content")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}

	key, err := Upload(context.Background(), Options{
		File:      file,
		Bucket:    "mg-archive",
		KeyPrefix: "cluster-a/",
		Endpoint:  srv.URL,
	})
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}

	if key != "cluster-a/must-gather.local.123.tar.gz" {
		t.Errorf("key = %q", key)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if gotPath != "/mg-archive/cluster-a/must-gather.local.123.tar.gz" {
		t.Errorf("path = %q, want path-style /bucket/key", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/") || !strings.Contains(gotAuth, "/us-east-1/s3/") {
		t.Errorf("Authorization header not SigV4 for default region: %q", gotAuth)
	}
	if string(gotBody) != string(content) {
		t.Errorf("body = %q, want %q", gotBody, content)
	}
}

func TestUploadServerError(t *testing.T) {
	isolateAWSEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>denied</Message></Error>`))
	}))
	defer srv.Close()

	file := filepath.Join(t.TempDir(), "mg.tar.gz")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Upload(context.Background(), Options{File: file, Bucket: "b", Endpoint: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("expected AccessDenied error, got %v", err)
	}
}

func TestUploadMissingFile(t *testing.T) {
	isolateAWSEnv(t)
	if _, err := Upload(context.Background(), Options{File: "/does/not/exist", Bucket: "b"}); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestRunS3UploadRequiresFlags(t *testing.T) {
	if rc := RunS3Upload([]string{"--bucket", "b"}); rc != 1 {
		t.Errorf("rc = %d, want 1 without --file", rc)
	}
	if rc := RunS3Upload([]string{"--file", "f"}); rc != 1 {
		t.Errorf("rc = %d, want 1 without --bucket", rc)
	}
	if rc := RunS3Upload([]string{"--unknown"}); rc != 1 {
		t.Errorf("rc = %d, want 1 for unknown flag", rc)
	}
}
