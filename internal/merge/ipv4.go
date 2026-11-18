package merge

import (
	"fmt"

	"china-geoip-mmdb/internal/asn"
	"china-geoip-mmdb/internal/source"
)

// MergeIPv4 is Merge restricted to plain IPv4 addresses.
func MergeIPv4(cities []source.CityRecord, asns []asn.Range, opt Options) ([]Record, Stats, error) {
	for _, c := range cities {
		if !c.Start.Is4() || !c.End.Is4() {
			return nil, Stats{}, fmt.Errorf("non-IPv4 city range %s-%s", c.Start, c.End)
		}
	}
	for _, a := range asns {
		if !a.Start.Is4() || !a.End.Is4() {
			return nil, Stats{}, fmt.Errorf("non-IPv4 asn range %s-%s", a.Start, a.End)
		}
	}
	return Merge(cities, asns, opt)
}
