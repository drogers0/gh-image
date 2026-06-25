package upload

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testClient returns a Client wired to a test server.
func testClient(srv *httptest.Server) *Client {
	return &Client{http: srv.Client(), baseURL: srv.URL}
}

// validPolicy is a policy response body with all required fields populated.
// uploadURL is filled in per-test to point at the test server's S3 route.
func validPolicy(uploadURL string) string {
	return fmt.Sprintf(`{
		"upload_url": %q,
		"asset": {"id": 99, "name": "pic.png"},
		"form": {"key": "k", "policy": "p"},
		"asset_upload_authenticity_token": "AUTH"
	}`, uploadURL)
}

func TestNewClient(t *testing.T) {
	c := NewClient(&http.Cookie{Name: "user_session", Value: "tok"})
	if c == nil || c.http == nil {
		t.Fatal("expected non-nil Client with non-nil http client")
	}
	if c.baseURL != "https://github.com" {
		t.Errorf("baseURL = %q, want https://github.com", c.baseURL)
	}
	if c.http.Jar == nil {
		t.Error("expected the http client to carry the GitHub cookie jar")
	}
}

func TestRequestPolicy(t *testing.T) {
	t.Run("success parses the policy and sends the form fields", func(t *testing.T) {
		var gotFields map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseMultipartForm(1 << 20)
			gotFields = map[string]string{}
			for k, v := range r.MultipartForm.Value {
				gotFields[k] = v[0]
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(validPolicy("https://s3.example/upload")))
		}))
		defer srv.Close()

		policy, err := testClient(srv).requestPolicy("octo", "hello", "UTOKEN", 42, "pic.png", 1234, "image/png")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if policy.UploadURL != "https://s3.example/upload" || policy.Asset.ID != 99 || policy.AssetUploadAuthenticityToken != "AUTH" {
			t.Errorf("unexpected policy: %+v", policy)
		}
		want := map[string]string{
			"name": "pic.png", "size": "1234", "content_type": "image/png",
			"authenticity_token": "UTOKEN", "repository_id": "42",
		}
		for k, v := range want {
			if gotFields[k] != v {
				t.Errorf("form field %q = %q, want %q", k, gotFields[k], v)
			}
		}
	})

	t.Run("non-201 status is an error with the body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("denied"))
		}))
		defer srv.Close()
		_, err := testClient(srv).requestPolicy("octo", "hello", "t", 1, "f", 1, "image/png")
		if err == nil || !strings.Contains(err.Error(), "expected 201, got 403") || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("expected 403 error with body, got %v", err)
		}
	})

	t.Run("missing required fields each error", func(t *testing.T) {
		cases := []struct {
			name, body, want string
		}{
			{"no upload_url", `{"asset":{"id":1},"form":{"k":"v"},"asset_upload_authenticity_token":"a"}`, "missing upload_url"},
			{"no auth token", `{"upload_url":"u","asset":{"id":1},"form":{"k":"v"}}`, "missing asset_upload_authenticity_token"},
			{"no form", `{"upload_url":"u","asset":{"id":1},"asset_upload_authenticity_token":"a"}`, "missing form fields"},
			{"no asset id", `{"upload_url":"u","form":{"k":"v"},"asset_upload_authenticity_token":"a"}`, "missing asset ID"},
			{"bad json", `{not json`, "decoding policy response"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusCreated)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()
				_, err := testClient(srv).requestPolicy("octo", "hello", "t", 1, "f", 1, "image/png")
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("want error %q, got %v", tc.want, err)
				}
			})
		}
	})
}

func TestFinalizeUpload(t *testing.T) {
	policy := &policyResponse{AssetUploadAuthenticityToken: "AUTH"}
	policy.Asset.ID = 99

	t.Run("success builds the full Result", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method = %s, want PUT", r.Method)
			}
			if !strings.HasSuffix(r.URL.Path, "/upload/assets/99") {
				t.Errorf("path = %s, want .../upload/assets/99", r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"href":"https://gh/assets/x","name":"pic.png"}`))
		}))
		defer srv.Close()

		res, err := testClient(srv).finalizeUpload("octo", "hello", policy)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res.URL != "https://gh/assets/x" || res.Name != "pic.png" || res.Markdown != "![pic.png](https://gh/assets/x)" {
			t.Errorf("unexpected Result: %+v", res)
		}
	})

	t.Run("non-200 is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		}))
		defer srv.Close()
		_, err := testClient(srv).finalizeUpload("octo", "hello", policy)
		if err == nil || !strings.Contains(err.Error(), "expected 200, got 500") {
			t.Fatalf("expected 500 error, got %v", err)
		}
	})

	t.Run("malformed json is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{not json`))
		}))
		defer srv.Close()
		_, err := testClient(srv).finalizeUpload("octo", "hello", policy)
		if err == nil || !strings.Contains(err.Error(), "decoding finalize response") {
			t.Fatalf("expected decode error, got %v", err)
		}
	})
}

func TestUploadToS3(t *testing.T) {
	writeFile := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "pic.png")
		if err := os.WriteFile(p, []byte("imagedata"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	policy := func(url string) *policyResponse {
		return &policyResponse{UploadURL: url, Form: map[string]string{"key": "k", "extra": "z"}}
	}

	t.Run("success on 2xx; file is the last field", func(t *testing.T) {
		var lastField string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mr, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("not multipart: %v", err)
			}
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				lastField = part.FormName()
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		if err := uploadToS3(policy(srv.URL), writeFile(t), "pic.png", "image/png"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lastField != "file" {
			t.Errorf("last multipart field = %q, want file", lastField)
		}
	})

	t.Run("non-2xx is an error with the body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("policy violation"))
		}))
		defer srv.Close()
		err := uploadToS3(policy(srv.URL), writeFile(t), "pic.png", "image/png")
		if err == nil || !strings.Contains(err.Error(), "S3 returned 403") || !strings.Contains(err.Error(), "policy violation") {
			t.Fatalf("expected 403 error with body, got %v", err)
		}
	})

	t.Run("missing file is an error before any request", func(t *testing.T) {
		err := uploadToS3(policy("http://127.0.0.1:0"), filepath.Join(t.TempDir(), "nope.png"), "nope.png", "image/png")
		if err == nil || !strings.Contains(err.Error(), "opening file") {
			t.Fatalf("expected opening-file error, got %v", err)
		}
	})

	t.Run("unreachable S3 endpoint is a request error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close()
		err := uploadToS3(policy(url), writeFile(t), "pic.png", "image/png")
		if err == nil || !strings.Contains(err.Error(), "S3 upload request") {
			t.Fatalf("expected S3 request error, got %v", err)
		}
	})
}

func TestUpload_FullFlow(t *testing.T) {
	img := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(img, []byte("pngbytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	// One server routes all four steps. Plain HTTP (not TLS): the S3 leg uses
	// uploadToS3's own bare client with the default transport.
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/octo/hello", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`x={"uploadToken":"TKN"}`))
	})
	mux.HandleFunc("/upload/policies/assets", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(validPolicy(srv.URL + "/s3")))
	})
	mux.HandleFunc("/s3", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/upload/assets/99", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"href":"https://gh/assets/x","name":"shot.png"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	res, err := testClient(srv).Upload("octo", "hello", 42, img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Markdown != "![shot.png](https://gh/assets/x)" {
		t.Errorf("Markdown = %q", res.Markdown)
	}
}

// TestUpload_StepErrors drives the full flow but makes one step fail, covering
// each of Upload's step-wrapping error branches.
func TestUpload_StepErrors(t *testing.T) {
	img := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		failStep  string // which route returns a failure
		wantInErr string
	}{
		{"step 0 token", "token", "step 0 (get upload token)"},
		{"step 1 policy", "policy", "step 1 (request policy)"},
		{"step 2 s3", "s3", "step 2 (S3 upload)"},
		{"step 3 finalize", "finalize", "step 3 (finalize)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			var srv *httptest.Server
			mux.HandleFunc("/octo/hello", func(w http.ResponseWriter, r *http.Request) {
				if tc.failStep == "token" {
					_, _ = w.Write([]byte(`no token here`))
					return
				}
				_, _ = w.Write([]byte(`x={"uploadToken":"TKN"}`))
			})
			mux.HandleFunc("/upload/policies/assets", func(w http.ResponseWriter, r *http.Request) {
				if tc.failStep == "policy" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(validPolicy(srv.URL + "/s3")))
			})
			mux.HandleFunc("/s3", func(w http.ResponseWriter, r *http.Request) {
				if tc.failStep == "s3" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			})
			mux.HandleFunc("/upload/assets/99", func(w http.ResponseWriter, r *http.Request) {
				if tc.failStep == "finalize" {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(`{"href":"h","name":"n"}`))
			})
			srv = httptest.NewServer(mux)
			defer srv.Close()

			_, err := testClient(srv).Upload("octo", "hello", 42, img)
			if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
				t.Fatalf("want error %q, got %v", tc.wantInErr, err)
			}
		})
	}
}

func TestGetUploadToken_NetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now unreachable
	c := &Client{http: &http.Client{}, baseURL: url}
	_, err := c.getUploadToken("octo", "hello")
	if err == nil || !strings.Contains(err.Error(), "fetching repo page") {
		t.Fatalf("expected fetch error, got %v", err)
	}
}

func TestUpload_StatErrorBeforeRequests(t *testing.T) {
	c := NewClient(&http.Cookie{Name: "user_session", Value: "t"})
	_, err := c.Upload("octo", "hello", 1, filepath.Join(t.TempDir(), "missing.png"))
	if err == nil || !strings.Contains(err.Error(), "image file:") {
		t.Fatalf("expected image-file error, got %v", err)
	}
}

func TestDetectContentType(t *testing.T) {
	// Make the test hermetic: minimal CI images may lack .png in the mime registry.
	_ = mime.AddExtensionType(".png", "image/png")
	if ct := detectContentType("a.png"); !strings.HasPrefix(ct, "image/png") {
		t.Errorf("detectContentType(.png) = %q, want image/png prefix", ct)
	}
	if ct := detectContentType("a.zzz"); ct != "application/octet-stream" {
		t.Errorf("detectContentType(.zzz) = %q, want application/octet-stream", ct)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 200); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	if got := truncate("abcdef", 3); got != "abc..." {
		t.Errorf("truncate to 3 = %q, want abc...", got)
	}
	// Multibyte: 3 runes of a 5-rune string, not split mid-rune.
	if got := truncate("héllo", 3); got != "hél..." {
		t.Errorf("multibyte truncate = %q, want hél...", got)
	}
}

// jsonRoundTrip guards the policyResponse tags against accidental drift.
func TestPolicyResponseJSONTags(t *testing.T) {
	var p policyResponse
	if err := json.Unmarshal([]byte(validPolicy("u")), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Asset.Name != "pic.png" {
		t.Errorf("asset.name = %q", p.Asset.Name)
	}
}
