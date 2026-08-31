package ui

import (
	"strings"
	"testing"
)

func TestKeymapNoDuplicateKeysPerContext(t *testing.T) {
	km := DefaultKeymap()
	for _, ctx := range []Context{CtxBoard, CtxLink} {
		seen := map[string]Action{}
		for _, b := range km {
			if b.Contexts&ctx == 0 {
				continue
			}
			for _, k := range b.Keys {
				if prev, dup := seen[k]; dup {
					t.Errorf("context %d: key %q bound to both action %d and %d", ctx, k, prev, b.Action)
				}
				seen[k] = b.Action
			}
		}
	}
}

func TestKeymapLookupResolvesEveryBoundKey(t *testing.T) {
	km := DefaultKeymap()
	for _, b := range km {
		for _, ctx := range []Context{CtxBoard, CtxLink} {
			if b.Contexts&ctx == 0 {
				continue
			}
			for _, k := range b.Keys {
				got, ok := km.Lookup(ctx, k)
				if !ok || got.Action != b.Action {
					t.Errorf("context %d key %q: lookup returned action %d ok=%v, want %d", ctx, k, got.Action, ok, b.Action)
				}
			}
		}
	}
	if _, ok := km.Lookup(CtxBoard, "5"); ok {
		t.Error("digit keys must not act outside link mode")
	}
	if _, ok := km.Lookup(CtxLink, "space"); ok {
		t.Error("space must not place pegs while in link mode")
	}
}

func TestKeymapUsesOnlyMultiplexerSafeKeys(t *testing.T) {
	// Inside tmux-like multiplexers only unmodified keys, uppercase letters
	// and plain ctrl+letter are dependable. Anything else in a binding is a
	// design regression.
	for _, b := range DefaultKeymap() {
		for _, k := range b.Keys {
			if strings.Contains(k, "+") && !strings.HasPrefix(k, "ctrl+") {
				t.Errorf("binding %q uses modifier combination %q", b.Help, k)
			}
			if strings.Contains(k, "shift") || strings.Contains(k, "alt") {
				t.Errorf("binding %q uses unreliable modifier %q", b.Help, k)
			}
		}
	}
}

func TestHelpTableRendersEveryBinding(t *testing.T) {
	km := DefaultKeymap()
	table := km.HelpTable(CtxBoard | CtxLink)
	rows := strings.Split(strings.TrimRight(table, "\n"), "\n")
	if len(rows) != len(km) {
		t.Fatalf("help table has %d rows for %d bindings", len(rows), len(km))
	}
	for i, b := range km {
		if !strings.Contains(rows[i], b.Help) {
			t.Errorf("row %d %q lacks help text %q", i, rows[i], b.Help)
		}
	}
}
