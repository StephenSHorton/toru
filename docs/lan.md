# LAN — play LAN games with friends over the internet

LAN puts everyone in a Toru room on one shared virtual network, the way Radmin
VPN or Hamachi do. Turn it on and Windows gets a network adapter called
**Toru LAN**. Each friend gets a fixed address like `100.96.12.34`, and games
that look for servers on the local network find the friends' games as if
everyone were in the same house. There is no account and no separate VPN app:
the room code you already share for **Call** is the network name and password.

**Status: design only. Nothing below is built yet.** It is based on main at
`6829d41` (Call and screen sharing landed in `eee0fd6`, #58). It is written for
Stephen and Blake. Once a piece ships, the matching section here becomes the
user doc in the same style as [Send Files](send-files.md) and
[Auto Clicker](clicker.md), and the design notes move to the end.

In one line: **[wireguard-go](https://git.zx2c4.com/wireguard-go/about/) over
[pion ICE](https://github.com/pion/ice), on a [Wintun](https://www.wintun.net/)
Layer 3 adapter, owned by a small SYSTEM service, with a self-hosted
[pion/turn](https://github.com/pion/turn) relay for the cases where two PCs
cannot reach each other directly.** Signaling stays in the existing room code
in `internal/stream`.

## What Radmin VPN is, and why we can't read its source

Radmin VPN is closed-source freeware from Famatech (company in the British
Virgin Islands). There is no public source. Its EULA
(<https://www.radmin-vpn.com/eula/>, §1.2 and §3.1) forbids reverse
engineering, decompiling and disassembling, so nothing here comes from its
binaries. What we know comes from the copy installed on Stephen's PC
(2.1.4951.1): its INF files, its own log and registry values, the live network
state Windows reports, and its public pages.

**What the installed copy shows:**

- **A real Layer 2 adapter.** `C:\Program Files (x86)\Radmin VPN\Driver.1.1\NetMP60.inf`
  installs `RvNetMP60.sys`, a virtual Ethernet NDIS miniport (`*IfType=6`,
  802.3). ARP and IPv6 neighbor discovery cross the tunnel: the neighbor table
  maps a peer's `26.x` address to a `02-50-…` MAC.
- **One SYSTEM service owns everything.** `RvControlSvc.exe /service` runs as
  LocalSystem and holds every socket. The Qt GUI (`RvRvpnGui.exe`) has no
  sockets of its own.
- **Addresses come from Famatech's server and belong to the node.** The log
  says `Registered as #<RID>, 26.218.8.16/fdfd::1ada:810`. The address stayed
  the same across 55 service starts and a 2.0 → 2.1 upgrade, and one address
  serves every network the node joins.
- **`26.0.0.0/8` is not private space.** ARIN lists it as `DISANET26`, the US
  DoD. Hamachi borrowed `25.0.0.0/8` the same way. Toru does not copy this.
- **The Windows tweaks are what make games work:**
  - interface metric forced to **1** with automatic metric off, so games that
    broadcast on "the first adapter" pick Radmin;
  - a dummy default route `0.0.0.0/0 via 26.0.0.1` at metric 9256, never used
    for traffic, which lets Windows identify the network and mark it
    **Private**;
  - on-link routes for `26.255.255.255/32`, `255.255.255.255/32` and
    `224.0.0.0/4` on the adapter;
  - the adapter joins SSDP, mDNS and LLMNR groups and keeps File and Printer
    Sharing bound, like a normal LAN card.
- **Firewall:** only two rules. `Radmin VPN Control Service` allows anything
  inbound to the service exe on all profiles; `Radmin VPN icmpv4` allows ping
  from `26.0.0.0/8`. The "Gaming Networks block Windows services" promise has
  no matching Windows Firewall rule, so it must be enforced inside the
  service or driver.
- **Relay first, then direct.** The log uses `TcpRelay/incoming`, `TCP/incoming`
  and `Channel to <node> is replaced by TCP/incoming`. The `ProxyServers`
  registry value lists 23 IPs; the three looked up are Hetzner Cloud. The live
  "direct" link on this PC was TCP over Windows' own Teredo interface.
- **Voice and chat ride the VPN.** Radmin bundles a voice and text chat stack
  (`raudiox.dll`, `vcintcx.dll`, `vcintsx.dll`, `rchatx.dll`) that talks to
  peers over the overlay. That is the "voice rides on the LAN" idea, already
  shipping.
- **Encryption:** the site claims "end-to-end 256-bit AES" and publishes no
  cipher mode, key exchange or relay trust statement. The only evidence is a
  bundled OpenSSL 3.6.1. Treat it as marketing.

**What Toru takes from this:** one flat subnet, metric 1, on-link broadcast and
multicast routes, broadcast copied to every peer, a Private network profile
plus an inbound allow rule, a SYSTEM service with an unprivileged GUI, relay
fallback with an upgrade to direct, and blocking Windows services between
members.

**What Toru does not take:** the borrowed `26/8` range, a kernel driver of our
own (section [Layer 2 or Layer 3](#layer-2-or-layer-3)), and a central server
that hands out addresses. Toru rooms already do that job.

## How the open-source equivalents do it

| Project | Adapter | Peers find each other | Relay | Broadcast | License, and what we may use |
| --- | --- | --- | --- | --- | --- |
| [Tailscale](https://github.com/tailscale/tailscale) | Wintun (L3) | coordination server; its own "disco" path probing; STUN, UPnP/NAT-PMP/PCP | DERP over HTTPS, always first, then direct | **Not forwarded** ([forum](https://forum.tailscale.com/t/broadcast-multicast-support/156)) | BSD-3. Read `wgengine/router/osrouter/ifconfig_windows.go` (`setPrivateNetwork`, metric, MTU) and `safesocket`; `net/portmapper` is importable |
| [NetBird](https://github.com/netbirdio/netbird) | Wintun, fixed GUID | Signal service passes ICE candidates (pion fork) | TURN, later its own WebSocket relay | Not forwarded (wireguard-go drops it) | Client BSD-3; `management/`, `signal/`, `relay/` are **AGPL**, don't touch. `client/iface/bind/ice_bind.go` is the model for our bind |
| [wireguard-windows](https://git.zx2c4.com/wireguard-windows/) | Wintun | static config | none | n/a | MIT. Manager service and `\\.\pipe\ProtectedPrefix\Administrators\WireGuard\<name>` pipes |
| [ZeroTier](https://github.com/zerotier/ZeroTierOne) | own TAP (`zttap300`, a tap-windows6 fork) | root servers | root servers | native (L2) | Agent MPL-2.0 since 1.16.0; `nonfree/` controller bans incorporation into any product; the driver is GPLv2 |
| [n2n](https://github.com/ntop/n2n) | OpenVPN TAP | supernode | supernode | native (L2) | GPLv3: ideas only |
| [SoftEther](https://github.com/SoftEtherVPN/SoftEtherVPN) | own NIC driver | hub server | hub | L2 bridging | Apache-2.0; a heavier hub model than we need |

Two things every L3 design teaches:

1. **WireGuard drops broadcast.** `wireguard-go/device/send.go` picks a peer per
   packet with `allowedips.Lookup(dst)`. `255.255.255.255`, `x.x.255.255` and
   `224/4` match no peer and are dropped. Toru has to copy those packets to
   peers itself, in a layer in front of wireguard-go (section
   [Broadcast and LAN discovery](#broadcast-and-lan-discovery)).
2. **The Windows tweaks are separate from the tunnel.** Tailscale marks its
   network Private through the Network List Manager COM API and sets its own
   metric. Without that, a new adapter is an "Unidentified network" on the
   Public firewall profile and inbound game traffic is blocked.

## Layers

```
 Toru.exe (normal user, asInvoker)             toru-lan.exe (LocalSystem service, demand-start)
 ┌──────────────────────────────┐              ┌───────────────────────────────────────────┐
 │ StreamPage → LanCard (UI)    │              │ pipe server (narrow API)                  │
 │ StreamService                │  named pipe  │ identity key (never leaves the service)   │
 │   room.go  (MQTT, sealed)    │◄────────────►│ per-peer ice.Agent  ── TURN / STUN        │
 │   msgLan  ⇄ pipe Signal      │              │ iceBind  (conn.Bind over ice.Conn)        │
 │   presence: LanInfo          │              │ wireguard-go device                       │
 └──────────────────────────────┘              │ shim: broadcast fan-out, port block,      │
                                               │       anti-spoof                          │
                                               │ Wintun "Toru LAN" + winipcfg + firewall   │
                                               └───────────────────────────────────────────┘
```

From the bottom up:

### Adapter: Wintun, Layer 3

- Set `tun.WintunTunnelType = "Toru"` first (Toru must not join WireGuard's
  adapter pool, and the Wintun license §3e forbids using the WireGuard or
  Wintun name to promote Toru). Then
  `tun.CreateTUNWithRequestedGUID("Toru LAN", &fixedGUID, 1280)` from
  `golang.zx2c4.com/wireguard/tun`.
- **One fixed GUID** baked into the service. Windows then keeps one network
  profile for the adapter, so the Private category and firewall profile
  survive restarts.
- Ship the signed, unmodified `wintun.dll` 0.14.1 (amd64; arm64 if we ever
  ship arm64) from <https://www.wintun.net/builds/wintun-0.14.1.zip>. Load it
  by full path and call
  `windows.SetDefaultDllDirectories(LOAD_LIBRARY_SEARCH_SYSTEM32 | LOAD_LIBRARY_SEARCH_APPLICATION_DIR)`
  at service start, as Tailscale does.
- Address and routes through `golang.zx2c4.com/wireguard/windows/tunnel/winipcfg`:
  - `luid.SetIPAddresses([]netip.Prefix{100.96.x.y/16})` (the on-link /16 route
    and the subnet broadcast come with it);
  - on-link `255.255.255.255/32` and `224.0.0.0/4` routes on the adapter, the
    same as Radmin;
  - IPv4 interface: `UseAutomaticMetric=false`, `Metric=1`, `NLMTU=1280`,
    `Set()`. Metric 1 is applied **only while LAN is on**;
  - IPv6 left unconfigured at first.
- **No default route.** Internet traffic never goes through the overlay.

**MTU 1280 to start.** WireGuard adds 32 bytes, the outer UDP and IP add 28
(IPv4) or 48 (IPv6), and TURN ChannelData adds 4 when relayed: 84 bytes in the
worst case, so 1416 fits a clean 1500 path, but PPPoE, DS-Lite and phone
hotspots often run 1420-1460 underneath. 1280 always fits, and games rarely
send packets over about 1200 bytes. After the spike, measure raising it to
about 1380.

### Transport: wireguard-go over pion ICE

- **Engine:** `device.NewDevice(shim, iceBind, logger)` from
  `golang.zx2c4.com/wireguard/device`. It brings the Noise IK handshake,
  ChaCha20-Poly1305, rekeying, the replay window and keepalives
  (`PersistentKeepalive=20`), none of which we write.
- **`iceBind`** (new, `internal/lan/bind.go`) is a `conn.Bind`. Each peer has a
  fake endpoint (`127.77.0.<idx>`) that maps to that peer's `*ice.Conn`. `Send`
  writes to the conn behind the endpoint; one receive goroutine per conn feeds
  a shared channel. The design follows NetBird's `ice_bind.go` but is written
  fresh against our pion versions: importing NetBird would drag in
  `pion/*/v3`, its own ICE fork and its wireguard-go fork. We skip NetBird's
  UDP mux (one ICE socket per peer is simpler).
- **Per-peer `ice.Agent`** (`pion/ice/v4`, promoted from indirect to direct):
  host, srflx (STUN) and relay (TURN) candidates, trickled one by one through
  the room. There is no browser in this path, so unlike `broadcast.go` and
  `Call.tsx` we don't wait for full gathering. The smaller peer id is the
  controlling side, the same rule as `Call.tsx` `onSignal`.
- **Peer config:** `device.IpcSet` with `public_key=<their key>`,
  `endpoint=127.77.0.<idx>`, `allowed_ip=<their overlay IP>/32`.
- **The relay is untrusted by design.** Traffic is WireGuard end to end, so
  neither the MQTT brokers nor the TURN server can read or change it.

Why not the other two transports (both were costed):

- **A WebRTC DataChannel** (`Ordered: false, MaxRetransmits: 0`), copying the
  `broadcast.go` pattern, is the least new code. But pion/sctp starts at
  `initialMTU = 1191` (`pion/sctp@v1.12.0/association.go:79`) and the
  v4.2.25 `SettingEngine` has no setting to change it, so the adapter MTU would
  be about 1100. SCTP also runs congestion control on unreliable streams,
  which adds queueing under loss. Both hurt games.
- **Raw ICE plus pion/dtls** keeps broadcast native (we frame packets
  ourselves) and pion/dtls already has a replay window. It costs 37 bytes per
  record with pion's default AES-128-GCM suite. It is a fine design; we pick
  WireGuard because it is the more audited data plane, binds peers to public
  keys instead of certificates carried in signaling, and has endpoint roaming
  that makes the relay-to-direct switch clean. See
  [Open decisions](#open-decisions).

The transport sits behind a small interface (`Send`, `Recv`, `Close`, path
info), so the bind can be swapped without touching the shim, pipe or UI.

### NAT traversal and relay

- **Candidates:** host; srflx from the shared STUN list; relay from our TURN
  server on UDP 3478, TCP 3478 and `turns:` on TCP 443. The last one is what
  works on hotel, campus and office Wi-Fi that blocks UDP.
- **Connect on whatever works first.** Relay candidates are gathered in
  parallel, and ICE nominates the first working pair. ICE timeouts match
  `broadcast.go`: disconnected 5 s, failed 15 s, keepalive 2 s.
- **Upgrade from relay to direct, make-before-break.** While a peer's selected
  pair is relay, every 30 s with backoff the service builds a second
  `ice.Agent` for that peer with relay candidates turned off. If it connects,
  `iceBind` points the peer's endpoint at the new conn and closes the old
  agent. WireGuard doesn't notice beyond a roamed endpoint. **Do not use
  `Agent.Restart` for this:** in pion/ice v4.4.8 (`agent.go:2134` on) it clears
  the selected pair and goes back to Checking, which stalls the working link.
  `ice.WithRenomination` (`agent_options.go:673`) is the alternative to try if
  the second agent turns out awkward.
- **Recovery:** on ICE `failed`, back off 1, 2, 4, 8 s (cap 30 s) and start a
  fresh agent. A `ConnectivityLoop` watches `NotifyIpInterfaceChange` and
  re-dials when the user moves between Wi-Fi and Ethernet.
- **A broker outage does not tear down tunnels.** Once a tunnel is up its
  traffic never touches MQTT. A member that times out of presence (13 s)
  keeps its tunnel while ICE stays connected; it is dropped once it has been
  gone 60 s with the tunnel down, or at once when it leaves the room.
- **Hellos are resent.** The GUI keeps the service's last room-wide Hello
  and sends it again to a member that shows up (or turns LAN on) with no
  working tunnel to us, at most every 20 s per member, and room-wide when a
  broker reconnects after the send found none. Without that, a member that
  dropped us (a restart, a long outage) would never get our Hello again.
- **Optional later:** `tailscale.com/net/portmapper` (BSD-3) for UPnP, NAT-PMP
  and PCP, to raise the share of direct paths.

**The relay server** is `cmd/toru-relay`, built on the `pion/turn/v5` server
(MIT) and run on one small VPS:

- Credentials are short-lived, never shipped in the binary: username
  `expiry:peerId`, password `HMAC-SHA1(secret, username)` (the TURN REST
  scheme; pion/turn has `LongTermTURNRESTAuthHandler`). Static credentials get
  scraped and the relay becomes an open proxy.
- A small HTTPS endpoint on the same box hands them out. **Toru (the GUI)
  fetches them**, not the service, because Call and Watch need them with LAN
  off or never installed. The baseline gate is what TURN REST deployments
  usually do: per-IP rate limit, short expiry (about 10 minutes, refreshed
  while in use) and per-credential bandwidth caps. A stronger gate (proof of
  room membership) can be added later without changing clients much.
- Hardened: no relaying to RFC 1918, loopback or link-local peers (so it can't
  reach the VPS's own network), per-user bandwidth caps, allocations of
  10 minutes or less with refresh.
- Optional: a Mosquitto broker on the same box as a third broker in
  `room.go`, so the control plane does not rest only on emqx and hivemq.

### Identity and addressing

- **The key lives in the service.** On first start the service generates a
  Curve25519 key (the WireGuard static key) and keeps it in
  `%ProgramData%\Toru\lan\` with a SYSTEM-only ACL. Toru only ever sees the
  public key. One key per Windows user (by SID), so two accounts on one PC are
  two nodes.
- **One IP per install, across rooms** (Radmin's model): host part =
  `H(pubkey ‖ salt) mod 65533 + 2`, skipping `.0.0`, `.0.1` and `.255.255`, in
  `100.96.0.0/16`. The same friend has the same address in every room and
  after every restart, so "join 100.96.12.34" can be saved in a game.
- **Collisions** (about 1 in 65k per pair) are seen through presence: the
  lower public key keeps the address; the GUI sends the other node's service
  `Readdress{salt}`, and it re-hashes with the counter and stores the result.
- **Range collisions:** on `Up`, the service checks the /16 against existing
  routes (`winipcfg.GetIPForwardTable2`). On overlap (another `100.64/10` user
  such as Tailscale, or a carrier CGNAT that leaks onto the LAN) it refuses
  with *Address range in use* and the card says which adapter owns it.
- **One LAN room at a time.** Joining another room swaps peers, not the
  address.
- **Stable peer ids, with or without the service:** add a persistent
  `InstallID` (16 random bytes) to `%AppData%\toru\stream.json` and use it
  as the peer id in place of the per-run `randHex(8)` from `NewService`. It is
  GUI-level, so Call gets stable ids even for people who never set up LAN, and
  pins can key on it (section [Security model](#security-model)).

### Signaling: the existing room, in the GUI

The service never sees the room code and has no MQTT client. The GUI's
`StreamService` carries LAN signaling between the room and the pipe, exactly
like it carries call SDP today (`call.go`, `msgCall`):

- `internal/stream/wire.go`: `msgLan = "lan"`, body
  `{kind: hello|cand|offer|answer|bye, ufrag, pwd, cand, wgPub, ip}`. It is
  sealed by the room key like every other message.
- `internal/stream/service.go`: `case msgLan:` in `onMessage` (line 413)
  forwards to the pipe; replies go out through `s.send(to, msgLan, body)`
  (line 349). Encryption, the freshness window and de-duplication across both
  brokers come from `room.go` unchanged.
- Presence: `Lan *LanInfo{IP, WGPub, On}` in `presenceBody` (`wire.go:16`) and
  `Member` (`service.go:31`). Members that time out in `presenceLoop` or send
  `msgLeave` become `Drop{peer}` on the pipe, mirroring `bc.removePeer`.

Toru is a single-instance tray app (`SingleInstance`,
`DisableQuitOnLastWindowClosed`), so the GUI process is running whenever the
LAN is. If Toru quits, the service keeps existing tunnels up but no new peers
join; it takes the adapter down after the same 60 s rule.

### The service and the pipe

- `toru-lan.exe` (run with no command by the SCM), a LocalSystem service named `ToruLan`, built on
  `golang.org/x/sys/windows/svc` and `svc/mgr` (x/sys v0.43.0 is already a
  direct dependency). **Demand-start**: Toru starts it when LAN is turned on;
  the service's own security descriptor lets interactive users start and stop
  it, so that needs no UAC. Recovery action: restart on failure.
  `SERVICE_SID_TYPE_UNRESTRICTED`.
- Pipe `\\.\pipe\ProtectedPrefix\Administrators\Toru\lan` via
  `github.com/Microsoft/go-winio` `ListenPipe` (already indirect, v0.6.2; make
  direct) with `D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)`. ProtectedPrefix
  stops a non-admin process from squatting the name.
- **Client check:** the service binds a LAN session to the Windows session of
  the client that sent `Up` (`GetNamedPipeClientSessionId`), and rejects
  clients from other sessions until `Down`. Comparing against the active
  console session would break RDP users. **No exe-path check:** Toru installs
  to `%LOCALAPPDATA%\Programs\Toru`, which the user can write, so a path check
  stops nothing.
- **The whole API** (JSON lines, versioned):
  - GUI → service: `Hello{protocol}`, `Up{roomTopicHash, iceServers}`,
    `SetICE{iceServers}` (fresh TURN credentials, fetched by the GUI),
    `Signal{peer, blob}`, `Drop{peer}`, `Readdress{salt}` (the GUI saw an
    address collision in presence; the service owns the address and
    re-derives it), `SetPolicy{allowServices, mdns}`, `Sign{blob}` (the
    service signs a `lan-admit` with its key for approval mode), `Down`,
    `Status`.
  - Service → GUI: `Hello{protocol, version, pubKey, ip}`, `Signal{peer, blob}`,
    `PeerState{peer, state, path: direct|relay, rttMs}`, `Error{code}`.
  - The pipe never accepts routes, firewall rules, file paths or addresses
    outside the overlay. A compromised user process can at worst run a LAN.

### Firewall and network profile

- **Private category** through `INetworkListManager` (go-ole, already
  indirect): match the adapter GUID, `SetCategory(1)`, retry until NLA has
  identified the network (Tailscale `setPrivateNetwork`, reimplemented with
  its notice if code is copied). Fall back to Radmin's high-metric dummy
  gateway only if NLA leaves the adapter Unidentified.
- **Inbound allow rule** `Toru LAN (inbound)`, created by the setup exe through
  `INetFwPolicy2`: allow, all programs, `InterfaceAlias="Toru LAN"`,
  `RemoteAddress=100.96.0.0/16`, any profile (it is pinned to the adapter, so
  a physical NIC never gets it). Plus `Toru LAN ICMP` (echo) for the same
  range, so **Ping** works. Without the rule every game raises a Windows
  Security Alert, and on a Public network that prompt defaults to block.
- **Windows services stay closed by default, in the data path.** The rule
  above opens SMB (445), RPC (135), NetBIOS (137-139), RDP (3389) and WinRM
  (5985-5986) to every member, whatever the profile. The shim drops inbound
  TCP and UDP to those ports unless the room's **Allow file sharing and remote
  desktop** toggle is on. Because it sits in the service, it holds even if the
  user's firewall is wide open.
- **Known limit:** if the user once clicked *Don't allow* on a game's firewall
  prompt, Windows keeps a per-exe block rule, and block beats allow. The doc
  and the card's help link say how to remove it.

## What is reused from internal/stream

| Reused | Where | How |
| --- | --- | --- |
| Room code, scrypt key and topic, sealed envelopes, two brokers, de-duplication, ±2 min window | `internal/stream/room.go` | unchanged; LAN signaling is one more message type |
| Message switch and `send()` | `service.go` `onMessage` (413), `send` (349) | add `case msgLan:` |
| Presence and leave lifecycle | `service.go` `presenceLoop` (382), `wire.go` `presenceBody` | add `LanInfo`; hook removals like `bc.removePeer` |
| Smaller-id-offers rule | `frontend/src/routes/Call.tsx` `onSignal` | same rule picks the ICE controlling side |
| ICE timeouts | `broadcast.go` `SetICETimeouts(5s, 15s, 2s)` | same values on the LAN agents |
| STUN list | `broadcast.go:25-27`, copied in `Call.tsx:15-16` and `Watch.tsx:16-17` | becomes **one** Go-provided list (with TURN) served to all three |
| Room UI | `frontend/src/stream/StreamPage.tsx` (`RoomCard`, `MemberList` at 324) | `LanCard` after the member list; LAN chips in `MemberList` |
| Test harness | `internal/stream/e2e_windows_test.go`, `callbot_windows_test.go` | two in-process peers; a fake `tun.Device` |
| pion stack | `go.mod`: webrtc v4.2.25 direct; ice v4.4.8, turn v5.1.2, stun v4.0.1, dtls v3.1.15 indirect | ice and turn become direct |

New code lives in `internal/lan` (bind, agent, shim, pipe client and server,
adapter) and two commands: `cmd/toru-lan` (the service, and its own
elevated `install` / `uninstall`) and `cmd/toru-relay`.

## How Call and screen sharing get easier

1. **The relay fixes calls and streams with LAN off.** Today Go and both JS
   pages use STUN only. Two PCs behind symmetric NAT or CGNAT on both ends
   cannot connect at all: no call, no stream. The shared ICE list with our
   TURN server fixes that for **Call**, **Watch** and the streamer in one
   change, and it ships before anything else in LAN. This is the biggest win
   and it needs no service or admin.
2. **With LAN on, calls ride an already-punched path.** Each side has a host
   candidate on its `100.96.x.y` address, and ICE prefers host pairs over
   srflx and relay, so the call connects across the overlay with no STUN luck.
   The Call window has microphone permission, and Chromium shows raw host IPs
   to pages that have it, so both sides of a voice call expose their overlay
   address. Watch has no microphone permission and hides its host IPs behind
   mDNS names; the Go streamer still offers its raw overlay address, so the
   viewer should reach it as a peer-reflexive pair. That last part is to be
   confirmed in the spike.
3. **Diagnostics.** Presence shows each member's LAN state and *Direct* or
   *Relayed* path, so "why won't the call connect" becomes visible.
4. **Stable ids** from the `InstallID` (above) make members and pins
   consistent across restarts for Call too, LAN or not.
5. **Later, optional:** the streamer could address viewers by overlay IP and
   skip per-session gathering (the 2.5-4 s full-gather wait). Voice as plain
   RTP to overlay IPs, the way Radmin's bundled voice works, is not worth it
   while the WebView call works.

Call and Stream **never depend on the service**. They keep working with LAN off
or never installed. Running a call over LAN costs double encryption
(WireGuard and DTLS-SRTP) and a smaller MTU, which WebRTC's roughly 1200-byte
packets fit.

## Broadcast and LAN discovery

**The shim** (`internal/lan/shim.go`) is a `tun.Device` between Wintun and
wireguard-go.

Outgoing (read from Wintun), by IPv4 destination:

| Destination | Action |
| --- | --- |
| a peer's overlay IP | pass to wireguard-go (unicast) |
| `255.255.255.255` | copy to every peer |
| `100.96.255.255` (subnet broadcast) | copy to every peer |
| `224.0.0.0/4` | copy to every peer, after filters |
| anything else, and IPv6 | drop |

Each copy is wrapped in a unicast UDP packet to the peer's overlay IP on a
reserved port (proposed **UDP 7787**; 41641 is Tailscale's default and would
confuse anyone reading captures) with a 4-byte `TBC1` marker, then sent through
wireguard-go. A second marker, `TEF1`, is reserved for whole Ethernet frames,
so an L2 backend later needs no protocol change. So broadcast is encrypted and authenticated like everything else.
The wrapper adds 32 bytes, so a broadcast larger than MTU − 32 (1248 at
MTU 1280) is dropped and counted; game discovery packets are far smaller.

Incoming: the shim recognizes the reserved port from an overlay source,
strips the wrapper, checks that the inner source IP is that peer's overlay IP
(anti-spoofing), and writes the inner packet into Wintun **unchanged**. Every
peer is on the same /16, so the source is already on-link, no checksum fix is
needed, and Factorio's same-subnet check passes. Loops can't form: packets
written into Wintun never come back out of `Read`. The same receive path
applies the Windows-services port block.

**Filters:** drop IGMP and LLMNR (`224.0.0.252`); mDNS (`224.0.0.251`) and SSDP
(`239.255.255.250`) are off by default (they leak host names and services and
make the Windows network browser noisy; toggle in Advanced); per source at most
200 packets/s and 256 kB/s of broadcast; TTL left alone.

**The Windows limited-broadcast quirk.** Winsock sends `255.255.255.255`, and
multicast with no `IP_MULTICAST_IF`, out of the lowest-metric route only. With
metric 1 and the on-link `/32` and `224/4` routes, that is Toru LAN while LAN is
on. This also fixes games that pick "the first adapter" or put their own IP in
the payload (DirectPlay, Age of Empires 2). The cost: while LAN is on, those
games look for servers on Toru LAN, not the home network. The card says so,
along with *Turn LAN on before you start the game* (multicast group
membership binds to whichever interface was the default when the game
joined). A WinIPBroadcast-style re-sender that keeps the physical LAN working
is a later option; WinIPBroadcast is GPL-3, so it would be reimplemented.

### Game compatibility

Nothing here has been tested on Toru yet. "Source" is how sure the research
was about the game's mechanism.

| Game | How players find each other | On LAN (L3 + fan-out) | Source |
| --- | --- | --- | --- |
| Minecraft Java | multicast `224.0.2.60:4445` every ~1.5 s, TTL 1; direct IP always works | Yes, with LAN on before launch | Verified ([LanServerPinger](https://maven.fabricmc.net/docs/yarn-1.21.7+build.1/net/minecraft/client/network/LanServerPinger.html)) |
| Minecraft Bedrock | UDP 19132 broadcast | Yes | General knowledge |
| Warcraft 3 | UDP 6112 to `255.255.255.255` | Yes, needs metric 1 | Verified (Cisco thread, udp-broadcast-relay example) |
| StarCraft / Brood War | UDP 6111 broadcast (old versions IPX) | Yes | General knowledge |
| Age of Empires 2 (original / HD), DirectPlay titles | broadcast UDP 47624, TCP 2300-2400, own IP in payload | Yes: metric 1 makes the Toru IP the embedded one; the rule covers the ports | General knowledge |
| Age of Empires 2 DE | lobby through online services | Yes (plain L3) | General knowledge |
| Factorio | broadcast to `.255` on 34196, then 34197; may reject off-subnet broadcasts | Yes: shared /16, packets unchanged; direct connect works anyway | Forum ([Factorio](https://forums.factorio.com/viewtopic.php?p=575306)) |
| Terraria | join by IP, 7777 | Yes | General knowledge |
| Stardew Valley | join by IP, 24642 | Yes | Port verified ([stardewvalley.net](https://www.stardewvalley.net/multiplayer/)) |
| Valheim, Diablo 2 (TCP 4000), other join-by-IP games | unicast | Yes | General knowledge |
| IPX titles through IPXWrapper, default UDP mode (C&C, Red Alert, Diablo 1, Warcraft 2) | UDP broadcast | Yes, if IPXWrapper is in its default mode | IPXWrapper README |
| IPXWrapper "Real IPX" mode, raw Ethernet, NetBEUI, MAC-dependent software | Ethernet frames | **No.** The only class that needs L2 | — |

### Layer 2 or Layer 3

Radmin and ZeroTier are L2; Wintun is L3 only (`WintunAllocateSendPacket` takes
IP packets, no Ethernet). Everything in the table except the last row works on
L3 once broadcast is fanned out and the Windows tweaks are applied. L2 would
cost:

- a forked tap-windows6 (or zttap300) with a new component ID, its GPLv2 source
  published as a separate component;
- an EV code-signing certificate on a hardware token, a Partner Center
  account, and attestation signing again after every driver change (Microsoft
  labels attestation "for testing purposes only", a policy risk);
- an HVCI-compatible driver, since Memory Integrity is on by default on new
  Windows 11 installs;
- a TAP binding, MAC assignment and a learning switch in the service.

Shortcut, unverified: redistribute OpenVPN's signed `tap0901`, sharing its ID
with any installed OpenVPN; whether it loads on Windows 11 with Secure Boot is
unconfirmed. Everything above the adapter (service, pipe, rooms, ICE,
WireGuard, relay, UI) would stay the same with an L2 backend.

## Elevation, the service and installer changes

Wintun's SDDL (`api/main.c:68-69`,
`O:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)S:(ML;;NWNRNX;;;HI)`) lets only SYSTEM or an
elevated High-integrity process open the adapter. Toru is `asInvoker`
(`build/windows/wails.exe.manifest`) and installs per-user with no UAC
(`.github/workflows/release.yml:67-74`: `-DWAILS_INSTALL_SCOPE=user
-DREQUEST_EXECUTION_LEVEL=user`). So the release installer can't register a
service, and that stays true: switching the whole installer to machine scope
would make every auto-update prompt UAC.

**First time LAN is turned on:**

1. The card says *LAN needs a one-time administrator setup* and **Set up**.
2. Toru runs `toru-lan.exe install` with `ShellExecuteEx` verb `runas`: one UAC
   prompt.
3. `toru-lan.exe install` checks the SHA-256 of `wintun.dll` against
   `api.WintunSHA256` (Authenticode once releases are signed), copies itself
   and the DLL to `%ProgramFiles%\Toru\lan\`, registers `ToruLan` (demand-start,
   SD that lets interactive users start and stop it), and creates the two
   firewall rules.
4. After that, turning LAN on and off needs no prompts.

**Why Program Files:** a SYSTEM service registered from the user-writable
`%LOCALAPPDATA%\Programs\Toru` would let any process running as the user
replace the exe or plant a DLL and become SYSTEM.

**Updates:** the silent per-user update (`internal/update/service.go`, `/S
/NORELAUNCH`) can't touch Program Files. The service reports its version and
pipe protocol in `Hello`. When the GUI is newer, the card shows **Update LAN
(needs admin)**, which re-runs `toru-lan.exe install`: stop, replace, start. Keep
mechanism in the service and policy in the GUI so service changes are rare,
and keep the pipe protocol backward compatible within a major version. A
service that updates itself from GitHub would be an unsigned SYSTEM-level
auto-updater (SHA256SUMS comes from the same release, so it adds no
independent trust); only consider it once releases are Authenticode-signed.

**Uninstall:** **Remove LAN** in Advanced, and the NSIS uninstaller when the
service exists, run `toru-lan.exe uninstall` elevated: stop and delete
the service, remove the adapter (and the Wintun driver only if no other Wintun
adapters remain), delete the firewall rules, `%ProgramFiles%\Toru\lan` and
`%ProgramData%\Toru\lan`. The per-user uninstaller can't do machine-level work
silently, so this shows a UAC prompt; refusing it leaves the service in place
and says so.

**Installer and release changes:**

- `release.yml`: build `toru-lan.exe`, check the vendored `wintun.dll` against
  `api.WintunSHA256`, add both (and Wintun's license) to SHA256SUMS.
- `build/windows/nsis/project.nsi`: install `toru-lan.exe` and
  `wintun.dll` (plus Wintun's `LICENSE.txt`) into the per-user folder as the
  payload; uninstall hook as above.
- `build/windows/info.json`: replace the Wails placeholders (`"© 2026, My
  Company"`, `"This is a comment"`, the old "macOS-style screenshot"
  description) in the same change. They appear in the UAC dialog and the
  service metadata.
- `THIRD_PARTY_NOTICES`: WireGuard, Wintun, and anything adapted from Tailscale
  or NetBird.

## Security model

- **Who can join:** anyone with the room code, the same as Call today. The code
  keys the sealed room; brokers see a topic, timing and sizes, never content.
- **Keys:** each node's WireGuard key is generated and kept by the service;
  public keys travel in sealed room messages. LAN traffic is WireGuard end to
  end, so the brokers and the TURN relay can neither read nor alter it.
- **Pins:** the first time Toru sees a member it records `{InstallID → wgPub,
  name}` in `stream.json`. The peer id is the `InstallID`, not derived from the
  key, so a changed key under a known id is detectable. A known member showing up with a different key
  gets a warning badge in `MemberList`, and with **Approve new members** on,
  is kept off the LAN.
- **Approval (optional):** the room creator approves keys and sends sealed,
  signed `lan-admit{wgPub}` messages; peers add only admitted keys.
- **Eviction:** **New code** creates a fresh room code and re-invites pinned
  members. It is the only way to drop someone who already has the code.
- **What members can reach:** every port the firewall allows on every member's
  PC, minus the Windows services the shim blocks by default. Games are
  reachable on purpose.
- **Anti-spoofing:** a peer can only send from its own overlay address (WireGuard
  AllowedIPs for unicast, the shim check for broadcast).
- **Local privilege:** service binaries in admin-only Program Files, DLL loaded
  by full path, the pipe API can't express routes, rules or paths, private key
  never leaves the service.
- **Relay:** short-lived HMAC credentials from a membership-gated endpoint, no
  relaying into private ranges, bandwidth caps.
- **Honest limits:**
  - Releases are not Authenticode-signed yet, so UAC shows *Unknown publisher*,
    and malware already running as the user could swap the per-user setup exe
    before the user approves the prompt. That is the same trust the per-user
    Toru install already has; signing is the fix.
  - Anyone in the room can see who else is in it and every member's overlay
    IP.

## Build order

No step depends on a later one; each can be tested on its own.

1. **Spike S1, the go/no-go.** An elevated test binary on two PCs: Wintun with
   the fixed GUID, winipcfg (/16, metric 1, `/32` and `224/4` routes), Private,
   firewall rule, a hard-coded WireGuard peer over raw UDP, and the broadcast
   wrapper. Pass: Warcraft 3 lists the other PC's game, Minecraft Java shows
   the LAN world, Factorio finds the server, and a Watch stream pairs over the
   overlay. Also one run forced through TURN. If Windows does not deliver
   injected broadcasts to sockets, the design moves to L2 before any service
   or UI work.
2. **Shared ICE list.** One Go-provided list, served to `Call.tsx` and
   `Watch.tsx` through a binding, replacing the three copies.
3. **`cmd/toru-relay`:** pion/turn server, the credential endpoint, hardening
   flags; TURN entries in the shared list. Test both PCs behind a phone
   hotspot or CGNAT, and on a UDP-blocked network (`turns:443`). Calls and
   streams between hard NATs work from here, with LAN off.
4. **`cmd/toru-lan install|uninstall`:** elevated install, update and uninstall; payload
   hash check; service SD; firewall rules through COM.
5. **`cmd/toru-lan` (service) and `internal/lan/pipe`:** service host, versioned pipe
   API, session check, key generation and storage.
6. **`internal/lan/adapter`:** Wintun bring-up, winipcfg, Private category with
   retry, range-overlap check, metric only while on.
7. **`internal/lan/bind.go` and `agent.go`:** the ICE-backed `conn.Bind`, trickle,
   TURN, make-before-break upgrade, backoff and network-change re-dial.
8. **`internal/lan/shim.go`:** fan-out, wrapper, anti-spoof, Windows-services
   block, filters and rate limits.
9. **GUI bridge in `internal/stream`:** `msgLan`, `LanInfo` in presence and
   `Member`, `Drop` on timeout and leave, `runas` launch of the setup exe.
10. **Identity extras:** `InstallID`, address derivation and the `Readdress`
    collision rule, TOFU pins,
    optional approval and **New code**.
11. **UI:** `LanCard` in `StreamPage.tsx` after the member list, LAN chips in
    `MemberList`, a `LanService` registered in `main.go` `Services`, bindings
    regenerated with `build/Taskfile.yml generate:bindings`.
12. **Release plumbing:** `release.yml`, `project.nsi`, `info.json`,
    `THIRD_PARTY_NOTICES`; a version-mismatch test against an older service.
13. **Tests:** shim unit tests on a fake `tun.Device`; an in-process two-peer
    test of bind and agent with a mock TURN, shaped like
    `e2e_windows_test.go`; a pipe test that rejects a client from another
    session; the two-PC game run from S1 repeated on the real build.
14. **Optional:** Port share fallback (below), portmapper, third MQTT broker,
    WinIPBroadcast-style re-sender, IPv6 ULA prefix for Minecraft's
    `ff75:230::60`.

## The UI

On the **Call** page, after the member list (no new window, no new nav item:
LAN shares the room):

- **LAN** switch. First time: *LAN needs a one-time administrator setup* →
  **Set up**.
- Status: *Starting…*, *On — 100.96.12.34* with **Copy**, *Update LAN (needs
  admin)*, *Address range in use*, *Off*.
- Notes: *Turn LAN on before you start the game.* *While LAN is on, LAN games
  look for servers on Toru LAN, not your home network.*
- Per member in `MemberList`: overlay IP with copy, *Direct* / *Relayed* /
  *Connecting…* / *LAN off*, ping in ms, **Ping**, key-changed badge.
- **Advanced:** **Allow file sharing and remote desktop** (off), **Share mDNS
  and SSDP** (off), **Approve new members** (off), **Remove LAN**.

## Open decisions

Each has a recommendation; Stephen picks.

1. **Relay.** None (symmetric-to-symmetric pairs never connect, for LAN or
   calls); public free TURN (unknown reliability); Cloudflare Realtime TURN
   (managed, still needs our credential endpoint because the API key can't
   ship); self-hosted pion/turn on a VPS. **Recommend self-hosted pion/turn
   with the credential endpoint**, URL configurable, Cloudflare addable as a
   second entry.
2. **L2 or L3.** **Recommend L3 on Wintun**; revisit only if S1 fails or a Real
   IPX / MAC-dependent game matters to the group.
3. **Transport.** wireguard-go over ICE (more new code: bind and shim);
   raw ICE plus pion/dtls (native broadcast, pion already shipped); DataChannel
   (least code, MTU about 1100, SCTP queueing). **Recommend wireguard-go over
   ICE**, with raw ICE plus DTLS as the fallback behind the same interface.
4. **Address range.** A /16 in `100.64/10` (collides with Tailscale users and
   some carrier CGNAT) or a fixed `10.x/16` (collides with home and office
   LANs). Never `25/8` or `26/8`. **Recommend `100.96.0.0/16` with the
   route-overlap check.**
5. **Broadcast mode.** Metric 1 while on (Radmin behaviour; home-LAN discovery
   off meanwhile) or a re-sender that leaves the metric alone (more capture
   work). **Recommend metric 1 only while LAN is on**, re-sender later if
   anyone misses home-LAN discovery.
6. **mDNS and SSDP.** **Recommend off by default** with the Advanced toggle.
7. **Windows services between members.** **Recommend blocked by default** in
   the shim, per-room toggle.
8. **Who can join.** Code only, or code plus pins plus optional approval.
   **Recommend code plus TOFU pins, approval off by default.**
9. **Service start.** Demand-start (no always-on SYSTEM process) or Automatic.
   **Recommend demand-start.**
10. **Update path.** Re-run the setup exe with UAC, or a self-updating service.
    **Recommend UAC re-run** until releases are Authenticode-signed; signing
    itself is worth deciding alongside this.
11. **No-admin fallback.** A **Port share** mode using
    `wireguard/tun/netstack` inside the unelevated app that forwards one chosen
    port ("expose 7777") for users who refuse UAC; join-by-IP only, no
    discovery. **Recommend later, only if someone asks.**
12. **Calls over the overlay.** Leave Call and Stream on their own ICE (they
    pick the overlay automatically when it is up) or force them onto it.
    **Recommend leaving them on their own ICE** with the shared TURN list.
13. **Name.** **LAN** for the switch and docs, **Toru LAN** for the Windows
    adapter (users see it in Network Connections and firewall rules).
    Alternatives: *Play Together*, *Virtual LAN*. **Recommend LAN.**

## Licenses

Toru stays MIT (`LICENSE`).

| Component | License | Use |
| --- | --- | --- |
| `golang.zx2c4.com/wireguard` (`device`, `tun`, `conn`) | MIT | import (new) |
| `golang.zx2c4.com/wireguard/windows/tunnel/winipcfg` | MIT | import (new) |
| `wintun.dll` 0.14.1, signed | Wintun Prebuilt Binaries License | ship unmodified alongside software that uses only `wintun.h` (§3d); keep notices (§3c); no WireGuard/Wintun names in promotion (§3e) |
| Wintun driver source | GPL-2.0 | never linked; DLL API only |
| pion ice, turn, stun, dtls, webrtc | MIT | already in `go.mod` |
| `golang.org/x/sys/windows/svc`, `svc/mgr` | BSD-3 | direct already |
| `github.com/Microsoft/go-winio` | MIT | promote to direct |
| `github.com/go-ole/go-ole` | MIT | promote to direct |
| Tailscale (`osrouter/ifconfig_windows.go`, `safesocket`, `net/portmapper`) | BSD-3 | reimplement, or copy/import with notice |
| NetBird client `iface/bind` | BSD-3 | design reference; notice if adapted |
| NetBird `management/`, `signal/`, `relay/`, `combined/` | AGPL-3 | do not use |
| ZeroTier `nonfree/` | source-available, no product use | do not use |
| n2n, WinIPBroadcast, IPXWrapper | GPL | ideas only |
| tap-windows6 / zttap300 | GPL-2 | only on the L2 path, as a separate published driver |

## Still to check

- S1: injected broadcast and multicast reach game sockets (the whole L3 plan
  rests on it).
- Watch pairing over the overlay when the WebView viewer hides host IPs behind
  mDNS.
- NLA behaviour on the fixed-GUID adapter after a reboot (does Private stick
  without retries).
- The `INetworkListManager` CLSID (`{DCB00C01-570F-4A9B-8D69-199FDBA5723B}`)
  against Tailscale's `util/winutil/winnet`.
- Raising the MTU from 1280 to about 1380 on real paths.
- Relay credential lifetime against long relayed sessions. Every TURN
  Refresh, CreatePermission and ChannelBind needs an unexpired credential, so
  a relayed link drops when the credential it allocated with expires. Decided
  for now: `toru-relay -cred-ttl` defaults to 12 h. Moving live allocations
  to fresh credentials (LAN agent, Call) is the later fix. See
  [`cmd/toru-relay`](../cmd/toru-relay/README.md#known-gaps).
- The relay's 10-minute allocation lifetime is the default, not a hard cap:
  pion/turn v5 honours a client-requested `LIFETIME` under one hour. The
  credential expiry is what bounds it today.

## Where things will live

| What | Where |
| --- | --- |
| Service and Wintun DLL | `%ProgramFiles%\Toru\lan\` (`toru-lan.exe`, `wintun.dll`, `LICENSE.txt`) |
| Setup payload (per-user install) | `%LOCALAPPDATA%\Programs\Toru\` (`toru-lan.exe`, `wintun.dll`, `wintun-LICENSE.txt`: what `install` copies) |
| Node key and assigned address | `%ProgramData%\Toru\lan\` (SYSTEM only) |
| Pins and LAN toggles | `%AppData%\toru\stream.json` |
| Windows service | `ToruLan` (demand-start, LocalSystem) |
| Pipe | `\\.\pipe\ProtectedPrefix\Administrators\Toru\lan` |
| Adapter | **Toru LAN** (Wintun, tunnel type `Toru`) |
| Firewall rules | `Toru LAN (inbound)`, `Toru LAN ICMP` |
| Code | `internal/lan/`, `cmd/toru-lan/`, `cmd/toru-relay/` |

Back to the [README](../README.md).
