// Package registry looks up Profiles by name.
package registry

import (
	"fmt"
	"slices"

	"github.com/kanishka-sahoo/unfraud/internal/profile"
	"github.com/kanishka-sahoo/unfraud/internal/profile/tabformer"
)

var profiles = map[string]profile.Profile{
	tabformer.Profile.Name: tabformer.Profile,
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
