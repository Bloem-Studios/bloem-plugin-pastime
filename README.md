# Bloem Pastime plugin

> [!CAUTION]
> **Offered without support.** Pastime is provided as-is, with no support of any kind. Issues are turned off, questions go unanswered, and pull requests are not accepted: they are closed without review. You are free to fork it under the AGPL v3.

This is a plugin for [Bloem Server](https://github.com/Bloem-Studios/bloem-server). It links
a Bloem profile to a profile on a [Pastime](https://github.com/Bloem-Studios/pastime)
server and keeps the two in step: what the profile watches, rates, favourites and
puts on its watchlist goes to Pastime, and what Pastime recorded elsewhere comes back.
It is for people who run both Bloem Server and Pastime.

Bloem Server's watch sync comes from [Silo](https://github.com/Silo-Server/silo-server), the
media server Bloem Server is built on. The plugin is a watch provider for that sync: the
plugin capability is `watch_sync_provider.v1`, with ID `pastime`, in plugin `bloem.pastime`.
Pastime's own pages, and the messages the plugin hands to Bloem Server, say "Silo" for this
reason.

## What it does

For each connected profile the plugin sends Pastime:

- live playback: start, pause and stop, with the position and the runtime;
- finished plays from the watch history, each recorded once even if it is sent again;
- favourites, watchlist entries and ratings, added, changed and removed. Favourite and
  watchlist removals go out only when the connection's **Sync favorite removals** and **Sync
  watchlist removals** switches are on;
- with each film or episode, its TMDB, IMDb and TheTVDB IDs. For an episode it also sends the
  show's IDs, the season and episode numbers, and the episode and show titles.

It brings back from Pastime and gives to Bloem Server:

- favourites, watchlist entries and ratings;
- finished plays and resume points that Pastime recorded from somewhere other than Bloem
  Server, for example marked by hand, imported from another tracker or reported by another
  media server. Plays that Bloem Server sent are never read back. The connection's
  **Import watched history** and **Import paused progress** switches control these;
- removals that Pastime names: a removed favourite, watchlist entry or rating, and a movie or
  show deleted from the profile's Pastime library. Bloem Server applies a removal only to
  items this connection synced and only as the connection's removal settings allow. A deleted
  movie or show removes the profile's watch history of it up to the deletion time and keeps
  later watches.

How it behaves:

- Ratings are whole stars. Pastime stores one to five stars, which are the even values 2 to 10
  on Bloem's ten-point scale. An odd rating is not sent; its result tells the person to use an
  even one.
- An item that is missing from a page of Pastime's lists is never treated as removed. Only a
  removal that Pastime names counts.
- Titles are matched by TMDB, IMDb and TheTVDB IDs. A title that came from Pastime can also
  be named by Pastime's own catalog number.
- Batches hold 1 to 100 changes. Each change gets its own result, so one that Pastime refuses
  does not stop the others.
- Each request to Pastime times out after 20 seconds. Changes that do not fit before Bloem
  Server's call deadline wait for the next run. When Pastime asks for a pause, the plugin
  passes it on for at most five minutes (one minute if Pastime gives no time).
- If Pastime is down or the address is wrong, the whole batch waits for the next run and
  nothing is lost. A problem with one change affects only that change. A refused key asks the
  person to create a new one.

What it does not do:

- It does not sync unwatched marks, dropped shows or watchlist order.
- It does not sign in with a password or a code; it connects with a Pastime key only.
- It runs no background work and keeps no state or files. It only answers Bloem Server's
  calls, so it needs no storage approval.
- It never sends a key to any address other than the configured Pastime address, never follows
  a redirect, and never logs or returns a key or one of Pastime's own error messages.

## Requirements

- [Bloem Server](https://github.com/Bloem-Studios/bloem-server) with plugin support.
- A [Pastime](https://github.com/Bloem-Studios/pastime) server that people can reach over
  `https`, and a Pastime profile for each person who connects.
- Linux (amd64 or arm64) or macOS (arm64) for the Bloem Server host.

## Install and connect

1. As a platform administrator, open Bloem's **Plugins** page, **Catalog** tab, and add the
   [plugin catalog](https://github.com/Bloem-Studios/bloem-plugins) feed with **Add
   repository**:
   `https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/manifest.json`
2. Install `bloem.pastime` from the catalog.
3. In the plugin settings, set **Pastime address** to the address people open Pastime at, for
   example `https://pastime.example.com`. It must be `https` with no path (a trailing `/` is
   fine). Bloem Server sends the address with every call, so a change applies at once.
4. Each person opens Pastime's **Connect Silo** page and creates a key for their Pastime
   profile. In Bloem they switch to the matching profile, open **Settings**, **Watch
   Providers**, choose **Pastime**, paste the key and select **Connect**. A new key replaces the
   profile's previous one. Disconnecting in Bloem, or revoking the key in Pastime, stops the
   sync.

Until the address is set, Pastime is listed under Watch Providers as "Server setup required."
and **Connect** is turned off. Syncs of existing connections wait without using retries.

## Configuration reference

The manifest declares one required installation setting group, `pastime`, with one public
field. Nothing in it is secret.

| Setting | Label | Required | Rule |
| --- | --- | --- | --- |
| `pastime.pastime_url` | **Pastime address** | yes | `https` origin with a host name; no user information, query, fragment or path other than `/`; at most 255 characters |

Each profile's connection holds its Pastime key, which Bloem Server stores, and the sync
switches that Bloem Server shows for a watch provider: **Scrobble playback**, **Send watched
changes**, **Import watched history**, **Import paused progress**, **Sync favorites**, **Sync
favorite removals**, **Sync watchlist**, **Sync watchlist removals**, **Send ratings** and
**Import ratings**.

Environment: `BLOEM_PASTIME_ALLOW_LOOPBACK_HTTP=1` in the plugin process allows `http://` for
`localhost` or a loopback IP. It is meant for isolated tests. Bloem Server passes only an
allowlisted environment to plugins, and the admin form accepts only `https`.

## Build from source

Requires Go 1.26 or newer (see `go.mod`). The SDK is the public
[`github.com/Bloem-Studios/bloem-plugin-sdk`](https://github.com/Bloem-Studios/bloem-plugin-sdk)
(Apache-2.0). Run from the repository root:

- `make test`: run the tests.
- `make verify`: tests with the race detector (including a real plugin process), `go vet`,
  a build, and a check of the manifest the built binary prints.
- `make build`: build `bin/bloem-pastime`.
- `make release VERSION=x.y.z`: reproducible binaries for Linux (amd64 and arm64) and macOS
  (arm64) under `bin/release-<version>/`, with checksums, manifests, archives and the catalog
  entry. `make release-check` checks that those files agree.

More detail: [how the plugin works inside](docs/architecture.md), [the Pastime sync
protocol](docs/protocol.md), [what the checks prove](docs/verification.md) and
[dependencies and licences](docs/provenance.md).

## Security

Report vulnerabilities privately through GitHub's private vulnerability reporting:
https://github.com/Bloem-Studios/bloem-plugin-pastime/security/advisories/new

## Licence

GNU Affero General Public License v3 (AGPL-3.0-only); see [LICENSE](LICENSE). Third-party
dependencies keep their own licences, copied into `third_party/licenses/`.
