// Package validate checks a generated MMDB against the merged records.
package validate

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"

	"china-geoip-mmdb/internal/config"
	"china-geoip-mmdb/internal/merge"
)

// LookupRecord is the decoded shape of a China-City-ASN record.
type LookupRecord struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code" json:"iso_code"`
		Names   map[string]string `maxminddb:"names" json:"names,omitempty"`
	} `maxminddb:"country" json:"country"`
	Subdivisions []struct {
		Names map[string]string `maxminddb:"names" json:"names"`
	} `maxminddb:"subdivisions" json:"subdivisions,omitempty"`
	City *struct {
		Names map[string]string `maxminddb:"names" json:"names"`
	} `maxminddb:"city" json:"city,omitempty"`
	District string `maxminddb:"district" json:"district,omitempty"`
	ISP      string `maxminddb:"isp" json:"isp,omitempty"`
	Cloud    string `maxminddb:"cloud" json:"cloud,omitempty"`
	ASN      uint32 `maxminddb:"autonomous_system_number" json:"autonomous_system_number,omitempty"`
	ASNOrg   string `maxminddb:"autonomous_system_organization" json:"autonomous_system_organization,omitempty"`
}

func (r LookupRecord) Province() string {
	if len(r.Subdivisions) == 0 {
		return ""
	}
	return r.Subdivisions[0].Names["zh-CN"]
}

func (r LookupRecord) CityName() string {
	if r.City == nil {
		return ""
	}
	return r.City.Names["zh-CN"]
}

// Lookup returns the record for ip and whether it was found.
func Lookup(db *maxminddb.Reader, ip netip.Addr) (LookupRecord, bool, error) {
	var rec LookupRecord
	res := db.Lookup(ip)
	if err := res.Err(); err != nil {
		return rec, false, err
	}
	if !res.Found() {
		return rec, false, nil
	}
	return rec, true, res.Decode(&rec)
}

type Options struct {
	DatabaseType string
	CountryCode  string
	SampleSize   int
	Checks       []config.Check
}

type Report struct {
	SampledLookups int
	Checks         int
	Failures       []string
}

const maxReportedFailures = 20

// File opens path and verifies metadata, sampled merged ranges and explicit
// known-IP checks. It returns an error if any check fails.
func File(path string, v4, v6 []merge.Record, opt Options) (Report, error) {
	var rep Report
	db, err := maxminddb.Open(path)
	if err != nil {
		return rep, fmt.Errorf("open %s: %w", path, err)
	}
	defer db.Close()

	fail := func(format string, a ...any) {
		rep.Failures = append(rep.Failures, fmt.Sprintf(format, a...))
	}
	md := db.Metadata
	if md.IPVersion != 6 {
		fail("metadata ip_version=%d, want 6", md.IPVersion)
	}
	if opt.DatabaseType != "" && md.DatabaseType != opt.DatabaseType {
		fail("metadata database_type=%q, want %q", md.DatabaseType, opt.DatabaseType)
	}
	if md.NodeCount == 0 {
		fail("database has no nodes")
	}

	for _, set := range [][]merge.Record{v4, v6} {
		for _, r := range sample(set, opt.SampleSize) {
			for _, ip := range []netip.Addr{r.Start, r.End} {
				rep.SampledLookups++
				if err := expectRecord(db, ip, r, opt.CountryCode); err != nil {
					fail("%v", err)
				}
			}
		}
	}

	for _, ch := range opt.Checks {
		rep.Checks++
		if err := runCheck(db, ch, opt.CountryCode); err != nil {
			fail("check %s: %v", ch.IP, err)
		}
	}

	if len(rep.Failures) > 0 {
		shown := rep.Failures
		if len(shown) > maxReportedFailures {
			shown = shown[:maxReportedFailures]
		}
		return rep, fmt.Errorf("%d validation failure(s): %w", len(rep.Failures), errors.New(joinLines(shown)))
	}
	return rep, nil
}

func expectRecord(db *maxminddb.Reader, ip netip.Addr, want merge.Record, cc string) error {
	got, found, err := Lookup(db, ip)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", ip, err)
	}
	if !found {
		return fmt.Errorf("lookup %s: not found (expected range %s-%s)", ip, want.Start, want.End)
	}
	var diffs []string
	cmp := func(field, g, w string) {
		if g != w {
			diffs = append(diffs, fmt.Sprintf("%s=%q want %q", field, g, w))
		}
	}
	cmp("country", got.Country.ISOCode, cc)
	cmp("province", got.Province(), want.Province)
	cmp("city", got.CityName(), want.City)
	cmp("district", got.District, want.District)
	cmp("isp", got.ISP, want.ISP)
	cmp("cloud", got.Cloud, want.Cloud)
	wantASN := uint32(0)
	if want.HasASN {
		wantASN = want.ASN
	}
	if got.ASN != wantASN {
		diffs = append(diffs, fmt.Sprintf("asn=%d want %d", got.ASN, wantASN))
	}
	cmp("asn_org", got.ASNOrg, want.ASNOrg)
	if len(diffs) > 0 {
		return fmt.Errorf("lookup %s: %v", ip, diffs)
	}
	return nil
}

func runCheck(db *maxminddb.Reader, ch config.Check, cc string) error {
	ip, err := netip.ParseAddr(ch.IP)
	if err != nil {
		return err
	}
	got, found, err := Lookup(db, ip)
	if err != nil {
		return err
	}
	if ch.Absent {
		if found {
			return fmt.Errorf("expected absent, found %s/%s", got.Province(), got.CityName())
		}
		return nil
	}
	if !found {
		return errors.New("not found")
	}
	var diffs []string
	cmp := func(field, g, w string) {
		if w != "" && g != w {
			diffs = append(diffs, fmt.Sprintf("%s=%q want %q", field, g, w))
		}
	}
	cmp("country", got.Country.ISOCode, cc)
	cmp("province", got.Province(), ch.Province)
	cmp("city", got.CityName(), ch.City)
	cmp("district", got.District, ch.District)
	cmp("isp", got.ISP, ch.ISP)
	cmp("cloud", got.Cloud, ch.Cloud)
	cmp("asn_org", got.ASNOrg, ch.ASNOrg)
	if ch.ASN != nil && got.ASN != *ch.ASN {
		diffs = append(diffs, fmt.Sprintf("asn=%d want %d", got.ASN, *ch.ASN))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("%v", diffs)
	}
	return nil
}

// sample picks up to n records spread evenly, always including first and last.
func sample(rs []merge.Record, n int) []merge.Record {
	if n <= 0 || len(rs) == 0 {
		return nil
	}
	if len(rs) <= n {
		return rs
	}
	if n == 1 {
		return rs[:1]
	}
	out := make([]merge.Record, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, rs[i*(len(rs)-1)/(n-1)])
	}
	return out
}

func joinLines(ss []string) string {
	return "\n  - " + strings.Join(ss, "\n  - ")
}
