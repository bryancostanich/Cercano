// Package statelease coordinates cooperative state writers with an offline
// setup reset. Participants retain a shared lease for their entire lifetime;
// reset requires an exclusive lease. The separate autolaunch lock is not a
// replacement for this lease. Acquisitions are nonblocking and never unlink the
// lock file. This cannot constrain older, nonparticipating binaries.
package statelease

import (
	"errors"
	"os"
	"sync"
)

var ErrBusy = errors.New("Cercano state is in use; close clients and agents before setup reset")
var ErrUnsupported = errors.New("setup reset coordination is unsupported on this platform")

// Lease holds an operating-system lock. Closing it twice is safe.
type Lease struct {
	file *os.File
	once sync.Once
	err  error
}

func (l *Lease) Close() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if l.file != nil {
			l.err = l.file.Close()
		}
	})
	return l.err
}
func Participate(root string) (*Lease, error) { return acquire(root, false) }
func TryReset(root string) (*Lease, error)    { return acquire(root, true) }
