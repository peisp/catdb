package mysqldrv

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

const defaultDialTimeout = 15 * time.Second

func randomID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func parseDurationDefault(v string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	return def
}

func parseIntDefault(v string, def int) int {
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return def
}

func parseLocation(v string) *time.Location {
	v = stringDefault(v, "Local")
	if loc, err := time.LoadLocation(v); err == nil {
		return loc
	}
	return time.Local
}
