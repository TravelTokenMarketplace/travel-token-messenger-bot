# Bot TTM Rebrand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebrand `camino-messenger-bot` → `travel-token-messenger-bot` across module path, protocol/contracts dependencies, identifiers, CI, and docs, so it builds and runs against the already-rebranded protocol (`ttm.*`) and contracts (`ttmaccount`).

**Architecture:** One Go module, so a half-renamed tree won't compile. Work proceeds as ordered phases, each an independently-building PR into `dev`: (0) repo/remote setup, (1) module path, (2) protocol repoint `cmp→ttm`, (3) contracts repoint `cmaccount→ttmaccount`, (4) bot identifiers, (5) infra/CI/docker, (6) docs/config. Regenerate codegen output rather than hand-editing it.

**Tech Stack:** Go 1.25.10, buf (BSR gen SDK), go-ethereum abigen bindings, Cobra CLI, Docker, GitHub Actions.

## Global Constraints

- Module path: `github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13` — keep the `/v13` major suffix (synced to protocol `release-13`).
- Brand: "Travel Token Messenger" / `TravelTokenMessenger`; identifier prefix `TTM` replaces `CM`/`CaminoMessenger`; protocol namespace `ttm.` replaces `cmp.`.
- Env-var prefix: `CMB` → `TTMB`.
- Docker: GHCR only. Delete the Docker Hub (`c4tplatform`) workflow.
- Left unchanged (out of scope): `github.com/chain4travel/camino-license`, the `camino-matrix-go` submodule + `maunium.net/go/mautrix => ./camino-matrix-go` replace, `github.com/chain4travel/camino-matrix-app-service`, `github.com/chain4travel/caminogoeth-compat`. `header.yaml` (already updated).
- Loose-ends → `TODOS.md`, do NOT invent values: Base Sepolia contract addresses (pending deployment), Matrix host `messenger.chain4travel.com`, `internal/messaging/mint.go` `https://camino.network` NFT metadata URLs.
- Each phase: `scripts/build_test.sh` + `scripts/lint.sh` must pass before its PR. Codegen is deterministic — `.github/workflows/check-clean-branch.sh` must stay green.
- Repo root for all paths below: the bot repo working tree. Branch per phase off `dev`; open one PR per phase.

---

### Task 1: Phase 0 — Repo & remote setup + prereq verification

**Files:**
- Modify: none in-tree (git remotes + GitHub). Optionally `.gitmodules` (only if the submodule repo moves — it does not).

**Interfaces:**
- Produces: a pushed `travel-token-messenger-bot` GitHub repo with `origin` pointing at it; confirmed-resolvable protocol gen SDK and contracts module for Tasks 3–4.

- [ ] **Step 1: Verify the protocol gen SDK for release-13 is resolvable**

Run:
```bash
go list -m buf.build/gen/go/ttm/messenger-protocol/grpc/go@latest
go list -m buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go@latest
```
Expected: both print a pseudo-version (e.g. `v1.6.x-<timestamp>-<commit>.1`). If either 404s, STOP — protocol `release-13` must be pushed to BSR with a Go gen SDK first.

- [ ] **Step 2: Verify the contracts module is fetchable**

Run:
```bash
go list -m github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts@latest
```
Expected: prints a version/pseudo-version. If it 404s, STOP — the contracts repo must be pushed to GitHub first.

- [ ] **Step 3: Create the new GitHub repo (match old visibility)**

Run:
```bash
gh repo create TravelTokenMarketplace/travel-token-messenger-bot --private
```
Expected: repo created. (Use `--public` only if the old repo is public.)

- [ ] **Step 4: Rewire remotes, keeping full history**

Run:
```bash
git remote rename origin old
git remote add origin git@github.com:TravelTokenMarketplace/travel-token-messenger-bot.git
git push origin --all && git push origin --tags
```
Expected: all branches + tags pushed to the new repo. Then set the default branch to `dev` on GitHub (`gh repo edit TravelTokenMarketplace/travel-token-messenger-bot --default-branch dev`).

- [ ] **Step 5: No commit** — this task changes remotes/GitHub only. Proceed to Task 2.

---

### Task 2: Phase 1 — Module path rename

**Files:**
- Modify: `go.mod:1` (module line), all `*.go` importing the module (~927 imports), `scripts/build.sh` (LDFLAGS `-X` paths), `scripts/generate_grpc_service_handlers.sh:137,161-162,196`.

**Interfaces:**
- Produces: module `github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13`; all later tasks import from this path.

- [ ] **Step 1: Branch off dev**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-1-module-path
```

- [ ] **Step 2: Rename the module and all self-imports**

The old path is a unique string, so a scoped replace is safe:
```bash
OLD='github.com/chain4travel/camino-messenger-bot/v13'
NEW='github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13'
grep -rl --include='*.go' -F "$OLD" . | xargs sed -i "s#${OLD}#${NEW}#g"
sed -i "s#module ${OLD}#module ${NEW}#" go.mod
grep -rl -F "$OLD" scripts/ | xargs -r sed -i "s#${OLD}#${NEW}#g"
```

- [ ] **Step 3: Tidy and confirm nothing references the old module path**

Run:
```bash
go mod tidy
grep -rn 'chain4travel/camino-messenger-bot' . --include='*.go' --include='*.sh'
```
Expected: `grep` prints nothing.

- [ ] **Step 4: Build, test, lint**

Run: `scripts/build_test.sh && scripts/lint.sh`
Expected: PASS. (External deps unchanged; only the internal path moved.)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: rename Go module to travel-token-messenger-bot"
```

- [ ] **Step 6: Push and open PR**

```bash
git push -u origin rebrand/phase-1-module-path
gh pr create --base dev --title "Phase 1: rename Go module path" --body "Module rename to github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13. Builds/tests green."
```

---

### Task 3: Phase 2 — Protocol repoint (`cmp` → `ttm`)

**Files:**
- Modify: `go.mod:6-7` (buf SDK requires), `scripts/constants.sh:31-32`, `scripts/resolve_protocol_release.sh:10,38-39`, `scripts/generate_grpc_service_handlers.sh:3,276,279,315,318,379,390,442,445-446,467`, `templates/v1/*.tpl`, `templates/v2/*.tpl`.
- Regenerate: `internal/rpc/generated/*` (48 clients + register files), all `cmp/services|cmp/types` imports (~298 files).

**Interfaces:**
- Consumes: module path from Task 2.
- Produces: service-ID constants of the form `"ttm.services.<pkg>.<v>.<Name>Service"`; imports under `buf.build/gen/go/ttm/messenger-protocol/{grpc,protocolbuffers}/go/ttm/...`.

- [ ] **Step 1: Branch off dev (after Phase 1 merges)**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-2-protocol
```

- [ ] **Step 2: Repoint the codegen scripts to the new BSR owner/module**

```bash
sed -i \
  -e 's#buf.build/gen/go/chain4travel/camino-messenger-protocol#buf.build/gen/go/ttm/messenger-protocol#g' \
  -e 's#cmp\\.services#ttm\\.services#g' \
  -e 's#cmp/services#ttm/services#g' \
  scripts/constants.sh scripts/generate_grpc_service_handlers.sh
sed -i \
  -e 's#"owner": "chain4travel"#"owner": "ttm"#' \
  -e 's#"module": "camino-messenger-protocol"#"module": "messenger-protocol"#' \
  -e 's#chain4travel/camino-messenger-protocol#ttm/messenger-protocol#g' \
  scripts/resolve_protocol_release.sh
```
Verify: `grep -rn 'chain4travel/camino-messenger-protocol\|cmp/services\|cmp\.services' scripts/` prints nothing.

- [ ] **Step 3: Repoint the buf SDK requires in go.mod to the release-13 versions**

Resolve the new pseudo-versions (the repointed resolve script queries BSR):
```bash
GRPC_VER=$(scripts/resolve_protocol_release.sh buf.build/gen/go/ttm/messenger-protocol/grpc/go)
PB_VER=$(scripts/resolve_protocol_release.sh buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go)
echo "grpc=$GRPC_VER pb=$PB_VER"   # must be non-empty pseudo-versions for release-13
go mod edit -droprequire buf.build/gen/go/chain4travel/camino-messenger-protocol/grpc/go
go mod edit -droprequire buf.build/gen/go/chain4travel/camino-messenger-protocol/protocolbuffers/go
go get buf.build/gen/go/ttm/messenger-protocol/grpc/go@${GRPC_VER}
go get buf.build/gen/go/ttm/messenger-protocol/protocolbuffers/go@${PB_VER}
```
Expected: `go.mod` now requires the two `ttm/messenger-protocol` SDK modules.

- [ ] **Step 4: Rewrite protocol import paths in Go source**

```bash
grep -rl --include='*.go' \
  -e 'buf.build/gen/go/chain4travel/camino-messenger-protocol' . \
  | xargs sed -i \
    -e 's#buf.build/gen/go/chain4travel/camino-messenger-protocol#buf.build/gen/go/ttm/messenger-protocol#g' \
    -e 's#messenger-protocol/grpc/go/cmp/#messenger-protocol/grpc/go/ttm/#g' \
    -e 's#messenger-protocol/protocolbuffers/go/cmp/#messenger-protocol/protocolbuffers/go/ttm/#g'
```

- [ ] **Step 5: Update templates so regenerated constants emit `ttm.services.*`**

```bash
grep -rl -e 'cmp\.services' -e 'cmp/services' -e 'chain4travel/camino-messenger-protocol' templates/ \
  | xargs -r sed -i \
    -e 's#cmp\.services#ttm.services#g' \
    -e 's#cmp/services#ttm/services#g' \
    -e 's#chain4travel/camino-messenger-protocol#ttm/messenger-protocol#g'
```
Verify: `grep -rn 'cmp' templates/` prints nothing.

- [ ] **Step 6: Regenerate the gRPC service handlers and protobuf codegen**

Run:
```bash
go mod download
scripts/generate_grpc_service_handlers.sh
scripts/protobuf_codegen.sh
```
Expected: `internal/rpc/generated/*_client.go` now define e.g. `MintServiceV5 = "ttm.services.book.v5.MintService"`. Confirm:
```bash
grep -rn '"cmp\.services' internal/rpc/generated/   # must print nothing
grep -c '"ttm.services' internal/rpc/generated/bookv5_Mint_client.go   # >= 1
```

- [ ] **Step 7: Build, test, lint, and check codegen is clean**

Run: `scripts/build_test.sh && scripts/lint.sh && .github/workflows/check-clean-branch.sh`
Expected: PASS with no uncommitted codegen drift.

- [ ] **Step 8: Commit, push, PR**

```bash
git add -A
git commit -m "refactor: repoint protocol to ttm/messenger-protocol (cmp -> ttm)"
git push -u origin rebrand/phase-2-protocol
gh pr create --base dev --title "Phase 2: protocol repoint cmp -> ttm" --body "Switch buf SDK to buf.build/gen/go/ttm/messenger-protocol (release-13); regenerate service handlers to ttm.services.* ; update codegen scripts + templates."
```

---

### Task 4: Phase 3 — Contracts repoint (`cmaccount` → `ttmaccount`)

**Files:**
- Modify: `go.mod:10,99-101` (require + replace block), `pkg/cm_accounts/cm_accounts.go`, `pkg/cm_accounts/revert.go`, `pkg/booking/booking.go` (import unchanged path but verify), `internal/messaging/service_registry.go`, `internal/eventlistener/subscriber/subscriber.go`, `internal/eventlistener/subscription_token_bought.go`, `internal/eventlistener/subscription_cancellation.go`, `tests/e2e/blockchain/client.go`, `internal/version/version.go:57,72-73`.
- Regenerate: `pkg/cm_accounts/mock_cm_accounts.go` (via `scripts/mock.gen.sh`).

**Interfaces:**
- Consumes: module path (Task 2), protocol imports (Task 3).
- Produces: contracts imported from `github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts/{ttmaccount,ttmaccountmanager,bookingtoken,bookingtokenoperator,erc20}`; identifiers `Ttmaccount*`.

- [ ] **Step 1: Branch off dev**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-3-contracts
```

- [ ] **Step 2: Rewrite go.mod — drop old require+replace, require the new module**

```bash
go mod edit -dropreplace github.com/chain4travel/camino-messenger-contracts/go/contracts
go mod edit -droprequire github.com/chain4travel/camino-messenger-contracts/go/contracts
NEW_CONTRACTS=$(go list -m github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts@latest | cut -d' ' -f2)
go get github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts@${NEW_CONTRACTS}
```
(The `maunium.net/go/mautrix => ./camino-matrix-go` replace stays.)

- [ ] **Step 3: Rewrite contracts import paths, package selectors, and identifiers**

The old contracts base path is unique; the package tokens `cmaccount`/`cmaccountmanager` and `Cmaccount*` are distinctive:
```bash
FILES=$(grep -rl --include='*.go' 'chain4travel/camino-messenger-contracts' .)
echo "$FILES" | xargs sed -i \
  -e 's#github.com/chain4travel/camino-messenger-contracts/go/contracts#github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts#g' \
  -e 's#/go/contracts/cmaccountmanager#/go/contracts/ttmaccountmanager#g' \
  -e 's#/go/contracts/cmaccount#/go/contracts/ttmaccount#g' \
  -e 's#\bcmaccountmanager\.#ttmaccountmanager.#g' \
  -e 's#\bcmaccount\.#ttmaccount.#g' \
  -e 's#\bCmaccountmanager#Ttmaccountmanager#g' \
  -e 's#\bCmaccount#Ttmaccount#g'
```

- [ ] **Step 4: Fix the version.go label string**

`internal/version/version.go` prints the literal label `"camino-messenger-contracts"` (line ~72) beside the module commit. Update it:
```bash
sed -i 's#"camino-messenger-contracts"#"travel-token-messenger-contracts"#' internal/version/version.go
```
Verify the module-path string on line ~57 was already updated by Step 3:
```bash
grep -n 'contracts' internal/version/version.go
```
Expected: references `travel-token-messenger-contracts`, none `camino`.

- [ ] **Step 5: Regenerate mocks and tidy**

Run:
```bash
scripts/mock.gen.sh
go mod tidy
grep -rn 'chain4travel/camino-messenger-contracts\|\bCmaccount\|\bcmaccount\.' . --include='*.go'
```
Expected: `grep` prints nothing.

- [ ] **Step 6: Build, test, lint**

Run: `scripts/build_test.sh && scripts/lint.sh && .github/workflows/check-clean-branch.sh`
Expected: PASS.

- [ ] **Step 7: Commit, push, PR**

```bash
git add -A
git commit -m "refactor: repoint contracts to travel-token-messenger-contracts (cmaccount -> ttmaccount)"
git push -u origin rebrand/phase-3-contracts
gh pr create --base dev --title "Phase 3: contracts repoint cmaccount -> ttmaccount" --body "Require github.com/TravelTokenMarketplace/travel-token-messenger-contracts; ttmaccount/ttmaccountmanager packages + Ttmaccount* identifiers; regenerate mocks."
```

---

### Task 5: Phase 4 — Bot-internal identifiers

**Files:**
- Rename (git mv): `pkg/cm_accounts/` → `pkg/ttm_accounts/`, `pkg/cmbcommon/` → `pkg/ttmcommon/`, `cmd/camino_messenger_bot.go` → `cmd/travel_token_messenger_bot.go`, `pkg/cm_accounts/mock_cm_accounts.go` → `mock_ttm_accounts.go`.
- Modify: all files referencing `CMAccount`/`cmAccount`/`cmaccounts` package/`cmbcommon`; `config/config_reader.go:20`; `pp-mock/server/server.go:74-76`; `tests/e2e/e2e_test.go:31,56,57`; `cmd/travel_token_messenger_bot.go:22-25`; `scripts/mock.gen.sh` / `scripts/mocks.mockgen*.txt` (mock target paths).

**Interfaces:**
- Consumes: contracts binding type `ttmaccount.Ttmaccount` (Task 4).
- Produces: package `ttmaccounts` at `pkg/ttm_accounts`, `ttmcommon` at `pkg/ttmcommon`; env prefix `TTMB`; command `travel-token-messenger-bot`.

- [ ] **Step 1: Branch off dev**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-4-identifiers
```

- [ ] **Step 2: git mv the renamed dirs/files (history follows)**

```bash
git mv pkg/cm_accounts pkg/ttm_accounts
git mv pkg/ttm_accounts/mock_cm_accounts.go pkg/ttm_accounts/mock_ttm_accounts.go
git mv pkg/cmbcommon pkg/ttmcommon
git mv pkg/ttmcommon/cmbcommon.go pkg/ttmcommon/ttmcommon.go
git mv cmd/camino_messenger_bot.go cmd/travel_token_messenger_bot.go
```

- [ ] **Step 3: Rewrite identifiers and import paths**

```bash
grep -rl --include='*.go' \
  -e 'CMAccount' -e 'cmAccount' -e 'pkg/cm_accounts' -e 'cmbcommon' -e 'package cmaccounts' . \
  | xargs sed -i \
    -e 's#/v13/pkg/cm_accounts#/v13/pkg/ttm_accounts#g' \
    -e 's#/v13/pkg/cmbcommon#/v13/pkg/ttmcommon#g' \
    -e 's#package cmaccounts#package ttmaccounts#g' \
    -e 's#\bcmaccounts\.#ttmaccounts.#g' \
    -e 's#package cmbcommon#package ttmcommon#g' \
    -e 's#\bcmbcommon\.#ttmcommon.#g' \
    -e 's#CMAccount#TTMAccount#g' \
    -e 's#cmAccount#ttmAccount#g'
```
Note: `CMAccount`/`cmAccount` cover `cmAccountAddress`, `managerCMAccountImplementationSlot`, `IsCMAccountImplementationUpToDate`, etc. Review the diff for any unintended hits (e.g. inside strings that are on-chain names — there should be none, service names are `ttm.services.*`).

- [ ] **Step 4: Change the env-var prefix `CMB` → `TTMB`**

```bash
sed -i 's#const envPrefix = "CMB"#const envPrefix = "TTMB"#' config/config_reader.go
grep -rl 'CMB_' . --include='*.go' | xargs -r sed -i 's#CMB_#TTMB_#g'
grep -rn 'CMB' . --include='*.go'
```
Expected: last grep prints nothing (all `CMB`/`CMB_` now `TTMB`).

- [ ] **Step 5: Update the Cobra command metadata**

Edit `cmd/travel_token_messenger_bot.go` lines ~22-25:
```go
	Use:          "travel-token-messenger-bot",
	Short:        "starts travel token messenger bot",
	SuggestFor:   []string{"travel-token-messenger", "travel-token-messenger-bot", "ttm-bot", "ttmb"},
```

- [ ] **Step 6: Fix mock generation targets, regenerate, tidy**

Update any `cm_accounts`/`cmbcommon` paths in `scripts/mocks.mockgen.txt` / `scripts/mocks.mockgen.source.txt`:
```bash
sed -i -e 's#pkg/cm_accounts#pkg/ttm_accounts#g' -e 's#pkg/cmbcommon#pkg/ttmcommon#g' \
  -e 's#mock_cm_accounts#mock_ttm_accounts#g' scripts/mocks.mockgen*.txt
scripts/mock.gen.sh
go mod tidy
```

- [ ] **Step 7: Build, test, lint, codegen-clean**

Run: `scripts/build_test.sh && scripts/lint.sh && .github/workflows/check-clean-branch.sh`
Expected: PASS. Also confirm the CLI name:
```bash
go run . --help 2>&1 | head -3   # usage line shows "travel-token-messenger-bot"
```

- [ ] **Step 8: Commit, push, PR**

```bash
git add -A
git commit -m "refactor: rebrand bot identifiers (CMAccount->TTMAccount, CMB->TTMB, cmd rename)"
git push -u origin rebrand/phase-4-identifiers
gh pr create --base dev --title "Phase 4: bot-internal identifiers" --body "CMAccount->TTMAccount, cm_accounts->ttm_accounts, cmbcommon->ttmcommon, CMB->TTMB env prefix, command renamed to travel-token-messenger-bot."
```

---

### Task 6: Phase 5 — Infra / CI / Docker

**Files:**
- Delete: `.github/workflows/docker.yml`.
- Modify: `.github/workflows/ghcr.yml` (build args), `.github/workflows/ci.yml:5,113` (drop `c4t` branch trigger + Docker Hub temp build), `.github/workflows/release.yml:34-36`, `Dockerfile:14-17,26,29`, `Dockerfile.plugin`, `docker-compose.yml:4,14,17`, `scripts/build.sh:4,51,77-83`, all `scripts/*.sh` using `CAMINOBOT_PATH`, `scripts/constants.sh:21-27` (`CAMINO_BOT_*` vars), `proto/buf.yaml:2`.

**Interfaces:**
- Produces: binary `build/travel-token-messenger-bot`; GHCR image; buf module `buf.build/ttm/messenger-bot`.

- [ ] **Step 1: Branch off dev**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-5-infra
```

- [ ] **Step 2: Delete the Docker Hub workflow**

```bash
git rm .github/workflows/docker.yml
```

- [ ] **Step 3: Rename binary + build-arg + path variables across scripts and Docker**

```bash
grep -rl 'camino-messenger-bot\|CAMINO_BOT_\|CAMINOBOT_PATH\|c4tplatform' \
  Dockerfile Dockerfile.plugin docker-compose.yml scripts/ .github/workflows/ \
  | xargs -r sed -i \
    -e 's#c4tplatform/camino-messenger-bot#travel-token-messenger-bot#g' \
    -e 's#camino-messenger-bot#travel-token-messenger-bot#g' \
    -e 's#CAMINO_BOT_#TTM_BOT_#g' \
    -e 's#CAMINOBOT_PATH#TTMBOT_PATH#g'
```

- [ ] **Step 4: Point buf.yaml at the new BSR owner**

```bash
sed -i 's#buf.build/chain4travel/camino-messenger-bot#buf.build/ttm/messenger-bot#' proto/buf.yaml
```

- [ ] **Step 5: Remove the stale `c4t` PR-trigger branch in ci.yml**

Edit `.github/workflows/ci.yml` line ~5: change `branches: [c4t, dev]` → `branches: [dev]`. Confirm no `c4tplatform`/`temp` Docker Hub build step remains:
```bash
grep -n 'c4t' .github/workflows/ci.yml   # expect nothing
```

- [ ] **Step 6: Verify GHCR image path and remaining refs**

```bash
grep -rn 'camino\|c4t' .github/workflows/ Dockerfile Dockerfile.plugin docker-compose.yml proto/buf.yaml
```
Expected: nothing (GHCR image derives from `${GITHUB_REPOSITORY}`, now `travel-token-messenger-bot`).

- [ ] **Step 7: Build the binary + Docker images**

Run:
```bash
scripts/build.sh
ls build/travel-token-messenger-bot
docker build -t ttm-bot-test -f Dockerfile --build-arg TTM_BOT_TAG=dev .
docker build -t ttm-plugin-test -f Dockerfile.plugin .
```
Expected: binary present at `build/travel-token-messenger-bot`; both images build.

- [ ] **Step 8: Test + lint + shellcheck**

Run: `scripts/build_test.sh && scripts/lint.sh && scripts/shellcheck.sh`
Expected: PASS.

- [ ] **Step 9: Commit, push, PR**

```bash
git add -A
git commit -m "ci: GHCR-only publishing and rebrand build/image names to travel-token-messenger-bot"
git push -u origin rebrand/phase-5-infra
gh pr create --base dev --title "Phase 5: infra/CI/docker rebrand" --body "Delete Docker Hub workflow (GHCR only); binary/image/build-arg/path renames; buf module -> buf.build/ttm/messenger-bot; drop stale c4t branch trigger."
```

---

### Task 7: Phase 6 — Docs / config / prose + loose-ends

**Files:**
- Modify: `README.MD`, `.gitpod.yml:4,9`, `tests/e2e/README.MD`.
- Delete: `examples/config/camino-messenger-bot-supplier-camino.yaml`, `examples/config/camino-messenger-bot-distributor-camino.yaml`.
- Rename (git mv): remaining `examples/config/camino-messenger-bot-*.yaml` → `travel-token-messenger-bot-*.yaml`.
- Create/append: `TODOS.md` in the workspace parent (`../TODOS.md` relative to repo root — the rebrand playbook's TODOS file, next to `REBRANDING.md`), NOT committed into the repo.

**Interfaces:**
- Consumes: nothing new.
- Produces: a clean `grep -ri camino` sweep except documented external deps + loose-ends.

- [ ] **Step 1: Branch off dev**

```bash
git checkout dev && git pull && git checkout -b rebrand/phase-6-docs
```

- [ ] **Step 2: Delete the Camino mainnet example configs**

```bash
git rm examples/config/camino-messenger-bot-supplier-camino.yaml \
       examples/config/camino-messenger-bot-distributor-camino.yaml
```

- [ ] **Step 3: Rename remaining example configs off the camino- prefix**

```bash
for f in examples/config/camino-messenger-bot-*.yaml; do
  git mv "$f" "$(echo "$f" | sed 's#camino-messenger-bot-#travel-token-messenger-bot-#')"
done
ls examples/config/
```

- [ ] **Step 4: Rewrite README + gitpod prose**

Edit `README.MD`: title `# Camino-Messenger-Bot` → `# Travel-Token-Messenger-Bot`; headers "Camino Messenger Account" → "Travel Token Messenger Account", "Camino Messenger Protocol (CMP)" → "Travel Token Messenger Protocol (TTMP)"; replace `docs.camino.network` / `suite.camino.network` links (leave a bracketed `<!-- TODO: brand docs URL -->` note if no replacement exists yet); `buf.build/chain4travel/camino-messenger-protocol` → `buf.build/ttm/messenger-protocol`. Edit `.gitpod.yml`: docs URL line ~4 (TODO note if unknown) and build path line ~9 `cmd/camino-messenger-bot/main.go` → `main.go` (entry is repo-root `main.go`). Then:
```bash
grep -rn 'camino-messenger-bot\|Camino-Messenger' README.MD .gitpod.yml tests/e2e/README.MD
```
Expected: nothing except intentional historical mentions.

- [ ] **Step 5: Record loose-ends in the playbook TODOS (outside the repo)**

Append to `../TODOS.md` (the rebrand-playbook TODOS next to `REBRANDING.md`; this file is NOT part of the repo):
```
## bot rebrand loose-ends (2026-07-18)
- Base Sepolia contract addresses in examples/config/*.yaml + config/test_config.yaml are placeholders pending the fresh contracts deployment.
- Matrix homeserver `messenger.chain4travel.com` still in all configs — replace when new homeserver exists.
- internal/messaging/mint.go NFT metadata externalURL + image still https://camino.network — replace when brand site/assets exist.
- External chain4travel deps intentionally kept: camino-license, camino-matrix-go submodule, camino-matrix-app-service, caminogoeth-compat.
```

- [ ] **Step 6: Final sweep — confirm only expected leftovers**

Run:
```bash
grep -ri camino --exclude-dir={.git,vendor,build,camino-matrix-go} . | grep -v 'docs/superpowers'
```
Expected leftovers ONLY: `chain4travel/camino-license`, `chain4travel/camino-matrix-go`, `chain4travel/camino-matrix-app-service`, `chain4travel/caminogoeth-compat`, the `mint.go` camino.network URLs, `messenger.chain4travel.com` Matrix hosts, and any dated historical design docs. Anything else must be fixed before committing.

- [ ] **Step 7: Build, test, lint**

Run: `scripts/build_test.sh && scripts/lint.sh`
Expected: PASS.

- [ ] **Step 8: Commit, push, PR**

```bash
git add -A
git commit -m "docs: rebrand README/config/prose; delete Camino mainnet example configs"
git push -u origin rebrand/phase-6-docs
gh pr create --base dev --title "Phase 6: docs/config rebrand" --body "README + gitpod prose; delete camino mainnet configs; rename example configs; loose-ends recorded in playbook TODOS."
```

---

### Task 8: End-to-end verification

**Files:** none (verification only).

- [ ] **Step 1: Run the full e2e harness (after all phases merged to dev)**

```bash
git checkout dev && git pull
scripts/e2e.sh
```
Expected: PASS (blockchain + matrix + bot + partner_plugin harness green).

- [ ] **Step 2: Confirm service-name resolution end-to-end**

Inspect the e2e logs / a running bot to confirm requested service names resolve as `ttm.services.*` and that the bot reads `IsServiceSupported` against a `TTMAccount` without error. Once the fresh Base Sepolia contracts deployment addresses are available, plug them into a config and confirm a bot startup against a Base Sepolia RPC.

- [ ] **Step 3: Update the rebrand playbook status table**

In `../REBRANDING.md`, set the `bot` row to ✅ done with the merge date and PR links. (Playbook file, outside the repo — not committed here.)

---

## Notes for the executor

- Blanket `sed` across `*.go` is safe only for the distinctive tokens used above (`CMAccount`, `cmAccount`, unique import paths, `cmp/services`, `cmp.services`). Do NOT run an unscoped `s/cm//` or `s/camino//`. Review each phase's `git diff` before committing.
- The two-letter `cm` substring appears in unrelated identifiers (e.g. `go-cmp`, `cmd`) — never target it directly.
- If `resolve_protocol_release.sh` cannot reach BSR, resolve the SDK versions manually via `buf` / the buf.build UI for the `release-13` label and pass them to `go get`.
- Phases are ordered by dependency; do not start Phase 2 before Phase 1 merges, etc. Each PR rebases on the latest `dev`.
