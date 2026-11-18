package merge

import "china-geoip-mmdb/internal/asn"

// Tag splits records at tag boundaries and sets Cloud to the Org of the
// covering tag range. Both inputs must be sorted, non-overlapping and of the
// same address family. It returns the tagged records and the number of record
// pieces that received a tag.
func Tag(recs []Record, tags []asn.Range) ([]Record, int) {
	out := make([]Record, 0, len(recs))
	tagged := 0
	emit := func(r Record) {
		if k := len(out); k > 0 && out[k-1].End.Next() == r.Start && out[k-1].sameData(r) {
			out[k-1].End = r.End
			return
		}
		out = append(out, r)
	}
	j := 0
	for _, r := range recs {
		for j < len(tags) && tags[j].End.Less(r.Start) {
			j++
		}
		cur, done := r.Start, false
		for k := j; k < len(tags) && tags[k].Start.Compare(r.End) <= 0; k++ {
			t := tags[k]
			if t.Start.Compare(cur) > 0 {
				p := r
				p.Start, p.End = cur, t.Start.Prev()
				emit(p)
				cur = t.Start
			}
			p := r
			p.Start, p.End, p.Cloud = cur, minAddr(t.End, r.End), t.Org
			emit(p)
			tagged++
			if p.End == r.End {
				done = true
				break
			}
			cur = p.End.Next()
		}
		if !done {
			p := r
			p.Start = cur
			emit(p)
		}
	}
	return out, tagged
}
