// A faithful port of Python difflib.SequenceMatcher (autojunk on, no isjunk),
// used for the preferences edit ledger. The ledger keeps, for a unified diff
// with n=0 context, exactly the per-hunk -removed/+added lines in order (the
// first two unified-diff lines are file headers and each hunk's @@ line is a
// marker; both are dropped by the legacy queue.py, so they are never built).
package dashboard

// diffOp is one get_opcodes() entry.
type diffOp struct {
	tag            string // "replace", "delete", "insert", "equal"
	i1, i2, j1, j2 int
}

type sequenceMatcher struct {
	a, b []string
	b2j  map[string][]int
}

func newSequenceMatcher(a, b []string) *sequenceMatcher {
	m := &sequenceMatcher{a: a, b: b}
	m.b2j = map[string][]int{}
	for j, s := range b {
		m.b2j[s] = append(m.b2j[s], j)
	}
	// autojunk: with len(b) >= 200, elements seen more than 1+len(b)//100
	// times are "popular" and dropped from b2j.
	if n := len(b); n >= 200 {
		ntest := n/100 + 1
		for s, idx := range m.b2j {
			if len(idx) > ntest {
				delete(m.b2j, s)
			}
		}
	}
	return m
}

// findLongestMatch is SequenceMatcher.find_longest_match over
// a[alo:ahi] x b[blo:bhi]. No junk (bjunk empty), so only the non-junk
// extension pass applies.
func (m *sequenceMatcher) findLongestMatch(alo, ahi, blo, bhi int) (besti, bestj, bestsize int) {
	besti, bestj, bestsize = alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}

// matchingBlocks is get_matching_blocks (iterative queue, adjacent-merge,
// terminal sentinel).
func (m *sequenceMatcher) matchingBlocks() [][3]int {
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(m.a), 0, len(m.b)}}
	var raw [][3]int
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := m.findLongestMatch(s.alo, s.ahi, s.blo, s.bhi)
		if k > 0 {
			raw = append(raw, [3]int{i, j, k})
			if s.alo < i && s.blo < j {
				queue = append(queue, span{s.alo, i, s.blo, j})
			}
			if i+k < s.ahi && j+k < s.bhi {
				queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
			}
		}
	}
	sortBlocks(raw)
	var out [][3]int
	i1, j1, k1 := 0, 0, 0
	for _, blk := range raw {
		if i1+k1 == blk[0] && j1+k1 == blk[1] {
			k1 += blk[2]
		} else {
			if k1 > 0 {
				out = append(out, [3]int{i1, j1, k1})
			}
			i1, j1, k1 = blk[0], blk[1], blk[2]
		}
	}
	if k1 > 0 {
		out = append(out, [3]int{i1, j1, k1})
	}
	return append(out, [3]int{len(m.a), len(m.b), 0})
}

func sortBlocks(blocks [][3]int) {
	// insertion sort by (i, j, k) — block lists are small
	for x := 1; x < len(blocks); x++ {
		for y := x; y > 0 && less3(blocks[y], blocks[y-1]); y-- {
			blocks[y], blocks[y-1] = blocks[y-1], blocks[y]
		}
	}
}

func less3(a, b [3]int) bool {
	if a[0] != b[0] {
		return a[0] < b[0]
	}
	if a[1] != b[1] {
		return a[1] < b[1]
	}
	return a[2] < b[2]
}

// opcodes is get_opcodes().
func (m *sequenceMatcher) opcodes() []diffOp {
	var out []diffOp
	i, j := 0, 0
	for _, blk := range m.matchingBlocks() {
		ai, bj, size := blk[0], blk[1], blk[2]
		tag := ""
		switch {
		case i < ai && j < bj:
			tag = "replace"
		case i < ai:
			tag = "delete"
		case j < bj:
			tag = "insert"
		}
		if tag != "" {
			out = append(out, diffOp{tag, i, ai, j, bj})
		}
		i, j = ai+size, bj+size
		if size > 0 {
			out = append(out, diffOp{"equal", ai, i, bj, j})
		}
	}
	return out
}

// editLedgerDiff returns the preference-history entry body: what remains of
// difflib.unified_diff(old, new, lineterm="", n=0) after dropping the two
// file-header lines and the @@ hunk markers — per hunk, the removed lines
// (-) then the added lines (+), in opcode order. Empty when nothing changed.
func editLedgerDiff(old, new []string) []string {
	m := newSequenceMatcher(old, new)
	var body []string
	for _, op := range m.opcodes() {
		if op.tag == "equal" {
			continue
		}
		for _, l := range old[op.i1:op.i2] {
			body = append(body, "-"+l)
		}
		for _, l := range new[op.j1:op.j2] {
			body = append(body, "+"+l)
		}
	}
	return body
}
