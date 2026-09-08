# Homebrew distribution plan

How goSSMS gets a working `brew install` on macOS, given the release process in
`.github/workflows/release.yml`. Nothing here is implemented yet; the README
still says "Homebrew and PPA installation options are coming soon."

Scope: macOS. The same tap serves Linuxbrew for free (the release already
builds `linux/amd64` and `linux/arm64`), so the formula covers Linux too — but
the deliverable is the Mac one, and the PPA is a separate thread.

## Decisions taken up front

**A personal tap (`radix29/homebrew-tap`), not homebrew-core.** homebrew-core
has a notability bar (roughly 75 stars/forks/watchers plus a real user base)
and its own review queue; a tap needs neither and can ship the same day as a
release. `brew install radix29/tap/gossms` is the resulting command. Moving to
homebrew-core later is additive — see § Later: homebrew-core.

**A binary formula, not a build-from-source formula.** Two hard reasons:

1. `go.mod` carries an active `replace github.com/radix29/gosmo => ../gosmo`
   (and an `ignore ../gosmo`). A source build from a GitHub source tarball has
   no sibling `../gosmo`, so `go build ./cmd/gossms` fails outright. The same
   directive is why `go install github.com/radix29/gossms@v0.0.9` fails — Go
   refuses `@version` installs of a module containing `replace`.
2. Even if that were fixed, a source formula would have to reproduce the
   `-ldflags -X internal/version.Version=…` from the workflow or every
   Homebrew-installed copy would report `(devel)`.

The release workflow already publishes exactly what a binary formula wants:
`gossms_<tag>_darwin_arm64.tar.gz` with the version baked in, plus
`checksums.txt`.

## Groundwork already done

Two prerequisites are in the tree; both ship with the next release, before any
formula exists.

- **`darwin/amd64` is in `TARGETS`** (`.github/workflows/release.yml`).
  Homebrew still supports Intel macOS, and a formula carrying no `x86_64` URL
  fails there with a bare "no available download". The loop, archiving,
  checksums and upload all handled the extra target unchanged; the cross-build
  was verified with `GOOS=darwin GOARCH=amd64 go build ./cmd/gossms`.
- **`gossms --version`** (`cmd/gossms/main.go`) prints name, version, commit,
  build date, platform, licence and the gosmo version, then exits 0 — before
  the log file is opened and before `tui.NewApp()`, so it needs no TTY. It also
  accepts `-version` and `-v`. This is what `test do` and `brew audit --strict`
  need, and it is worth having on its own for bug reports.

The licence question is settled too: `internal/version.License` is already
`GPL-3.0-or-later`, and the formula uses the same string.

## Steps

### 1. Create the tap repository — `radix29/homebrew-tap`

Done — **https://github.com/radix29/homebrew-tap**, public, default branch
`main`. The `homebrew-` prefix is what makes `radix29/tap` resolve, so
`brew install radix29/tap/gossms` works. Local checkout: `~/go/homebrew-tap`.

The formula is **not** committed there by hand — the release workflow generates
and pushes it (step 2). The tap starts with just the README, and the first tag
after this work lands creates `Formula/gossms.rb`. That is deliberate: `v0.0.9`
has no `darwin_amd64` asset, so a formula pinned to it could not serve Intel
Macs anyway.

### 2. Generate and push the formula from the release workflow

Done — the `homebrew` job in `.github/workflows/release.yml`, gated on
`if: github.ref_type == 'tag'` so a `workflow_dispatch` run cannot publish a
formula whose URLs 404. It checks out the tap, downloads the release's own
`checksums.txt`, reads the four SHA-256s out of it, renders
`Formula/gossms.rb`, and commits only when the file actually changed.

Reading the published `checksums.txt` back over the network (rather than
re-hashing a local `dist/`) also proves every asset the formula references is
really downloadable.

**The workflow's own `GITHUB_TOKEN` cannot write to another repository.** The
credential is a **write-enabled deploy key** rather than a PAT: an ed25519
keypair whose public half is registered on `radix29/homebrew-tap` as "gossms
release workflow (rotated 2026-09-08)", **key id 162692240**, and whose private
half is the `HOMEBREW_TAP_DEPLOY_KEY` secret on `radix29/gossms`. A deploy key
is scoped to exactly one repository, carries none of an account's other access,
and does not expire the way a fine-grained PAT does. `actions/checkout`'s
`ssh-key:` input also points the remote at SSH, so the `git push` step needs no
credential of its own.

Write access was proved with the real key — a push of a throwaway branch and
its deletion — both for the original key and again after the rotation.

The private half is kept at `~/go/tap_id` (`~/go/tap_id.pub` alongside). It is
outside any git repository; do not move it into one. The apt repository's
equivalents live beside it — see `docs/ppa.md`.

To rotate: generate a keypair with `ssh-keygen -t ed25519`, register it with
`gh api -X POST repos/radix29/homebrew-tap/keys -f key=... -F read_only=false`,
`gh secret set HOMEBREW_TAP_DEPLOY_KEY --repo radix29/gossms < <private key>`,
**verify the new key pushes**, and only then
`gh api -X DELETE repos/radix29/homebrew-tap/keys/<old id>`. That order matters:
until the secret is replaced the workflow still authenticates with the old key,
so deleting it first breaks the job.

The generated formula, for a tag `v0.0.10` (Homebrew's `version` drops the
leading `v`; the URLs keep it):

```ruby
class Gossms < Formula
  desc "Terminal reimplementation of SQL Server Management Studio"
  homepage "https://github.com/radix29/gossms"
  version "0.0.10"
  license "GPL-3.0-or-later"

  on_macos do
    on_arm do
      url ".../gossms_v0.0.10_darwin_arm64.tar.gz"
      sha256 "..."
    end
    on_intel do
      url ".../gossms_v0.0.10_darwin_amd64.tar.gz"
      sha256 "..."
    end
  end

  on_linux do
    # ... linux_arm64 and linux_amd64, the same shape
  end

  def install
    bin.install "gossms"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/gossms --version")
  end
end
```

Three things checked against the real artifacts rather than assumed, by
downloading the `v0.0.9` release:

- Each archive's root is a single directory `gossms_<tag>_<os>_<arch>/`
  holding `gossms`, `README.md`, `LICENSE`. Homebrew strips a single root
  directory automatically, so `bin.install "gossms"` is correct.
- `checksums.txt` is plain `sha256sum` output — `<hash>  <name>`, two spaces,
  bare filenames with no `./` prefix.
- `gossms --version` exits 0 with no TTY, first line `gossms v<tag>`, so
  `assert_match version.to_s` matches the `0.0.10` inside `v0.0.10`.

**The renderer's failure path is the part worth guarding.** A first draft
called `sha_for` from inside the formula heredoc; when a checksum was missing,
the `exit 1` killed only the command-substitution subshell, `set -e` never saw
it, and a formula with **empty `sha256` fields** was written and would have
been pushed. Every hash is now resolved into a variable before the heredoc, so
a missing one fails the job with nothing written. Both paths were dry-run
locally against a synthetic `checksums.txt`.

Formula downloads are not quarantined the way casks are, so the unsigned
binaries run without a Gatekeeper prompt. No `xattr` caveat is needed.

Still to add: a `livecheck` block pointing at the releases page, so
`brew livecheck` reports new tags.

### 3. Verify on a real Mac

`go test ./...` proves nothing about this. After the first tag that runs the
new job:

```sh
brew tap radix29/tap
brew install radix29/tap/gossms
gossms --version          # must print the tag, not (devel)
brew test gossms
brew audit --strict --online radix29/tap/gossms
brew uninstall gossms && brew untap radix29/tap
```

`brew audit --strict` is the gate — it catches desc/license/livecheck problems
that would otherwise surface as homebrew-core review comments later.

### 4. Update the docs

- README § Installation: replace "Homebrew and PPA installation options are
  coming soon" with the `brew install radix29/tap/gossms` command. The platform
  list is already corrected for `darwin/amd64`. Hold this until step 3 passes —
  the README should not advertise a command nobody has run.
- `RELEASE.md` / `CHANGELOG.md` for the release that first ships it (per
  `CLAUDE.md`, only as part of a release, not as part of this work).
- Any entry this leaves open goes to `docs/open-threads.md`.

## Later: homebrew-core

Worth revisiting once goSSMS is notable enough. Prerequisites, all of which are
work in their own right:

- The `replace` directive must not reach the tagged source — homebrew-core
  builds Go formulae from source with `go build`. That means either vendoring
  gosmo's tagged version and dropping the `replace` before tagging, or a
  release step that rewrites `go.mod`. This conflicts with the deliberately
  active `replace` in `CLAUDE.md`; it is a real design decision, not a chore.
- The formula must set the version ldflags itself.
- Stable, non-pre-release versioning and the notability bar.

Until then the tap is the supported path, and nothing about it has to be undone
to move to core later.

## What is left

The repository, the credential and the workflow job are all in place. What
remains cannot be done from Linux:

1. Tag a release, and check the `homebrew` job actually pushed
   `Formula/gossms.rb` to the tap.
2. Work through § 3 **on a real Mac** — nothing in this work has run on macOS.
3. Add the `livecheck` block, then drop the README's "coming soon" line.
