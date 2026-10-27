# toru-relay

Toru's TURN relay. When two PCs can't reach each other directly (both behind
symmetric NAT or carrier-grade NAT, or one on a network that blocks UDP),
Call, Watch and LAN send their traffic through this server instead. It is a
[pion/turn](https://github.com/pion/turn) v5 server plus a small HTTPS
endpoint that hands out short-lived credentials. Design: [docs/lan.md](../../docs/lan.md),
"NAT traversal and relay".

The relay never sees content. Calls and streams are DTLS-SRTP and LAN is
WireGuard, both end to end. The relay's own job is to not become an open
proxy, so it:

- accepts only **TURN REST credentials**: username `expiry:peerId`, password
  `base64(HMAC-SHA1(secret, username))`. Nothing static ships in Toru; the
  secret stays on this box;
- **refuses peers in private and local ranges**: RFC 1918, loopback,
  link-local (including the cloud metadata address `169.254.169.254`),
  CGNAT `100.64.0.0/10` (which also covers the LAN overlay `100.96.0.0/16`),
  multicast, reserved and documentation blocks, IPv6 ULA, link-local and
  NAT64/6to4 prefixes;
- sends to **its own addresses only on relay ports**. Two peers both relayed
  through this server talk to each other's relay address, which is this
  host's public IP, so that has to work; SSH, the TURN listeners and the
  credential endpoint on the same IP stay out of reach;
- caps **bandwidth per user** (`-user-kbps`, both directions together; packets
  over the budget are dropped) and **per client IP** (`-ip-kbps`), and
  **allocations** per user, per client IP and in total. The user is the peer
  id the caller put in its credential request, so it is the per-IP caps that
  stop one host from minting ids to fill the relay or multiply its bandwidth;
- refuses **TCP relay allocations** (RFC 6062). Clients can still *reach* the
  relay over TCP and TLS; only the relayed leg is UDP;
- rate-limits the credential endpoint **per client IP**.

## Ports

| Port | What |
| --- | --- |
| UDP 3478 | TURN over UDP |
| TCP 3478 | TURN over TCP |
| TCP 443 | TURN over TLS (`turns:`), for networks that block UDP. Needs `-cert`/`-key` |
| TCP 8443 | `GET /turn-credentials` over HTTPS. Needs `-cert`/`-key` |
| UDP 49152-65535 | relay ports, one per allocation (`-relay-min-port`, `-relay-max-port`) |

## Flags and environment

Every flag can also be set from the environment as `TORU_RELAY_<FLAG>`, dashes
as underscores (`-public-ip` is `TORU_RELAY_PUBLIC_IP`). A flag on the command
line wins.

| Flag | Default | |
| --- | --- | --- |
| `-secret` / `-secret-file` | | shared secret, at least 16 bytes. Use the env var or a file, not the command line |
| `-public-ip` | | this host's public IPv4 address (required) |
| `-public-host` | the IP | DNS name put in the URIs; must match the certificate for `turns:` |
| `-realm` | `toru` | TURN realm |
| `-listen-ip` | `0.0.0.0` | address every socket binds to |
| `-udp-port`, `-tcp-port`, `-tls-port` | 3478, 3478, 443 | 0 turns one off |
| `-cert`, `-key` | | PEM files; re-read when they change (certbot renewals need no restart) |
| `-https-addr` | `:8443` | credential endpoint over HTTPS |
| `-http-addr` | | credential endpoint over plain HTTP, for a TLS reverse proxy in front (e.g. `127.0.0.1:8080`) |
| `-trust-forwarded-for` | off | rate-limit on the right-most `X-Forwarded-For` hop; only behind your own proxy |
| `-cred-ttl` | `12h` | how long each credential is valid; a relayed session lasts at most this long |
| `-alloc-lifetime` | `10m` | allocation lifetime the server grants by default (at most 10m) |
| `-user-kbps` | 20000 | relay bandwidth per user, kbit/s (0 = unlimited) |
| `-ip-kbps` | 40000 | relay bandwidth per client IP, all its users together, kbit/s (0 = unlimited) |
| `-max-allocs-per-user`, `-max-allocs-per-ip`, `-max-allocs` | 16, 64, 2000 | allocation caps (0 = unlimited) |
| `-cred-rate`, `-cred-burst` | 20/min, 10 | credential requests per client IP |

## The credential endpoint

```
GET https://relay.example.com:8443/turn-credentials?peer=<peerId>
```

`peerId` is 1-64 characters of `A-Z a-z 0-9 _ -` (Toru sends its InstallID).
The answer goes straight into an ICE server entry:

```json
{
  "username": "1761600000:0123456789abcdef0123456789abcdef",
  "credential": "kq3Yh0x8JcHk6rC0h1sCw2f7r+U=",
  "ttl": 600,
  "uris": [
    "turn:relay.example.com:3478?transport=udp",
    "turn:relay.example.com:3478?transport=tcp",
    "turns:relay.example.com:443?transport=tcp"
  ]
}
```

`/healthz` answers `ok <n> allocations`.

## Deploy on a Linux VPS

Any small VPS with a public IPv4 address works. Build on any machine with Go:

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o toru-relay ./cmd/toru-relay
scp toru-relay root@relay.example.com:/usr/local/bin/
```

On the VPS:

```sh
useradd --system --no-create-home --shell /usr/sbin/nologin toru-relay
install -d -m 0750 -o root -g toru-relay /etc/toru-relay
openssl rand -base64 32 > /etc/toru-relay/secret
chgrp toru-relay /etc/toru-relay/secret && chmod 0640 /etc/toru-relay/secret
```

A certificate for `turns:` and the HTTPS endpoint, e.g. with certbot.
Standalone mode answers the HTTP-01 challenge on TCP 80, which the relay
doesn't use, so it works alongside it as long as port 80 is open (renewals
too); DNS validation needs no port at all:

```sh
certbot certonly --standalone -d relay.example.com
# let the service user read the live files
chgrp -R toru-relay /etc/letsencrypt/live /etc/letsencrypt/archive
chmod -R g+rX /etc/letsencrypt/live /etc/letsencrypt/archive
```

`/etc/toru-relay/env`:

```sh
TORU_RELAY_SECRET_FILE=/etc/toru-relay/secret
TORU_RELAY_PUBLIC_IP=203.0.113.10
TORU_RELAY_PUBLIC_HOST=relay.example.com
TORU_RELAY_CERT=/etc/letsencrypt/live/relay.example.com/fullchain.pem
TORU_RELAY_KEY=/etc/letsencrypt/live/relay.example.com/privkey.pem
```

`/etc/systemd/system/toru-relay.service`:

```ini
[Unit]
Description=Toru TURN relay
After=network-online.target
Wants=network-online.target

[Service]
User=toru-relay
Group=toru-relay
EnvironmentFile=/etc/toru-relay/env
ExecStart=/usr/local/bin/toru-relay
Restart=on-failure
RestartSec=2
# Port 443 without root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
PrivateDevices=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_NETLINK AF_UNIX
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

```sh
systemctl daemon-reload
systemctl enable --now toru-relay
journalctl -u toru-relay -f
curl "https://relay.example.com:8443/turn-credentials?peer=test"
```

Firewall (ufw shown; the VPS provider's firewall needs the same):

```sh
ufw allow 3478/udp
ufw allow 3478/tcp
ufw allow 443/tcp
ufw allow 8443/tcp
ufw allow 49152:65535/udp
ufw allow 80/tcp   # only for certbot standalone (HTTP-01)
```

Keep everything else on the public IP closed. The relay already refuses to
send to its own non-relay ports, but a closed firewall is the second wall.

**Behind a reverse proxy** (Caddy, nginx) for the credential endpoint: run
with `-http-addr 127.0.0.1:8080 -trust-forwarded-for`, proxy
`/turn-credentials` to it, and leave `-cert`/`-key` set only if `turns:` on
443 is still served by the relay itself (the proxy then can't also own 443).

## Testing it

`go test ./cmd/toru-relay` runs the auth, credential endpoint, IP filter,
quota and bandwidth tests, plus a real allocate-and-echo through the server.
Every socket in them is on 127.0.0.1.

From a client PC, fetch a credential and paste it into the WebRTC
[Trickle ICE](https://webrtc.github.io/samples/src/content/peerconnection/trickle-ice/)
page (TURN URI, username, credential), then **Gather candidates**. A `relay`
candidate with the relay's public IP means allocation works; try each URI on
its own to check UDP, TCP and TLS separately. That runs in the browser, so no
local test binary has to open a socket.

## Known gaps

- **Allocation lifetime is not a hard cap.** `-alloc-lifetime` is what the
  server grants when the client doesn't ask; pion honours a client-requested
  `LIFETIME` up to one hour, and the credential's integrity check covers that
  attribute, so the relay can't rewrite it. What does bound an allocation is
  the credential: every Refresh, CreatePermission and ChannelBind must carry
  an unexpired one, so an allocation dies at most one requested lifetime after
  its credential expires (permissions lapse 5 minutes after their last
  refresh, so relayed traffic stops sooner).
- **Long relayed sessions need fresh credentials.** The same rule means a
  client loses its relay once the credential it allocated with expires. The
  `-cred-ttl` default is therefore 12 hours, not minutes: clients don't yet
  move a live allocation to a fresh credential (LAN's `SetICE` only reaches
  new agents), and the endpoint is open anyway, so a short lifetime buys
  little. Lowering it needs that client work first.
- **The credential endpoint is open.** Anyone who can reach it gets a
  credential; the per-IP rate limit and the per-IP allocation and bandwidth
  caps are the gate (the per-user caps key on a peer id the caller picks). A
  proof of room membership can be added later without changing clients much.
- **IPv4 only.** The relay hands out IPv4 relay addresses; IPv6 allocations
  fail.
