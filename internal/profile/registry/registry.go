// Package registry looks up Profiles by name.
package registry

import (
	"fmt"
	"slices"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/elliptic"
	"github.com/kanishka-sahoo/unfraud/internal/profile/ieeecis"
	"github.com/kanishka-sahoo/unfraud/internal/profile/paysim"
)

var profiles = map[string]profile.Profile{
	ieeecis.Profile.Name:  ieeecis.Profile,
	paysim.Profile.Name:   paysim.Profile,
	elliptic.Profile.Name: elliptic.Profile,
}

// Get returns the named Profile.
func Get(name string) (profile.Profile, error) {
	p, ok := profiles[name]
	if !ok {
		return profile.Profile{}, fmt.Errorf("unknown profile %q (have %v)", name, Names())
	}
	return p, nil
}

// Names lists the registered Profiles, sorted.
func Names() []string {
	n := make([]string, 0, len(profiles))
	for k := range profiles {
		n = append(n, k)
	}
	slices.Sort(n)
	return n
}
