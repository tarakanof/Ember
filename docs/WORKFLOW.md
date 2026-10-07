# Workflow: issue to installed release

How a change goes from an idea to the user's server, Mac and knob. The safety
rules (live state, smoke runs, the knob) are in [`../AGENTS.md`](../AGENTS.md);
they apply at every step.

## 1. Issue and spec

- File a GitHub issue for every change. Write the user's decisions on the issue.
- A non-trivial design gets a spec in the Obsidian vault, never in the repo:
  `Specs/ember/YYYY-MM-DD-<topic>-design.md` (vault path in
  `AGENTS.local.md`). Link it from the issue. Firmware specs go in
  `Specs/cinder/`.
- Research (APIs, other projects, resource cost) runs in a subagent; its result
  goes into the spec, and the issue gets a short summary.

## 2. Worktree and implementation

- One worktree and branch per issue:
  `git worktree add ~/Github/Ember-wt/<slug> -b <type>/<issue>-<slug> origin/main`.
  Never edit the main checkout while a change is in flight. Branch names must be
  new: `git branch -a | grep <slug>` first.
- Independent issues run in parallel, one subagent per issue, each in its own
  worktree. The subagent implements, pushes and opens the PR; it does not merge.
- Dependent PRs stack (`--base` the earlier branch) and retarget to `main` when
  the base merges. PRs that touch the same files merge one at a time, rebasing
  the next.
- TDD. Before each push:
  - Go: `gofmt`, `go vet ./...`, `go test ./... -race`.
  - App: `swift test --package-path macos`, `xcodegen generate` + an unsigned
    build, `scripts/strings.sh check`.
  - Dashboard JSON shape changed: regenerate goldens with `-update`
    (`cmd/ember/testdata/dashboard`); they are also EmberKit's decode fixtures.
- Conventional Commits (`feat(knob): …`, `fix: …`), body says why. No
  `Co-Authored-By`. Details: [`STYLE.md`](STYLE.md) Appendix A.
- New behavior also updates the doc that owns it: routes in [`API.md`](API.md),
  design in [`ARCHITECTURE.md`](ARCHITECTURE.md), toggles and operations in
  [`RUNBOOK.md`](RUNBOOK.md).

## 3. Pull request

- Body: `Closes #N`, what changed and why, and **evidence**: test output,
  measured numbers (latency, CPU, writes, memory), exact JSON shapes,
  screenshots for UI.
- Measure against the live server where possible (reads only); a scratch server
  follows the smoke-run rules in AGENTS.md.

## 4. Review

- An **independent review** by a fresh agent (Opus) that did not write the code.
  It reads the diff and the issue and looks for correctness, regressions of
  user-visible behavior, resource cost and missing tests.
- Findings go in a PR comment (the source of truth, not a scratch file).
- Fix each real finding with a test that fails without the fix; reply to each
  finding (fixed / not a bug + why). Re-review after non-trivial fixes.
- Merge only with no open blocker and green CI.

## 5. Merge and release

- `gh pr merge --merge` (merge commit), then remove the worktree and branch.
- Release when the server, app or producers changed for users, from a clean,
  in-sync `main`, once the user asked for the release: `scripts/release.sh X.Y.Z "title" --yes`. It bumps the app
  version in `macos/project.yml`, tags `vX.Y.Z` and publishes a GitHub Release,
  which triggers `docker-publish.yml` (server image `:X.Y.Z`, `:latest`) and
  `release-producers.yml` (headless producer archives + `SHA256SUMS`).
- If SSH push fails in a sandbox (1Password agent):
  `GIT_SSH_COMMAND="ssh -o IdentityAgent=none -o IdentitiesOnly=yes"`, or HTTPS
  with gh credentials:
  `git -c credential.helper= -c 'credential.helper=!gh auth git-credential' push https://github.com/tarakanof/Ember.git <branch>`.

## 6. Deploy and install

- **Server**: once `docker-publish.yml` for the tag succeeds, update the
  container through the `unraid` MCP (`.mcp.json`; approve it once when
  Claude Code asks): `refresh_docker_digests` with `confirm: true`, then
  `list_docker_containers` with `name: "ember"` (a substring filter: act only
  on the one container named `Ember`). If `update_available` isn't true after
  3 tries 20 s apart, or a tool is missing or unsupported, stop and ask the
  user to update by hand. Otherwise `update_docker_container` with its id and
  `confirm: true` (pull + recreate from the Unraid template, a few seconds
  down), then check `/healthz` and `/version` on the live server
  (`EMBER_SERVER_URL` in `producer.env`, or mDNS `_ember._tcp`) for the new
  tag. Without the MCP, the user updates the container and says "server
  updated".
  - The MCP is the user's `tarakanof/Unraid-MCP` container on the Unraid box
    (streamable HTTP, `UNRAID_MCP_URL`, bearer `UNRAID_MCP_TOKEN` from the
    shell env; mutations on in the container). Its Unraid API key is the
    boundary: it holds only `DOCKER` `READ_ANY`
    + `UPDATE_ANY`, so array, VM, parity and notification calls fail at the
    API whatever tools the server registers. `.claude/settings.json` also
    denies the known write tools except `refresh_docker_digests` and
    `update_docker_container` (a denylist: a write tool added by a newer
    unraid-mcp is not in it: a prompt in manual mode, the classifier in auto
    mode, nothing in bypass mode). Update
    only the Ember container.
- **Mac app** (after a release that changed it):
  ```sh
  scripts/build-local.sh <scratch>/ember-build
  osascript -e 'quit app "Ember"'
  rm -rf /Applications/Ember.app   # ditto merges; stale files would stay
  ditto <scratch>/ember-build/Build/Products/Release/Ember.app /Applications/Ember.app
  open /Applications/Ember.app
  ```
- **Claude plugin** (hooks changed): `claude plugin marketplace update ember`,
  `claude plugin update ember@ember`. Running Claude sessions pick it up only
  after `/reload-plugins` or a restart; tell the user.
- **Knob firmware**: the `cinder` repo, `docs/workflow.md` there.

## 7. Verify on the devices

- Knob: `firmware/tools/snapshot.py` (cinder) captures the screen over USB;
  the last checkin (`GET /v1/devices`, owner token) shows firmware version and link health.
- To show a state on the knob without waiting for real work, post a demo
  status and re-post it every 10 s (the server drops quiet sessions):
  `POST /v1/status {"source":"demo","tool":"claude","session":"demo","state":"running"}`;
  `DELETE` it when done.
- RUNBOOK "On-device verification" has the clock equivalents.

## 8. Report

- Extremely concise: what shipped (PR, version), measured result, what the user
  must do (update server, `/reload-plugins`, replug), open follow-ups.

## Obsidian vault

- Project task note `AI/Tasks/Ember.md`: current status and the canonical
  open-items list (the repo has no `TODO.md`).
- Before vault updates read `AI/SCHEMA.md` and `AI/Rules/{Core,Tasks,Security}.md`.
  When meaningful work lands, update the task note, `AI/Dashboard.md` if status
  or priority changed, and append a line to `AI/log.md`.
- `docs/superpowers/` stays gitignored so specs cannot land in the repo.
