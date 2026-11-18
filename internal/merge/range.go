// Package merge intersects city ranges with ASN ranges.
package merge

import (
	"fmt"
	"math/big"
	"net/netip"

	"china-geoip-mmdb/internal/asn"
	"china-geoip-mmdb/internal/source"
)

// Record is one output range: city attributes plus optional ASN data.
type Record struct {
	Start    netip.Addr
	End      netip.Addr
	Province string
	City     string
	District string
	ISP      string
	HasASN   bool
	ASN      uint32
	ASNOrg   string
	// Cloud names the cloud/IDC provider whose published ranges cover this record.
	Cloud string
}

func (r Record) sameData(o Record) bool {
	return r.Province == o.Province && r.City == o.City && r.District == o.District &&
		r.ISP == o.ISP && r.HasASN == o.HasASN && r.ASN == o.ASN && r.ASNOrg == o.ASNOrg && r.Cloud == o.Cloud
}

type Options struct {
	// PreserveCityWithoutASN keeps city sub-ranges that no ASN network covers.
	PreserveCityWithoutASN bool
}

type Stats struct {
	CityRanges      int
	OutputRanges    int
	MatchedRanges   int
	UnmatchedRanges int
	TotalAddrs      *big.Int
	MatchedAddrs    *big.Int
	// CitiesWithASN counts input city ranges that received at least one ASN.
	CitiesWithASN int
}

func newStats() Stats { return Stats{TotalAddrs: new(big.Int), MatchedAddrs: new(big.Int)} }

// AddrCoverage returns the share of city addresses covered by an ASN, in percent.
func (s Stats) AddrCoverage() float64 { return percent(s.MatchedAddrs, s.TotalAddrs) }

// RangeCoverage returns the share of input city ranges that received an ASN.
func (s Stats) RangeCoverage() float64 {
	if s.CityRanges == 0 {
		return 0
	}
	return float64(s.CitiesWithASN) * 100 / float64(s.CityRanges)
}

// Merge splits every city range at ASN network boundaries. Both inputs must be
// sorted by start address, non-overlapping and of the same address family.
// Gaps before, between and after ASN networks keep the city data without ASN.
func Merge(cities []source.CityRecord, asns []asn.Range, opt Options) ([]Record, Stats, error) {
	stats := newStats()
	stats.CityRanges = len(cities)
	if err := checkSorted(cities, asns); err != nil {
		return nil, stats, err
	}

	out := make([]Record, 0, len(cities))
	emit := func(c source.CityRecord, start, end netip.Addr, a *asn.Range) {
		n := addrCount(start, end)
		stats.TotalAddrs.Add(stats.TotalAddrs, n)
		r := Record{Start: start, End: end, Province: c.Province, City: c.City, District: c.District, ISP: c.ISP}
		if a != nil {
			r.HasASN, r.ASN, r.ASNOrg = true, a.ASN, a.Org
			stats.MatchedAddrs.Add(stats.MatchedAddrs, n)
			stats.MatchedRanges++
		} else {
			stats.UnmatchedRanges++
			if !opt.PreserveCityWithoutASN {
				return
			}
		}
		if k := len(out); k > 0 && out[k-1].End.Next() == r.Start && out[k-1].sameData(r) {
			out[k-1].End = r.End
			return
		}
		out = append(out, r)
	}

	j := 0
	for _, c := range cities {
		for j < len(asns) && asns[j].End.Compare(c.Start) < 0 {
			j++
		}
		cur, done, matched := c.Start, false, false
		for k := j; k < len(asns) && asns[k].Start.Compare(c.End) <= 0; k++ {
			a := &asns[k]
			if a.Start.Compare(cur) > 0 {
				emit(c, cur, a.Start.Prev(), nil)
				cur = a.Start
			}
			end := minAddr(a.End, c.End)
			emit(c, cur, end, a)
			matched = true
			if end == c.End {
				done = true
				break
			}
			cur = end.Next()
		}
		if !done {
			emit(c, cur, c.End, nil)
		}
		if matched {
			stats.CitiesWithASN++
		}
	}
	stats.OutputRanges = len(out)
	return out, stats, nil
}

func checkSorted(cities []source.CityRecord, asns []asn.Range) error {
	for i, c := range cities {
		if c.Start.Compare(c.End) > 0 {
			return fmt.Errorf("city range %s-%s: start after end", c.Start, c.End)
		}
		if i > 0 && cities[i-1].End.Compare(c.Start) >= 0 {
			return fmt.Errorf("city ranges not sorted or overlapping at %s", c.Start)
		}
	}
	for i, a := range asns {
		if a.Start.Compare(a.End) > 0 {
			return fmt.Errorf("asn range %s-%s: start after end", a.Start, a.End)
		}
		if i > 0 && asns[i-1].End.Compare(a.Start) >= 0 {
			return fmt.Errorf("asn ranges not sorted or overlapping at %s", a.Start)
		}
	}
	return nil
}

func minAddr(a, b netip.Addr) netip.Addr {
	if a.Compare(b) < 0 {
		return a
	}
	return b
}

// addrCount returns end-start+1 using 128-bit arithmetic for both families.
func addrCount(start, end netip.Addr) *big.Int {
	s, e := start.As16(), end.As16()
	n := new(big.Int).Sub(new(big.Int).SetBytes(e[:]), new(big.Int).SetBytes(s[:]))
	return n.Add(n, big.NewInt(1))
}

func percent(part, total *big.Int) float64 {
	if total == nil || total.Sign() == 0 {
		return 0
	}
	r := new(big.Rat).SetFrac(new(big.Int).Mul(part, big.NewInt(100)), total)
	f, _ := r.Float64()
	return f
}
