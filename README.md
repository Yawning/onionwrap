## onionwrap - Delicious Onion Service Wraps.
### Yawning Angel (yawning at torproject dot org)

onionwrap is a simple application that creates a Tor Onion ("Hidden")
Service with a pre-configured port mapping and launches a child process.
It is sort of like a torsocks, but for servers, with a slightly more involved
commandline.

### WARNING

There is usually more to secure hidden service administration than simply
setting up a port mapping, and onionwrap explicitly does nothing against
application layer deanonymization of the Hidden Service.

### Dependencies

 * bulb (https://github.com/yawning/bulb)
 * tor (Requires a recent build of tor >= 0.2.7.0)

### Usage

`onionwrap
  [-control-port="URI"]
  [-debug] [-quiet]
  [-onion-key="PATH"] [-generate]
  [-inetd] [-no-rewrite] -port="" CMD [ARGS...]`

 * `-control-port` specifies the Tor control port instance to use in URI
   format.  If tor is running as a system service this will either usually
   be `tcp://127.0.0.1:9051` (default) or `unix:///var/run/control`.  The
   `TOR_CONTROL_PORT` enviornment variable can also be used to specify this.

 * Logging control arguments:
   * `-debug` enables significant amounts of debug output to stdout.  None of
     the debug information is sanitized, and thus can/will include things like
     private keys, onion service IDs, IP addresses.
   * `-quiet` suppresses informational output usually written to stdout.

 * Private key options:
   * `-onion-key` specifies the location of the private key.
   * `-generate` specifies that if the private key is missing, tor should
     generate one at runtime.

 * Service process arguments:
   * `-inetd` enables the built in inetd superserver.
   * `-no-rewrite` disables rewriting the server processes's arguments.  When
     not explicitly disabled, certain strings in the `ARGS...` vector are
     replaced with values taken from the command line options:
      * `%VIRTPORT` - The `VIRTPORT` of the Onion Service.
      * `%TPORT` - The port component of `TARGET`, if applicable.
      * `%TADDR` - The entire `TARGET` value.
   * `-port` specifies the VIRTPORT and TARGET of the Onion Service,
     separated by a ','.  As in `torrc`, the TARGET may be omitted entirely
     (`127.0.0.1:VIRTPORT`), contain only a port (`127.0.0.1:VIRTPORT`), be
     an AF_UNIX socket (`unix:/path/to/socket`), or an IP Address/Port
     combination. 

 * Enviornment variables:
   * `TOR_CONTROL_PORT` an alternative way of specifying `-control-port`.
   * `TOR_CONTROL_PASSWD` used to specify the Tor control port password, for
     people that do not use Cookie Auth.

### Examples

Create an Onion Service backed by `godoc`, discarding the private key after the
service terminates:
```
$ ./onionwrap -port="80,8080" godoc -http=:%TPORT
INFO: Created onion: [REDACTED].onion:80 -> 127.0.0.1:8080
INFO: Waiting for HS descriptor to be posted to a HSDir.
INFO: HS descriptor was posted, starting the service.
```

Create an Onion Service backed by `/bin/ls`, discarding the private key after
the service terminates:
```
$ ./onionwrap -port="23,2323" -inetd /bin/ls
INFO: Created onion: [REDACTED].onion:23 -> 127.0.0.1:2323
INFO: Waiting for HS descriptor to be posted to a HSDir.
INFO: HS descriptor was posted, starting the service.
```

Create an Onion Service backed by `nyancat`, using the private key stored in
`/home/yawning/nyan.pem`, creating a new private key if the file does not exist.
```
$ ./onionwrap -port="23,2323" -inetd -onion-key="/home/yawning/nyan.pem" -generate nyancat -t
INFO: Created onion: [REDACTED].onion:23 -> 127.0.0.1:2323
INFO: Waiting for HS descriptor to be posted to a HSDir.
INFO: HS descriptor was posted, starting the service.
```

### FAQs

 * Wait, unless I specify a PEM file, my onion's private key gets deleted?
 
   Yes.

 * I get an error "Failed to create onion service:" what's wrong?
 
   Is your tor instance new enough?  Neither Tor Browser nor Debian nor any
   other distribution package something that's recent enough (Needs commit
   `f61088ce2321be7c408b4298258a268a0a546e78` or newer).

 * Can I specify the control port password as a command line argument?
 
   No.

 * I wrapped `/bin/bash` and someone owned my box, help!

   Hahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahaha

 * I wrapped `/bin/ifconfig` and there's a scary van parked outside, help!

   Hahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahaha
   hahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahahaha

