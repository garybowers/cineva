# Federation design

Status: agreed design, built in milestone 10 (post-v1). Supersedes the
pairwise-peering sketch that CLAUDE.md carried before October 2026.

## The scenario

Three households each run a Cineva server: ours, Mum and Dad's, and Rob's.
Each has its own libraries on its own disks. Linked together, they should
feel like **one Cineva**:

- Open any of the three servers and you see the same thing: everyone's
  films and series, your own watch history, the same layout.
- Sign in with your own account wherever you are, including on Mum and
  Dad's TV.
- Choose to look at just one server's libraries when you want to.
- When a server is offline, its titles quietly disappear, or play from
  another copy. They never become dead tiles.

The linked servers form a **nebula**, which is also what the product calls
it.

## Terms

| Term | Meaning |
|---|---|
| **Nebula** | The set of linked servers. A server belongs to at most one nebula. |
| **Member** | A server in the nebula. |
| **Home server** | The member a user account belongs to. It is the authority for who that user is, what they may see, and what they have watched. |
| **Owning server** | The member whose disks hold a file. It is the only one that reads or transcodes it. |
| **Serving server** | The member whose address the browser or app is talking to. It renders the UI and proxies everything else. |

One request can involve all three. Rob, visiting Mum and Dad, opens their
server (serving), signs in as himself (home: Rob's), and plays a film from
our disks (owning: ours).

## Principles

1. **One UI, served by any member.** Every member serves the same nebula-wide
   web client. There is no separate nebula website and no central server.
2. **Clients only ever talk to the serving server.** Media, metadata,
   sign-in and watch state from other members all come through it. This is
   what lets a phone at Mum and Dad's work even when our server is behind a
   router it cannot reach.
3. **Watch state lives at home.** The serving server reads and writes it on
   the user's behalf and keeps no copy beyond a short write buffer.
4. **Open by default, the owner can lock down.** Every library is visible to
   everyone in the nebula unless the server that owns it restricts it, to
   chosen users and groups from any member.
5. **Absence is invisible.** An offline member's titles leave every view, or
   switch to another copy, and come back the moment it returns.
6. **No third-party service is required.** No accounts elsewhere, nothing
   phoning home. A relay is needed only when no member can accept
   connections (see Reachability), and it can be any machine the family
   controls.

## Joining a nebula

Every server already has an Ed25519 identity keypair, created on first
start (`server_identity`, migration 0001).

**Creating:** an admin creates the nebula on their server. That server
becomes its first member.

**Inviting:** any member's admin creates a one-time invite code. The code
carries:

- the nebula id;
- one or more ways to reach the inviting member;
- the inviting member's public key fingerprint;
- a random secret;
- an expiry (default 24 hours).

**Joining:** the joining admin pastes the code into their own admin UI.

1. The joining server connects to the inviter, checks its key against the
   fingerprint, and proves it holds the secret.
2. The inviter adds it to the roster and sends the roster back: every
   member's public key and addresses.
3. The newcomer then links with every other member. They accept it because
   the inviter has added it to the roster.

**Underneath:** every pair of members has its own link, authenticated by
mutual TLS against the exchanged, pinned keys. The nebula adds the
introductions, so a family of five is one invite per server rather than ten
pairings.

**Leaving:** any server can remove itself from its nebula at any time, on
its own admin's say-so, with no one else's agreement.

- It tells the other members it is leaving, closes its links, and drops
  everything it cached from them: their items, artwork and segments.
- The others drop it from the roster and drop what they cached from it.
- Its own users' delegated sessions on the other members are revoked.
- Its users' watch state was always kept on the server itself, so nothing
  is lost.
- A member that is offline when another leaves learns of it from the
  roster when it reconnects.

**Refusing a peer:** every member keeps a local block list, so a server can
stop linking with a particular peer without anyone else's agreement. This
is the one form of removal that does not depend on the departing server.
Whether a member can eject *another* member from the nebula for everyone
is still open; see below.

## Reachability

Nothing is assumed about home routers. The design works when **no member
accepts inbound connections at all**: carrier-grade NAT at Mum and Dad's,
and no port forwarding anywhere.

**Outbound only:** each member keeps a long-lived outbound connection to a
**rendezvous**. The rendezvous:

- tells members how to reach each other;
- helps them open direct connections through their routers;
- relays the traffic when no direct connection can be made.

**Where the rendezvous runs:**

- If any member turns out to be reachable from the internet, that member is
  the rendezvous. It is decided automatically from each member's own
  reachability check.
- Otherwise it is a small relay process, the same binary with a
  `relay` subcommand, run on any machine with a public address, such as a
  cheap VPS. It is configured on one member and shared with the rest
  through the roster.

**What a relay sees:** only encrypted bytes. The mutual-TLS session runs end
to end between the two members over the relayed path, so the relay sees
neither content nor identities beyond the routing it does.

**Existing networks:** if the houses are already joined by Tailscale,
WireGuard or Slack's Nebula VPN (no relation to the name), a member's
address is simply its address on that
network, and no rendezvous is needed. Addresses are a list per member, tried
in order.

**Media is the heavy traffic.** Direct links are always preferred over
relayed ones, and the relay's bandwidth use shows in the admin.

## Metadata sync

**Pull-based, per pair:** each member pulls from each other member an
incremental feed of changes since its last cursor, covering only the
libraries shared with the nebula. Within a feed, items carry their metadata
and segments (intro and credits), not files.

**Local cache:** remote items are cached locally as rows in `items`, under a
library row whose `source` is `federated` and whose `peer_id` names the
owning member. Migration 0001 reserved both columns for this. Caching keeps
browsing instant and lets everything that queries `items` (search, genres,
collections, recommendations) work across the nebula unchanged.

**Artwork** is fetched from the owner on first view and cached like any
other artwork.

**Presence:** a heartbeat over each link decides presence. A member that
misses heartbeats is marked offline, and its cached items are filtered out
of every listing until it returns. Nothing is deleted, so it reappears
instantly.

## Libraries: merged and per server

**Merged views (the default):** one Movies and one TV Shows across the
whole nebula, rather than three of each.

- **Matching copies:** titles are matched across members by a **content
  key** that does not depend on which server scanned them:
  - films: `tmdb:movie:<id>`;
  - series: `tmdb:tv:<id>`;
  - episodes: `tmdb:tv:<id>:s<season>e<episode>`;
  - later domains: MusicBrainz release and recording ids, ISBN or Open
    Library ids for books.
- **Unmatched items** have no content key and are never merged; they appear
  once per copy.
- **Each title lists its copies**, and plays from the best available:
  1. the serving server's own copy, if there is one;
  2. otherwise, of the members online, the copy that can be played directly
     on this device;
  3. otherwise the best quality copy, then the lowest-latency link.

  The choice is made when playback starts, never fixed in the listing, so a
  member going offline only changes which copy plays.
- **A badge on each title** says where its copies are, with the per-copy
  detail on its page. This builds on the existing multi-version support for
  films (`version_of`).

**Per-server scope:** a picker in the sidebar ("All servers", "Mine",
"Mum and Dad's", "Rob's") narrows every view to one member's libraries,
shown as that member organises them. This is the "just one server's UI",
inside the same app. Going straight to a member's own address still works
too, as it does today.

**Access:** the same rule as on a single server, which has been in place
since migration 0026: a library is open to everyone until restricted.

- **Open:** an open library is visible to every user of every member.
- **Restricted:** its owning server's admin can restrict it to chosen users
  and groups. The list can name users and groups of any member, such as
  `rob@Rob's` or Rob's "Kids" group; `library_access.server_id` already
  says whose each entry is.
- **Groups stay with their server:** Rob's admin decides who is in Rob's
  "Kids", and Rob's server reports its group memberships with each user's
  identity.
- **Keeping a library private:** a library only for the household is
  restricted to that server's own users or groups. Nothing then leaves the
  server for anyone else.

## Identity and sign-in anywhere

Every account belongs to one home server. Its nebula-wide identity is the
pair (home server key, user id), shown to people as `rob@Rob's`.

**Signing in on another member:**

1. The user picks their home server on the sign-in screen, which lists the
   nebula's members.
2. They give their username and password or PIN.
3. The serving server relays the check to the home server over their
   federation link.
4. The home server checks the credential and returns a **delegated session
   token**, scoped to that serving server and that user, with an expiry.
5. The serving server keeps a local session tied to the remote identity.

The home server can list and revoke delegated sessions from its own admin,
and they lapse when the user changes their password.

**Why the credential passes through the serving server:** the browser
often cannot reach the home server at all (principle 2). So the serving
server sees the credential in transit. It never stores it, and it is a
member the family has explicitly linked with.

Without that constraint the right design would be a redirect to the home
server. An app that can reach the home server directly may use that instead.

**Authority:** the owning server decides who may see its libraries, from its
access lists. The home server vouches for who the user is and which of its
groups they belong to, and the serving server enforces the result. No
serving server can widen a library's access beyond what its owner allows.

## Watch state

**Where it lives:** watch state stays on the home server, as CLAUDE.md has
always required.

**What it is keyed by:** the content key, not a server's item id. Watching
*Dune* from Mum and Dad's copy marks *Dune* watched for you, whichever copy
you use next. Items with no content key fall back to (owning server, item
id).

**Reads and writes:** when the user is on another member, that member
forwards progress and watched flags to the home server and reads Continue
Watching and Next Up from it.

**When the home server is offline:** writes are buffered on the serving
server and pushed when the link returns, last write wins per item. This is
the same rule the offline-sync milestone uses for devices.

**Next Up and autoplay** keep working server-side, with the home server's
watched flags and the merged episode list.

## Playback

**Transcoding:** the owning server transcodes its own files with its own
hardware. The serving server proxies the stream, direct play or HLS, over
the federation link. Seeking, subtitles, audio tracks, intro and credits
behave as they do locally.

**Bandwidth:** home upload speeds are the constraint; a parents' house
might manage 10–20 Mbps. Each link measures what it can carry, and remote
playback defaults to a quality that fits. Downloads for offline viewing
come from the owning server, through the serving server, like everything
else.

## One UI, many versions

**The version problem:** each member embeds its UI in its own binary, so "a
single UI" depends on members running compatible versions.

- **Protocol version:** the federation protocol is versioned
  (`/federation/v1`). Members advertise their protocol version and a list
  of capabilities when they link.
- **Below the minimum:** a member below the nebula's minimum protocol
  version is linked but its content is hidden, with the reason shown in
  the admin.
- **Missing capabilities:** features a member lacks degrade where the user
  would meet them. For example, no intro skip on its titles if it predates
  intro detection.
- **Updates:** the admin's nebula page lists every member's version and
  flags drift ("Mum and Dad's server is two releases behind"). There is no
  auto-update and no phone-home; the prompt is for the person who looks
  after that server.

## Admin

Each member keeps its own admin for its own libraries, users, sharing and
transcoding.

A **Nebula** page adds:

- the members, with status, version, link type (direct, relayed or VPN)
  and measured bandwidth;
- what each member shares;
- invites;
- delegated sessions for this server's users;
- the relay's traffic, when there is one.

## Preparing now

These cost little before milestone 10 and a great deal after it.

1. **Content keys on items.** Compute and store the key at scan time from
   what matching already finds. It is needed for merging copies and for
   watch state.
2. **Watch state keyed by content key.** `play_state` is keyed by local item
   id today, which cannot follow a user across copies or servers. Add the
   content key alongside, and backfill.
3. **Users carry their home server's identity.** A local user is (this
   server's key, user id), so a remote one is the same shape later.
4. **A protocol version constant** and a capabilities list, even before
   there is a protocol to version.
5. **Items know their owning server.** It is implied today by the library's
   `source` and `peer_id`; keep every query that lists items going through
   the library row, so federated rows need no special cases.

## Open questions

- **Ejecting another member.** Leaving is always a server's own decision,
  and each member can block a peer locally. Should anyone also be able to
  remove a *different* server from the nebula for everyone — its inviter,
  any admin, or a majority? Needed for a server that is lost or no longer
  trusted and will never leave on its own.
- **Hosting the relay.** Does the relay ship in the same binary or as a
  separate one? Does it take every member's traffic, or only the pairs that
  cannot connect directly?
- **Disagreeing matches.** Two members can match the same file
  differently, for example a remake against the original. Their copies then
  carry different content keys and simply do not merge, which is safe. A
  "these are the same" correction would be a later feature.
- **User-level sharing.** Should a member be able to share a library with
  particular remote users rather than the whole nebula?

## Building it

Milestone 10 splits into stages. Each one works on its own.

| Stage | Delivers |
|---|---|
| 10a | Nebula membership, invites, pinned mutual-TLS links over direct addresses and existing VPNs, heartbeat. |
| 10b | Metadata sync into the local cache, and per-server libraries visible to the nebula (the per-server scope). |
| 10c | Proxied playback and downloads from the owning server. |
| 10d | Merged views: content-key matching, copy selection, source badges. |
| 10e | Sign-in anywhere and watch state at home, with delegated sessions and buffered writes. |
| 10f | Rendezvous and relay for members with no inbound connectivity. |
