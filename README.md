# Bloem Pastime plugin

> [!CAUTION]
> **Offered without support.** Pastime is provided as-is, with no support of any kind. Issues are turned off, questions go unanswered, and pull requests are not accepted: they are closed without review. You are free to fork it under the AGPL v3.

This is a plugin for [Bloem Server](https://github.com/Bloem-Studios/bloem-server). It links
a Bloem profile to a profile on a [Pastime](https://github.com/Bloem-Studios/pastime)
server and keeps the two in step: what the profile watches, rates, favourites and
puts on its watchlist goes to Pastime, and what Pastime recorded elsewhere comes back.
It is for people who run both Bloem Server and Pastime.

## What it does

Bloem Server has a watch sync: it notes what each profile watches and changes, and hands those
changes to a watch provider, a plugin that talks to one tracking service. This plugin is the
watch provider for Pastime. Each profile that connects gets its own Pastime key, which Bloem
Server stores.

For each connected profile the plugin sends Pastime:

- live playback (start, pause, stop) and finished plays from the watch history;
- with each film or episode, its TMDB, TheTVDB and IMDb IDs, and for an episode its season and
  episode numbers, its title and its show's title;
- favourites, watchlist and ratings: what is added, changed and removed in Bloem. Favourite
  and watchlist removals are sent only when the connection's **Sync favorite removals** and
  **Sync watchlist removals** switches are on (both start off).

It brings back from Pastime:

- favourites, watchlist and ratings, as additions only;
- finished plays and resume points that Pastime recorded from somewhere other than Bloem
  (marked by hand, imported from another tracker, or reported by another media server). Plays
  Bloem sent are never read back. Each connection has **Import watched** and **Import
  progress** switches;
- from version 0.4.0, removals Pastime reports: a movie or show deleted from the profile's
  Pastime library removes that profile's watch history of it in Bloem, and a removed favourite,
  watchlist item or rating is removed too, if the connection synced that item and its removal
  settings allow it. This needs a Bloem Server that applies watched removals; an older one
  reports a sync warning and changes nothing.

Ratings are whole stars: Pastime stores one to five stars, which are the even values 2 to 10 on
Bloem's ten-point scale. An odd rating is not sent; it is refused with a message asking for an
even one. Unwatched edits, dropped shows and watchlist order are not synced.

The plugin only answers the server's calls. It runs no background work and keeps no state.

## Install

1. As a platform administrator, open Bloem's **Plugins** page, **Catalog** tab, and add the
   plugin catalog feed under **Repositories, Add repository**:
   `https://raw.githubusercontent.com/Bloem-Studios/bloem-plugins/main/manifest.json`
   (the catalog is at https://github.com/Bloem-Studios/bloem-plugins).
2. Install `bloem.pastime` from the catalog. Releases exist for Linux (amd64 and arm64) and
   macOS (arm64).
3. In the plugin settings, set **Pastime address** to the address people open Pastime at, for
   example `https://pastime.example.com`. It must be `https` with no path (a trailing `/` is
   fine). The server sends the address with every call, so a change applies at once.
4. Each person opens Pastime's **Connect Silo** page, creates a key for their Pastime profile,
   then in Bloem switches to the matching profile, opens **Settings, Watch Providers**,
   chooses **Pastime** and pastes the key. A new key replaces the profile's previous one.
   Disconnecting in Bloem, or revoking the key in Pastime, stops the sync.

Until the address is set, Pastime is listed under Watch Providers as "Server setup required."
with its **Connect** button turned off, and syncs of existing connections wait without using
retries. (Pastime's pages and the plugin's messages say "Silo" because Bloem Server speaks
Silo's watch-sync contract.)

The plugin has no storage and so needs no native storage approval.

If Pastime is down or the address is wrong, the whole batch waits for the next run and nothing
is lost. If a key is refused, the person is asked to create a new one. A problem with one change
affects only that change. A rate limit pauses the connection for as long as Pastime asks, up to
five minutes.

## Configuration reference

The manifest declares one required installation setting group, `pastime`, with one public field.
Nothing in it is secret.

| Setting | Required | Rule |
| --- | --- | --- |
| `pastime.pastime_url` (label **Pastime address**) | yes | `https` origin with a host name; no user information, query, fragment or path other than `/`; at most 255 characters |

Each profile's connection holds its Pastime key (stored by Bloem Server) and the switches
described above, which Bloem Server provides.

Environment: `BLOEM_PASTIME_ALLOW_LOOPBACK_HTTP=1` in the plugin process allows `http://` for
`localhost` or a loopback IP. It is meant for isolated tests; Bloem Server does not pass
environment variables through to plugins, and the admin form only accepts `https`.

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
dependencies keep their own licences, copied into `third_party/licenses/`. Releases made before
the AGPL v3 was applied were published under a proprietary licence and stay under it.
