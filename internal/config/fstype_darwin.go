//go:build darwin

package config

import "golang.org/x/sys/unix"

// fsTypeName reads the name macOS reports directly.
func fsTypeName(st *unix.Statfs_t) string {
	name := make([]byte, 0, len(st.Fstypename))
	for _, c := range st.Fstypename {
		if c == 0 {
			break
		}
		name = append(name, c)
	}
	return string(name)
}
