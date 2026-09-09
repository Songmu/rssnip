//go:build !(aix || darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || solaris)

package rssnip

import "os"

func openLocalFeed(path string) (*os.File, error) {
	return os.Open(path)
}
