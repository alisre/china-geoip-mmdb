package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestVerifyIPInfo(t *testing.T) {
	dir, cfgPath := setup(t, "")
	must(t, run(cfgPath))
	db := filepath.Join(dir, "output/China-City-ASN.mmdb")

	orgs := map[string]string{"1.0.1.0": "AS4134 CHINANET", "1.0.8.0": "AS4809 CN2", "240e::": "AS9999 OTHER"}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ip := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/json")
		if r.URL.Query().Get("token") != "tok" {
			t.Errorf("token not sent: %s", r.URL)
		}
		org, ok := orgs[ip]
		if !ok {
			http.Error(w, "unexpected "+ip, http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"ip":"` + ip + `","org":"` + org + `"}`))
	}))
	defer srv.Close()
	old := ipinfoBaseURL
	ipinfoBaseURL = srv.URL
	defer func() { ipinfoBaseURL = old }()

	if err := runVerify(cfgPath, db, verifyOptions{Samples: 100, Token: "tok", Workers: 2}); err != nil {
		t.Fatalf("report-only verify failed: %v", err)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("calls=%d, want 3", n)
	}
	err := runVerify(cfgPath, db, verifyOptions{Samples: 100, Token: "tok", MinMatch: 90, Workers: 1})
	if err == nil || !strings.Contains(err.Error(), "66.67%") {
		t.Fatalf("expected match-rate failure, got %v", err)
	}
}
