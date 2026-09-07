package profile

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// searchStore holds the profile set the search tests query. The names are
// deliberately confusable with each other: two beginning "B", two containing
// "an", one differing from a query only by an accent.
func searchStore(t *testing.T) *Store {
	t.Helper()
	s := openStore(t, t.TempDir())
	for _, name := range []string{"Balint", "Bernadett", "Zsófia", "Jane Smith", "Ana-Maria", "Bella Ackland"} {
		if _, err := s.Create(name); err != nil {
			t.Fatalf("Create(%q): %v", name, err)
		}
	}
	return s
}

func TestSearchEmptyQueryBrowsesMostRecentlyUsedFirst(t *testing.T) {
	s := searchStore(t)
	got := s.Search("   ")
	if len(got) != 6 {
		t.Fatalf("Search(\"\") returned %d matches, want every profile", len(got))
	}
	// Created in order, one second apart, so the last created is first.
	want := []string{"Bella Ackland", "Ana-Maria", "Jane Smith", "Zsófia", "Bernadett", "Balint"}
	for i, name := range want {
		if got[i].Profile.Name != name {
			t.Fatalf("Search(\"\")[%d] = %q, want %q", i, got[i].Profile.Name, name)
		}
	}
}

func TestSearchFindsMistypedNames(t *testing.T) {
	s := searchStore(t)
	cases := []struct {
		query, want, why string
	}{
		{"balitn", "Balint", "transposed last two letters"},
		{"balnt", "Balint", "dropped a letter"},
		{"BALINT", "Balint", "wrong case"},
		{"lin", "Balint", "only the middle of the name"},
		{"ballint", "Balint", "doubled a letter"},
		{"brenadett", "Bernadett", "transposed letters mid-name"},
		{"bernadet", "Bernadett", "dropped a trailing letter"},
		{"jane smth", "Jane Smith", "dropped a letter in a two-word name"},
		{"anamaria", "Ana-Maria", "omitted the punctuation"},
		{"zsofia", "Zsófia", "omitted the accent"},
		{"acklnad", "Bella Ackland", "transposition inside the surname"},
	}
	for _, c := range cases {
		got := s.Search(c.query)
		if len(got) == 0 {
			t.Errorf("Search(%q) found nothing, want %q (%s)", c.query, c.want, c.why)
			continue
		}
		if got[0].Profile.Name != c.want {
			names := make([]string, len(got))
			for i, m := range got {
				names[i] = m.Profile.Name
			}
			t.Errorf("Search(%q) ranked %v, want %q first (%s)", c.query, names, c.want, c.why)
		}
	}
}

func TestSearchRanksExactPrefixAboveScatteredMatch(t *testing.T) {
	s := searchStore(t)
	got := s.Search("bal")
	if len(got) < 2 {
		t.Fatalf("Search(\"bal\") returned %d matches, want the prefix match and the scattered one", len(got))
	}
	if got[0].Profile.Name != "Balint" {
		t.Fatalf("Search(\"bal\")[0] = %q, want the prefix match Balint", got[0].Profile.Name)
	}
	scattered := slices.IndexFunc(got, func(m Match) bool { return m.Profile.Name == "Bella Ackland" })
	if scattered < 0 {
		t.Fatal("Search(\"bal\") did not find the scattered match Bella Ackland at all")
	}
	if got[0].Score <= got[scattered].Score {
		t.Fatalf("prefix match scored %d, scattered match scored %d: want the prefix to score higher",
			got[0].Score, got[scattered].Score)
	}
}

func TestSearchRanksSubsequenceMatchesAboveTypoRescues(t *testing.T) {
	s := searchStore(t)
	// "bernadet" is contained in Bernadett as a subsequence; the same query is
	// only two edits from "bella ackland" would it ever be rescued, so the
	// contained match must come first.
	got := s.Search("bernadet")
	if len(got) == 0 {
		t.Fatal("Search(\"bernadet\") found nothing")
	}
	if got[0].Profile.Name != "Bernadett" {
		t.Fatalf("Search(\"bernadet\")[0] = %q, want Bernadett", got[0].Profile.Name)
	}
	if got[0].Score <= rescueScoreFloor {
		t.Fatalf("contained match scored %d, want it above the rescue floor %d", got[0].Score, rescueScoreFloor)
	}
	for _, m := range got[1:] {
		if m.Score > rescueScoreFloor {
			t.Fatalf("%q scored %d as a subsequence match, want only Bernadett to contain the query",
				m.Profile.Name, m.Score)
		}
	}
}

func TestSearchPositionsAreRuneIndexes(t *testing.T) {
	s := searchStore(t)
	got := s.Search("fia")
	if len(got) == 0 || got[0].Profile.Name != "Zsófia" {
		t.Fatalf("Search(\"fia\") = %+v, want Zsófia first", got)
	}
	// "Zsófia" is Z s ó f i a: the query matches runes 3, 4 and 5, which sit at
	// byte offsets 4, 5 and 6 because of the two-byte ó.
	want := []int{3, 4, 5}
	if !reflect.DeepEqual(got[0].Positions, want) {
		t.Fatalf("Positions = %v, want rune indexes %v", got[0].Positions, want)
	}

	// A rescued match highlights the part of the name it did line up with.
	got = s.Search("zsofia")
	if len(got) == 0 || got[0].Profile.Name != "Zsófia" {
		t.Fatalf("Search(\"zsofia\") = %+v, want Zsófia first", got)
	}
	if len(got[0].Positions) == 0 {
		t.Fatal("rescued match reported no positions to highlight")
	}
	for _, p := range got[0].Positions {
		if p < 0 || p > 5 {
			t.Fatalf("rescued position %d is outside the six runes of %q", p, got[0].Profile.Name)
		}
	}
}

func TestSearchRejectsUnrelatedQuery(t *testing.T) {
	s := searchStore(t)
	for _, query := range []string{"qqqq", "xylophone", "0000"} {
		if got := s.Search(query); len(got) != 0 {
			t.Fatalf("Search(%q) = %+v, want no matches", query, got)
		}
	}
}

func TestSearchOnEmptyStore(t *testing.T) {
	s := openStore(t, t.TempDir())
	if got := s.Search(""); len(got) != 0 {
		t.Fatalf("Search(\"\") on an empty store = %+v, want nothing", got)
	}
	if got := s.Search("balint"); len(got) != 0 {
		t.Fatalf("Search on an empty store = %+v, want nothing", got)
	}
}

func TestInfixEditDistance(t *testing.T) {
	cases := []struct {
		query, name string
		want        int
	}{
		{"", "", 0},
		{"balint", "balint", 0},
		{"balitn", "balint", 1},  // transposition counts as one edit
		{"balnt", "balint", 1},   // deletion
		{"ballint", "balint", 1}, // insertion
		{"balont", "balint", 1},  // substitution
		{"bal", "balint", 0},     // a literal infix costs nothing
		{"lin", "balint", 0},
		{"acklnad", "bella ackland", 1}, // transposition inside an infix
		{"zsofia", "zsófia", 1},
		{"balint", "", 6},
		{"qqqq", "balint", 4},
	}
	for _, c := range cases {
		if got := infixEditDistance([]rune(c.query), []rune(c.name)); got != c.want {
			t.Errorf("infixEditDistance(%q, %q) = %d, want %d", c.query, c.name, got, c.want)
		}
	}
}

// TestSearchProfilesKeepsTheOrderItWasGiven covers the promise the shared
// matcher makes to a caller that is not a store. The command line matches
// names taken from the recorded results, where a name may belong to a player
// with no profile any more and so no last-used time at all; the order those
// arrive in is the caller's decision, and equally good matches have to come
// back in it. A store passes List order, which is why its own results are
// most recently used first.
func TestSearchProfilesKeepsTheOrderItWasGiven(t *testing.T) {
	older := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	// Deliberately not in recency order: the caller's order is the one that
	// must survive.
	given := []Profile{{Name: "Ida", LastUsed: older}, {Name: "Ada", LastUsed: newer}}

	browse := SearchProfiles(given, "  ")
	if len(browse) != 2 || browse[0].Profile.Name != "Ida" || browse[1].Profile.Name != "Ada" {
		t.Errorf("browsing reordered the profiles: %+v", browse)
	}

	got := SearchProfiles(given, "da")
	if len(got) != 2 {
		t.Fatalf("SearchProfiles(%q) matched %d profiles, want both: %+v", "da", len(got), got)
	}
	if got[0].Score != got[1].Score {
		t.Fatalf("the fixture no longer scores these two alike, so it cannot test a tie: %+v", got)
	}
	if got[0].Profile.Name != "Ida" || got[1].Profile.Name != "Ada" {
		t.Errorf("equally good matches came back as %s then %s, want the order they were given",
			got[0].Profile.Name, got[1].Profile.Name)
	}
}

// TestStoreSearchAndSearchProfilesAgree covers the reason the matcher was
// extracted: the interface and the command line both rank names, and a second
// notion of what counts as close enough would have them disagree about the same
// typed name.
func TestStoreSearchAndSearchProfilesAgree(t *testing.T) {
	s := searchStore(t)
	profiles := s.List()
	for _, query := range []string{"", "bal", "balitn", "an", "zsofia", "smith", "qqqq"} {
		method := s.Search(query)
		shared := SearchProfiles(profiles, query)
		if !reflect.DeepEqual(method, shared) {
			t.Errorf("Search(%q) = %+v, but the shared matcher says %+v", query, method, shared)
		}
	}
}
