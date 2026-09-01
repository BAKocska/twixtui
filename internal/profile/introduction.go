package profile

import "fmt"

// Whether a player has been through the interface's short introduction is a
// property of that player, so it is a field on the profile rather than a file
// beside the store.
//
// The case that decides it is two people sharing one machine with a profile
// each. The introduction exists because a newcomer is otherwise dropped at a
// menu with no idea what TwixT is; a machine-wide flag would mean that the
// first person to launch the program consumes it for everybody, and the second
// person — who is exactly the newcomer the introduction was written for — never
// sees it. The mirror case, one person on a second machine, does not decide
// anything: profiles are local to a machine and there is nothing here that syncs
// them, so no scheme available to this package can know that the person has seen
// the introduction elsewhere. That costs one keypress on the new machine, which
// is the same price a machine-wide flag would charge, so it is not a reason to
// prefer one over the other.
//
// Being a field rather than its own file is what makes it follow the identity for
// free: Rename keeps it, because renaming edits the entry in place, and Delete
// takes it with the profile, which is right — a name that has been deleted and
// created again is being used by somebody who wants a fresh start. A sidecar
// file keyed by name would have needed hooks in both of those to stay in step,
// and two files that can disagree is a worse failure than the one this avoids.
//
// storeVersion deliberately does not move for this. The version guard is there
// so that an older binary cannot silently drop fields it does not understand
// when it writes the file back, and bumping it would make an older binary refuse
// the whole store and present the player with no profiles at all. Weighed
// against that, the field's own loss is trivial: a downgrade clears it on its
// next write and the player is offered the introduction once more, which one
// keypress dismisses. A guard that costs somebody their profile list is not
// worth spending on a boolean whose loss costs a keypress.

// MarkIntroduced records that a profile has been through the introduction. It is
// called when the introduction is left, whether the player read it through or
// skipped it: somebody who skipped does not want it again next launch, so the
// two departures are the same fact.
func (s *Store) MarkIntroduced(name string) error {
	return s.mutate(func(ps *[]Profile) error {
		i := indexOf(*ps, name)
		if i < 0 {
			return fmt.Errorf("%q: %w", name, ErrNotFound)
		}
		(*ps)[i].Introduced = true
		return nil
	})
}
