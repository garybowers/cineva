# Contributing to Cineva

Thank you for wanting to help. This guide covers how to get a change into
Cineva: setting up, what a good change looks like, and how it is reviewed.

[CLAUDE.md](CLAUDE.md) is the full reference for the product's requirements,
the code's layout and its conventions — it is written for coding assistants
but serves people equally well. Read it before a change of any size.

## Before you start

- **Bugs**: open an issue with what you did, what you expected, what happened,
  the Cineva version (`cineva version`), and the relevant part of the server
  log. For a playback problem, include the file's codecs and container (the
  title's page in the web client shows them) and the browser.
- **Features**: open an issue to discuss it before writing much code. Cineva
  has a plan — see the milestones in CLAUDE.md and the deferred work in
  [BACKLOG.md](BACKLOG.md) — and a change that fits it is far easier to accept.
  Items on the backlog are a good place to start.
- **Security issues**: do not open a public issue. Report them privately to
  the maintainer through GitHub's private vulnerability reporting.

## Setting up

Follow "Building from source" in the [README](README.md). In short:

```sh
git clone --recurse-submodules git@github.com:garybowers/cineva.git
cd cineva
make setup   # once: builds the static FFmpeg library and installs UI dependencies
make run     # the server on http://localhost:4747
```

`make setup` compiles FFmpeg from source and takes a while the first time;
the README lists the packages it needs.

For UI work, run the Vite dev server alongside the Go server:
`npm --prefix server/ui/client run dev` (or `server/ui/admin`).

Never run the server against a media library you cannot afford to have
rescanned or re-matched — use a small test library of your own.

## Making a change

Work on a branch and open a pull request against `main`. CI builds, lints and
tests every pull request on linux/amd64 and linux/arm64, and checks every
commit is covered by the contributor agreement (see the end of this guide);
it has to pass.

Before pushing, run:

```sh
make lint    # gofmt and go vet
make test    # the Go tests
make ui      # if you changed either UI — the built dist folders are committed
```

and, for UI changes, `npx svelte-check` in the UI's folder, adding no new
errors or warnings.

### What every change needs

- **Tests** for new behaviour: table-driven, in a `_test.go` file beside the
  code. Store tests run against a real temporary SQLite database, not mocks.
- **The API spec** (`server/docs/openapi.yaml`, browsable at `/api/docs` on a
  running server) updated in the same change as
  any handler. The spec is the contract third-party clients are generated
  from, and the tests fail if a route is missing from it or an operation has
  no tag. Never change an existing response's shape: a breaking change is a
  new API version.
- **A new migration** for any schema change — never an edit to one that has
  already been applied.
- **Configuration** through Viper only, with any new key added to
  `server/cineva.example.yaml` alongside its `CINEVA_*` environment variable.
- **A backlog entry** in BACKLOG.md for anything you consciously leave out.

### Code style

- Go: standard `gofmt` style; errors wrapped with `fmt.Errorf("...: %w", err)`
  and handled, never dropped. Handlers and Cobra commands stay thin — logic
  lives in service packages, and SQL only in `server/internal/store`.
- Frontend: TypeScript and Svelte 5 runes. Every API call goes through the
  UI's `lib/api.ts`. Design at phone width first; every screen must work there.
- Colours come only from the theme tokens (`var(--accent)` and friends) —
  never a colour value in a component. Adding a token means adding it to
  every theme in `server/ui/themes/`.
- Icons are Lucide. No emoji in the UI, in documentation or in comments.
- British English in all prose: UI text, documentation, comments, commit
  messages and API descriptions (colour, catalogue, licence, -ise). Code
  keeps the spelling an API dictates (`color:` in CSS).
- Comments explain why, not what — the reason a thing is done this way, the
  case it guards against, the trade-off chosen.

### Commits and pull requests

- Keep a pull request to one change. A refactor that a feature needs is best
  sent separately, ahead of it.
- Commit messages: a short summary line in the imperative ("Add episode
  stills to the queue"), a blank line, then what changed and why.
- In the pull request, say how you tested it — and, for UI changes, include a
  screenshot at phone width and at desktop width.

### Dependencies

Add one only when it earns its place, and check its licence first: it must be
compatible with the AGPLv3 (MIT, BSD, ISC, Apache-2.0, LGPL, GPLv3 and AGPLv3
are; GPLv2-only and "non-commercial" licences are not). Go modules must keep
the server buildable as a single binary.

## Licence and the contributor agreement

Cineva is licensed under the GNU Affero General Public Licence, version 3 or
later. Contributions are accepted under the
[Contributor Licence Agreement](CLA.md): you keep the copyright in your work,
and grant the maintainer a licence to use it — including to relicense the
Project, but only ever under an open-source licence.

To agree, once:

1. Read [CLA.md](CLA.md).
2. In your first pull request, add a line to
   [CLA-SIGNATURES.txt](CLA-SIGNATURES.txt) —
   `Your Name <you@example.com> | YYYY-MM-DD | CLA 1.0` — using the email
   your commits are made with.

And on every commit, sign off: `git commit -s`. If you forget, fix the
branch with `git rebase --signoff origin/main` and force-push it.

`scripts/check-cla.sh` checks both; CI runs it on every pull request, and you
can run it yourself before pushing.
