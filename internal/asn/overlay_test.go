package asn

import (
	"compress/gzip"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func rg(start, end string, num uint32, org string) Range {
	return Range{Start: netip.MustParseAddr(start), End: netip.MustParseAddr(end), ASN: num, Org: org}
}

func equalRanges(t *testing.T, got, want []Range) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d ranges %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOverlayReplace(t *testing.T) {
	base := []Range{
		rg("114.112.0.0", "114.119.255.255", 4808, "China Unicom Beijing"),
		rg("114.120.0.0", "114.120.255.255", 4134, "CHINANET"),
	}
	over := []Range{rg("114.116.0.0", "114.116.255.255", 55990, "Huawei Cloud")}
	got, n := Overlay(base, over, nil)
	equalRanges(t, got, []Range{
		rg("114.112.0.0", "114.115.255.255", 4808, "China Unicom Beijing"),
		rg("114.116.0.0", "114.116.255.255", 55990, "Huawei Cloud"),
		rg("114.117.0.0", "114.119.255.255", 4808, "China Unicom Beijing"),
		rg("114.120.0.0", "114.120.255.255", 4134, "CHINANET"),
	})
	if n != 1 {
		t.Errorf("taken=%d", n)
	}
}

func TestOverlayKeepAndGaps(t *testing.T) {
	base := []Range{
		rg("10.0.0.0", "10.0.0.255", 55990, "Huawei"),
		rg("10.0.2.0", "10.0.2.255", 4808, "Unicom"),
	}
	over := []Range{rg("10.0.0.0", "10.0.3.255", 55990, "Huawei")}
	keep := func(r Range) bool { return r.ASN == 55990 || r.ASN == 136907 }
	got, n := Overlay(base, over, keep)
	// Kept base piece and the over pieces carry identical data and coalesce.
	equalRanges(t, got, []Range{rg("10.0.0.0", "10.0.3.255", 55990, "Huawei")})
	if n != 3 {
		t.Errorf("taken=%d, want 3 (gap, replaced, tail gap)", n)
	}
}

func TestOverlayGapFillOnly(t *testing.T) {
	base := []Range{rg("1.0.0.0", "1.0.0.255", 1, "a")}
	over := []Range{rg("0.255.255.0", "1.0.1.255", 2, "b")}
	got, n := Overlay(base, over, func(Range) bool { return true })
	equalRanges(t, got, []Range{
		rg("0.255.255.0", "0.255.255.255", 2, "b"),
		rg("1.0.0.0", "1.0.0.255", 1, "a"),
		rg("1.0.1.0", "1.0.1.255", 2, "b"),
	})
	if n != 2 {
		t.Errorf("taken=%d", n)
	}
}

func TestOverlayBoundaries(t *testing.T) {
	base := []Range{rg("0.0.0.0", "255.255.255.255", 1, "all")}
	over := []Range{
		rg("0.0.0.0", "0.0.0.0", 2, "first"),
		rg("128.0.0.0", "128.0.0.1", 3, "mid"),
		rg("255.255.255.255", "255.255.255.255", 4, "last"),
	}
	got, _ := Overlay(base, over, nil)
	equalRanges(t, got, []Range{
		rg("0.0.0.0", "0.0.0.0", 2, "first"),
		rg("0.0.0.1", "127.255.255.255", 1, "all"),
		rg("128.0.0.0", "128.0.0.1", 3, "mid"),
		rg("128.0.0.2", "255.255.255.254", 1, "all"),
		rg("255.255.255.255", "255.255.255.255", 4, "last"),
	})
}

func TestOverlayOverSpansManyBase(t *testing.T) {
	base := []Range{
		rg("2.0.0.0", "2.0.0.9", 1, "a"),
		rg("2.0.0.20", "2.0.0.29", 2, "b"),
		rg("2.0.0.40", "2.0.0.49", 3, "c"),
	}
	over := []Range{rg("2.0.0.5", "2.0.0.45", 9, "x")}
	got, _ := Overlay(base, over, func(r Range) bool { return r.ASN == 2 })
	equalRanges(t, got, []Range{
		rg("2.0.0.0", "2.0.0.4", 1, "a"),
		rg("2.0.0.5", "2.0.0.19", 9, "x"),
		rg("2.0.0.20", "2.0.0.29", 2, "b"),
		rg("2.0.0.30", "2.0.0.45", 9, "x"),
		rg("2.0.0.46", "2.0.0.49", 3, "c"),
	})
}

func TestIntersect(t *testing.T) {
	rs := []Range{rg("1.0.0.0", "1.0.0.255", 1, "a"), rg("1.0.2.0", "1.0.5.255", 2, "b")}
	set := []Range{rg("1.0.0.128", "1.0.3.127", 0, ""), rg("1.0.4.0", "1.0.4.0", 0, "")}
	equalRanges(t, Intersect(rs, set), []Range{
		rg("1.0.0.128", "1.0.0.255", 1, "a"),
		rg("1.0.2.0", "1.0.3.127", 2, "b"),
		rg("1.0.4.0", "1.0.4.0", 2, "b"),
	})
}

func TestReadDelegatedAndIP2ASN(t *testing.T) {
	dir := t.TempDir()
	del := filepath.Join(dir, "delegated")
	body := "2|apnic|20261117|1|19830613|20261116|+1000\napnic|*|ipv4|*|1|summary\n" +
		"apnic|CN|ipv4|1.0.1.0|256|20110414|allocated\napnic|CN|ipv4|1.0.2.0|512|20110414|allocated\n" +
		"apnic|CN|ipv4|1.1.0.0|768|20110414|assigned\napnic|HK|ipv4|1.0.4.0|256|20110414|allocated\n" +
		"apnic|CN|ipv6|240e::|20|20131014|allocated\n"
	if err := os.WriteFile(del, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cidrs := filepath.Join(dir, "china.txt")
	if err := os.WriteFile(cidrs, []byte("1.0.4.0/24\n1.1.2.0/23\n2001:250::/30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v4, v6, err := ReadIPSet([]string{del, cidrs}, "CN")
	if err != nil {
		t.Fatal(err)
	}
	equalRanges(t, v4, []Range{rg("1.0.1.0", "1.0.4.255", 0, ""), rg("1.1.0.0", "1.1.3.255", 0, "")})
	if len(v6) != 2 || v6[1].End.String() != "240e:fff:ffff:ffff:ffff:ffff:ffff:ffff" {
		t.Errorf("v6=%+v", v6)
	}

	gzPath := filepath.Join(dir, "ip2asn.tsv.gz")
	f, err := os.Create(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(f)
	zw.Write([]byte("1.0.1.0\t1.0.3.255\t0\tNone\tNot routed\n" +
		"114.116.64.0\t114.116.223.255\t23724\tCN\tCHINANET-IDC-BJ-AP - IDC, China Telecommunications Corporation\n" +
		"240e::\t240e:ff:ffff:ffff:ffff:ffff:ffff:ffff\t4134\tCN\tCHINANET-BACKBONE\n" +
		"::ffff:0.0.0.0\t::ffff:255.255.255.255\t1\tUS\tmapped\n"))
	zw.Close()
	f.Close()
	b4, b6, err := ReadIP2ASN([]string{gzPath})
	if err != nil {
		t.Fatal(err)
	}
	equalRanges(t, b4, []Range{rg("114.116.64.0", "114.116.223.255", 23724, "IDC, China Telecommunications Corporation")})
	if len(b6) != 1 || b6[0].ASN != 4134 || b6[0].Org != "CHINANET-BACKBONE" {
		t.Errorf("b6=%+v", b6)
	}
}

func TestReadLists(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cidrs := write("huawei.txt", "# comment\n114.116.0.0/16\n114.116.128.0/17\n114.117.0.0/16\n\n2407:c080::/29\n::ffff:1.2.3.0/120\n")
	v4, v6, err := ReadCIDRs([]string{cidrs})
	if err != nil {
		t.Fatal(err)
	}
	equalRanges(t, v4, []Range{rg("1.2.3.0", "1.2.3.255", 0, ""), rg("114.116.0.0", "114.117.255.255", 0, "")})
	if len(v6) != 1 || v6[0].Start.String() != "2407:c080::" {
		t.Errorf("v6=%+v", v6)
	}

	ov := write("ov.txt", "114.116.220.0/24 AS55990 Huawei Cloud Service data center\n240e::/24 4134\n")
	o4, o6, err := ReadOverrides(ov)
	if err != nil {
		t.Fatal(err)
	}
	equalRanges(t, o4, []Range{rg("114.116.220.0", "114.116.220.255", 55990, "Huawei Cloud Service data center")})
	if len(o6) != 1 || o6[0].ASN != 4134 || o6[0].Org != "" {
		t.Errorf("o6=%+v", o6)
	}

	if _, _, err := ReadOverrides(write("bad.txt", "1.0.0.0/8 4134\n1.1.0.0/16 4837\n")); err == nil {
		t.Error("overlapping overrides accepted")
	}
	if _, _, err := ReadOverrides(write("bad2.txt", "1.0.0.0/8 x\n")); err == nil {
		t.Error("invalid asn accepted")
	}

	names, err := ReadNames(write("ASN.China.list", "// header\nIP-ASN,55990 // Huawei Cloud Service data center\nIP-ASN,4134 // China Telecom Backbone\n"))
	if err != nil {
		t.Fatal(err)
	}
	if names[55990] != "Huawei Cloud Service data center" || names[4134] != "China Telecom Backbone" {
		t.Errorf("names=%v", names)
	}
}
