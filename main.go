// main.go - onionwrap
//
// To the extent possible under law, Yawning Angel waived all copyright
// and related or neighboring rights to onionwrap, using the creative
// commons "cc0" public domain dedication. See LICENSE or
// <http://creativecommons.org/publicdomain/zero/1.0/> for full details.

// onionwrap serves delicious Onion Service Wraps.
package main

import (
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	gofmt "fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yawning/bulb"
	"github.com/yawning/bulb/utils"
)

const (
	controlPortEnv       = "TOR_CONTROL_PORT"
	controlPortPasswdEnv = "TOR_CONTROL_PASSWD"

	localhost          = "127.0.0.1"
	defaultControlPort = "tcp://" + localhost + ":9051"

	sigKillDelay = 5 * time.Second
)

var debugSpew bool
var quietSpew bool
var noRewriteArgs bool

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

func parsePortArg(arg string) (virtPort, targetPort, target string, err error) {
	// This is formated as VIRTPORT[,TARGET], which is identical to
	// what the ADD_ONION command expects out of the 'Port' arguments.
	//
	//  * If the 'TARGET' is omitted, 'VIRTPORT' is mirrored.
	//  * If 'TARGET' only a naked port, then '127.0.0.1:TARGET' is used.
	//  * If 'TARGET' has the prefix 'unix:' the rest is treated as a path
	//    for an AF_UNIX socket.
	//  * Otherwise 'TARGET' is parsed as an IP Address/Port.
	//
	// Note: rendservice.c:parse_port_config()'s Doxygen comment lies and
	// specifies 'socket:' as the prefix, but it really is 'unix:'.
	if arg == "" {
		return "", "", "", errors.New("no Onion Service port specified")
	}
	splitArg := strings.SplitN(arg, ",", 2)
	virtPort = splitArg[0]
	if _, err = parsePort(virtPort); err != nil {
		return "", "", "", err
	}
	if len(splitArg) == 1 {
		// Only a 'VIRTPORT' was provided, mirror it onto the target.
		return virtPort, virtPort, localhost + ":" + virtPort, nil
	}

	target = splitArg[1]
	if _, err = parsePort(target); err == nil {
		// The 'TARGET' is a naked port.
		return virtPort, target, localhost + ":" + target, nil
	}
	if unixPath := strings.TrimPrefix(target, "unix:"); unixPath != target {
		return virtPort, "", unixPath, nil
	}
	tcpAddr, err := net.ResolveTCPAddr("tcp", target)
	if err != nil {
		return "", "", "", err
	}
	if tcpAddr.Port == 0 {
		return "", "", "", errors.New("target has invalid port '0'")
	}
	targetPort = strconv.Itoa(tcpAddr.Port)
	return
}

func loadPrivateKey(path string) (string, error) {
	rawFile, err := ioutil.ReadFile(path)
	if err != nil {
		return "", err
	}

	keyType, privKey, err := parsePrivateKeyPEM(rawFile)
	if err == nil {
		return keyType + ":" + base64.StdEncoding.EncodeToString(privKey), nil
	}

	return "", errors.New("invalid/unknown key file format")
}

func parsePrivateKeyPEM(raw []byte) (string, []byte, error) {
	var p *pem.Block
	for {
		p, raw = pem.Decode(raw)
		if p == nil {
			break
		}
		if p.Type == "RSA PRIVATE KEY" {
			return "RSA1024", p.Bytes, nil
		}
	}
	return "", nil, errors.New("no valid PEM data found")
}

func main() {
	//
	// Parse/validate the command line arguments.
	//

	const controlPortArg = "control-port"
	ctrlPortArg := flag.String(controlPortArg, "", "Tor control port")
	flag.Lookup(controlPortArg).DefValue = defaultControlPort
	hsPortArg := flag.String("port", "", "Onion Service port")
	hsKeyArg := flag.String("onion-key", "", "Onion Service private key")
	flag.BoolVar(&debugSpew, "debug", false, "Print debug messages to stderr")
	flag.BoolVar(&quietSpew, "quiet", false, "Suppress non-error messages")
	flag.BoolVar(&noRewriteArgs, "no-rewrite", false, "Disable rewriting subprocess arguments")
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

	virtPort, targetPort, target, err := parsePortArg(*hsPortArg)
	if err != nil {
		errorf("Invalid virtual port: %v\n", err)
	}

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
	if !noRewriteArgs {
		// Unless explicitly disabled, replace certain variables in the
		// subprocess command line arguments with values propagated from
		// the onionwrap command line.
		//
		//  * %VPORT - The 'VIRTPORT'.
		//  * %TPORT - The port component of 'TARGET'.
		//  * %TADDR - The entire 'TARGET'.
		for i := 1; i < len(cmd.Args); i++ {
			v := cmd.Args[i]
			v = strings.Replace(v, "%VPORT", virtPort, -1)
			if targetPort != "" {
				// AF_UNIX targets won't have a port.
				v = strings.Replace(v, "%TPORT", targetPort, -1)
			}
			v = strings.Replace(v, "%TADDR", target, -1)
			cmd.Args[i] = v
		}
	}

	var hsKeyStr string
	if *hsKeyArg != "" {
		if _, err = os.Stat(*hsKeyArg); err == nil {
			hsKeyStr, err = loadPrivateKey(*hsKeyArg)
			if err != nil {
				errorf("Failed to load Onion key: %v\n", err)
			}
		} else if os.IsNotExist(err) {
			errorf("Onion Key does not exit: %v\n", *hsKeyArg)
		} else {
			// Something is wrong with the argument.
			errorf("Failed to stat Onion key: %v\n", err)
		}
	}

	debugf("Cmd: %v\n", cmd.Args)
	debugf("CtrlPort: %v, %v\n", ctrlNet, ctrlAddr)
	debugf("VirtPort: %v Target: %v\n", virtPort, target)

	//
	// Do the actual work.
	//

	// Connect/authenticate with the control port.
	ctrlConn, err := bulb.Dial(ctrlNet, ctrlAddr)
	if err != nil {
		errorf("Failed to connect to the control port: %v\n", err)
	}
	defer ctrlConn.Close()
	if debugSpew {
		log.SetOutput(os.Stderr)
		ctrlConn.Debug(debugSpew)
	}
	if err = ctrlConn.Authenticate(os.Getenv(controlPortPasswdEnv)); err != nil {
		errorf("Failed to authenticate with the control port: %v\n", err)
	}

	// Initialize the Onion Service.
	var resp *bulb.Response
	if hsKeyStr == "" {
		resp, err = ctrlConn.Request("ADD_ONION NEW:BEST Port=%s Flags=DiscardPK", *hsPortArg)
	} else {
		resp, err = ctrlConn.Request("ADD_ONION %s Port=%s", hsKeyStr, *hsPortArg)
	}
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
	if serviceID == "" {
		// This should *NEVER* happen since the command succeded, and
		// the spec guarantees that this will be sent.
		errorf("Failed to determine service ID.")
	}
	infof("Created onion: %s.onion:%s -> %s\n", serviceID, virtPort, target)

	// TODO: Wait till the HS descriptor has been published?

	// Initialize the signal handling and launch the process.
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	err = cmd.Start()
	if err != nil {
		os.Exit(-1)
	}
	doneChan := make(chan error)
	go func() {
		doneChan <- cmd.Wait()
	}()

	// Wait for the child to finish, or a signal to arrive.
	select {
	case <-doneChan:
	case sig := <-sigChan:
		// Propagate the signal to the child, and wait for it to die.
		debugf("received signal: %v\n", sig)
		cmd.Process.Signal(sig)
		select {
		case <-doneChan:
		case <-time.After(sigKillDelay):
			debugf("post signal delay elapsed, killing child\n")
			cmd.Process.Kill()
			os.Exit(-1)
		}
	}

	debugf("child process terminated\n")
	if !cmd.ProcessState.Success() {
		// ProcessState doesn't give the exact return value. :(
		os.Exit(-1)
	}
	os.Exit(0)
}
