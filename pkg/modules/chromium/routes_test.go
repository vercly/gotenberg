package chromium

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/gotenberg/gotenberg/v8/pkg/modules/api"
)

// TestHandleChromiumError_Crashed pins the mapping of a Chromium renderer
// crash to a 503 Service Unavailable. When the renderer crashes mid-conversion,
// the request must fail fast with 503 rather than hang until the deadline and
// surface as a generic timeout.
// See https://github.com/gotenberg/gotenberg/issues/1640.
func TestHandleChromiumError_Crashed(t *testing.T) {
	// Mirror the wrapping done by [chromiumBrowser.do].
	err := handleChromiumError(fmt.Errorf("handle tasks: %w", ErrChromiumCrashed), Options{})
	if err == nil {
		t.Fatal("expected an error, got none")
	}

	status, message := api.ParseError(err)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d (message: %s)", status, http.StatusServiceUnavailable, message)
	}

	want := "Chromium crashed while processing the request. Retry, or reduce the workload if the problem persists."
	if message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
}

// TestHandleChromiumError_CrashedTakesPrecedence guards the ordering in
// [handleChromiumError]: a crash is a server-side failure and must map to 503
// even when the error chain also carries a marker that another branch would
// map to a client-error status.
func TestHandleChromiumError_CrashedTakesPrecedence(t *testing.T) {
	err := handleChromiumError(
		fmt.Errorf("handle tasks: %w; %w", ErrChromiumCrashed, ErrInvalidHttpStatusCode),
		Options{},
	)
	if err == nil {
		t.Fatal("expected an error, got none")
	}

	status, _ := api.ParseError(err)
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

// TestHandleChromiumError_ConsoleMessages pins the response of
// "failOnConsoleMessageTypes": a 409 that lists the recorded messages.
// See https://github.com/gotenberg/gotenberg/issues/1667.
func TestHandleChromiumError_ConsoleMessages(t *testing.T) {
	// Mirror the wrapping done by [chromiumBrowser.do].
	err := handleChromiumError(fmt.Errorf("%s: %w", "[warning] foo\n[error] bar", ErrConsoleMessages), Options{})
	if err == nil {
		t.Fatal("expected an error, got none")
	}

	status, message := api.ParseError(err)
	if status != http.StatusConflict {
		t.Errorf("status = %d, want %d (message: %s)", status, http.StatusConflict, message)
	}

	want := "Chromium console messages:\n[warning] foo\n[error] bar"
	if message != want {
		t.Errorf("message = %q, want %q", message, want)
	}
}

// TestValidateUrlScheme pins the schemes the URL routes accept. Chromium's
// own schemes must never reach the browser: devtools:// exposes privileged
// bindings to waitForExpression, and chrome:// can crash the browser.
func TestValidateUrlScheme(t *testing.T) {
	for _, tc := range []struct {
		url     string
		wantErr bool
	}{
		{"http://example.com", false},
		{"https://example.com/path?q=1", false},
		{"HTTPS://example.com", false},
		{"file:///tmp/foo/index.html", true},
		{"devtools://devtools/bundled/devtools_app.html", true},
		{"DevTools://devtools/bundled/devtools_app.html", true},
		{"chrome://crash", true},
		{"chrome-untrusted://terminal/", true},
		{"chrome-extension://abc/index.html", true},
		{"view-source:https://example.com", true},
		{"javascript:alert(1)", true},
		{"data:text/html,foo", true},
		{"about:blank", true},
		{"example.com", true},
		{"", true},
	} {
		t.Run(tc.url, func(t *testing.T) {
			_, _, err := validateUrlScheme(tc.url)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateUrlScheme(%q) error = %v, want error: %t", tc.url, err, tc.wantErr)
			}
			if err == nil {
				return
			}

			status, _ := api.ParseError(err)
			if status != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
			}
		})
	}
}

func TestResolveUrlForRoute_AllowFileUrlPrefixes(t *testing.T) {
	prefixes := []string{"/tmp/reports", "/var/data/html/"}

	for _, tc := range []struct {
		name     string
		url      string
		prefixes []string
		wantURL  string
		wantFile bool
		wantErr  bool
	}{
		{
			name:     "http URL unaffected with prefixes",
			url:      "https://example.com/a",
			prefixes: prefixes,
			wantURL:  "https://example.com/a",
			wantFile: false,
			wantErr:  false,
		},
		{
			name:     "file URL rejected when prefixes empty",
			url:      "file:///tmp/reports/issuer/123.html",
			prefixes: nil,
			wantErr:  true,
		},
		{
			name:     "file URL inside allowed prefix accepted",
			url:      "file:///tmp/reports/issuer/123.html",
			prefixes: prefixes,
			wantURL:  "file:///tmp/reports/issuer/123.html",
			wantFile: true,
			wantErr:  false,
		},
		{
			name:     "file URL inside second allowed prefix accepted",
			url:      "file:///var/data/html/doc.html",
			prefixes: prefixes,
			wantURL:  "file:///var/data/html/doc.html",
			wantFile: true,
			wantErr:  false,
		},
		{
			name:     "file URL with internal .. staying inside prefix normalized",
			url:      "file:///tmp/reports/a/../b/doc.html",
			prefixes: prefixes,
			wantURL:  "file:///tmp/reports/b/doc.html",
			wantFile: true,
			wantErr:  false,
		},
		{
			name:     "file URL traversal escaping prefix rejected",
			url:      "file:///tmp/reports/../etc/passwd",
			prefixes: prefixes,
			wantErr:  true,
		},
		{
			name:     "file URL outside prefix rejected",
			url:      "file:///etc/passwd",
			prefixes: prefixes,
			wantErr:  true,
		},
		{
			name:     "file URL sibling prefix boundary rejected",
			url:      "file:///tmp/reports-other/doc.html",
			prefixes: prefixes,
			wantErr:  true,
		},
		{
			name:     "file URL exact directory without child rejected",
			url:      "file:///tmp/reports",
			prefixes: prefixes,
			wantErr:  true,
		},
		{
			name:     "file URL with host rejected",
			url:      "file://localhost/tmp/reports/doc.html",
			prefixes: prefixes,
			wantErr:  true,
		},
		{
			name:     "root prefix ignored",
			url:      "file:///etc/passwd",
			prefixes: []string{"/"},
			wantErr:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotFile, err := validateUrlScheme(tc.url, tc.prefixes...)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateUrlScheme(%q, %v) error = %v, want error: %t", tc.url, tc.prefixes, err, tc.wantErr)
			}
			if err != nil {
				status, _ := api.ParseError(err)
				if status != http.StatusBadRequest {
					t.Errorf("status = %d, want %d", status, http.StatusBadRequest)
				}
				return
			}
			if gotURL != tc.wantURL {
				t.Errorf("gotURL = %q, want %q", gotURL, tc.wantURL)
			}
			if gotFile != tc.wantFile {
				t.Errorf("gotFile = %v, want %v", gotFile, tc.wantFile)
			}
		})
	}
}

func TestChromiumValidate_AllowFileUrlPrefixes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefixes []string
		want     []string
		wantErr  bool
	}{
		{
			name:     "empty slice valid",
			prefixes: nil,
			want:     []string{},
			wantErr:  false,
		},
		{
			name:     "valid absolute paths cleaned",
			prefixes: []string{"/tmp/reports/", " /var/reports/../data "},
			want:     []string{"/tmp/reports", "/var/data"},
			wantErr:  false,
		},
		{
			name:     "root slash rejected",
			prefixes: []string{"/"},
			wantErr:  true,
		},
		{
			name:     "cleaned to root slash rejected",
			prefixes: []string{"/tmp/.."},
			wantErr:  true,
		},
		{
			name:     "relative path rejected",
			prefixes: []string{"tmp/reports"},
			wantErr:  true,
		},
		{
			name:     "empty entry rejected",
			prefixes: []string{""},
			wantErr:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			mod := &Chromium{
				maxConcurrency:       1,
				allowFileUrlPrefixes: tc.prefixes,
				args: browserArguments{
					binPath:           tmpDir,
					hyphenDataDirPath: tmpDir,
				},
			}
			err := mod.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() error = %v, want error: %t", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if len(mod.allowFileUrlPrefixes) != len(tc.want) {
				t.Fatalf("allowFileUrlPrefixes = %v, want %v", mod.allowFileUrlPrefixes, tc.want)
			}
			for i := range tc.want {
				if mod.allowFileUrlPrefixes[i] != tc.want[i] {
					t.Errorf("allowFileUrlPrefixes[%d] = %q, want %q", i, mod.allowFileUrlPrefixes[i], tc.want[i])
				}
			}
		})
	}
}
