//go:build !unix && !windows

package cmd

import (
	"fmt"
	"runtime"
)

func repositoryInstanceID(string) (string, error) {
	// wt releases target Unix and Windows. Fail closed on a newly supported
	// platform until it has an identity with the same replacement semantics;
	// falling back to a mutable timestamp or path would recreate the hole this
	// value closes.
	return "", fmt.Errorf("repository filesystem identity is not supported on %s", runtime.GOOS)
}
