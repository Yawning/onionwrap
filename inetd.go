// inetd.go - A trivial inetd implementation.
//
// To the extent possible under law, Yawning Angel waived all copyright
// and related or neighboring rights to onionwrap, using the creative
// commons "cc0" public domain dedication. See LICENSE or
// <http://creativecommons.org/publicdomain/zero/1.0/> for full details.

package main

import (
	"io"
	"net"
	"os/exec"
	"sync"
)

func runInetd(targetNet, targetAddr string, cmd *exec.Cmd) {
	l, err := net.Listen(targetNet, targetAddr)
	if err != nil {
		errorf("Failed to create an inetd listener: %v\n", err)
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			if e, ok := err.(net.Error); ok && !e.Temporary() {
				errorf("Critical Accept() failure: %v\n", err)
			}
			continue
		}
		debugf("inetd: new connection: %s\n", conn.RemoteAddr())
		go onInetdConn(conn, cmd)
	}
}

func onInetdConn(conn net.Conn, cmdProto *exec.Cmd) {
	defer conn.Close()

	var cmd *exec.Cmd
	if len(cmdProto.Args) > 1 {
		cmd = exec.Command(cmdProto.Args[0], cmdProto.Args[1:]...)
	} else {
		cmd = exec.Command(cmdProto.Args[0])
	}

	// Sigh, for some reason just setting cmd.Stdin/cmd.Stdout to
	// conn doesn't result in closes getting propagated, so Run()
	// doesn't appear to unblock, even when conn is closed.
	//
	// Do this the hard way.

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		infof("inetd: Failed to create stdin pipe: %v\n", err)
		return
	}
	defer stdinPipe.Close()

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		infof("inetd: Failed to create stdout pipe: %v\n", err)
		return
	}
	defer stdoutPipe.Close()

	if err = cmd.Start(); err != nil {
		infof("inetd: Failed to start command: %v\n", err)
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go copyLoop(&wg, conn, stdinPipe)
	go copyLoop(&wg, stdoutPipe, conn)
	wg.Wait()

	cmd.Process.Kill()
	cmd.Wait()

	debugf("inetd: closed connection: %s\n", conn.RemoteAddr())
}

func copyLoop(wg *sync.WaitGroup, src io.ReadCloser, dst io.WriteCloser) {
	defer src.Close()
	defer dst.Close()
	defer wg.Done()

	var buf [1024]byte
	for {
		n, rdErr := src.Read(buf[:])
		if n > 0 {
			_, wrErr := dst.Write(buf[:n])
			if wrErr != nil {
				return
			}
		}
		if rdErr != nil {
			return
		}
	}
}
