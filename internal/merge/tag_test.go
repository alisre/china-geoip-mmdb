package merge

import (
	"testing"

	"china-geoip-mmdb/internal/asn"
)

func TestTag(t *testing.T) {
	rec := func(start, end, cityName string, n uint32) Record {
		return Record{Start: a(start), End: a(end), Province: "北京市", City: cityName, HasASN: true, ASN: n}
	}
	tag := func(start, end, name string) asn.Range { return asn.Range{Start: a(start), End: a(end), Org: name} }

	recs := []Record{
		rec("10.0.0.0", "10.0.0.255", "北京市", 23724),
		rec("10.0.1.0", "10.0.1.255", "北京市", 4808),
		rec("10.0.2.0", "10.0.2.255", "海淀区", 4808),
	}
	tags := []asn.Range{
		tag("10.0.0.128", "10.0.1.255", "华为云"),
		tag("10.0.2.0", "10.0.2.255", "华为云"),
	}
	got, n := Tag(recs, tags)
	type w struct{ start, end, cloud string }
	ws := []w{
		{"10.0.0.0", "10.0.0.127", ""},
		{"10.0.0.128", "10.0.0.255", "华为云"},
		{"10.0.1.0", "10.0.1.255", "华为云"},
		{"10.0.2.0", "10.0.2.255", "华为云"},
	}
	if len(got) != len(ws) || n != 3 {
		t.Fatalf("tagged=%d got %+v", n, got)
	}
	for i, x := range ws {
		if got[i].Start != a(x.start) || got[i].End != a(x.end) || got[i].Cloud != x.cloud {
			t.Errorf("[%d] %s-%s cloud=%q, want %+v", i, got[i].Start, got[i].End, got[i].Cloud, x)
		}
	}
	if got[1].ASN != 23724 || got[2].ASN != 4808 {
		t.Errorf("ASN not preserved: %+v", got)
	}

	// Adjacent pieces that become identical after tagging coalesce.
	same := []Record{rec("10.1.0.0", "10.1.0.255", "北京市", 1), rec("10.1.1.0", "10.1.1.255", "北京市", 1)}
	got, _ = Tag(same, []asn.Range{tag("10.1.0.0", "10.1.1.255", "阿里云")})
	if len(got) != 1 || got[0].End != a("10.1.1.255") || got[0].Cloud != "阿里云" {
		t.Errorf("coalesce: %+v", got)
	}
}
