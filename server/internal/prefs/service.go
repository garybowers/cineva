package prefs

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/garybowers/cineva/server/internal/store"
)

// Service reads and writes preferences through the store, applying the
// registry's types and the admin's locks.
type Service struct {
	store store.PrefRepo
}

func NewService(st store.PrefRepo) *Service { return &Service{store: st} }

// LockedError is a write to a preference an admin has locked.
type LockedError struct {
	Key       string
	Source    Source // group or server
	GroupName string
}

func (e *LockedError) Error() string {
	if e.Source == SourceGroup {
		return fmt.Sprintf("%s is set by your administrator for the group %s", e.Key, e.GroupName)
	}
	return fmt.Sprintf("%s is set by your administrator", e.Key)
}

// ErrInvalid wraps every rejected value: a wrong type, an unknown choice, a
// key or value too long, or a key that cannot be set at that layer.
var ErrInvalid = errors.New("invalid preference")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// layers is the stack for one viewer, most specific first. deviceID may be
// "" (an API key, which belongs to no device).
func (s *Service) layers(ctx context.Context, userID, deviceID string) ([]Layer, error) {
	var stack []Layer
	if deviceID != "" {
		dev, err := s.store.DevicePrefs(ctx, deviceID)
		if err != nil {
			return nil, fmt.Errorf("device prefs: %w", err)
		}
		stack = append(stack, Layer{Source: SourceDevice, Values: plain(dev)})
	}
	user, err := s.store.Prefs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("user prefs: %w", err)
	}
	stack = append(stack, Layer{Source: SourceUser, Values: plain(user)})

	groups, err := s.store.GroupsOf(ctx, userID)
	if err != nil {
		return nil, err
	}
	defaults, err := s.store.PrefDefaults(ctx)
	if err != nil {
		return nil, err
	}
	byOwner := ownerValues(defaults)
	for _, g := range groups {
		stack = append(stack, Layer{Source: SourceGroup, GroupID: g.ID, GroupName: g.Name, Values: byOwner[ownerKey(store.PrefOwnerGroup, g.ID)]})
	}
	stack = append(stack, Layer{Source: SourceServer, Values: byOwner[ownerKey(store.PrefOwnerServer, "")]})
	return stack, nil
}

func plain(m map[string]string) map[string]Setting {
	out := make(map[string]Setting, len(m))
	for k, v := range m {
		out[k] = Setting{Value: v}
	}
	return out
}

func ownerKey(kind, id string) string { return kind + "/" + id }

func ownerValues(defaults []store.PrefDefault) map[string]map[string]Setting {
	out := map[string]map[string]Setting{}
	for _, d := range defaults {
		k := ownerKey(d.OwnerKind, d.OwnerID)
		if out[k] == nil {
			out[k] = map[string]Setting{}
		}
		out[k][d.Key] = Setting{Value: d.Value, Locked: d.Locked}
	}
	return out
}

// Resolve is every preference for a user on a device ("" for none).
func (s *Service) Resolve(ctx context.Context, userID, deviceID string) (map[string]Resolved, error) {
	stack, err := s.layers(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	return Resolve(stack), nil
}

// checkRaw applies the storage limits every layer shares.
func checkRaw(changes map[string]*string) error {
	if len(changes) > MaxKeys {
		return invalid("too many preferences")
	}
	for k, v := range changes {
		if k == "" || len(k) > MaxKeyLen || (v != nil && len(*v) > MaxValueLen) {
			return invalid("preference key or value too long")
		}
	}
	return nil
}

// SetUser merges changes into the user's own layer: a value sets a key, nil
// clears it back to the default. A registered key must be valid for its
// type, and one an admin has locked for this user is a *LockedError;
// clearing a locked key is allowed, since it changes nothing they see.
// Keys the registry does not know are stored as they are.
func (s *Service) SetUser(ctx context.Context, userID string, changes map[string]*string) error {
	if err := s.check(ctx, userID, changes, false); err != nil {
		return err
	}
	if err := s.store.SetPrefs(ctx, userID, changes); err != nil {
		return fmt.Errorf("set user prefs: %w", err)
	}
	return nil
}

// SetDevice merges changes into a device's layer. Only device-scoped keys
// belong there. The caller has checked the device is the user's.
func (s *Service) SetDevice(ctx context.Context, userID, deviceID string, changes map[string]*string) error {
	if err := s.check(ctx, userID, changes, true); err != nil {
		return err
	}
	if err := s.store.SetDevicePrefs(ctx, deviceID, changes); err != nil {
		return fmt.Errorf("set device prefs: %w", err)
	}
	return nil
}

func (s *Service) check(ctx context.Context, userID string, changes map[string]*string, device bool) error {
	if err := checkRaw(changes); err != nil {
		return err
	}
	var resolved map[string]Resolved
	for _, k := range sortedKeys(changes) {
		v := changes[k]
		p := Lookup(k)
		if p == nil {
			if device {
				return invalid("%s is not a device preference", k)
			}
			continue
		}
		if device && p.Scope != ScopeDevice {
			return invalid("%s belongs to the person, not the device", k)
		}
		if v == nil {
			continue
		}
		if err := p.Validate(*v); err != nil {
			return invalid("%s", err)
		}
		if resolved == nil {
			var err error
			if resolved, err = s.Resolve(ctx, userID, ""); err != nil {
				return err
			}
		}
		if r := resolved[k]; r.Locked {
			return &LockedError{Key: k, Source: r.Source, GroupName: r.GroupName}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// GroupDefaults is one group's defaults, for the admin's table.
type GroupDefaults struct {
	ID       string                       `json:"id"`
	Name     string                       `json:"name"`
	Priority int                          `json:"priority"`
	Defaults map[string]store.PrefSetting `json:"defaults"`
}

// Defaults is every admin default: the server's, and each group's in
// priority order. Rows for keys no longer registered are left out.
type Defaults struct {
	Server map[string]store.PrefSetting `json:"server"`
	Groups []GroupDefaults              `json:"groups"`
}

// AllDefaults reads the admin's layers. groups is every group, in priority
// order, so a group with no defaults yet still gets its column.
func (s *Service) AllDefaults(ctx context.Context, groups []store.UserGroup) (Defaults, error) {
	rows, err := s.store.PrefDefaults(ctx)
	if err != nil {
		return Defaults{}, err
	}
	pick := func(kind, id string) map[string]store.PrefSetting {
		out := map[string]store.PrefSetting{}
		for _, d := range rows {
			if d.OwnerKind == kind && d.OwnerID == id && Lookup(d.Key) != nil {
				out[d.Key] = store.PrefSetting{Value: d.Value, Locked: d.Locked}
			}
		}
		return out
	}
	out := Defaults{Server: pick(store.PrefOwnerServer, ""), Groups: []GroupDefaults{}}
	for _, g := range groups {
		out.Groups = append(out.Groups, GroupDefaults{ID: g.ID, Name: g.Name, Priority: g.Priority, Defaults: pick(store.PrefOwnerGroup, g.ID)})
	}
	return out, nil
}

// SetDefaults merges changes into the server's defaults (ownerKind server)
// or a group's: a setting sets a key, nil clears it. Only registered keys
// can have a default, a value must suit its type, and only a lockable key
// can be locked.
func (s *Service) SetDefaults(ctx context.Context, ownerKind, ownerID string, changes map[string]*store.PrefSetting) error {
	if len(changes) > MaxKeys {
		return invalid("too many preferences")
	}
	for _, k := range sortedKeys(changes) {
		p := Lookup(k)
		if p == nil {
			return invalid("%s is not a preference the server knows", k)
		}
		v := changes[k]
		if v == nil {
			continue
		}
		if err := p.Validate(v.Value); err != nil {
			return invalid("%s", err)
		}
		if v.Locked && !p.Lockable {
			return invalid("%s cannot be locked", k)
		}
	}
	if err := s.store.SetPrefDefaults(ctx, ownerKind, ownerID, changes); err != nil {
		return fmt.Errorf("set pref defaults: %w", err)
	}
	return nil
}
