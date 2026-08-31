package profile

import (
	"fmt"
	"log"
	"os"
)

// exampleDir creates a throwaway configuration directory. Real callers pass an
// empty string to Open and get the user's own directory.
func exampleDir() (string, func()) {
	dir, err := os.MkdirTemp("", "twixtui-example")
	if err != nil {
		log.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

func ExampleStore_Search() {
	dir, cleanup := exampleDir()
	defer cleanup()

	store, err := Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, name := range []string{"Balint", "Bernadett", "Bella Ackland"} {
		if _, err := store.Create(name); err != nil {
			log.Fatal(err)
		}
	}

	// The player transposed the last two letters of their own name.
	for _, m := range store.Search("balitn") {
		fmt.Println(m.Profile.Name, m.Positions)
	}
	// Output:
	// Balint [0 1 2 3 5]
}

func ExampleStore_List() {
	dir, cleanup := exampleDir()
	defer cleanup()

	store, err := Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, name := range []string{"Balint", "Bernadett"} {
		if _, err := store.Create(name); err != nil {
			log.Fatal(err)
		}
	}
	if err := store.Touch("balint"); err != nil {
		log.Fatal(err)
	}

	// Most recently used first, which is the order the launch prompt offers.
	for _, p := range store.List() {
		fmt.Println(p.Name)
	}
	// Output:
	// Balint
	// Bernadett
}
