package netemu

// indexMap maps a replica ID to its 0-based index in Config.Replicas.
type indexMap map[string]int

func buildIndexMap(replicas []Replica) indexMap {
	m := make(indexMap, len(replicas))
	for i, r := range replicas {
		m[r.ID] = i
	}
	return m
}

// flowMark returns a unique non-zero 32-bit integer for the ordered pair
// (fromIdx, toIdx) within a set of n replicas.
//
// The formula is: fromIdx*n + toIdx + 1
//
// Properties:
//   - Unique for all ordered pairs where fromIdx != toIdx.
//   - Minimum value is 1 (avoids mark 0, which the kernel treats as "unset").
//   - Maximum value is n^2 - n + n - 1 + 1 = n^2 (for n=100: 10000), well
//     within iptables' 32-bit mark space and tc's 16-bit classid minor field.
func flowMark(fromIdx, toIdx, n int) int {
	return fromIdx*n + toIdx + 1
}

// defaultClassMinor returns the tc classid minor number used for the
// unconstrained catch-all HTB class. It is chosen to be larger than any
// flowMark value so it never collides.
func defaultClassMinor(n int) int {
	return n*n + 1
}
