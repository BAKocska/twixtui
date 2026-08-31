package game

// unionFind tracks which pegs form a connected chain, with the four board edges
// as extra virtual members. Union by size without path compression keeps every
// merge individually reversible, which is what lets search undo a move in
// constant time instead of recomputing connectivity.
type unionFind struct {
	parent []int32
	size   []int32
	// merges records the child of each union, newest last, so that rollback can
	// detach them in reverse.
	merges []int32
}

func (u *unionFind) reset(n int) {
	if cap(u.parent) < n {
		u.parent = make([]int32, n)
		u.size = make([]int32, n)
	}
	u.parent = u.parent[:n]
	u.size = u.size[:n]
	for i := range n {
		u.parent[i] = int32(i)
		u.size[i] = 1
	}
	u.merges = u.merges[:0]
}

func (u *unionFind) copyFrom(o *unionFind) {
	u.parent = append(u.parent[:0], o.parent...)
	u.size = append(u.size[:0], o.size...)
	u.merges = append(u.merges[:0], o.merges...)
}

func (u *unionFind) find(x int) int {
	for int32(x) != u.parent[x] {
		x = int(u.parent[x])
	}
	return x
}

// union merges the groups of a and b, returning whether they were distinct.
func (u *unionFind) union(a, b int) bool {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return false
	}
	if u.size[ra] < u.size[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = int32(ra)
	u.size[ra] += u.size[rb]
	u.merges = append(u.merges, int32(rb))
	return true
}

func (u *unionFind) connected(a, b int) bool { return u.find(a) == u.find(b) }

// mark returns a token for the current state, for use with rollback.
func (u *unionFind) mark() int { return len(u.merges) }

// rollback undoes every union performed since the given mark.
func (u *unionFind) rollback(mark int) {
	for i := len(u.merges) - 1; i >= mark; i-- {
		child := u.merges[i]
		root := u.parent[child]
		u.size[root] -= u.size[child]
		u.parent[child] = child
	}
	u.merges = u.merges[:mark]
}
