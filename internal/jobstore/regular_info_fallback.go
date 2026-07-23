//go:build !darwin && !linux

package jobstore

import "io/fs"

func validateRegularFilePlatform(fs.FileInfo, string) error { return nil }
