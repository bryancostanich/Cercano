package enterprise

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"cercano/source/server/internal/modelpolicy"
)

// Host owns the one enterprise connection used by this running Cercano process.
// Credential access is lazy for standalone users. A durable marker distinguishes
// an unconfigured host from a managed host whose credentials have disappeared.
type Host struct {
	mu                        sync.Mutex
	commands                  sync.Mutex
	options                   HostOptions
	manager                   *Manager
	managed, changing, closed bool
	active                    int
	lastError                 string
	release                   func()
	cancel                    context.CancelFunc
	done                      chan struct{}
}

type HostOptions struct {
	Directory string
	Manager   Options
	OpenStore func() (CredentialStore, error)
}

type HostStatus struct {
	Status
	Managed           bool `json:"managed"`
	EnforcementActive bool `json:"enforcement_active"`
	Changing          bool `json:"changing"`
}

func NewHost(options HostOptions) (*Host, error) {
	if options.Directory == "" {
		return nil, errors.New("enterprise state directory required")
	}
	if options.OpenStore == nil {
		options.OpenStore = OpenCredentialStore
	}
	if options.Manager.CachePath == "" {
		options.Manager.CachePath = filepath.Join(options.Directory, "active-bundle.json")
	}
	h := &Host{options: options}
	if h.markerPresent() {
		h.managed = true
		if err := h.initializeLocked(); err != nil {
			h.lastError = err.Error()
		} else {
			h.startPollingLocked()
		}
	}
	return h, nil
}
func (h *Host) markerPath() string { return filepath.Join(h.options.Directory, "managed") }
func (h *Host) markerPresent() bool {
	_, err := os.Lstat(h.markerPath())
	return !errors.Is(err, os.ErrNotExist)
}

// PolicyManaged also notices another host's activation. An already-running
// standalone process must not bypass the profile by keeping stale startup state.
// It cannot use the other host's credential lock, so it will deny model requests.
func (h *Host) PolicyManaged() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.managed && h.markerPresent() {
		h.managed = true
		h.lastError = "enterprise profile activated by another host; connect to that host or restart"
	}
	return h.managed
}
func (h *Host) initializeLocked() error {
	if h.manager != nil {
		return nil
	}
	if h.release == nil {
		release, err := LockConnection(filepath.Join(h.options.Directory, "connection.lock"))
		if err != nil {
			return err
		}
		h.release = release
	}
	store, err := h.options.OpenStore()
	if err != nil {
		return ErrCredentialStore
	}
	manager, err := New(store, h.options.Manager)
	if err != nil {
		return err
	}
	h.manager = manager
	return nil
}

// A sync or logout with no existing account is not a request to enter managed
// mode. The old connection-preview credentials may be adopted explicitly here.
func (h *Host) existingConnectionLocked() error {
	if h.closed {
		return errors.New("enterprise host is shutting down")
	}
	if h.managed {
		return nil
	}
	if h.active > 0 {
		return ErrBusy
	}
	err := h.initializeLocked()
	if err == nil && !h.manager.Status().Connected {
		err = ErrNotConnected
	}
	if err != nil {
		h.manager = nil
		if h.release != nil {
			h.release()
			h.release = nil
		}
	}
	return err
}

func (h *Host) activateLocked() error {
	if h.closed {
		return errors.New("enterprise host is shutting down")
	}
	if (!h.managed || h.manager == nil) && h.active > 0 {
		return ErrBusy
	}
	// Acquire ownership before writing the durable mode marker. An empty or
	// interrupted marker is still managed; no parsing error can restore standalone.
	if h.release == nil {
		release, err := LockConnection(filepath.Join(h.options.Directory, "connection.lock"))
		if err != nil {
			return err
		}
		h.release = release
	}
	f, err := os.OpenFile(h.markerPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		err = f.Sync()
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil && !errors.Is(err, os.ErrExist) {
		return ErrCredentialStore
	}
	h.managed = true
	if err := syncProfileDirectory(h.options.Directory); err != nil {
		return ErrCredentialStore
	}
	return h.initializeLocked()
}
func syncProfileDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func (h *Host) startPollingLocked() {
	if h.closed || h.manager == nil || !h.manager.Status().Connected {
		return
	}
	if h.done != nil {
		select {
		case <-h.done:
		default:
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	manager, done := h.manager, h.done
	go func() { defer close(done); manager.Run(ctx) }()
}
func (h *Host) stopPolling() {
	h.mu.Lock()
	cancel, done := h.cancel, h.done
	h.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
func (h *Host) record(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastError = ""
	if err != nil {
		h.lastError = err.Error()
	}
}

func (h *Host) Login(ctx context.Context, server, organization string, open func(string) error) error {
	// Validate before changing modes, opening Keychain, or writing state.
	validator := Manager{options: h.options.Manager}
	if !validUUID(organization) || open == nil {
		return errors.New("valid organization and browser opener required")
	}
	if err := validator.validOrigin(server); err != nil {
		return err
	}
	h.commands.Lock()
	defer h.commands.Unlock()
	h.mu.Lock()
	if h.active > 0 || h.changing {
		h.mu.Unlock()
		return ErrBusy
	}
	err := h.activateLocked()
	if err != nil {
		h.lastError = err.Error()
		h.mu.Unlock()
		return err
	}
	h.changing = true
	manager := h.manager
	h.mu.Unlock()
	h.stopPolling()
	err = manager.Login(ctx, server, organization, open)
	if err == nil {
		err = manager.Sync(ctx)
	}
	h.mu.Lock()
	h.changing = false
	h.startPollingLocked()
	h.mu.Unlock()
	h.record(err)
	return err
}
func (h *Host) Sync(ctx context.Context) error {
	h.commands.Lock()
	defer h.commands.Unlock()
	h.mu.Lock()
	err := h.existingConnectionLocked()
	if err == nil {
		err = h.activateLocked()
	}
	manager := h.manager
	h.mu.Unlock()
	if err == nil {
		h.stopPolling()
		err = manager.Sync(ctx)
		h.mu.Lock()
		h.startPollingLocked()
		h.mu.Unlock()
	}
	h.record(err)
	return err
}
func (h *Host) Logout(ctx context.Context) error {
	h.commands.Lock()
	defer h.commands.Unlock()
	h.mu.Lock()
	if h.active > 0 || h.changing {
		h.mu.Unlock()
		return ErrBusy
	}
	err := h.existingConnectionLocked()
	if errors.Is(err, ErrNotConnected) {
		h.mu.Unlock()
		return nil
	}
	if err == nil {
		err = h.activateLocked()
	}
	manager := h.manager
	if err != nil {
		h.mu.Unlock()
		h.record(err)
		return err
	}
	h.changing = true
	h.mu.Unlock()
	h.stopPolling()
	err = manager.Logout(ctx)
	h.mu.Lock()
	h.changing = false
	h.mu.Unlock()
	h.record(err)
	return err
}

// UseStandalone is an explicit mode change, separate from logout. It never
// discards a connected enterprise credential or interrupts an active turn.
func (h *Host) UseStandalone() error {
	h.PolicyManaged()
	h.commands.Lock()
	defer h.commands.Unlock()
	h.mu.Lock()
	if h.active > 0 || h.changing {
		h.mu.Unlock()
		return ErrBusy
	}
	if h.closed {
		h.mu.Unlock()
		return errors.New("enterprise host is shutting down")
	}
	if !h.managed {
		h.mu.Unlock()
		return nil
	}
	if h.managed && h.manager == nil {
		if err := h.initializeLocked(); err != nil {
			h.mu.Unlock()
			return err
		}
	}
	if h.manager != nil && h.manager.Status().Connected {
		h.mu.Unlock()
		return errors.New("log out before selecting standalone mode")
	}
	h.changing = true
	h.mu.Unlock()
	h.stopPolling()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changing = false
	if err := os.Remove(h.markerPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrCredentialStore
	}
	if err := syncProfileDirectory(h.options.Directory); err != nil {
		return ErrCredentialStore
	}
	h.managed = false
	h.manager = nil
	h.lastError = ""
	if h.release != nil {
		h.release()
		h.release = nil
	}
	return nil
}
func (h *Host) Status() HostStatus {
	h.PolicyManaged()
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HostStatus{Managed: h.managed, EnforcementActive: h.managed, Changing: h.changing}
	if h.manager != nil && !h.changing {
		s.Status = h.manager.Status()
	}
	if s.Error == "" && !s.Usable && h.lastError != "" {
		s.Error = h.lastError
	}
	return s
}
func (h *Host) Authorize(ctx context.Context, a modelpolicy.Attempt) error {
	h.mu.Lock()
	manager, blocked := h.manager, h.changing || h.closed
	h.mu.Unlock()
	if blocked || manager == nil {
		return modelpolicy.Deny(a, "enterprise host has no usable connection")
	}
	return manager.Authorize(ctx, a)
}

type bundleContextKey struct{}

// BundleFromContext returns the immutable policy/default/skill snapshot for this
// work. Callers must continue to use Authorize for the latest restrictions.
func BundleFromContext(ctx context.Context) (Bundle, bool) {
	b, ok := ctx.Value(bundleContextKey{}).(Bundle)
	return b, ok
}

// Begin keeps connection changes out of active work and pins a complete bundle.
// Standalone work is counted too, so activating enterprise cannot silently change
// a turn's defaults halfway through its execution.
func (h *Host) Begin(ctx context.Context) (context.Context, func(), error) {
	h.PolicyManaged()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || h.changing {
		return ctx, nil, ErrBusy
	}
	finish := func() {}
	if h.managed {
		if h.manager == nil {
			return ctx, nil, ErrDenied
		}
		bundle, release, err := h.manager.Begin()
		if err != nil {
			return ctx, nil, err
		}
		finish = release
		ctx = context.WithValue(ctx, bundleContextKey{}, bundle)
	}
	h.active++
	var once sync.Once
	return ctx, func() { once.Do(func() { finish(); h.mu.Lock(); h.active--; h.mu.Unlock() }) }, nil
}
func (h *Host) Close() {
	h.commands.Lock()
	defer h.commands.Unlock()
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	h.stopPolling()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.release != nil {
		h.release()
		h.release = nil
	}
}
