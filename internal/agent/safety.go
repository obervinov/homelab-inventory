package agent

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
)

// CheckURL refuses a report URL that would send the bearer token in clear
// text. Plain http is accepted only for a loopback address.
func CheckURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("url %q is not a valid absolute URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
		return fmt.Errorf("url %q would send the token unencrypted: use https", raw)
	}
	return fmt.Errorf("url %q: unsupported scheme %q", raw, u.Scheme)
}

// ReadTrustedFile returns the content of a file only when no other user could
// have written it. The agent usually runs as root and executes the commands the
// config lists, so a config someone else can change is a way to run code as root.
//
// The file is opened once, without following a symlink, and both the check and
// the read use that descriptor: swapping the path after the check changes
// nothing. The directory is checked too, because whoever can write it can
// replace the file.
func ReadTrustedFile(path string) ([]byte, error) {
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err := checkOwnerAndMode(filepath.Dir(path), dir); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if err := checkOwnerAndMode(path, info); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

func checkOwnerAndMode(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is writable by group or others (mode %04o)", path, info.Mode().Perm())
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("%s: cannot determine the owner on this platform", path)
	}
	if uid := int(st.Uid); uid != 0 && uid != os.Geteuid() {
		return fmt.Errorf("%s is owned by uid %d, expected root or the user running the agent", path, uid)
	}
	return nil
}
