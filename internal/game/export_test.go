package game

// Digest is Record.digest, exposed to this package's external test package.
//
// A record close to the size limit cannot be played: a game on the widest board
// this build allows encodes to a few kilobytes, so such a fixture has to be
// built field by field. Every field of it is exported except the digest over
// them, which is what makes the fixture a record rather than a text file, and
// nothing outside this package can compute it. This file is only compiled into
// a test binary, so the program itself still has no way to produce a digest for
// a record it did not derive from a game.
func Digest(r Record) string { return r.digest() }
