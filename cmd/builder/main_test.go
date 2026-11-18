package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oschwald/maxminddb-golang/v2"

	"china-geoip-mmdb/internal/testutil"
	"china-geoip-mmdb/internal/validate"

	"net/netip"
)

const v4Src = `1.0.0.0|1.0.0.255|亚洲|澳大利亚|昆士兰|布里斯班||Cloudflare|AS13335
1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市|鼓楼区|中国电信|宽带
1.0.8.0|1.0.15.255|亚洲|中国|广东省|广州市||中国电信|
1.0.16.0|1.0.16.255|亚洲|中国香港|香港|||PCCW|AS4760
36.0.0.0|36.0.0.255|亚洲|中国|北京市|北京市|海淀区|中国联通|
`

const v6Src = `240e::|240e:ff:ffff:ffff:ffff:ffff:ffff:ffff|亚洲|中国|广东省|深圳市|南山区|中国电信|
2001:db8::|2001:db8::ffff|亚洲|美国|||||
`

func setup(t *testing.T, extraValidate string) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "data"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "data/ipv4_source.txt"), []byte(v4Src), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "data/ipv6_source.txt"), []byte(v6Src), 0o644))
	testutil.WriteASNDB(t, filepath.Join(dir, "data/GeoLite2-ASN.mmdb"), []testutil.ASNNet{
		testutil.Net("1.0.1.0/25", 4134, "CHINANET-BACKBONE"),
		testutil.Net("1.0.8.0/22", 4809, "CHINATELECOM CN2"),
		testutil.Net("240e::/25", 4134, "CHINANET-BACKBONE"),
		testutil.Net("8.8.8.0/24", 15169, "GOOGLE"),
	})
	cfg := `source:
  ipv4: ./data/ipv4_source.txt
  ipv6: ./data/ipv6_source.txt
  asn: ./data/GeoLite2-ASN.mmdb
output:
  file: ./output/China-City-ASN.mmdb
validate:
  sample_size: 100
  checks:
    - {ip: 1.0.1.1, province: 福建省, city: 福州市, district: 鼓楼区, isp: 中国电信, asn: 4134, asn_org: CHINANET-BACKBONE}
    - {ip: 8.8.8.8, absent: true}
    - {ip: 1.0.16.1, absent: true}
` + extraValidate
	cfgPath = filepath.Join(dir, "config.yaml")
	must(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))
	return dir, cfgPath
}

func TestEndToEnd(t *testing.T) {
	dir, cfgPath := setup(t, "")
	if err := run(cfgPath); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "output/China-City-ASN.mmdb")
	db, err := maxminddb.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tests := []struct {
		ip       string
		found    bool
		province string
		city     string
		asn      uint32
	}{
		{"1.0.1.1", true, "福建省", "福州市", 4134},
		{"1.0.1.200", true, "福建省", "福州市", 0}, // city kept without ASN
		{"1.0.9.1", true, "广东省", "广州市", 4809},
		{"1.0.12.1", true, "广东省", "广州市", 0},
		{"36.0.0.1", true, "北京市", "北京市", 0},
		{"::ffff:1.0.1.1", true, "福建省", "福州市", 4134}, // IPv4-mapped alias
		{"240e::1", true, "广东省", "深圳市", 4134},
		{"240e:80::1", true, "广东省", "深圳市", 0},
		{"1.0.0.1", false, "", "", 0},     // Australia
		{"1.0.16.1", false, "", "", 0},    // Hong Kong
		{"8.8.8.8", false, "", "", 0},     // not in source
		{"2001:db8::1", false, "", "", 0}, // US
	}
	for _, tt := range tests {
		rec, found, err := validate.Lookup(db, netip.MustParseAddr(tt.ip))
		if err != nil {
			t.Fatal(err)
		}
		if found != tt.found {
			t.Errorf("%s: found=%v want %v", tt.ip, found, tt.found)
			continue
		}
		if !found {
			continue
		}
		if rec.Country.ISOCode != "CN" || rec.Country.Names["zh-CN"] != "中国" {
			t.Errorf("%s: country %+v", tt.ip, rec.Country)
		}
		if rec.Province() != tt.province || rec.CityName() != tt.city || rec.ASN != tt.asn {
			t.Errorf("%s: got %s/%s/%d want %s/%s/%d", tt.ip, rec.Province(), rec.CityName(), rec.ASN, tt.province, tt.city, tt.asn)
		}
	}
	if db.Metadata.DatabaseType != "China-City-ASN" || db.Metadata.IPVersion != 6 {
		t.Errorf("metadata %+v", db.Metadata)
	}
}

func TestFailedValidationKeepsPreviousOutput(t *testing.T) {
	dir, cfgPath := setup(t, "    - {ip: 1.0.1.1, city: 深圳市}\n")
	out := filepath.Join(dir, "output/China-City-ASN.mmdb")
	must(t, os.MkdirAll(filepath.Dir(out), 0o755))
	must(t, os.WriteFile(out, []byte("previous"), 0o644))

	err := run(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "validation failed") {
		t.Fatalf("expected validation failure, got %v", err)
	}
	data, _ := os.ReadFile(out)
	if string(data) != "previous" {
		t.Error("previous output was overwritten")
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestEmptyInputFails(t *testing.T) {
	dir, cfgPath := setup(t, "")
	must(t, os.WriteFile(filepath.Join(dir, "data/ipv6_source.txt"), nil, 0o644))
	if err := run(cfgPath); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty input error, got %v", err)
	}
}

func TestASNFixLayers(t *testing.T) {
	dir, cfgPath := setup(t, "    - {ip: 1.0.8.1, asn: 55990, asn_org: Huawei Cloud Service data center}\n")
	testutil.WriteASNDB(t, filepath.Join(dir, "data/fallback.mmdb"), []testutil.ASNNet{
		testutil.Net("36.0.0.0/24", 4808, "China Unicom Beijing Province Network"),
		testutil.Net("1.0.1.0/24", 9999, "must not replace GeoLite2"),
	})
	must(t, os.WriteFile(filepath.Join(dir, "data/huawei.txt"), []byte("1.0.8.0/24\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "data/names.list"), []byte("IP-ASN,55990 // Huawei Cloud Service data center\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "data/overrides.txt"), []byte("1.0.1.128/25 4538 CERNET\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "data/delegated"), []byte("2|apnic|20261117|1|19830613|20261116|+1000\n"+
		"apnic|CN|ipv4|1.0.8.0|1024|20110412|allocated\napnic|HK|ipv4|1.0.12.0|1024|20110412|allocated\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "data/ip2asn.tsv"), []byte("1.0.9.0\t1.0.9.255\t4812\tCN\tCHINANET-SH-AP - China Telecom (Group)\n"+
		"1.0.10.0\t1.0.10.255\t0\tNone\tNot routed\n1.0.12.0\t1.0.12.255\t4837\tCN\tCHINA169 - China Unicom\n"), 0o644))
	cfg, err := os.ReadFile(cfgPath)
	must(t, err)
	cfg = append(cfg, `asn_fix:
  china_ip: [./data/delegated]
  bgp: [./data/ip2asn.tsv]
  fallback_mmdb: ./data/fallback.mmdb
  asn_names: ./data/names.list
  providers:
    - {name: huawei, files: [./data/huawei.txt], asns: [55990, 136907]}
  overrides: ./data/overrides.txt
cloud:
  - {name: 华为云, files: [./data/huawei.txt]}
  - {name: IDC, files: [./data/huawei.txt, ./data/idc.txt]}
`...)
	must(t, os.WriteFile(filepath.Join(dir, "data/idc.txt"), []byte("1.0.9.0/24\n"), 0o644))
	must(t, os.WriteFile(cfgPath, cfg, 0o644))
	if err := run(cfgPath); err != nil {
		t.Fatal(err)
	}
	db, err := maxminddb.Open(filepath.Join(dir, "output/China-City-ASN.mmdb"))
	must(t, err)
	defer db.Close()

	tests := []struct {
		ip  string
		asn uint32
		org string
	}{
		{"1.0.1.1", 4134, "CHINANET-BACKBONE"},                      // GeoLite2 kept, fallback ignored
		{"1.0.1.200", 4538, "CERNET"},                               // manual override
		{"1.0.8.1", 55990, "Huawei Cloud Service data center"},      // provider replaces 4809
		{"1.0.9.1", 4812, "China Telecom (Group)"},                  // BGP origin replaces GeoLite2
		{"1.0.10.1", 4809, "CHINATELECOM CN2"},                      // unrouted: GeoLite2 kept
		{"1.0.12.1", 0, ""},                                         // BGP entry outside China set ignored
		{"36.0.0.1", 4808, "China Unicom Beijing Province Network"}, // fallback fills gap
	}
	for _, tt := range tests {
		rec, found, err := validate.Lookup(db, netip.MustParseAddr(tt.ip))
		must(t, err)
		if !found || rec.ASN != tt.asn || rec.ASNOrg != tt.org {
			t.Errorf("%s: found=%v asn=%d org=%q, want %d %q", tt.ip, found, rec.ASN, rec.ASNOrg, tt.asn, tt.org)
		}
	}
	for ip, want := range map[string]string{"1.0.8.1": "华为云", "1.0.9.1": "IDC", "1.0.10.1": ""} {
		rec, _, err := validate.Lookup(db, netip.MustParseAddr(ip))
		must(t, err)
		if rec.Cloud != want {
			t.Errorf("%s: cloud=%q, want %q", ip, rec.Cloud, want)
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
