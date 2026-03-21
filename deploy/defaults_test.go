package deploy

import (
	"testing"

	"sslctlw/cert"
	"sslctlw/config"
)

func TestDefaultDeployer_NonNil(t *testing.T) {
	cfg := &config.Config{}
	store := &cert.OrderStore{BaseDir: t.TempDir()}

	deployer := DefaultDeployer(cfg, store)

	if deployer == nil {
		t.Fatal("DefaultDeployer() 返回 nil")
	}
	if deployer.Converter == nil {
		t.Error("Converter 不应为 nil")
	}
	if deployer.Installer == nil {
		t.Error("Installer 不应为 nil")
	}
	if deployer.Binder == nil {
		t.Error("Binder 不应为 nil")
	}
	if deployer.Store == nil {
		t.Error("Store 不应为 nil")
	}
}

func TestNewClientForCert(t *testing.T) {
	t.Run("无URL", func(t *testing.T) {
		certCfg := &config.CertConfig{
			OrderID: 100,
			Domain:  "example.com",
			API:     config.CertAPIConfig{},
		}
		_, err := NewClientForCert(certCfg)
		if err == nil {
			t.Error("无 URL 时应该返回错误")
		}
	})

	t.Run("无Token", func(t *testing.T) {
		certCfg := &config.CertConfig{
			OrderID: 100,
			Domain:  "example.com",
			API:     config.CertAPIConfig{URL: "https://api.example.com"},
		}
		_, err := NewClientForCert(certCfg)
		if err == nil {
			t.Error("无 Token 时应该返回错误")
		}
	})
}
