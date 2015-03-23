## onionwrap - Delicious Onion Service Wraps.
### Yawning Angel (yawning at torproject dot org)

onionwrap is a simple application that creates a Tor Onion ("Hidden")
Service with a pre-configured port mapping and launches a child process.
It is sort of like a torsocks, but for servers, with a slightly more involved
commandline.

WARNING(s):
 * There is usually more to secure hidden service administration than simply
   setting up a port mapping, and onionwrap explicitly does nothing against
   application layer deanonymization of the Hidden Service.
 * Currently only one-shot hidden services are supported.  The private key for
   the onion WILL DISAPEAR when onionwrap exits.

TODO:
 * Add support for loading/saving the Onion Service key.

Dependencies:
 * bulb (https://github.com/yawning/bulb)
 * tor (Requires my #6411 feature branch.)
