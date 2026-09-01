package netplay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// A relay is an intermediary that is not trusted and cannot be authenticated.
// It is told a pairing code so that it can put two clients in the same room, it
// sees every byte of what it then carries, and being a byte pump it can replace
// any of those bytes with any others. Nothing about the frame format stops it:
// the position hash and the transcript digest are computed from the moves, so
// anybody holding the plaintext can compute them for a game that never
// happened. A relay operator could resign on either player's behalf and the
// victim's own engine would accept it.
//
// The pairing code is what closes that, because it is the one thing the two
// players share and the relay is not given all of. The host generates it in two
// parts:
//
//	ABCDEF-GHJKMNPQ-RSTVWXYZ
//	room   key
//
// Only the room travels in the JOIN line. Both ends derive a key from the rest
// and authenticate every frame with it, so a relay that alters, injects,
// replays, reorders or drops a frame is refused by the far end rather than
// believed. What the relay can still do is read: the frames are plaintext and
// the key protects their integrity, not their secrecy. relay.go's documentation
// says so in as many words.
//
// A key is only ever derived from a pairing code, so this applies to relayed
// games alone. Two players on a direct connection have no shared secret to
// derive one from and the protocol runs unauthenticated there, exactly as
// before: a direct connection has no intermediary in the first place.

const (
	// pairingRoomLen is the part of a pairing code the relay is told. Six
	// characters of the 32-character alphabet is a shade over 30 bits, which is
	// ample for telling concurrent games apart without being a chore to read
	// out.
	pairingRoomLen = 6
	// pairingKeyLen is the part the relay is never told. Sixteen characters is
	// 80 bits: far past guessing, and the whole code is still one line to read
	// out or paste.
	pairingKeyLen = 16
	// pairingCodeLen is how long a whole code is once the grouping dashes and
	// the case have been normalised away.
	pairingCodeLen = pairingRoomLen + pairingKeyLen
)

// PairingCode returns a fresh pairing code for a relayed game. It is one string
// for the player to pass on, in two parts: the first names the room the relay
// pairs the two clients in, and the rest is the key they authenticate the game
// with and the relay never sees. It is written in dashed groups so that a player
// can read it out; case, the dashes and the characters that are misread by eye
// are all forgiven when it is typed back in.
func PairingCode() string {
	key := randomCode(pairingKeyLen)
	return randomCode(pairingRoomLen) + "-" + key[:pairingKeyLen/2] + "-" + key[pairingKeyLen/2:]
}

// splitPairingCode separates the room a relay is told from the key it is not.
//
// A code too short to carry a key is refused rather than played
// unauthenticated. A player who was handed a code and pasted only the beginning
// of it would otherwise get a game a relay can forge, and would get it
// silently, which is the one outcome worth ruling out: the point of the key part
// is that nobody has to trust the relay.
func splitPairingCode(code string) (room string, key []byte, err error) {
	normalised, err := normalisePairingCode(code)
	if err != nil {
		return "", nil, err
	}
	if len(normalised) < pairingCodeLen {
		return "", nil, fmt.Errorf("a pairing code is %d characters, of which the first %d name the room the relay pairs you in and the other %d are the key that stops the relay from tampering with the game; this one is %d, so paste the whole code, not just its beginning", pairingCodeLen, pairingRoomLen, pairingKeyLen, len(normalised))
	}
	room = normalised[:pairingRoomLen]
	return room, deriveFrameKey(room, normalised[pairingRoomLen:]), nil
}

// The tags keep these two derivations distinct from each other and from every
// other digest in this package, so that no value produced for one purpose can
// be replayed as a value for another.
const (
	frameKeyTag = "twixt-frame-key/1"
	frameMACTag = "twixt-frame-mac/1"
)

// deriveFrameKey turns the key part of a pairing code into the key both ends
// authenticate frames with. The room is mixed in as well, so a key is only ever
// valid for the room it was generated alongside.
func deriveFrameKey(room, secret string) []byte {
	h := sha256.New()
	h.Write([]byte(frameKeyTag))
	h.Write([]byte{0})
	h.Write([]byte(room))
	h.Write([]byte{0})
	h.Write([]byte(secret))
	return h.Sum(nil)
}

// The direction one frame travelled in. It is part of what a tag covers, so a
// frame cannot be reflected back at the end that sent it.
const (
	dirHost  = 'h'
	dirGuest = 'g'
)

// frameMACLen is how much of the HMAC travels with a frame. Sixteen bytes is far
// past what a forger could search, and keeps the tag to 32 characters of
// hexadecimal on a message that is a couple of hundred bytes.
const frameMACLen = 16

// frameMAC authenticates one message.
//
// What it covers is the message as the receiver will act on it, together with
// the direction it travelled in and its position in that direction's sequence.
// Covering the position is what makes intact bytes insufficient: a frame is
// valid only as the nth thing the other end said, so a relay that replays a
// frame, swaps two of them or swallows one is caught at the next frame rather
// than at some later divergence.
func frameMAC(key []byte, dir byte, seq uint64, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(frameMACTag))
	mac.Write([]byte{0, dir})
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], seq)
	mac.Write(counter[:])
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)[:frameMACLen])
}

// authBytes is the encoding a tag is computed over: the message with its own tag
// field cleared.
//
// It is derived from the decoded message rather than from the bytes that
// arrived, which is deliberate. It means the tag covers exactly the fields this
// end will act on, so a relay may re-spell, reorder or add anything the decoder
// throws away without invalidating a frame, and cannot change anything the
// decoder keeps without invalidating it. Marshalling a struct with no maps in it
// is deterministic, so both ends compute the same bytes from the same message.
func authBytes(m message) ([]byte, error) {
	m.MAC = ""
	payload, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encoding a %s message to authenticate it: %w", m.Type, err)
	}
	return payload, nil
}
