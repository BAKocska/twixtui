package netplay

import (
	"net"
	"strings"
	"testing"
)

// TestBindAddrFillsInThePortAndKeepsTheInterface covers what a host may ask to
// listen on. The interface is the part that decides who can reach the game, so
// it has to survive being turned into an address to bind.
func TestBindAddrFillsInThePortAndKeepsTheInterface(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ":" + DefaultPort},
		{":4271", ":4271"},
		{"127.0.0.1", "127.0.0.1:" + DefaultPort},
		{"127.0.0.1:0", "127.0.0.1:0"},
		{"::1", "[::1]:" + DefaultPort},
		{"[::1]:4271", "[::1]:4271"},
		{"0.0.0.0:4271", "0.0.0.0:4271"},
	} {
		got, err := BindAddr(c.in)
		if err != nil {
			t.Errorf("BindAddr(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BindAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBindAddrRefusesWhatIsNotAnInterface: a name that resolves somewhere is
// not a thing to bind, and a host who typed one has to be told rather than
// find out from whatever does or does not connect.
func TestBindAddrRefusesWhatIsNotAnInterface(t *testing.T) {
	for _, in := range []string{"localhost", "example.com:4271", "not an address", "127.0.0.1:70000", "127.0.0.1:http"} {
		if got, err := BindAddr(in); err == nil {
			t.Errorf("BindAddr(%q) accepted it as %q", in, got)
		}
	}
}

// TestBindAddrKeepsALinkLocalAddressesZone: a link-local address carries the
// interface it belongs to as "%en0" and is not usable without it — fe80::1
// alone does not say which of this machine's links it is on. The zone used to
// be read as part of the address, which left the whole thing looking like no
// address at all, so the one form a link-local bind works in was the one form
// refused.
//
// Nothing is bound here. Which interfaces the machine running this holds is
// not something a test may assume, and what this covers is which addresses
// are accepted rather than what a socket does with one; loopback is the only
// address any test in this package binds.
func TestBindAddrKeepsALinkLocalAddressesZone(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"fe80::1%en0", "[fe80::1%en0]:" + DefaultPort},
		{"[fe80::1%en0]:4271", "[fe80::1%en0]:4271"},
		{"[fe80::1%en0]:0", "[fe80::1%en0]:0"},
	} {
		got, err := BindAddr(c.in)
		if err != nil {
			t.Errorf("BindAddr(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("BindAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// A zone says which interface, not which host: a name that would have to
	// be resolved is still not a thing to bind, zone or no zone.
	for _, in := range []string{"localhost%en0", "example.com%en0", "%en0"} {
		if got, err := BindAddr(in); err == nil {
			t.Errorf("BindAddr(%q) accepted it as %q", in, got)
		}
	}
}

// TestBindToLoopbackListensThereAndNowhereElse is what --bind exists for: the
// interface the caller asked for is the interface the socket is bound to, so
// a host who asked for this machine alone is not reachable from the network.
//
// The assertion stops at the bound address rather than dialling one of this
// machine's other addresses at the same port: that a socket bound to
// 127.0.0.1 does not answer elsewhere is the operating system's guarantee,
// and a dial that failed for want of anything listening would be measuring
// the absence of an unrelated service. What this code decides — and what used
// to be wrong — is which address is bound and which one the opponent is told.
func TestBindToLoopbackListensThereAndNowhereElse(t *testing.T) {
	h, err := Bind("127.0.0.1:0")
	if err != nil {
		t.Fatalf("binding loopback: %v", err)
	}
	defer h.Close()

	host, port, err := net.SplitHostPort(h.Addr())
	if err != nil {
		t.Fatalf("the bound address %q is not host and port: %v", h.Addr(), err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("asked for loopback and bound %q", h.Addr())
	}
	if port == "0" {
		t.Fatal("the bound address still says port 0, so there is nothing to hand out")
	}
	// It really is listening there.
	c, err := net.Dial("tcp", h.Addr())
	if err != nil {
		t.Fatalf("dialling the bound address: %v", err)
	}
	c.Close()
	// And what the opponent is told is the whole address, because this host
	// knows it. A wildcard bind is the case that does not.
	if got, want := JoinTarget(h.Addr()), net.JoinHostPort(host, port); got != want {
		t.Errorf("the opponent would be told %q, want %q", got, want)
	}
}

// TestJoinTargetNamesWhatItKnows: a wildcard bind knows the port and not the
// address, and saying so is more use than printing the wildcard for somebody
// to copy into a join command that would not work.
func TestJoinTargetNamesWhatItKnows(t *testing.T) {
	for _, bound := range []string{"[::]:4270", "0.0.0.0:4270", ":4270"} {
		got := JoinTarget(bound)
		if !strings.HasSuffix(got, ":4270") {
			t.Errorf("JoinTarget(%q) = %q, which loses the port", bound, got)
		}
		if !strings.Contains(got, "your address") {
			t.Errorf("JoinTarget(%q) = %q, which offers a wildcard to copy", bound, got)
		}
	}
	if got, want := JoinTarget("192.168.1.9:4271"), "192.168.1.9:4271"; got != want {
		t.Errorf("JoinTarget of a bound interface = %q, want %q", got, want)
	}
}

// TestCheckPairingCodeAgreesWithPairing is the preflight a caller makes before
// it echoes a code back and promises to wait. It has to accept exactly what
// pairing accepts, forgiveness included, or it would refuse codes that work or
// wave through codes that do not.
func TestCheckPairingCodeAgreesWithPairing(t *testing.T) {
	code := PairingCode()
	for _, variant := range []string{
		code,
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		strings.ReplaceAll(code, "-", " "),
		" " + code + " ",
	} {
		if err := CheckPairingCode(variant); err != nil {
			t.Errorf("CheckPairingCode(%q) refused a code pairing accepts: %v", variant, err)
		}
	}

	room, _, err := splitPairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "   ", "A", room, room + "-123", code + "!"} {
		if err := CheckPairingCode(bad); err == nil {
			t.Errorf("CheckPairingCode(%q) accepted a code that cannot pair", bad)
		}
	}
}
