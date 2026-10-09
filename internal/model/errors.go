package model

import "errors"

// ErrInvalidOutcome means a value was not player, banker, or tie.
var ErrInvalidOutcome = errors.New("invalid outcome")
