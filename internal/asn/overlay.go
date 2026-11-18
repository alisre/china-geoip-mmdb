package asn

import "net/netip"

// Overlay lays over on top of base and returns the combined ranges plus the
// number of pieces taken from over. Both inputs must be sorted,
// non-overlapping and of the same address family.
//
// Addresses covered only by over take over's ASN. Where both cover an address,
// the base piece is kept if keep reports true for it, otherwise over wins;
// a nil keep means over always wins. keep returning true for everything turns
// Overlay into a pure gap filler.
func Overlay(base, over []Range, keep func(Range) bool) ([]Range, int) {
	out := make([]Range, 0, len(base)+len(over))
	taken := 0
	emitOver := func(o Range, start, end netip.Addr) {
		o.Start, o.End = start, end
		out = append(out, o)
		taken++
	}

	bi := 0
	var cur Range
	has := false
	next := func() {
		has = bi < len(base)
		if has {
			cur = base[bi]
			bi++
		}
	}
	next()

	for _, o := range over {
		for has && cur.End.Less(o.Start) {
			out = append(out, cur)
			next()
		}
		if has && cur.Start.Less(o.Start) {
			head := cur
			head.End = o.Start.Prev()
			out = append(out, head)
			cur.Start = o.Start
		}
		pos := o.Start
		for {
			if !has || cur.Start.Compare(o.End) > 0 {
				emitOver(o, pos, o.End)
				break
			}
			if cur.Start.Compare(pos) > 0 {
				emitOver(o, pos, cur.Start.Prev())
				pos = cur.Start
			}
			end := cur.End
			if o.End.Less(end) {
				end = o.End
			}
			piece := cur
			piece.Start, piece.End = pos, end
			if keep != nil && keep(piece) {
				out = append(out, piece)
			} else {
				emitOver(o, pos, end)
			}
			if end == cur.End {
				next()
			} else {
				cur.Start = end.Next()
			}
			if end == o.End {
				break
			}
			pos = end.Next()
		}
	}
	for has {
		out = append(out, cur)
		next()
	}
	return coalesce(out), taken
}

// Intersect clips rs to the addresses covered by set. Both inputs must be
// sorted, non-overlapping and of the same address family.
func Intersect(rs, set []Range) []Range {
	var out []Range
	j := 0
	for _, r := range rs {
		for j < len(set) && set[j].End.Less(r.Start) {
			j++
		}
		for k := j; k < len(set) && set[k].Start.Compare(r.End) <= 0; k++ {
			p := r
			if p.Start.Less(set[k].Start) {
				p.Start = set[k].Start
			}
			if set[k].End.Less(p.End) {
				p.End = set[k].End
			}
			out = append(out, p)
		}
	}
	return out
}
