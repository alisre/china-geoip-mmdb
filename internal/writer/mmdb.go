// Package writer builds the China-City-ASN MMDB.
package writer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"china-geoip-mmdb/internal/merge"
)

type Options struct {
	DatabaseType            string
	CountryCode             string
	CountryName             string
	RecordSize              int
	IncludeReservedNetworks bool
	// SkipReserved drops records rejected as reserved networks instead of
	// failing the build.
	SkipReserved bool
	Description  string
}

type Stats struct {
	Inserted        int
	SkippedReserved int
	DistinctValues  int
}

type valueKey struct {
	province, city, district, isp string
	hasASN                        bool
	asn                           uint32
	org                           string
	cloud                         string
}

// Builder accumulates records into an IPv6 (dual-stack) MMDB tree.
type Builder struct {
	opt    Options
	tree   *mmdbwriter.Tree
	keys   *identityKeys
	values map[valueKey]mmdbtype.Map
	stats  Stats
}

func New(opt Options) (*Builder, error) {
	keys := &identityKeys{byPtr: map[uintptr][]byte{}}
	desc := opt.Description
	if desc == "" {
		desc = "Mainland China city + ASN database (custom, not an official GeoIP2 database)"
	}
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType:            opt.DatabaseType,
		Description:             map[string]string{"en": desc},
		Languages:               []string{"zh-CN"},
		IPVersion:               6,
		RecordSize:              opt.RecordSize,
		IncludeReservedNetworks: opt.IncludeReservedNetworks,
		KeyGenerator:            keys,
	})
	if err != nil {
		return nil, err
	}
	return &Builder{opt: opt, tree: tree, keys: keys, values: map[valueKey]mmdbtype.Map{}}, nil
}

// Insert adds records and returns the ones actually written.
func (b *Builder) Insert(recs []merge.Record) ([]merge.Record, error) {
	kept := recs[:0:0]
	for _, r := range recs {
		err := b.tree.InsertRange(net.IP(r.Start.AsSlice()), net.IP(r.End.AsSlice()), b.value(r))
		var reserved *mmdbwriter.ReservedNetworkError
		switch {
		case err == nil:
			b.stats.Inserted++
			kept = append(kept, r)
		case errors.As(err, &reserved) && b.opt.SkipReserved:
			b.stats.SkippedReserved++
		default:
			return nil, fmt.Errorf("insert %s-%s: %w (set build.include_reserved_networks or build.skip_invalid_records if intended)", r.Start, r.End, err)
		}
	}
	return kept, nil
}

func (b *Builder) Stats() Stats {
	s := b.stats
	s.DistinctValues = len(b.values)
	return s
}

// WriteTemp serializes the tree to a temporary file next to finalPath and
// returns its path. Call Commit after the file has been validated.
func (b *Builder) WriteTemp(finalPath string) (string, error) {
	dir := filepath.Dir(finalPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(finalPath)+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if _, err := b.tree.WriteTo(f); err != nil {
		return fail(fmt.Errorf("write mmdb: %w", err))
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Chmod(0o644); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// Commit atomically replaces finalPath with the validated temp file.
func Commit(tmp, finalPath string) error {
	return os.Rename(tmp, finalPath)
}

func (b *Builder) value(r merge.Record) mmdbtype.Map {
	k := valueKey{r.Province, r.City, r.District, r.ISP, r.HasASN, r.ASN, r.ASNOrg, r.Cloud}
	if v, ok := b.values[k]; ok {
		return v
	}
	m := mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String(b.opt.CountryCode),
			"names":    mmdbtype.Map{"zh-CN": mmdbtype.String(b.opt.CountryName)},
		},
	}
	if r.Province != "" {
		m["subdivisions"] = mmdbtype.Slice{
			mmdbtype.Map{"names": mmdbtype.Map{"zh-CN": mmdbtype.String(r.Province)}},
		}
	}
	if r.City != "" {
		m["city"] = mmdbtype.Map{"names": mmdbtype.Map{"zh-CN": mmdbtype.String(r.City)}}
	}
	if r.District != "" {
		m["district"] = mmdbtype.String(r.District)
	}
	if r.ISP != "" {
		m["isp"] = mmdbtype.String(r.ISP)
	}
	if r.Cloud != "" {
		m["cloud"] = mmdbtype.String(r.Cloud)
	}
	if r.HasASN {
		m["autonomous_system_number"] = mmdbtype.Uint32(r.ASN)
		if r.ASNOrg != "" {
			m["autonomous_system_organization"] = mmdbtype.String(r.ASNOrg)
		}
	}
	b.values[k] = m
	sum := sha256.Sum256(fmt.Appendf(nil, "%q|%q|%q|%q|%t|%d|%q|%q", k.province, k.city, k.district, k.isp, k.hasASN, k.asn, k.org, k.cloud))
	b.keys.byPtr[reflect.ValueOf(m).Pointer()] = sum[:]
	return m
}

// identityKeys returns a precomputed key for the cached value maps, avoiding a
// full serialize+hash on every insert. Unknown values fall back to hashing
// their formatted representation (fmt sorts map keys, so it is stable).
type identityKeys struct {
	byPtr map[uintptr][]byte
}

func (k *identityKeys) Key(v mmdbtype.DataType) ([]byte, error) {
	if m, ok := v.(mmdbtype.Map); ok {
		if key, ok := k.byPtr[reflect.ValueOf(m).Pointer()]; ok {
			return key, nil
		}
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "fallback:%#v", v))
	return sum[:], nil
}
