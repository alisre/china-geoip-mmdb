// Package source parses ip2region-xdb source files
// (start_ip|end_ip|continent|country|province|city|district|isp|net) and keeps
// only mainland China rows.
package source

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"slices"
	"strings"
)

const fieldCount = 9

const (
	colStart = iota
	colEnd
	colContinent
	colCountry
	colProvince
	colCity
	colDistrict
	colISP
	colNet
)

type Family int

const (
	IPv4 Family = 4
	IPv6 Family = 6
)

type CityRecord struct {
	Start    netip.Addr
	End      netip.Addr
	Province string
	City     string
	District string
	ISP      string
}

// SameData reports whether two records carry the same location attributes.
func (r CityRecord) SameData(o CityRecord) bool {
	return r.Province == o.Province && r.City == o.City && r.District == o.District && r.ISP == o.ISP
}

type Options struct {
	Family           Family
	CountryName      string
	ExcludeProvinces []string
	// SkipInvalid drops malformed lines and overlapping ranges instead of
	// failing the whole parse.
	SkipInvalid bool
	// CityFromProvince lists provinces (municipalities such as 北京市) whose
	// empty city is filled with the province name. Upstream leaves city empty
	// when GeoCN only resolves to province level.
	CityFromProvince []string
}

type Stats struct {
	Lines             int
	Blank             int
	OtherCountry      int
	ExcludedProvince  int
	IPv4MappedSkipped int
	Invalid           int
	Duplicates        int
	Overlaps          int
	CityFilled        int
	Mainland          int
}

// ParseFile is Parse for a path. The file must exist and be non-empty.
func ParseFile(path string, opt Options) ([]CityRecord, Stats, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, Stats{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, Stats{}, err
	}
	if st.Size() == 0 {
		return nil, Stats{}, fmt.Errorf("%s is empty", path)
	}
	recs, stats, err := Parse(f, opt)
	if err != nil {
		return nil, stats, fmt.Errorf("%s: %w", path, err)
	}
	return recs, stats, nil
}

// Parse reads source lines and returns the mainland records sorted by start
// address with duplicates removed and overlaps rejected.
func Parse(r io.Reader, opt Options) ([]CityRecord, Stats, error) {
	if opt.Family != IPv4 && opt.Family != IPv6 {
		return nil, Stats{}, fmt.Errorf("invalid family %d", opt.Family)
	}
	var (
		stats  Stats
		recs   []CityRecord
		intern = map[string]string{}
	)
	in := func(s string) string {
		if s == "" {
			return ""
		}
		if v, ok := intern[s]; ok {
			return v
		}
		s = strings.Clone(s)
		intern[s] = s
		return s
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		stats.Lines++
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			stats.Blank++
			continue
		}
		rec, keep, err := parseLine(line, opt, &stats)
		if err != nil {
			stats.Invalid++
			if opt.SkipInvalid {
				continue
			}
			return nil, stats, fmt.Errorf("line %d: %w: %q", lineNo, err, truncate(line, 200))
		}
		if !keep {
			continue
		}
		rec.Province, rec.City, rec.District, rec.ISP = in(rec.Province), in(rec.City), in(rec.District), in(rec.ISP)
		recs = append(recs, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, stats, err
	}

	recs, err := normalize(recs, opt.SkipInvalid, &stats)
	if err != nil {
		return nil, stats, err
	}
	stats.Mainland = len(recs)
	return recs, stats, nil
}

func parseLine(line string, opt Options, stats *Stats) (CityRecord, bool, error) {
	f := strings.Split(line, "|")
	if len(f) != fieldCount {
		return CityRecord{}, false, fmt.Errorf("expected %d fields, got %d", fieldCount, len(f))
	}
	start, err := netip.ParseAddr(f[colStart])
	if err != nil {
		return CityRecord{}, false, fmt.Errorf("invalid start ip: %w", err)
	}
	end, err := netip.ParseAddr(f[colEnd])
	if err != nil {
		return CityRecord{}, false, fmt.Errorf("invalid end ip: %w", err)
	}
	if start.Zone() != "" || end.Zone() != "" {
		return CityRecord{}, false, fmt.Errorf("zoned address not allowed")
	}

	switch opt.Family {
	case IPv4:
		if !start.Is4() || !end.Is4() {
			return CityRecord{}, false, fmt.Errorf("not an IPv4 range")
		}
	case IPv6:
		if !start.Is6() || !end.Is6() {
			return CityRecord{}, false, fmt.Errorf("not an IPv6 range")
		}
		// IPv4 is taken from the IPv4 file only, so drop IPv4-mapped rows to
		// avoid writing the same address twice.
		if start.Is4In6() || end.Is4In6() || overlapsV4Mapped(start, end) {
			stats.IPv4MappedSkipped++
			return CityRecord{}, false, nil
		}
	}
	if start.Compare(end) > 0 {
		return CityRecord{}, false, fmt.Errorf("start ip greater than end ip")
	}

	if f[colCountry] != opt.CountryName {
		stats.OtherCountry++
		return CityRecord{}, false, nil
	}
	province := strings.TrimSpace(f[colProvince])
	for _, p := range opt.ExcludeProvinces {
		if p != "" && strings.HasPrefix(province, p) {
			stats.ExcludedProvince++
			return CityRecord{}, false, nil
		}
	}
	return CityRecord{
		Start:    start,
		End:      end,
		Province: province,
		City:     cityOrProvince(strings.TrimSpace(f[colCity]), province, opt.CityFromProvince, stats),
		District: strings.TrimSpace(f[colDistrict]),
		ISP:      strings.TrimSpace(f[colISP]),
	}, true, nil
}

func cityOrProvince(city, province string, fill []string, stats *Stats) string {
	if city != "" || province == "" {
		return city
	}
	for _, p := range fill {
		if p == province {
			stats.CityFilled++
			return province
		}
	}
	return city
}

var (
	v4MappedFirst = netip.MustParseAddr("::ffff:0.0.0.0")
	v4MappedLast  = netip.MustParseAddr("::ffff:255.255.255.255")
)

func overlapsV4Mapped(start, end netip.Addr) bool {
	return start.Compare(v4MappedLast) <= 0 && end.Compare(v4MappedFirst) >= 0
}

// normalize sorts records, removes exact duplicates, rejects (or drops) overlaps
// and coalesces adjacent ranges that carry the same data.
func normalize(recs []CityRecord, skipInvalid bool, stats *Stats) ([]CityRecord, error) {
	slices.SortStableFunc(recs, func(a, b CityRecord) int {
		if c := a.Start.Compare(b.Start); c != 0 {
			return c
		}
		return a.End.Compare(b.End)
	})
	out := recs[:0]
	for _, r := range recs {
		if len(out) == 0 {
			out = append(out, r)
			continue
		}
		last := &out[len(out)-1]
		if r.Start.Compare(last.End) <= 0 {
			if r.Start == last.Start && r.End == last.End && r.SameData(*last) {
				stats.Duplicates++
				continue
			}
			stats.Overlaps++
			if skipInvalid {
				continue
			}
			return nil, fmt.Errorf("overlapping ranges %s-%s and %s-%s", last.Start, last.End, r.Start, r.End)
		}
		if last.End.Next() == r.Start && last.SameData(r) {
			last.End = r.End
			continue
		}
		out = append(out, r)
	}
	return slices.Clip(out), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
