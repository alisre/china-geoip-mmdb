// Package asn reads networks from a GeoLite2-ASN style MMDB.
package asn

import (
	"fmt"
	"net/netip"
	"slices"

	"github.com/oschwald/maxminddb-golang/v2"
	"go4.org/netipx"
)

type Range struct {
	Start netip.Addr
	End   netip.Addr
	ASN   uint32
	Org   string
}

type Stats struct {
	Networks          int
	IPv4              int
	IPv6              int
	WithoutASN        int
	IPv4MappedSkipped int
	DatabaseType      string
	BuildEpoch        uint
}

type record struct {
	ASN uint32 `maxminddb:"autonomous_system_number"`
	Org string `maxminddb:"autonomous_system_organization"`
}

var v4Mapped = netip.MustParsePrefix("::ffff:0:0/96")

// Read returns the IPv4 and IPv6 ASN ranges, each sorted by start address and
// with adjacent ranges of the same ASN/org coalesced.
func Read(path string) (v4, v6 []Range, stats Stats, err error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("open asn db: %w", err)
	}
	defer db.Close()
	stats.DatabaseType = db.Metadata.DatabaseType
	stats.BuildEpoch = db.Metadata.BuildEpoch

	orgs := map[string]string{}
	for res := range db.Networks() {
		if err := res.Err(); err != nil {
			return nil, nil, stats, fmt.Errorf("iterate asn db: %w", err)
		}
		var rec record
		if err := res.Decode(&rec); err != nil {
			return nil, nil, stats, fmt.Errorf("decode %s: %w", res.Prefix(), err)
		}
		stats.Networks++
		if rec.ASN == 0 {
			stats.WithoutASN++
			continue
		}
		p, ok := normalizePrefix(res.Prefix())
		if !ok {
			stats.IPv4MappedSkipped++
			continue
		}
		org, seen := orgs[rec.Org]
		if !seen {
			orgs[rec.Org] = rec.Org
			org = rec.Org
		}
		rg := netipx.RangeOfPrefix(p)
		r := Range{Start: rg.From(), End: rg.To(), ASN: rec.ASN, Org: org}
		if p.Addr().Is4() {
			v4 = append(v4, r)
		} else {
			v6 = append(v6, r)
		}
	}
	v4, err = normalize(v4)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("ipv4: %w", err)
	}
	v6, err = normalize(v6)
	if err != nil {
		return nil, nil, stats, fmt.Errorf("ipv6: %w", err)
	}
	stats.IPv4, stats.IPv6 = len(v4), len(v6)
	return v4, v6, stats, nil
}

// normalizePrefix converts IPv4 networks reported in IPv6 form to plain IPv4
// and rejects IPv6 networks that overlap the IPv4-mapped space.
func normalizePrefix(p netip.Prefix) (netip.Prefix, bool) {
	a := p.Addr()
	if a.Is4() {
		return p.Masked(), true
	}
	if a.Is4In6() && p.Bits() >= 96 {
		return netip.PrefixFrom(a.Unmap(), p.Bits()-96).Masked(), true
	}
	if p.Overlaps(v4Mapped) {
		return netip.Prefix{}, false
	}
	return p.Masked(), true
}

func normalize(rs []Range) ([]Range, error) {
	slices.SortFunc(rs, func(a, b Range) int { return a.Start.Compare(b.Start) })
	for i := 1; i < len(rs); i++ {
		if last, r := rs[i-1], rs[i]; r.Start.Compare(last.End) <= 0 {
			return nil, fmt.Errorf("overlapping asn networks %s-%s and %s-%s", last.Start, last.End, r.Start, r.End)
		}
	}
	return coalesce(rs), nil
}

// coalesce merges adjacent ranges with the same ASN/org. rs must be sorted
// and non-overlapping; it is modified in place.
func coalesce(rs []Range) []Range {
	out := rs[:0]
	for _, r := range rs {
		if n := len(out); n > 0 {
			last := &out[n-1]
			if last.End.Next() == r.Start && last.ASN == r.ASN && last.Org == r.Org {
				last.End = r.End
				continue
			}
		}
		out = append(out, r)
	}
	return slices.Clip(out)
}
