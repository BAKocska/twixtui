package profile

import (
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"
)

// Match is a profile that a search query found.
type Match struct {
	Profile Profile
	// Score ranks the match; higher is better. Scores are only comparable
	// within one result set.
	Score int
	// Positions are the indexes, in runes, of the characters of Profile.Name
	// that the query matched, so the caller can highlight them.
	Positions []int
}

// Scores for queries rescued by edit distance rather than matched as a
// subsequence. Every rescue ranks below every subsequence match: a query that
// really is contained in a name is a better answer than one that needed a
// correction. fuzzy's own scores are bounded below by roughly the negated
// length of the name plus its leading-character penalty, so a floor a thousand
// points down cannot collide with them.
const (
	rescueScoreFloor = -1000
	rescueScoreStep  = 10
)

// Search ranks profiles against a query.
//
// An empty query returns every profile in List order, most recently used first,
// which is the browsable list a player scrolls when they cannot recall the name
// at all. A non-empty query is matched two ways:
//
//   - as a subsequence, scored by github.com/sahilm/fuzzy, which handles
//     partial names ("lin"), dropped letters ("balnt") and any capitalisation;
//   - failing that, by bounded edit distance against any part of the name,
//     which handles the typo classes a subsequence matcher structurally cannot
//     see — a transposition ("balitn"), a doubled letter ("ballint") or a wrong
//     letter ("balont") all put a query rune where no later occurrence exists.
//
// Every subsequence match outranks every rescued one, and rescues are ordered
// by how many corrections they needed. Ties break towards the most recently
// used profile.
func (s *Store) Search(query string) []Match {
	profiles := s.List()
	query = strings.TrimSpace(query)
	if query == "" {
		out := make([]Match, len(profiles))
		for i, p := range profiles {
			out[i] = Match{Profile: p}
		}
		return out
	}

	names := make([]string, len(profiles))
	for i, p := range profiles {
		names[i] = p.Name
	}
	found := make([]bool, len(profiles))
	out := make([]Match, 0, len(profiles))
	for _, m := range fuzzy.Find(query, names) {
		found[m.Index] = true
		out = append(out, Match{
			Profile:   profiles[m.Index],
			Score:     m.Score,
			Positions: runePositions(names[m.Index], m.MatchedIndexes),
		})
	}

	folded := []rune(foldKey(query))
	tolerance := editTolerance(len(folded))
	for i, p := range profiles {
		if found[i] {
			continue
		}
		name := []rune(foldKey(p.Name))
		distance := infixEditDistance(folded, name)
		if distance > tolerance {
			continue
		}
		// Within one distance bucket, prefer the shorter name: the same single
		// correction is stronger evidence against a six-character name than
		// against a thirteen-character one. The term is capped below the step
		// between buckets so it can never outrank a closer match.
		out = append(out, Match{
			Profile:   p,
			Score:     rescueScoreFloor - distance*rescueScoreStep - min(len(name), rescueScoreStep-1),
			Positions: greedyPositions(query, p.Name),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Profile.LastUsed.After(out[j].Profile.LastUsed)
	})
	return out
}

// editTolerance is how many corrections a query of n characters may need before
// the match stops being a plausible reading of the player's intent. It scales
// with length because one wrong letter in three characters could be any name,
// while two wrong letters in twelve is clearly a typo.
func editTolerance(n int) int {
	switch {
	case n < 4:
		return 0
	case n <= 6:
		return 1
	case n <= 10:
		return 2
	default:
		return 3
	}
}

// infixEditDistance is the fewest edits that turn the query into some part of
// name. Insertions, deletions and substitutions cost one, and so does
// transposing two adjacent characters; skipping name before and after the
// matched part is free.
//
// Two decisions are load-bearing. Counting a transposition as one edit is why
// this is not plain Levenshtein: swapping two letters is the commonest typing
// mistake and precisely the one a subsequence matcher cannot see. Matching an
// infix rather than the whole name is what lets a player type the part of the
// name they remember and still get away with a slip in it.
func infixEditDistance(query, name []rune) int {
	if len(query) == 0 {
		return 0
	}
	if len(name) == 0 {
		return len(query)
	}
	// Row i holds the distances between query[:i] and every prefix of name.
	// Row zero is all zeroes rather than 0,1,2,...: that is what makes skipping
	// a prefix of name free.
	prev2 := make([]int, len(name)+1)
	prev := make([]int, len(name)+1)
	cur := make([]int, len(name)+1)
	for i := 1; i <= len(query); i++ {
		cur[0] = i
		for j := 1; j <= len(name); j++ {
			cost := 1
			if query[i-1] == name[j-1] {
				cost = 0
			}
			d := min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && query[i-1] == name[j-2] && query[i-2] == name[j-1] {
				d = min(d, prev2[j-2]+1)
			}
			cur[j] = d
		}
		prev2, prev, cur = prev, cur, prev2
	}
	// Skipping the rest of name is free too, so the answer is the best cell in
	// the last row.
	best := prev[0]
	for _, d := range prev[1:] {
		best = min(best, d)
	}
	return best
}

// runePositions converts the byte offsets fuzzy reports into rune indexes,
// which is what a terminal renderer counts in.
func runePositions(s string, byteOffsets []int) []int {
	if len(byteOffsets) == 0 {
		return nil
	}
	out := make([]int, 0, len(byteOffsets))
	next := 0
	index := 0
	for offset := range s {
		if next < len(byteOffsets) && offset == byteOffsets[next] {
			out = append(out, index)
			next++
		}
		index++
	}
	return out
}

// greedyPositions reports which runes of name a rescued query matched, in
// order. A rescued query contains a mistake, so some of its runes match
// nothing; the result is the part of the name the caller can honestly
// highlight.
func greedyPositions(query, name string) []int {
	q := []rune(strings.ToLower(query))
	var out []int
	qi := 0
	for i, r := range []rune(strings.ToLower(name)) {
		if qi >= len(q) {
			break
		}
		if r == q[qi] {
			out = append(out, i)
			qi++
		}
	}
	return out
}
