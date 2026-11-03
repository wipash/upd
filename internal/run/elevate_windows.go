//go:build windows

package run

import (
	"context"
	"errors"
)

// PrepareElevation is not implemented on Windows yet.
func PrepareElevation(context.Context) (Elevator, error) {
	return nil, errors.New("running jobs as administrator is not supported on Windows yet")
}
