package asn

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"

	"go4.org/netipx"
)

// ReadCIDRs reads one-CIDR-per-line lists such as axpwx/IP-Data provider/*.txt
// or metowolf/iplist data/isp/*.txt. Blank lines and lines starting with # or
// // are ignored. The returned ranges are merged, sorted and carry no ASN.
func ReadCIDRs(paths []string) (v4, v6 []Range, err error) {
	var b4, b6 netipx.IPSetBuilder
	for _, path := range paths {
		err := scanLines(path, func(line string) error { return addCIDR(&b4, &b6, line) })
		if err != nil {
			return nil, nil, err
		}
	}
	if v4, err = setRanges(&b4); err != nil {
		return nil, nil, err
	}
	if v6, err = setRanges(&b6); err != nil {
		return nil, nil, err
	}
	return v4, v6, nil
}

func addCIDR(b4, b6 *netipx.IPSetBuilder, s string) error {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return err
	}
	p, ok := normalizePrefix(p)
	if !ok {
		return nil
	}
	if p.Addr().Is4() {
		b4.AddPrefix(p)
	} else {
		b6.AddPrefix(p)
	}
	return nil
}

func setRanges(b *netipx.IPSetBuilder) ([]Range, error) {
	s, err := b.IPSet()
	if err != nil {
		return nil, err
	}
	var out []Range
	for _, r := range s.Ranges() {
		out = append(out, Range{Start: r.From(), End: r.To()})
	}
	return out, nil
}

// ReadOverrides reads manual corrections, one "CIDR ASN [org...]" per line.
// Entries of the same address family must not overlap.
func ReadOverrides(path string) (v4, v6 []Range, err error) {
	err = scanLines(path, func(line string) error {
		f := strings.Fields(line)
		if len(f) < 2 {
			return fmt.Errorf("want \"CIDR ASN [org]\", got %q", line)
		}
		p, err := netip.ParsePrefix(f[0])
		if err != nil {
			return err
		}
		n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(f[1]), "AS"), 10, 32)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid asn %q", f[1])
		}
		p, ok := normalizePrefix(p)
		if !ok {
			return fmt.Errorf("%s overlaps the IPv4-mapped range; write it as plain IPv4", f[0])
		}
		rg := netipx.RangeOfPrefix(p)
		r := Range{Start: rg.From(), End: rg.To(), ASN: uint32(n), Org: strings.Join(f[2:], " ")}
		if p.Addr().Is4() {
			v4 = append(v4, r)
		} else {
			v6 = append(v6, r)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if v4, err = normalize(v4); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if v6, err = normalize(v6); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return v4, v6, nil
}

// ReadNames reads an ASN name list in missuo/ASN-China format:
//
//	IP-ASN,55990 // Huawei Cloud Service data center
//
// Lines in any other shape are ignored.
func ReadNames(path string) (map[uint32]string, error) {
	names := map[uint32]string{}
	err := scanLines(path, func(line string) error {
		rest, ok := strings.CutPrefix(line, "IP-ASN,")
		if !ok {
			return nil
		}
		num, name, _ := strings.Cut(rest, "//")
		n, err := strconv.ParseUint(strings.TrimSpace(num), 10, 32)
		if err != nil {
			return fmt.Errorf("invalid asn in %q", line)
		}
		if name = strings.TrimSpace(name); name != "" {
			names[uint32(n)] = name
		}
		return nil
	})
	return names, err
}

// ReadIPSet unions IP sets from mixed files: lines containing "|" are RIR
// delegated-extended stats (APNIC delegated-apnic-latest; only allocated/assigned
// blocks of country cc count), other lines are CIDRs (china-operator-ip
// china.txt/china6.txt, metowolf special/china.txt). Returns merged, sorted ranges.
func ReadIPSet(paths []string, cc string) (v4, v6 []Range, err error) {
	var b4, b6 netipx.IPSetBuilder
	for _, path := range paths {
		err := scanLines(path, func(line string) error {
			if !strings.Contains(line, "|") {
				return addCIDR(&b4, &b6, line)
			}
			return addDelegated(&b4, &b6, line, cc)
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if v4, err = setRanges(&b4); err != nil {
		return nil, nil, err
	}
	if v6, err = setRanges(&b6); err != nil {
		return nil, nil, err
	}
	return v4, v6, nil
}

func addDelegated(b4, b6 *netipx.IPSetBuilder, line, cc string) error {
	f := strings.Split(line, "|")
	if len(f) < 7 || f[1] != cc || (f[6] != "allocated" && f[6] != "assigned") {
		return nil
	}
	switch f[2] {
	case "ipv4":
		start, err := netip.ParseAddr(f[3])
		if err != nil || !start.Is4() {
			return fmt.Errorf("invalid ipv4 %q", f[3])
		}
		n, err := strconv.ParseUint(f[4], 10, 32)
		if err != nil || n == 0 {
			return fmt.Errorf("invalid count %q", f[4])
		}
		s := start.As4()
		u := uint64(s[0])<<24 | uint64(s[1])<<16 | uint64(s[2])<<8 | uint64(s[3])
		if u+n-1 > 0xffffffff {
			return fmt.Errorf("block %s+%d overflows", f[3], n)
		}
		e := uint32(u + n - 1)
		b4.AddRange(netipx.IPRangeFrom(start, netip.AddrFrom4([4]byte{byte(e >> 24), byte(e >> 16), byte(e >> 8), byte(e)})))
	case "ipv6":
		return addCIDR(b4, b6, f[3]+"/"+f[4])
	}
	return nil
}

// ReadIP2ASN reads BGP-derived iptoasn.com files (ip2asn-v4.tsv[.gz],
// ip2asn-v6.tsv[.gz]): "start\tend\tASN\tCC\tdescription". Unrouted ranges
// (ASN 0) are skipped. "HANDLE - Org" descriptions are reduced to "Org".
func ReadIP2ASN(paths []string) (v4, v6 []Range, err error) {
	mapped := netipx.RangeOfPrefix(v4Mapped)
	for _, path := range paths {
		err := scanLines(path, func(line string) error {
			f := strings.Split(line, "\t")
			if len(f) < 5 {
				return fmt.Errorf("want 5 tab-separated fields, got %d", len(f))
			}
			n, err := strconv.ParseUint(f[2], 10, 32)
			if err != nil {
				return fmt.Errorf("invalid asn %q", f[2])
			}
			if n == 0 {
				return nil
			}
			start, err1 := netip.ParseAddr(f[0])
			end, err2 := netip.ParseAddr(f[1])
			if err1 != nil || err2 != nil || start.Is4() != end.Is4() || end.Less(start) {
				return fmt.Errorf("invalid range %s-%s", f[0], f[1])
			}
			if !start.Is4() && netipx.IPRangeFrom(start, end).Overlaps(mapped) {
				return nil
			}
			org := f[4]
			if _, after, ok := strings.Cut(org, " - "); ok {
				org = after
			}
			r := Range{Start: start, End: end, ASN: uint32(n), Org: org}
			if start.Is4() {
				v4 = append(v4, r)
			} else {
				v6 = append(v6, r)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if v4, err = normalize(v4); err != nil {
		return nil, nil, err
	}
	if v6, err = normalize(v6); err != nil {
		return nil, nil, err
	}
	return v4, v6, nil
}

func scanLines(path string, fn func(line string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}
	sc := bufio.NewScanner(r)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		if err := fn(line); err != nil {
			return fmt.Errorf("%s:%d: %w", path, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
