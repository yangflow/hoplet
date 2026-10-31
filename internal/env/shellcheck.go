package env

import "os"

// usableShell reports whether path is an absolute path to an executable
// file. It lives alone in this file so that the lint exclusion for statting
// a path from the user database covers nothing else.
func usableShell(path string) bool {
	if path == "" || path[0] != '/' {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}
