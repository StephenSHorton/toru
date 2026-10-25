# Hato — send a file with a short code

Hato (鳩, *pigeon*) is Toru's carrier pigeon for files: pick a file or folder,
Toru gives you a short spoken code like `22-conformist-belfast`, the other
person types that code, and the file flies straight to them. No account, no
link that lives forever, nothing uploaded to a server you have to trust.

Hato started life as a standalone app, moved into suzuri, and now lives
natively in Toru as a page of the hub. Transport is
[magic-wormhole](https://magic-wormhole.readthedocs.io/) via the pure-Go
[`wormhole-william`](https://github.com/psanford/wormhole-william) library, so
the other end can be another Toru or the standard `wormhole` command line.

## Send

Open the hub (tray → **Open Toru…**) and click **Hato** in the left nav. Ways
in, and ways to start a send:

- **Drop zone** — drag a file or folder onto *Drop a file or folder here*.
- **Choose file…** / **Choose folder…** — the buttons under the drop zone open a
  native picker.
- **Library card** — hover a capture in **Library** and click its **Send** icon
  (*Send with Hato*). Toru starts sending that capture and switches to the
  Hato page, where the code appears.
- **Tray** — **Send a file… (Hato)** in the tray menu opens the hub straight on
  the Hato page; pick or drop the file from there.

Once the code is issued it appears in large monospace text on the send card.
**Copy code** puts it on the clipboard; **QR** shows the code as a QR image for
a phone to read; **Stop** cancels the send. The status line reads *Waiting for
the receiver…* until the other side types the code, then *Sending… NN%* with a
progress bar, then *Sent <name>*. Keep Toru open until it finishes.

Folders are sent whole: Toru walks the tree (symlinks are skipped) and the
receiver gets the same folder name with the same contents inside.

Only one send runs at a time. Starting another replaces the current one.

## Receive

On the Hato page, type the code into the **Receive** card's input (spaces or
mixed case are fine; Toru normalises them) and press Enter or click
**Receive**. The card shows the offered name and `received / total` with a
progress bar; **Cancel** aborts and removes the partial file.

Files land in the **Save to** folder shown on the card. By default that is your
`Downloads` folder (or your home folder if `Downloads` does not exist). Use
**Change…** to pick another folder, **Reset default** to go back, and **Open
folder** to open it in Explorer. When a transfer finishes the card reads
*Saved <name> to <dir>*, and **Show in Explorer** selects the new file.

Toru never overwrites: if `report.pdf` already exists the new one is saved as
`report (2).pdf`, then `report (3).pdf`, and so on. A received folder is
unpacked into `<Save to>\<name>\`, with the same `(2)` suffix rule on the folder
name. Offered names are reduced to a plain base name with Windows-illegal
characters stripped before anything touches disk.

Only one receive runs at a time, independent of any send in progress.

## Works with the wormhole CLI

Toru keeps the default magic-wormhole application ID, so it interoperates with
the reference tools. On the other machine:

```sh
pip install magic-wormhole
wormhole receive 22-conformist-belfast
```

The reverse works too: `wormhole send <path>` on any machine prints a code, and
you type that code into Toru's Receive card. Linux, macOS and Windows clients
all speak the same protocol.

## Privacy model

- The short code is the input to a **PAKE** (password-authenticated key
  exchange). Both sides derive the same session key from it; the rendezvous
  server never learns the key.
- The rendezvous server (mailbox) and the transit relay see **only ciphertext**
  and metadata such as sizes and timing. File names and contents are encrypted
  end to end.
- Each code is **single-use**. It is consumed by the first client that claims
  it and cannot be replayed.
- An attacker who does not know the code gets exactly **one guess**. A wrong
  guess fails the key exchange, both sides see an error, and the transfer is
  over; the sender has to start a new one with a fresh code.

Share the code the way you would share a short password: say it, text it, or
show the QR. Anyone who has it before the intended receiver can take the file.

## Limits

- **No hole punching.** Peers on the same network connect directly; across NATs
  the bytes ride the transit relay. The relay still sees only ciphertext, but
  throughput is bounded by the relay.
- **No resume.** A transfer that is cancelled or drops must be started again
  from the beginning, with a new code.
- **One send and one receive at a time.** Starting a new one replaces the old
  one.
- **Folders are staged as a zip first.** Sending a folder deflates the whole
  tree into a scratch file under `%TEMP%` before the code is issued, so the
  card sits on *Preparing…* for as long as that takes and the system drive
  needs roughly the folder's size free. The scratch file is removed when the
  send finishes or is stopped; one left by a crash is cleared at a later
  launch (Toru only sweeps scratch zips older than ten minutes, so a
  concurrent `wormhole send` is left alone).
- **Connecting gives up after a minute.** If nobody is offering the code you
  typed (a wrong number, or the sender already stopped), the receive card
  reports that instead of spinning forever.

## Self-hosting the relays

Both endpoints can be overridden with environment variables in Toru's
environment:

| Variable | Default when unset |
| --- | --- |
| `TORU_WORMHOLE_RENDEZVOUS` | `ws://relay.magic-wormhole.io:4000/v1` |
| `TORU_WORMHOLE_TRANSIT` | `transit.magic-wormhole.io:4001` |

Point them at your own [`magic-wormhole-mailbox-server`](https://github.com/magic-wormhole/magic-wormhole-mailbox-server)
and [`magic-wormhole-transit-relay`](https://github.com/magic-wormhole/magic-wormhole-transit-relay).
Both sides of a transfer must use the same rendezvous server; CLI users pass
`--relay-url` and `--transit-helper` to match.

## Where things live

| What | Where |
| --- | --- |
| Received files | `%USERPROFILE%\Downloads` (or your home folder) unless changed |
| Receive-folder preference | `%AppData%\toru\hato.json` → `{"receiveDir": ""}` (empty = default) |
| Temporary data (receive) | a `.part` file in the Save to folder while receiving; removed on finish, cancel or error |
| Temporary data (send) | `%TEMP%\wormhole-william-dir*`, the staged zip of a folder being sent; removed when the send ends, stale ones (older than ten minutes) at a later launch |

Back to the [README](../README.md).
