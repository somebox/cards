// Package config — time helper (separate so it can be swapped in tests).
package config

import "time"

func nowUTC() time.Time { return time.Now().UTC() }
