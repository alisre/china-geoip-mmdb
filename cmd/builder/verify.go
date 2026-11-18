package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"china-geoip-mmdb/internal/config"
)

var ipinfoBaseURL = "https://ipinfo.io"

var errRateLimited = errors.New("ipinfo rate limit reached")

type verifyOptions struct {
	Samples  int
	Token    string
	MinMatch float64 // percent; 0 = report only
	Workers  int
}

type sample struct {
	IP  netip.Addr
	ASN uint32
	Org string
}

// runVerify spot-checks the ASN of evenly spaced networks in the database
// against ipinfo.io and prints mismatches as TSV on stdout.
func runVerify(cfgPath, dbPath string, opt verifyOptions) error {
	if dbPath == "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		dbPath = cfg.Output.File
	}
	samples, err := pickSamples(dbPath, opt.Samples)
	if err != nil {
		return err
	}
	slog.Info("verifying against ipinfo.io", "db", dbPath, "samples", len(samples), "token", opt.Token != "")

	type result struct {
		s        sample
		asn      uint32
		org      string
		err      error
		finished bool
	}
	results := make([]result, len(samples))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &http.Client{Timeout: 10 * time.Second}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range max(opt.Workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				a, o, err := ipinfoASN(ctx, client, samples[i].IP, opt.Token)
				if errors.Is(err, errRateLimited) {
					cancel()
				}
				results[i] = result{s: samples[i], asn: a, org: o, err: err, finished: true}
			}
		}()
	}
feed:
	for i := range samples {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	var checked, matched, failed int
	fmt.Println("ip\tour_asn\tour_org\tipinfo_asn\tipinfo_org")
	for _, r := range results {
		switch {
		case !r.finished:
			continue
		case r.err != nil:
			failed++
			slog.Warn("ipinfo lookup failed", "ip", r.s.IP, "err", r.err)
			continue
		}
		checked++
		if r.asn == r.s.ASN {
			matched++
			continue
		}
		fmt.Printf("%s\t%d\t%s\t%d\t%s\n", r.s.IP, r.s.ASN, r.s.Org, r.asn, r.org)
	}
	rate := 0.0
	if checked > 0 {
		rate = float64(matched) * 100 / float64(checked)
	}
	slog.Info("ipinfo verification", "checked", checked, "matched", matched,
		"mismatched", checked-matched, "failed", failed, "match_rate", fmt.Sprintf("%.2f%%", rate))
	if ctx.Err() != nil {
		slog.Warn("stopped early: ipinfo rate limit; set IPINFO_TOKEN or lower -verify")
	}
	if opt.MinMatch > 0 && rate < opt.MinMatch {
		return fmt.Errorf("ipinfo match rate %.2f%% < %.2f%%", rate, opt.MinMatch)
	}
	return nil
}

// pickSamples returns the first address of n evenly spaced networks that carry
// an ASN. Aliased IPv4 networks are skipped by the reader.
func pickSamples(dbPath string, n int) ([]sample, error) {
	db, err := maxminddb.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var all []sample
	for res := range db.Networks() {
		if err := res.Err(); err != nil {
			return nil, err
		}
		var rec struct {
			ASN uint32 `maxminddb:"autonomous_system_number"`
			Org string `maxminddb:"autonomous_system_organization"`
		}
		if err := res.Decode(&rec); err != nil {
			return nil, err
		}
		if rec.ASN != 0 {
			all = append(all, sample{IP: res.Prefix().Addr().Unmap(), ASN: rec.ASN, Org: rec.Org})
		}
	}
	if n <= 0 || n >= len(all) {
		return all, nil
	}
	out := make([]sample, 0, n)
	for i := range n {
		out = append(out, all[i*len(all)/n])
	}
	return out, nil
}

// ipinfoASN returns the ASN from ipinfo.io's "org" field ("AS4134 CHINANET...").
func ipinfoASN(ctx context.Context, client *http.Client, ip netip.Addr, token string) (uint32, string, error) {
	u := ipinfoBaseURL + "/" + url.PathEscape(ip.String()) + "/json"
	if token != "" {
		u += "?" + url.Values{"token": {token}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return 0, "", errRateLimited
	}
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("http %d", resp.StatusCode)
	}
	var body struct {
		Org string `json:"org"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, "", err
	}
	num, org, _ := strings.Cut(body.Org, " ")
	n, err := strconv.ParseUint(strings.TrimPrefix(num, "AS"), 10, 32)
	if !strings.HasPrefix(num, "AS") || err != nil {
		return 0, body.Org, nil
	}
	return uint32(n), org, nil
}
