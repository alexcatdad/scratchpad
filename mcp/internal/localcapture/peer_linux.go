//go:build linux

package localcapture

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

func authorizedPeer(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	uid := -1
	if err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e == nil {
			uid = int(cred.Uid)
		}
	}); err != nil {
		return false
	}
	return uid == syscall.Getuid()
}
