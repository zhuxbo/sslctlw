package diagnose

import (
	"strings"
	"testing"
	"time"

	"sslctlw/cert"
	"sslctlw/config"
	"sslctlw/iis"
)

func TestTruncHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
		want string
	}{
		{"短于10位", "abcdef", "abcdef"},
		{"正好10位", "0123456789", "0123456789"},
		{"超过10位", "0123456789ab", "0123456789..."},
		{"空字符串", "", ""},
		{"完整SHA1", "830687452c7722a7b808f6ffc88149fefab7ffb5", "830687452c..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncHash(tt.hash)
			if got != tt.want {
				t.Errorf("truncHash(%q) = %q, want %q", tt.hash, got, tt.want)
			}
		})
	}
}

func TestCrossValidate_NoBindings(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "example.com", OrderID: 100},
		{Domain: "test.com", OrderID: 200},
	}

	results := crossValidate(configCerts, nil, nil)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !strings.Contains(r, "[警告]") || !strings.Contains(r, "未找到 SSL 绑定") {
			t.Errorf("expected warning about missing binding, got: %s", r)
		}
	}
}

func TestCrossValidate_BindingNotInStore(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "example.com", OrderID: 100},
	}
	sslBindings := []iis.SSLBinding{
		{HostnamePort: "example.com:443", CertHash: "aabbccddee11223344"},
	}
	hashToCert := map[string]*cert.CertInfo{} // 空存储

	results := crossValidate(configCerts, sslBindings, hashToCert)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0], "[错误]") || !strings.Contains(results[0], "不在存储中") {
		t.Errorf("expected error about cert not in store, got: %s", results[0])
	}
}

func TestCrossValidate_CertExpired(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "example.com", OrderID: 100},
	}
	sslBindings := []iis.SSLBinding{
		{HostnamePort: "example.com:443", CertHash: "AABBCCDDEE"},
	}
	hashToCert := map[string]*cert.CertInfo{
		"AABBCCDDEE": {
			Thumbprint: "AABBCCDDEE",
			NotAfter:   time.Now().Add(-24 * time.Hour), // 已过期
		},
	}

	results := crossValidate(configCerts, sslBindings, hashToCert)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0], "[错误]") || !strings.Contains(results[0], "已过期") {
		t.Errorf("expected error about expired cert, got: %s", results[0])
	}
}

func TestCrossValidate_CertValid(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "example.com", OrderID: 100},
	}
	sslBindings := []iis.SSLBinding{
		{HostnamePort: "example.com:443", CertHash: "aabbccddee"},
	}
	hashToCert := map[string]*cert.CertInfo{
		"AABBCCDDEE": {
			Thumbprint: "AABBCCDDEE",
			NotAfter:   time.Now().Add(90 * 24 * time.Hour), // 90 天后过期
			NotBefore:  time.Now().Add(-30 * 24 * time.Hour),
		},
	}

	results := crossValidate(configCerts, sslBindings, hashToCert)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0], "[正常]") {
		t.Errorf("expected normal status, got: %s", results[0])
	}
}

func TestCrossValidate_CaseInsensitive(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "Example.COM", OrderID: 100},
	}
	sslBindings := []iis.SSLBinding{
		{HostnamePort: "example.com:443", CertHash: "aaBBccDDee"},
	}
	hashToCert := map[string]*cert.CertInfo{
		"AABBCCDDEE": {
			Thumbprint: "AABBCCDDEE",
			NotAfter:   time.Now().Add(90 * 24 * time.Hour),
			NotBefore:  time.Now().Add(-30 * 24 * time.Hour),
		},
	}

	results := crossValidate(configCerts, sslBindings, hashToCert)

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0], "[正常]") {
		t.Errorf("expected match despite case difference, got: %s", results[0])
	}
}

func TestCrossValidate_Mixed(t *testing.T) {
	configCerts := []config.CertConfig{
		{Domain: "ok.com", OrderID: 1},
		{Domain: "missing.com", OrderID: 2},
		{Domain: "expired.com", OrderID: 3},
	}
	sslBindings := []iis.SSLBinding{
		{HostnamePort: "ok.com:443", CertHash: "AAAA"},
		{HostnamePort: "expired.com:443", CertHash: "BBBB"},
	}
	hashToCert := map[string]*cert.CertInfo{
		"AAAA": {
			Thumbprint: "AAAA",
			NotAfter:   time.Now().Add(90 * 24 * time.Hour),
			NotBefore:  time.Now().Add(-30 * 24 * time.Hour),
		},
		"BBBB": {
			Thumbprint: "BBBB",
			NotAfter:   time.Now().Add(-1 * time.Hour),
		},
	}

	results := crossValidate(configCerts, sslBindings, hashToCert)

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if !strings.Contains(results[0], "[正常]") {
		t.Errorf("result[0]: expected [正常], got: %s", results[0])
	}
	if !strings.Contains(results[1], "[警告]") {
		t.Errorf("result[1]: expected [警告], got: %s", results[1])
	}
	if !strings.Contains(results[2], "[错误]") && !strings.Contains(results[2], "已过期") {
		t.Errorf("result[2]: expected [错误] with 已过期, got: %s", results[2])
	}
}
