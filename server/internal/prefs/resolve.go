package prefs

// Source names the layer a resolved value came from.
type Source string

const (
	SourceDevice  Source = "device"
	SourceUser    Source = "user"
	SourceGroup   Source = "group"
	SourceServer  Source = "server"
	SourceBuiltin Source = "builtin"
)

// Admin reports whether the layer is one of the admin's: the only layers
// that can lock a preference.
func (s Source) Admin() bool { return s == SourceGroup || s == SourceServer }

// Setting is one layer's value for a key.
type Setting struct {
	Value  string
	Locked bool // admin layers only
}

// Layer is one set of values, most specific first in a stack. The item
// layer (an override for one series, film or library) will be another
// Layer at the front of the stack: nothing here needs to change for it.
type Layer struct {
	Source    Source
	GroupID   string // for SourceGroup
	GroupName string
	Values    map[string]Setting
}

// Origin says where a value came from.
type Origin struct {
	Value     string `json:"value"`
	Source    Source `json:"source"`
	GroupID   string `json:"groupId,omitempty"`
	GroupName string `json:"groupName,omitempty"`
}

// Resolved is a preference's value for one viewer, and why.
type Resolved struct {
	Origin
	// Locked means an admin layer decided it, and the viewer cannot change
	// it: Source and GroupName say whose lock it is.
	Locked bool `json:"locked"`
	// Default is what applies without the viewer's own choices (their
	// device's and their own): the admin's default, or the built-in one.
	// What "Reset" would go back to. Absent for keys the registry does not
	// know, which have no default.
	Default *Origin `json:"default,omitempty"`
}

func origin(l Layer, v string) Origin {
	return Origin{Value: v, Source: l.Source, GroupID: l.GroupID, GroupName: l.GroupName}
}

// Resolve decides every preference from a stack of layers, most specific
// first: device, user, the user's groups in the admin's order, then the
// server. The built-in default is below them all.
//
// A lock in an admin layer wins over every layer above it: the first
// locked admin layer, in stack order, decides. Otherwise the first layer
// with a value does. A value that is not valid for its key (stored before a
// choice was withdrawn, say) is skipped as if it were not set, so it can
// never reach playback. A device value applies only to device-scoped keys.
//
// Keys the registry does not know are passed through from the user layer as
// they are, so a client can keep a preference of its own without asking.
func Resolve(layers []Layer) map[string]Resolved {
	out := make(map[string]Resolved, len(registry))
	for i := range registry {
		out[registry[i].Key] = resolveOne(&registry[i], layers)
	}
	for _, l := range layers {
		if l.Source != SourceUser {
			continue
		}
		for k, v := range l.Values {
			if Lookup(k) == nil {
				out[k] = Resolved{Origin: origin(l, v.Value)}
			}
		}
	}
	return out
}

func resolveOne(p *Pref, layers []Layer) Resolved {
	usable := func(l Layer) (Setting, bool) {
		if l.Source == SourceDevice && p.Scope != ScopeDevice {
			return Setting{}, false
		}
		v, ok := l.Values[p.Key]
		if !ok || p.Validate(v.Value) != nil {
			return Setting{}, false
		}
		return v, true
	}
	builtin := Origin{Value: p.Default, Source: SourceBuiltin}

	// What applies from the admin's layers alone: a lock decides outright,
	// and otherwise it is the default beneath the viewer's own choices.
	var def *Origin
	for _, l := range layers {
		if !l.Source.Admin() {
			continue
		}
		v, ok := usable(l)
		if !ok {
			continue
		}
		if v.Locked && p.Lockable {
			o := origin(l, v.Value)
			return Resolved{Origin: o, Locked: true, Default: &o}
		}
		if def == nil {
			o := origin(l, v.Value)
			def = &o
		}
	}
	if def == nil {
		def = &builtin
	}
	for _, l := range layers {
		if l.Source.Admin() {
			break // the admin's layers are below the viewer's
		}
		if v, ok := usable(l); ok {
			return Resolved{Origin: origin(l, v.Value), Default: def}
		}
	}
	return Resolved{Origin: *def, Default: def}
}

// Values flattens resolved preferences to their values, as playback and
// /auth/prefs-era code read them.
func Values(resolved map[string]Resolved) map[string]string {
	out := make(map[string]string, len(resolved))
	for k, r := range resolved {
		out[k] = r.Value
	}
	return out
}
