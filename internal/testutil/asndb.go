// Package testutil builds small synthetic MMDB files for tests.
package testutil

import (
	"net"
	"os"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

type ASNNet struct {
	CIDR string
	ASN  uint32
	Org  string
}

func Net(cidr string, asn uint32, org string) ASNNet { return ASNNet{CIDR: cidr, ASN: asn, Org: org} }

// WriteASNDB writes a GeoLite2-ASN shaped IPv6 database to path.
func WriteASNDB(t testing.TB, path string, nets []ASNNet) {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{DatabaseType: "GeoLite2-ASN", IPVersion: 6, RecordSize: 24})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nets {
		_, ipn, err := net.ParseCIDR(n.CIDR)
		if err != nil {
			t.Fatal(err)
		}
		v := mmdbtype.Map{"autonomous_system_number": mmdbtype.Uint32(n.ASN)}
		if n.Org != "" {
			v["autonomous_system_organization"] = mmdbtype.String(n.Org)
		}
		if err := tree.Insert(ipn, v); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := tree.WriteTo(f); err != nil {
		t.Fatal(err)
	}
}
