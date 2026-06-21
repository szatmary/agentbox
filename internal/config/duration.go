package config

import "time"

// Duration is a [time.Duration] that marshals to/from a Go duration string
// (e.g. "3h", "30s") in TOML. It implements [encoding.TextUnmarshaler] and
// [encoding.TextMarshaler] so BurntSushi/toml decodes `max_wall = "3h"` directly.
type Duration time.Duration

// D returns the underlying [time.Duration].
func (d Duration) D() time.Duration { return time.Duration(d) }

// String returns the Go duration string.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalText parses a Go duration string.
func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// MarshalText renders the Go duration string.
func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}
