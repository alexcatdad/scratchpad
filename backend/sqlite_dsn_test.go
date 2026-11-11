package backend_test

import "net/url"

// sqliteTestDSN opens the test database the way the server does, with a busy
// timeout. Tests write to the file while a server process is running, and its
// background workers take the write lock briefly; without the timeout such a
// write fails at once with SQLITE_BUSY.
func sqliteTestDSN(path string) string {
	return "file:" + url.PathEscape(path) + "?_pragma=busy_timeout(5000)"
}
