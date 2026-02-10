// Package portutil provides TCP port utilities.
package portutil

import "net"

// FreePort returns a free local TCP port by briefly binding to port 0.
func FreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
