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

	onionKeyTypeRSA = "RSA1024"
	pemKeyTypeRSA   = "RSA PRIVATE KEY"
)

var debugSpew bool
var quietSpew bool
var doneChan chan error

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

	var p *pem.Block
	for {
		p, rawFile = pem.Decode(rawFile)
		if p == nil {
			break
		}
		if p.Type == pemKeyTypeRSA {
			return onionKeyTypeRSA + ":" + base64.StdEncoding.EncodeToString(p.Bytes), nil
		}
	}
	return "", errors.New("no valid PEM data found")
}

func savePrivateKey(path, keyStr string) (err error) {
	splitKey := strings.SplitN(keyStr, ":", 2)
	if len(splitKey) != 2 {
		return errors.New("failed to parse PrivateKey response")
	}

	var keyBlob []byte
	switch splitKey[0] {
	case onionKeyTypeRSA:
		// Serialize into a standard RSA Private Key PEM file.
		p := &pem.Block{Type: pemKeyTypeRSA}
		if p.Bytes, err = base64.StdEncoding.DecodeString(splitKey[1]); err != nil {
			return err
		}
		keyBlob = pem.EncodeToMemory(p)
	default:
		return errors.New("unknown key type: '" + splitKey[0] + "'")
	}
	return ioutil.WriteFile(path, keyBlob, 0600)
}

func main() {
	//
	// Parse/validate the command line arguments.
	//

	const controlPortArg = "control-port"
	ctrlPortArg := flag.String(controlPortArg, "", "Tor control port")
	flag.Lookup(controlPortArg).DefValue = defaultControlPort
	hsPortArg := flag.String("port", "", "Onion Service port")
	hsKeyArg := flag.String("onion-key", "", "Onion Service private key file")
	noRewriteArgs := flag.Bool("no-rewrite", false, "Disable rewriting subprocess arguments")
	generatePK := flag.Bool("generate", false, "Generate and save a new key if needed")
	inetd := flag.Bool("inetd", false, "Listen on the target port and fork/exec the comand per connection")
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
	if !*noRewriteArgs {
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
			if !*generatePK {
				errorf("Onion Key does not exist: %v\n", *hsKeyArg)
			}
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
		flags := " Flags=DiscardPK"
		if *generatePK {
			flags = ""
		}
		resp, err = ctrlConn.Request("ADD_ONION NEW:BEST Port=%s%s", *hsPortArg, flags)
	} else {
		resp, err = ctrlConn.Request("ADD_ONION %s Port=%s", hsKeyStr, *hsPortArg)
	}
	if err != nil {
		errorf("Failed to create onion service: %v\n", err)
	}
	var serviceID string
	for _, l := range resp.Data {
		const (
			serviceIDPrefix  = "ServiceID="
			privateKeyPrefix = "PrivateKey="
		)

		if strings.HasPrefix(l, serviceIDPrefix) {
			serviceID = strings.TrimPrefix(l, serviceIDPrefix)
		} else if strings.HasPrefix(l, privateKeyPrefix) {
			if !*generatePK || hsKeyStr != "" {
				errorf("Received a private key when we shouldn't have.\n")
			}
			hsKeyStr = strings.TrimPrefix(l, privateKeyPrefix)
			if err = savePrivateKey(*hsKeyArg, hsKeyStr); err != nil {
				errorf("Failed to save private key: %v\n", err)
			}
		}
	}
	if serviceID == "" {
		// This should *NEVER* happen since the command succeded, and
		// the spec guarantees that this will be sent.
		errorf("Failed to determine service ID.")
	}
	infof("Created onion: %s.onion:%s -> %s\n", serviceID, virtPort, target)

	// TODO: Wait till the HS descriptor has been published?
	ctrlConn.StartAsyncReader()
	doneChan = make(chan error)
	go func() {
		for {
			if _, err := ctrlConn.NextEvent(); err != nil {
				doneChan <- err
				return
			}
		}
	}()

	if *inetd {
		targetNet := "tcp"
		if targetPort == "" {
			targetNet = "unix"
		}
		runInetd(targetNet, target, cmd)
		os.Exit(0)
	}

	// Initialize the signal handling and launch the process.
	sigChan := make(chan os.Signal)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	err = cmd.Start()
	if err != nil {
		os.Exit(-1)
	}
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

	// Ensure that it's really dead.
	cmd.Process.Kill()

	debugf("child process terminated\n")
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		// ProcessState doesn't give the exact return value. :(
		os.Exit(-1)
	}
	os.Exit(0)
}
