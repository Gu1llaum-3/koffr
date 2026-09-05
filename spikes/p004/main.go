// P-004: does redirecting libpq through an SSH tunnel break TLS verification
// or .pgpass resolution?
//
// Opens an SSH connection to a bastion, listens on 127.0.0.1 on an ephemeral
// port, and forwards every accepted connection to the target over a
// direct-tcpip channel. This is exactly what internal/executor/ssh will do.
//
// It then prints the port it picked, because that is the crux: the port is not
// known until the tunnel is up, and .pgpass matches on host AND port, so the
// password file can only be written afterwards.
//
// Throwaway probe code: no retry, no teardown discipline, no tests.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	var (
		bastion = flag.String("bastion", "", "host:port of the SSH bastion")
		user    = flag.String("user", "probe", "SSH user")
		keyPath = flag.String("key", "", "path to the SSH private key")
		target  = flag.String("target", "", "host:port to forward to, as seen from the bastion")
		portOut = flag.String("port-file", "", "write the chosen local port here")
	)
	flag.Parse()

	key, err := os.ReadFile(*keyPath)
	if err != nil {
		log.Fatal("read key: ", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		log.Fatal("parse key: ", err)
	}

	client, err := ssh.Dial("tcp", *bastion, &ssh.ClientConfig{
		User:            *user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // probe only
		Timeout:         10 * time.Second,
	})
	if err != nil {
		log.Fatal("ssh dial: ", err)
	}
	defer client.Close() //nolint:errcheck

	// Port 0: the kernel picks. Koffr will do the same, which is why the
	// password file has to be written after this point.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal("listen: ", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	fmt.Println(port)
	if *portOut != "" {
		if err := os.WriteFile(*portOut, []byte(fmt.Sprint(port)), 0o600); err != nil {
			log.Fatal("write port file: ", err)
		}
	}

	for {
		local, err := ln.Accept()
		if err != nil {
			log.Fatal("accept: ", err)
		}
		go func() {
			remote, err := client.Dial("tcp", *target)
			if err != nil {
				log.Print("channel dial: ", err)
				local.Close() //nolint:errcheck
				return
			}
			go func() { _, _ = io.Copy(remote, local); remote.Close() }() //nolint:errcheck
			_, _ = io.Copy(local, remote)
			local.Close() //nolint:errcheck
		}()
	}
}
