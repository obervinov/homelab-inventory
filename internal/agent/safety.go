package agent

import (
	"fmt"
	"net"
	"net/url"
	"os"
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

// CheckConfigFile refuses a config another user could have written. The agent
// usually runs as root and executes the commands listed in that file, so a
// config writable by anyone else is a way to run code as root.
func CheckConfigFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("config %s is writable by group or others (mode %04o): chmod 600 it", path, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if uid := int(st.Uid); uid != 0 && uid != os.Geteuid() {
			return fmt.Errorf("config %s is owned by uid %d, expected root or the user running the agent", path, uid)
		}
	}
	return nil
}
