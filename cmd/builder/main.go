// Command builder generates China-City-ASN.mmdb from ip2region-xdb source
// files and GeoLite2-ASN.mmdb.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math/big"
	"net/netip"
	"os"
	"slices"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"china-geoip-mmdb/internal/asn"
	"china-geoip-mmdb/internal/config"
	"china-geoip-mmdb/internal/merge"
	"china-geoip-mmdb/internal/source"
	"china-geoip-mmdb/internal/validate"
	"china-geoip-mmdb/internal/writer"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	lookup := flag.String("lookup", "", "look up an IP in -db (or output.file) and print the record as JSON")
	dbPath := flag.String("db", "", "mmdb file for -lookup/-verify (defaults to output.file from config)")
	verify := flag.Int("verify", 0, "check the ASN of N evenly spaced networks in -db against ipinfo.io (token from IPINFO_TOKEN)")
	minMatch := flag.Float64("verify-min-match", 0, "fail -verify if the match rate (percent) is below this")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	var err error
	switch {
	case *lookup != "":
		err = runLookup(*cfgPath, *dbPath, *lookup)
	case *verify > 0:
		err = runVerify(*cfgPath, *dbPath, verifyOptions{
			Samples: *verify, Token: os.Getenv("IPINFO_TOKEN"), MinMatch: *minMatch, Workers: 4,
		})
	default:
		err = run(*cfgPath)
	}
	if err != nil {
		slog.Error("failed", "err", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	begin := time.Now()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if err := checkInputs(cfg); err != nil {
		return err
	}

	slog.Info("reading asn database", "path", cfg.Source.ASN)
	asn4, asn6, astats, err := asn.Read(cfg.Source.ASN)
	if err != nil {
		return err
	}
	slog.Info("asn database loaded", "type", astats.DatabaseType,
		"build", time.Unix(int64(astats.BuildEpoch), 0).UTC().Format(time.RFC3339),
		"networks", astats.Networks, "ipv4_ranges", astats.IPv4, "ipv6_ranges", astats.IPv6,
		"without_asn", astats.WithoutASN, "ipv4_mapped_skipped", astats.IPv4MappedSkipped)
	var china *ipSet
	if len(cfg.ASNFix.ChinaIP) > 0 {
		c4, c6, err := asn.ReadIPSet(cfg.ASNFix.ChinaIP, cfg.Filter.CountryCode)
		if err != nil {
			return fmt.Errorf("china ip: %w", err)
		}
		china = &ipSet{v4: c4, v6: c6}
		slog.Info("china ip set loaded", "files", len(cfg.ASNFix.ChinaIP), "ipv4_ranges", len(c4), "ipv6_ranges", len(c6))
	}
	if asn4, asn6, err = fixASN(cfg.ASNFix, china, asn4, asn6); err != nil {
		return err
	}
	tag4, tag6, err := loadCloudTags(cfg.Cloud)
	if err != nil {
		return err
	}

	mopt := merge.Options{PreserveCityWithoutASN: cfg.Build.PreserveCityWithoutASN}
	var out4, out6 []merge.Record
	if cfg.Filter.IncludeIPv4 {
		cities, err := parse(cfg, cfg.Source.IPv4, source.IPv4)
		if err != nil {
			return err
		}
		if len(cities) < cfg.Validate.MinIPv4Ranges {
			return fmt.Errorf("quality gate: %d IPv4 mainland ranges < min_ipv4_ranges %d", len(cities), cfg.Validate.MinIPv4Ranges)
		}
		if china != nil {
			logChinaCoverage("ipv4", cities, china.v4)
		}
		var st merge.Stats
		if out4, st, err = merge.MergeIPv4(cities, asn4, mopt); err != nil {
			return fmt.Errorf("merge ipv4: %w", err)
		}
		logMerge("ipv4", st)
		if len(tag4) > 0 {
			var n int
			out4, n = merge.Tag(out4, tag4)
			slog.Info("cloud tagged", "family", "ipv4", "pieces", n, "output_ranges", len(out4))
		}
	}
	if cfg.Filter.IncludeIPv6 {
		cities, err := parse(cfg, cfg.Source.IPv6, source.IPv6)
		if err != nil {
			return err
		}
		if len(cities) < cfg.Validate.MinIPv6Ranges {
			return fmt.Errorf("quality gate: %d IPv6 mainland ranges < min_ipv6_ranges %d", len(cities), cfg.Validate.MinIPv6Ranges)
		}
		if china != nil {
			logChinaCoverage("ipv6", cities, china.v6)
		}
		var st merge.Stats
		if out6, st, err = merge.MergeIPv6(cities, asn6, mopt); err != nil {
			return fmt.Errorf("merge ipv6: %w", err)
		}
		logMerge("ipv6", st)
		if len(tag6) > 0 {
			var n int
			out6, n = merge.Tag(out6, tag6)
			slog.Info("cloud tagged", "family", "ipv6", "pieces", n, "output_ranges", len(out6))
		}
	}
	if len(out4)+len(out6) == 0 {
		return errors.New("no mainland records to write; check filter.country_name against the source files")
	}

	b, err := writer.New(writer.Options{
		DatabaseType:            cfg.Build.DatabaseType,
		CountryCode:             cfg.Filter.CountryCode,
		CountryName:             cfg.Filter.CountryName,
		RecordSize:              cfg.Build.RecordSize,
		IncludeReservedNetworks: cfg.Build.IncludeReservedNetworks,
		SkipReserved:            cfg.Build.SkipInvalidRecords,
	})
	if err != nil {
		return err
	}
	slog.Info("building mmdb tree", "ipv4_ranges", len(out4), "ipv6_ranges", len(out6))
	if out4, err = b.Insert(out4); err != nil {
		return err
	}
	if out6, err = b.Insert(out6); err != nil {
		return err
	}
	ws := b.Stats()
	slog.Info("tree built", "inserted", ws.Inserted, "skipped_reserved", ws.SkippedReserved, "distinct_values", ws.DistinctValues)

	tmp, err := b.WriteTemp(cfg.Output.File)
	if err != nil {
		return err
	}
	rep, err := validate.File(tmp, out4, out6, validate.Options{
		DatabaseType: cfg.Build.DatabaseType,
		CountryCode:  cfg.Filter.CountryCode,
		SampleSize:   cfg.Validate.SampleSize,
		Checks:       cfg.Validate.Checks,
	})
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("validation failed, existing %s left untouched: %w", cfg.Output.File, err)
	}
	slog.Info("validation passed", "sampled_lookups", rep.SampledLookups, "checks", rep.Checks)

	if err := writer.Commit(tmp, cfg.Output.File); err != nil {
		os.Remove(tmp)
		return err
	}
	fi, err := os.Stat(cfg.Output.File)
	if err != nil {
		return err
	}
	slog.Info("done", "output", cfg.Output.File, "size_bytes", fi.Size(), "elapsed", time.Since(begin).Round(time.Millisecond))
	return nil
}

func checkInputs(cfg config.Config) error {
	paths := append([]string{cfg.Source.ASN}, cfg.ASNFix.Paths()...)
	for _, t := range cfg.Cloud {
		paths = append(paths, t.Files...)
	}
	if cfg.Filter.IncludeIPv4 {
		paths = append(paths, cfg.Source.IPv4)
	}
	if cfg.Filter.IncludeIPv6 {
		paths = append(paths, cfg.Source.IPv6)
	}
	var errs []error
	for _, p := range paths {
		fi, err := os.Stat(p)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("input %s: %w", p, err))
		case fi.Size() == 0:
			errs = append(errs, fmt.Errorf("input %s is empty", p))
		}
	}
	return errors.Join(errs...)
}

type ipSet struct{ v4, v6 []asn.Range }

// loadCloudTags merges the cloud lists into one sorted, non-overlapping range
// set per family whose Org holds the tag name; earlier entries win overlaps.
func loadCloudTags(tags []config.CloudTag) (v4, v6 []asn.Range, err error) {
	keepFirst := func(asn.Range) bool { return true }
	for _, t := range tags {
		c4, c6, err := asn.ReadCIDRs(t.Files)
		if err != nil {
			return nil, nil, fmt.Errorf("cloud %s: %w", t.Name, err)
		}
		for _, rs := range [][]asn.Range{c4, c6} {
			for i := range rs {
				rs[i].Org = t.Name
			}
		}
		v4, _ = asn.Overlay(v4, c4, keepFirst)
		v6, _ = asn.Overlay(v6, c6, keepFirst)
		slog.Info("cloud list loaded", "name", t.Name, "ipv4_ranges", len(c4), "ipv6_ranges", len(c6))
	}
	return v4, v6, nil
}

// logChinaCoverage cross-checks the city source's mainland ranges against the
// China IP set; addresses outside it may be mislabelled as mainland upstream.
func logChinaCoverage(fam string, cities []source.CityRecord, set []asn.Range) {
	_, st, err := merge.Merge(cities, set, merge.Options{})
	if err != nil {
		slog.Warn("china ip cross-check skipped", "family", fam, "err", err)
		return
	}
	outside := new(big.Int).Sub(st.TotalAddrs, st.MatchedAddrs)
	slog.Info("city source vs china ip set", "family", fam,
		"addr_coverage", fmt.Sprintf("%.2f%%", st.AddrCoverage()),
		"city_ranges_touching_set", fmt.Sprintf("%.2f%%", st.RangeCoverage()),
		"addrs_outside_set", outside.String())
}

// fixASN applies the asn_fix layers: BGP origin replaces source.asn inside the
// China IP set (everywhere if china is nil), the fallback MMDB fills gaps,
// provider lists replace foreign ASNs inside provider CIDRs, manual overrides
// always win.
func fixASN(fix config.ASNFix, china *ipSet, v4, v6 []asn.Range) ([]asn.Range, []asn.Range, error) {
	names := map[uint32]string{}
	if fix.Names != "" {
		var err error
		if names, err = asn.ReadNames(fix.Names); err != nil {
			return nil, nil, err
		}
		slog.Info("asn names loaded", "path", fix.Names, "count", len(names))
	}
	label := func(rs []asn.Range, num uint32, org string) {
		if org == "" {
			org = names[num]
		}
		for i := range rs {
			rs[i].ASN, rs[i].Org = num, org
		}
	}
	apply := func(layer string, o4, o6 []asn.Range, keep func(asn.Range) bool) {
		var n4, n6 int
		v4, n4 = asn.Overlay(v4, o4, keep)
		v6, n6 = asn.Overlay(v6, o6, keep)
		slog.Info("asn layer applied", "layer", layer, "ipv4_pieces", n4, "ipv6_pieces", n6)
	}

	if len(fix.BGP) > 0 {
		b4, b6, err := asn.ReadIP2ASN(fix.BGP)
		if err != nil {
			return nil, nil, fmt.Errorf("bgp: %w", err)
		}
		if china != nil {
			b4, b6 = asn.Intersect(b4, china.v4), asn.Intersect(b6, china.v6)
		}
		apply("bgp", b4, b6, nil)
	}
	if fix.FallbackMMDB != "" {
		f4, f6, _, err := asn.Read(fix.FallbackMMDB)
		if err != nil {
			return nil, nil, fmt.Errorf("fallback asn db: %w", err)
		}
		apply("fallback:"+fix.FallbackMMDB, f4, f6, func(asn.Range) bool { return true })
	}
	for _, p := range fix.Providers {
		p4, p6, err := asn.ReadCIDRs(p.Files)
		if err != nil {
			return nil, nil, fmt.Errorf("provider %s: %w", p.Name, err)
		}
		label(p4, p.ASNs[0], p.Org)
		label(p6, p.ASNs[0], p.Org)
		apply("provider:"+p.Name, p4, p6, func(r asn.Range) bool { return slices.Contains(p.ASNs, r.ASN) })
	}
	if fix.Overrides != "" {
		o4, o6, err := asn.ReadOverrides(fix.Overrides)
		if err != nil {
			return nil, nil, err
		}
		for _, rs := range [][]asn.Range{o4, o6} {
			for i := range rs {
				if rs[i].Org == "" {
					rs[i].Org = names[rs[i].ASN]
				}
			}
		}
		apply("overrides:"+fix.Overrides, o4, o6, nil)
	}
	return v4, v6, nil
}

func parse(cfg config.Config, path string, fam source.Family) ([]source.CityRecord, error) {
	slog.Info("parsing city source", "family", fam, "path", path)
	recs, st, err := source.ParseFile(path, source.Options{
		Family:           fam,
		CountryName:      cfg.Filter.CountryName,
		ExcludeProvinces: cfg.Filter.ExcludeProvinces,
		CityFromProvince: cfg.Filter.CityFromProvince,
		SkipInvalid:      cfg.Build.SkipInvalidRecords,
	})
	if err != nil {
		return nil, err
	}
	slog.Info("city source parsed", "family", fam, "lines", st.Lines, "mainland_ranges", st.Mainland,
		"other_country", st.OtherCountry, "excluded_province", st.ExcludedProvince,
		"ipv4_mapped_skipped", st.IPv4MappedSkipped, "invalid", st.Invalid,
		"duplicates", st.Duplicates, "overlaps", st.Overlaps, "city_filled_from_province", st.CityFilled)
	return recs, nil
}

func logMerge(fam string, st merge.Stats) {
	slog.Info("merged", "family", fam, "city_ranges", st.CityRanges, "output_ranges", st.OutputRanges,
		"asn_matched_ranges", st.MatchedRanges, "asn_unmatched_ranges", st.UnmatchedRanges,
		"city_ranges_with_asn", st.CitiesWithASN,
		"asn_coverage_by_range", fmt.Sprintf("%.2f%%", st.RangeCoverage()),
		"asn_coverage_by_addr", fmt.Sprintf("%.2f%%", st.AddrCoverage()),
		"total_addrs", st.TotalAddrs.String())
}

func runLookup(cfgPath, dbPath, ipStr string) error {
	if dbPath == "" {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return err
		}
		dbPath = cfg.Output.File
	}
	ip, err := netip.ParseAddr(ipStr)
	if err != nil {
		return err
	}
	db, err := maxminddb.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rec, found, err := validate.Lookup(db, ip)
	if err != nil {
		return err
	}
	if !found {
		fmt.Printf("%s: not found\n", ip)
		return nil
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(rec)
}
