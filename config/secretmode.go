package config

import (
	"fmt"
	"os"
)

// secretModeWarning reports a mode that leaves a secret file readable by
// someone other than this process, or "" when the mode is safe.
//
// The other bits are always a leak. The group bits are not, on their own: a
// Kubernetes secret volume is owned by root whatever the pod runs as, and
// fsGroup is the only way to hand it to a non-root container -- the kubelet
// chowns the mount to root:<fsGroup> and widens a read-only volume to at least
// 0440, so the tightest a mounted secret can be is 0440 root:<fsGroup>. That is
// group-readable by exactly one group, the pod's own. Warning about it would
// mean warning about every correctly locked-down Kubernetes deployment, which
// teaches operators to ignore the warning that matters.
//
// So the group bits are reported only when the group is one this process is
// not in, where they really do hand the secret to someone else.
func secretModeWarning(what, path string, info os.FileInfo) string {
	mode := info.Mode().Perm()
	if mode&0o007 == 0 && (mode&0o070 == 0 || fileGroupIsOurs(info)) {
		return ""
	}
	return fmt.Sprintf("%s %s is mode %#o, readable beyond its owner", what, path, mode)
}
