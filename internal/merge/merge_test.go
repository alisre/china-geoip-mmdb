package merge

import (
	"net/netip"
	"testing"

	"china-geoip-mmdb/internal/asn"
	"china-geoip-mmdb/internal/source"
)

func a(s string) netip.Addr { return netip.MustParseAddr(s) }

func city(start, end, cityName string) source.CityRecord {
	return source.CityRecord{Start: a(start), End: a(end), Province: "广东省", City: cityName, District: "南山区", ISP: "中国电信"}
}

func as(start, end string, n uint32) asn.Range {
	return asn.Range{Start: a(start), End: a(end), ASN: n, Org: "ORG"}
}

type want struct {
	start, end string
	asn        uint32 // 0 = no ASN
	city       string
}

func check(t *testing.T, got []Record, ws []want) {
	t.Helper()
	if len(got) != len(ws) {
		t.Fatalf("got %d records, want %d: %+v", len(got), len(ws), got)
	}
	for i, w := range ws {
		g := got[i]
		if g.Start != a(w.start) || g.End != a(w.end) {
			t.Errorf("[%d] range %s-%s, want %s-%s", i, g.Start, g.End, w.start, w.end)
		}
		if w.asn == 0 && g.HasASN {
			t.Errorf("[%d] unexpected asn %d", i, g.ASN)
		}
		if w.asn != 0 && (!g.HasASN || g.ASN != w.asn) {
			t.Errorf("[%d] asn %d (has=%v), want %d", i, g.ASN, g.HasASN, w.asn)
		}
		if w.city != "" && g.City != w.city {
			t.Errorf("[%d] city %q, want %q", i, g.City, w.city)
		}
		if g.Province != "广东省" || g.ISP != "中国电信" {
			t.Errorf("[%d] city fields not inherited: %+v", i, g)
		}
	}
}

func run(t *testing.T, cs []source.CityRecord, ns []asn.Range, preserve bool) ([]Record, Stats) {
	t.Helper()
	got, st, err := Merge(cs, ns, Options{PreserveCityWithoutASN: preserve})
	if err != nil {
		t.Fatal(err)
	}
	return got, st
}

func TestOneToOne(t *testing.T) {
	got, st := run(t, []source.CityRecord{city("192.0.2.0", "192.0.2.255", "深圳市")},
		[]asn.Range{as("192.0.2.0", "192.0.2.255", 4134)}, true)
	check(t, got, []want{{"192.0.2.0", "192.0.2.255", 4134, "深圳市"}})
	if st.AddrCoverage() != 100 || st.RangeCoverage() != 100 {
		t.Errorf("coverage %v/%v", st.AddrCoverage(), st.RangeCoverage())
	}
}

func TestCitySplitByMultipleASN(t *testing.T) {
	got, _ := run(t, []source.CityRecord{city("192.0.2.0", "192.0.2.255", "深圳市")},
		[]asn.Range{as("192.0.2.0", "192.0.2.127", 4134), as("192.0.2.128", "192.0.2.255", 4809)}, true)
	check(t, got, []want{
		{"192.0.2.0", "192.0.2.127", 4134, ""},
		{"192.0.2.128", "192.0.2.255", 4809, ""},
	})
}

func TestASNSpansMultipleCities(t *testing.T) {
	got, _ := run(t, []source.CityRecord{
		city("10.0.0.0", "10.0.0.99", "深圳市"),
		city("10.0.0.100", "10.0.0.199", "广州市"),
		city("10.0.1.0", "10.0.1.255", "东莞市"),
	}, []asn.Range{as("9.0.0.0", "10.0.1.127", 4134)}, true)
	check(t, got, []want{
		{"10.0.0.0", "10.0.0.99", 4134, "深圳市"},
		{"10.0.0.100", "10.0.0.199", 4134, "广州市"},
		{"10.0.1.0", "10.0.1.127", 4134, "东莞市"},
		{"10.0.1.128", "10.0.1.255", 0, "东莞市"},
	})
}

func TestNoASNCoverage(t *testing.T) {
	cs := []source.CityRecord{city("10.0.0.0", "10.0.0.255", "深圳市")}
	ns := []asn.Range{as("1.0.0.0", "1.0.0.255", 1), as("20.0.0.0", "20.0.0.255", 2)}
	got, st := run(t, cs, ns, true)
	check(t, got, []want{{"10.0.0.0", "10.0.0.255", 0, ""}})
	if st.UnmatchedRanges != 1 || st.AddrCoverage() != 0 {
		t.Errorf("stats %+v", st)
	}
	got, _ = run(t, cs, ns, false)
	if len(got) != 0 {
		t.Errorf("preserve=false should drop uncovered city, got %+v", got)
	}
}

func TestPartialCoverageGaps(t *testing.T) {
	got, st := run(t, []source.CityRecord{city("10.0.0.0", "10.0.0.255", "深圳市")}, []asn.Range{
		as("10.0.0.16", "10.0.0.31", 1),
		as("10.0.0.64", "10.0.0.127", 2),
	}, true)
	check(t, got, []want{
		{"10.0.0.0", "10.0.0.15", 0, ""},
		{"10.0.0.16", "10.0.0.31", 1, ""},
		{"10.0.0.32", "10.0.0.63", 0, ""},
		{"10.0.0.64", "10.0.0.127", 2, ""},
		{"10.0.0.128", "10.0.0.255", 0, ""},
	})
	if st.MatchedAddrs.Int64() != 80 || st.TotalAddrs.Int64() != 256 {
		t.Errorf("addr stats matched=%s total=%s", st.MatchedAddrs, st.TotalAddrs)
	}
}

func TestAdjacentSameASNCoalesced(t *testing.T) {
	got, _ := run(t, []source.CityRecord{city("10.0.0.0", "10.0.0.255", "深圳市")}, []asn.Range{
		as("10.0.0.0", "10.0.0.127", 7), as("10.0.0.128", "10.0.0.255", 7),
	}, true)
	check(t, got, []want{{"10.0.0.0", "10.0.0.255", 7, ""}})
}

func TestIPv4Boundaries(t *testing.T) {
	got, _ := run(t, []source.CityRecord{
		city("0.0.0.0", "0.0.0.0", "A"),
		city("255.255.255.0", "255.255.255.255", "B"),
	}, []asn.Range{as("0.0.0.0", "0.0.0.0", 1), as("255.255.255.128", "255.255.255.255", 2)}, true)
	check(t, got, []want{
		{"0.0.0.0", "0.0.0.0", 1, "A"},
		{"255.255.255.0", "255.255.255.127", 0, "B"},
		{"255.255.255.128", "255.255.255.255", 2, "B"},
	})
	if _, _, err := MergeIPv4(nil, []asn.Range{as("2001:db8::", "2001:db8::ff", 1)}, Options{}); err == nil {
		t.Error("MergeIPv4 accepted IPv6 asn range")
	}
}

func TestIPv6Boundaries(t *testing.T) {
	got, st, err := MergeIPv6([]source.CityRecord{
		city("240e::", "240e:ff:ffff:ffff:ffff:ffff:ffff:ffff", "深圳市"),
		city("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "X"),
	}, []asn.Range{
		as("240e::", "240e:7f:ffff:ffff:ffff:ffff:ffff:ffff", 4134),
		as("ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff80", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", 9),
	}, Options{PreserveCityWithoutASN: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, got, []want{
		{"240e::", "240e:7f:ffff:ffff:ffff:ffff:ffff:ffff", 4134, ""},
		{"240e:80::", "240e:ff:ffff:ffff:ffff:ffff:ffff:ffff", 0, ""},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff7f", 0, ""},
		{"ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff80", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", 9, ""},
	})
	if st.TotalAddrs.BitLen() <= 64 {
		t.Errorf("IPv6 address count should exceed 64 bits, got %s", st.TotalAddrs)
	}
	if _, _, err := MergeIPv6([]source.CityRecord{city("::ffff:1.2.3.4", "::ffff:1.2.3.4", "X")}, nil, Options{}); err == nil {
		t.Error("MergeIPv6 accepted IPv4-mapped range")
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := map[string]struct {
		cs []source.CityRecord
		ns []asn.Range
	}{
		"city overlap":   {cs: []source.CityRecord{city("10.0.0.0", "10.0.0.10", "A"), city("10.0.0.5", "10.0.0.20", "B")}},
		"city unsorted":  {cs: []source.CityRecord{city("10.0.1.0", "10.0.1.10", "A"), city("10.0.0.0", "10.0.0.10", "B")}},
		"city reversed":  {cs: []source.CityRecord{city("10.0.0.10", "10.0.0.0", "A")}},
		"asn overlap":    {ns: []asn.Range{as("10.0.0.0", "10.0.0.10", 1), as("10.0.0.10", "10.0.0.20", 2)}},
		"asn reversed":   {ns: []asn.Range{as("10.0.0.10", "10.0.0.0", 1)}},
		"city duplicate": {cs: []source.CityRecord{city("10.0.0.0", "10.0.0.10", "A"), city("10.0.0.0", "10.0.0.10", "A")}},
	}
	for name, c := range cases {
		if _, _, err := Merge(c.cs, c.ns, Options{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
