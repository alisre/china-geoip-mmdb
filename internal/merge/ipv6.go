package merge

import (
	"fmt"

	"china-geoip-mmdb/internal/asn"
	"china-geoip-mmdb/internal/source"
)

// MergeIPv6 is Merge restricted to native IPv6 addresses (no IPv4-mapped).
func MergeIPv6(cities []source.CityRecord, asns []asn.Range, opt Options) ([]Record, Stats, error) {
	for _, c := range cities {
		if !c.Start.Is6() || !c.End.Is6() || c.Start.Is4In6() || c.End.Is4In6() {
			return nil, Stats{}, fmt.Errorf("non-IPv6 city range %s-%s", c.Start, c.End)
		}
	}
	for _, a := range asns {
		if !a.Start.Is6() || !a.End.Is6() || a.Start.Is4In6() || a.End.Is4In6() {
			return nil, Stats{}, fmt.Errorf("non-IPv6 asn range %s-%s", a.Start, a.End)
		}
	}
	return Merge(cities, asns, opt)
}
