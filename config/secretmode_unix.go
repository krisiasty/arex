//go:build unix

package config

import (
	"os"
	"slices"
	"syscall"
)

// fileGroupIsOurs reports whether the file's group is one this process belongs
// to, so that group-readable means readable by us rather than by someone else.
func fileGroupIsOurs(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	gid := int(st.Gid)
	if gid == os.Getgid() || gid == os.Getegid() {
		return true
	}
	// Getgroups may or may not include the primary group depending on the
	// platform, which is why both are checked.
	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	return slices.Contains(groups, gid)
}
