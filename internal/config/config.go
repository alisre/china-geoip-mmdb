// Package config loads the YAML build configuration.
package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Source   Source     `yaml:"source"`
	ASNFix   ASNFix     `yaml:"asn_fix"`
	Cloud    []CloudTag `yaml:"cloud"`
	Output   Output     `yaml:"output"`
	Filter   Filter     `yaml:"filter"`
	Build    Build      `yaml:"build"`
	Validate Validate   `yaml:"validate"`
}

type Source struct {
	IPv4 string `yaml:"ipv4"`
	IPv6 string `yaml:"ipv6"`
	ASN  string `yaml:"asn"`
}

type Output struct {
	File string `yaml:"file"`
}

// ASNFix corrects source.asn. Layers apply in order: bgp (within china_ip),
// fallback_mmdb (gaps only), providers, overrides. Empty paths disable a layer.
type ASNFix struct {
	// ChinaIP files are unioned into the China IP set: RIR delegated stats
	// (filter.country_code rows) and/or CIDR lists. It limits the bgp layer and
	// is cross-checked against the city source's mainland ranges.
	ChinaIP []string `yaml:"china_ip"`
	// BGP lists iptoasn.com ip2asn-v4/v6.tsv[.gz] files. Their origin ASN
	// replaces source.asn for routed China addresses.
	BGP []string `yaml:"bgp"`
	// FallbackMMDB is a second GeoLite2-ASN shaped MMDB (e.g. DB-IP ASN Lite)
	// used only where source.asn has no ASN.
	FallbackMMDB string `yaml:"fallback_mmdb"`
	// Names is an ASN name list (missuo/ASN-China ASN.China.list) used to fill
	// empty org names of provider and override entries.
	Names     string     `yaml:"asn_names"`
	Providers []Provider `yaml:"providers"`
	// Overrides is a manual "CIDR ASN [org]" list that always wins.
	Overrides string `yaml:"overrides"`
}

// Provider is a cloud/IDC CIDR list. Inside these CIDRs an upstream ASN not in
// ASNs is replaced by ASNs[0] with Org.
type Provider struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
	ASNs  []uint32 `yaml:"asns"`
	Org   string   `yaml:"org"`
}

// CloudTag labels records covered by Files with Name in the "cloud" field.
// When lists overlap, the earlier entry wins.
type CloudTag struct {
	Name  string   `yaml:"name"`
	Files []string `yaml:"files"`
}

// Paths returns every input file the fix layers read.
func (f ASNFix) Paths() []string {
	var ps []string
	for _, p := range []string{f.FallbackMMDB, f.Names, f.Overrides} {
		if p != "" {
			ps = append(ps, p)
		}
	}
	ps = append(ps, f.ChinaIP...)
	ps = append(ps, f.BGP...)
	for _, p := range f.Providers {
		ps = append(ps, p.Files...)
	}
	return ps
}

type Filter struct {
	// CountryCode is written into country.iso_code of every output record.
	CountryCode string `yaml:"country_code"`
	// CountryName must equal the country column of the upstream source line.
	// Upstream writes exactly "中国" for mainland rows and "中国香港" /
	// "中国澳门" / "中国台湾" for HK/MO/TW.
	CountryName string `yaml:"country_name"`
	// ExcludeProvinces is a defensive filter: rows whose province starts with
	// any of these prefixes are dropped even if the country column matches.
	ExcludeProvinces []string `yaml:"exclude_provinces"`
	// CityFromProvince: when city is empty and province is one of these
	// (municipalities), city is set to the province name.
	CityFromProvince []string `yaml:"city_from_province"`
	IncludeIPv4      bool     `yaml:"include_ipv4"`
	IncludeIPv6      bool     `yaml:"include_ipv6"`
}

type Build struct {
	SkipInvalidRecords      bool   `yaml:"skip_invalid_records"`
	PreserveCityWithoutASN  bool   `yaml:"preserve_city_without_asn"`
	RecordSize              int    `yaml:"record_size"`
	IncludeReservedNetworks bool   `yaml:"include_reserved_networks"`
	DatabaseType            string `yaml:"database_type"`
}

type Validate struct {
	// SampleSize is the number of merged ranges (per address family) whose
	// first and last address are looked up in the generated database.
	SampleSize    int     `yaml:"sample_size"`
	MinIPv4Ranges int     `yaml:"min_ipv4_ranges"`
	MinIPv6Ranges int     `yaml:"min_ipv6_ranges"`
	Checks        []Check `yaml:"checks"`
}

// Check is a known-IP regression test. Empty string fields and a nil ASN are
// not compared. Absent=true asserts that the IP is not in the database.
type Check struct {
	IP       string  `yaml:"ip"`
	Absent   bool    `yaml:"absent"`
	Province string  `yaml:"province"`
	City     string  `yaml:"city"`
	District string  `yaml:"district"`
	ISP      string  `yaml:"isp"`
	Cloud    string  `yaml:"cloud"`
	ASN      *uint32 `yaml:"asn"`
	ASNOrg   string  `yaml:"asn_org"`
}

func Default() Config {
	return Config{
		Source: Source{
			IPv4: "./data/ipv4_source.txt",
			IPv6: "./data/ipv6_source.txt",
			ASN:  "./data/GeoLite2-ASN.mmdb",
		},
		Output: Output{File: "./output/China-City-ASN.mmdb"},
		Filter: Filter{
			CountryCode:      "CN",
			CountryName:      "中国",
			ExcludeProvinces: []string{"香港", "澳门", "台湾"},
			CityFromProvince: []string{"北京市", "上海市", "天津市", "重庆市"},
			IncludeIPv4:      true,
			IncludeIPv6:      true,
		},
		Build: Build{
			PreserveCityWithoutASN: true,
			RecordSize:             28,
			DatabaseType:           "China-City-ASN",
		},
		Validate: Validate{SampleSize: 2000},
	}
}

// Load reads the YAML file at path on top of Default(). Relative paths in the
// file are resolved against the directory containing the config file.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	base := filepath.Dir(path)
	paths := []*string{&cfg.Source.IPv4, &cfg.Source.IPv6, &cfg.Source.ASN, &cfg.Output.File,
		&cfg.ASNFix.FallbackMMDB, &cfg.ASNFix.Names, &cfg.ASNFix.Overrides}
	for i := range cfg.ASNFix.ChinaIP {
		paths = append(paths, &cfg.ASNFix.ChinaIP[i])
	}
	for i := range cfg.ASNFix.BGP {
		paths = append(paths, &cfg.ASNFix.BGP[i])
	}
	for i := range cfg.Cloud {
		for j := range cfg.Cloud[i].Files {
			paths = append(paths, &cfg.Cloud[i].Files[j])
		}
	}
	for i := range cfg.ASNFix.Providers {
		for j := range cfg.ASNFix.Providers[i].Files {
			paths = append(paths, &cfg.ASNFix.Providers[i].Files[j])
		}
	}
	for _, p := range paths {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	return cfg, cfg.Check()
}

func (c Config) Check() error {
	var errs []error
	if !c.Filter.IncludeIPv4 && !c.Filter.IncludeIPv6 {
		errs = append(errs, errors.New("filter: at least one of include_ipv4/include_ipv6 must be true"))
	}
	if c.Filter.IncludeIPv4 && c.Source.IPv4 == "" {
		errs = append(errs, errors.New("source.ipv4 is required"))
	}
	if c.Filter.IncludeIPv6 && c.Source.IPv6 == "" {
		errs = append(errs, errors.New("source.ipv6 is required"))
	}
	if c.Source.ASN == "" {
		errs = append(errs, errors.New("source.asn is required"))
	}
	if c.Output.File == "" {
		errs = append(errs, errors.New("output.file is required"))
	}
	if c.Filter.CountryName == "" || c.Filter.CountryCode == "" {
		errs = append(errs, errors.New("filter.country_name and filter.country_code are required"))
	}
	switch c.Build.RecordSize {
	case 24, 28, 32:
	default:
		errs = append(errs, fmt.Errorf("build.record_size must be 24, 28 or 32, got %d", c.Build.RecordSize))
	}
	for i, t := range c.Cloud {
		if t.Name == "" || len(t.Files) == 0 {
			errs = append(errs, fmt.Errorf("cloud[%d]: name and files are required", i))
		}
	}
	for i, p := range c.ASNFix.Providers {
		if p.Name == "" || len(p.Files) == 0 || len(p.ASNs) == 0 || slices.Contains(p.ASNs, 0) {
			errs = append(errs, fmt.Errorf("asn_fix.providers[%d]: name, files and non-zero asns are required", i))
		}
	}
	for i, ch := range c.Validate.Checks {
		if _, err := netip.ParseAddr(ch.IP); err != nil {
			errs = append(errs, fmt.Errorf("validate.checks[%d]: invalid ip %q", i, ch.IP))
		}
	}
	return errors.Join(errs...)
}
