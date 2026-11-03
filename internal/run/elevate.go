package run

import (
	"maps"
	"slices"
)

// Elevator runs commands with administrator rights.
type Elevator interface {
	// Wrap returns the command line that runs argv elevated with env set.
	Wrap(argv []string, env map[string]string) []string
	// Close stops keeping the credential alive.
	Close()
}

// envPairs renders env as sorted K=V strings.
func envPairs(env map[string]string) []string {
	var out []string
	for _, k := range slices.Sorted(maps.Keys(env)) {
		out = append(out, k+"="+env[k])
	}
	return out
}
