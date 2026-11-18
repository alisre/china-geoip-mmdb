package source

import (
	"strings"
	"testing"
)

const v4Sample = `1.0.0.0|1.0.0.255|亚洲|澳大利亚|昆士兰|布里斯班||Cloudflare|AS13335
1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||中国电信|宽带
1.0.2.0|1.0.3.255|亚洲|中国|福建省|福州市||中国电信|宽带
1.0.8.0|1.0.15.255|亚洲|中国|广东省|广州市|天河区|中国电信|
1.0.16.0|1.0.16.255|亚洲|中国香港|香港|||PCCW|AS4760
1.0.17.0|1.0.17.255|亚洲|中国台湾|台湾|台北||HiNet|AS3462
1.0.18.0|1.0.18.255|亚洲|中国澳门|澳门|||CTM|AS4609
1.0.19.0|1.0.19.255|亚洲|中国|香港特别行政区|||PCCW|
10.0.0.0|10.255.255.255|内网IP|内网IP|内网IP|私有网络A类|||

`

func opts(f Family) Options {
	return Options{Family: f, CountryName: "中国", ExcludeProvinces: []string{"香港", "澳门", "台湾"}}
}

func TestParseFiltersMainland(t *testing.T) {
	recs, st, err := Parse(strings.NewReader(v4Sample), opts(IPv4))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records: %+v", len(recs), recs)
	}
	// 1.0.1.0/24 and 1.0.2.0/23 are adjacent with identical data and coalesce.
	if recs[0].Start.String() != "1.0.1.0" || recs[0].End.String() != "1.0.3.255" || recs[0].City != "福州市" {
		t.Errorf("first record %+v", recs[0])
	}
	if recs[1].District != "天河区" || recs[1].ISP != "中国电信" || recs[1].Province != "广东省" {
		t.Errorf("second record %+v", recs[1])
	}
	if st.OtherCountry != 5 || st.ExcludedProvince != 1 || st.Blank != 1 || st.Mainland != 2 {
		t.Errorf("stats %+v", st)
	}
}

func TestParseIPv6(t *testing.T) {
	in := "240e::|240e:ff:ffff:ffff:ffff:ffff:ffff:ffff|亚洲|中国|广东省|深圳市||中国电信|\n" +
		"::ffff:1.0.1.0|::ffff:1.0.1.255|亚洲|中国|福建省|福州市||中国电信|\n" +
		"2001:db8::|2001:db8::ffff|亚洲|美国|||||\n"
	recs, st, err := Parse(strings.NewReader(in), opts(IPv6))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].City != "深圳市" || st.IPv4MappedSkipped != 1 {
		t.Fatalf("recs=%+v stats=%+v", recs, st)
	}
}

func TestParseUnsortedInputIsSorted(t *testing.T) {
	in := "1.0.8.0|1.0.8.255|亚洲|中国|广东省|广州市||电信|\n1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n"
	recs, _, err := Parse(strings.NewReader(in), opts(IPv4))
	if err != nil {
		t.Fatal(err)
	}
	if recs[0].City != "福州市" || recs[1].City != "广州市" {
		t.Errorf("not sorted: %+v", recs)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]struct {
		in  string
		fam Family
	}{
		"field count": {"1.0.1.0|1.0.1.255|亚洲|中国|福建省\n", IPv4},
		"bad ip":      {"1.0.1.x|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n", IPv4},
		"reversed":    {"1.0.1.255|1.0.1.0|亚洲|中国|福建省|福州市||电信|\n", IPv4},
		"family v4":   {"240e::|240e::ff|亚洲|中国|广东省|深圳市||电信|\n", IPv4},
		"family v6":   {"1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n", IPv6},
		"overlap":     {"1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n1.0.1.128|1.0.2.255|亚洲|中国|福建省|厦门市||电信|\n", IPv4},
	}
	for name, c := range cases {
		if _, _, err := Parse(strings.NewReader(c.in), opts(c.fam)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseCityFromProvince(t *testing.T) {
	in := "114.116.0.0|114.116.255.255|亚洲|中国|北京市|||电信|\n" +
		"1.0.1.0|1.0.1.255|亚洲|中国|浙江省|||电信|\n" +
		"1.0.2.0|1.0.2.255|亚洲|中国|上海市|上海市|浦东新区|电信|\n"
	o := opts(IPv4)
	o.CityFromProvince = []string{"北京市", "上海市", "天津市", "重庆市"}
	recs, st, err := Parse(strings.NewReader(in), o)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range recs {
		got[r.Province] = r.City
	}
	if got["北京市"] != "北京市" || got["浙江省"] != "" || got["上海市"] != "上海市" || st.CityFilled != 1 {
		t.Errorf("got %v stats %+v", got, st)
	}
}

func TestParseSkipInvalidAndDuplicates(t *testing.T) {
	in := "bad line\n" +
		"1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n" +
		"1.0.1.0|1.0.1.255|亚洲|中国|福建省|福州市||电信|\n" +
		"1.0.1.128|1.0.2.255|亚洲|中国|福建省|厦门市||电信|\n"
	o := opts(IPv4)
	o.SkipInvalid = true
	recs, st, err := Parse(strings.NewReader(in), o)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || st.Invalid != 1 || st.Duplicates != 1 || st.Overlaps != 1 {
		t.Errorf("recs=%+v stats=%+v", recs, st)
	}
}
