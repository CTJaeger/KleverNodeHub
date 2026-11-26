package agent

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/CTJaeger/KleverNodeHub/internal/crypto"
)

func TestRegistrationTLSConfig_PinsDashboardCA(t *testing.T) {
	ca, err := crypto.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	srvTLS, err := crypto.DashboardTLSConfig(ca)
	if err != nil {
		t.Fatal(err)
	}
	srvTLS.ClientAuth = tls.RequestClientCert // as the dashboard serves browsers
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.TLS = srvTLS
	srv.StartTLS()
	defer srv.Close()

	other, _ := crypto.NewCA()
	get := func(fp string) error {
		cfg, err := registrationTLSConfig(srv.URL, fp)
		if err != nil {
			return err
		}
		resp, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}).Get(srv.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}

	if err := get(crypto.Fingerprint(ca.Certificate)); err != nil {
		t.Errorf("pinned dashboard CA rejected: %v", err)
	}
	if err := get(crypto.Fingerprint(other.Certificate)); err == nil {
		t.Error("dashboard accepted although its CA does not match the pin")
	}
	if err := get(""); err == nil {
		t.Error("self-signed dashboard accepted without a pin")
	}
}
