// main.go - onionwrap
//
// To the extent possible under law, Yawning Angel waived all copyright
// and related or neighboring rights to onionwrap, using the creative
// commons "cc0" public domain dedication. See LICENSE or
// <http://creativecommons.org/publicdomain/zero/1.0/> for full details.

// onionwrap serves delicious Onion Service Wraps.
package main

import (
	"errors"
	"flag"
	gofmt "fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/yawning/bulb"
	"github.com/yawning/bulb/utils"
)

const (
	controlPortEnv       = "TOR_CONTROL_PORT"
	controlPortPasswdEnv = "TOR_CONTROL_PASSWD"

	localhost          = "127.0.0.1"
	defaultControlPort = "tcp://" + localhost + ":9051"
)

var debugSpew bool
var quietSpew bool

func infof(fmt string, args ...interface{}) {
	if !quietSpew {
		gofmt.Fprintf(os.Stderr, "INFO: "+fmt, args...)
	}
}

func errorf(fmt string, args ...interface{}) {
	gofmt.Fprintf(os.Stderr, "ERROR: "+fmt, args...)
	os.Exit(-1)
}

func debugf(fmt string, args ...interface{}) {
	// This explicitly overrides quietSpew.
	if debugSpew {
		gofmt.Fprintf(os.Stderr, "DEBUG: "+fmt, args...)
	}
}

func parsePort(portStr string) (uint16, error) {
	p, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return 0, err
	}
	if p == 0 {
		return 0, errors.New("invalid port '0'")
	}
	return uint16(p), nil
}

func parsePortArg(arg string) (virtPort, target string, err error) {
	// This is formated as VIRTPORT[,TARGET], which is identical to
	// what the ADD_ONION command expects out of the 'Port' arguments.
	// If the 'TARGET' is omitted, 'VIRTPORT' is mirrored.  If 'TARGET'
	// only a naked port, then '127.0.0.1:TARGET' is used, otherwise
	// 'TARGET' is treated as an address.
	//
	// TODO: Look at the rend service code to see how AF_UNIX targets
	// are handled.
	if arg == "" {
		return "", "", errors.New("no Onion Service port specified")
	}
	splitArg := strings.SplitN(arg, ",", 2)
	virtPort = splitArg[0]
	if _, err = parsePort(virtPort); err != nil {
		return "", "", err
	}
	if len(splitArg) == 1 {
		// Only a 'VIRTPORT' was provided, mirror it onto the target.
		return virtPort, localhost + ":" + virtPort, nil
	}

	target = splitArg[1]
	if _, err = parsePort(target); err == nil {
		// The 'TARGET' is a naked port.
		return virtPort, localhost + ":" + target, nil
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		return "", "", err
	}
	if tcpAddr.Port == 0 {
		return "", "", errors.New("target has invalid port '0'")
	}
	return
}

func main() {
	//
	// Parse/validate the command line arguments.
	//

	const controlPortArg = "control-port"
	ctrlPortArg := flag.String(controlPortArg, "", "Tor control port")
	flag.Lookup(controlPortArg).DefValue = defaultControlPort
	hsPortArg := flag.String("port", "", "Onion Service port")
	flag.BoolVar(&debugSpew, "debug", false, "Print debug messages to stderr")
	flag.BoolVar(&quietSpew, "quiet", false, "Suppress non-error messages")
	flag.Parse()

	// The control port is taken from the argument, the env var, and then
	// the hardcoded default in that order.
	if *ctrlPortArg == "" {
		*ctrlPortArg = os.Getenv(controlPortEnv)
		if *ctrlPortArg == "" {
			*ctrlPortArg = defaultControlPort
		}
	}
	ctrlNet, ctrlAddr, err := utils.ParseControlPortString(*ctrlPortArg)
	if err != nil {
		errorf("Invalid control port: %v\n", err)
	}

	virtPort, target, err := parsePortArg(*hsPortArg)
	if err != nil {
		errorf("Invalid virtual port: %v\n", err)
	}

	// The command that will be fork/execed.  As a convenience for
	// scripting, %VIRT_PORT in any of the arguments will be replaced
	// with the virtual port.
	cmdVec := flag.Args()
	var cmd *exec.Cmd
	switch len(cmdVec) {
	case 0:
		errorf("No command specified to wrap.\n")
	case 1:
		cmd = exec.Command(cmdVec[0])
	default:
		cmd = exec.Command(cmdVec[0], cmdVec[1:]...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	debugf("Cmd: %v\n", cmd.Args)
	debugf("CtrlPort: %v, %v\n", ctrlNet, ctrlAddr)
	debugf("VirtPort: %v Target: %v\n", virtPort, target)

	//
	// Do the actual work.
	//

	// Setup the Onion Service, after connecting to the control port.
	ctrlConn, err := bulb.Dial(ctrlNet, ctrlAddr)
	if err != nil {
		errorf("Failed to connect to the control port: %v\n", err)
	}
	defer ctrlConn.Close()
	if err = ctrlConn.Authenticate(os.Getenv(controlPortPasswdEnv)); err != nil {
		errorf("Failed to authenticate with the control port: %v\n", err)
	}

	// TODO: Support saving the PK/Loading a PK.
	resp, err := ctrlConn.Request("ADD_ONION NEW:BEST Port=%s Flags=DiscardPK", *hsPortArg)
	if err != nil {
		errorf("Failed to create onion service: %v\n", err)
	}
	var serviceID string
	for _, l := range resp.Data {
		serviceID = strings.TrimPrefix(l, "ServiceID=")
		if serviceID != l {
			break
		}
	}
	infof("Created onion: %s.onion:%s -> %s\n", serviceID, virtPort, target)

	// Launch the actual process, and block till it exits.  Cleanup
	// is automatic because tor will tear down the Onion Service
	// when the control connection gets closed.
	err = cmd.Run()
	if !cmd.ProcessState.Success() {
		os.Exit(-1)
	}
}
