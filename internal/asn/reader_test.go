package asn

import (
	"path/filepath"
	"testing"

	"china-geoip-mmdb/internal/testutil"
)

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asn.mmdb")
	testutil.WriteASNDB(t, path, []testutil.ASNNet{
		testutil.Net("1.0.1.0/24", 4134, "CHINANET-BACKBONE"),
		testutil.Net("1.0.2.0/23", 4134, "CHINANET-BACKBONE"),
		testutil.Net("1.0.8.0/21", 4809, "CHINATELECOM CN2"),
		testutil.Net("240e::/24", 4134, "CHINANET-BACKBONE"),
		testutil.Net("2408:8000::/20", 4837, "CHINA UNICOM China169 Backbone"),
	})
	v4, v6, st, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.DatabaseType != "GeoLite2-ASN" {
		t.Errorf("type %q", st.DatabaseType)
	}
	// 1.0.1.0/24 + 1.0.2.0/23 are adjacent with same ASN and coalesce.
	if len(v4) != 2 {
		t.Fatalf("v4=%+v", v4)
	}
	if !v4[0].Start.Is4() || v4[0].Start.String() != "1.0.1.0" || v4[0].End.String() != "1.0.3.255" || v4[0].ASN != 4134 {
		t.Errorf("v4[0]=%+v", v4[0])
	}
	if v4[1].Org != "CHINATELECOM CN2" || v4[1].End.String() != "1.0.15.255" {
		t.Errorf("v4[1]=%+v", v4[1])
	}
	if len(v6) != 2 || v6[0].Start.String() != "2408:8000::" || v6[1].Start.String() != "240e::" ||
		v6[1].End.String() != "240e:ff:ffff:ffff:ffff:ffff:ffff:ffff" {
		t.Errorf("v6=%+v", v6)
	}
	for _, r := range v6 {
		if r.Start.Is4In6() {
			t.Errorf("IPv4-mapped leaked into v6: %+v", r)
		}
	}
}
