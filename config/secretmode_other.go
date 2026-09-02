//go:build !unix

package config

import "os"

// fileGroupIsOurs is false off Unix, where a file has no group this process
// could belong to and the permission bits carry no such meaning. Every group
// bit is then reported, which is the conservative answer.
func fileGroupIsOurs(os.FileInfo) bool { return false }
