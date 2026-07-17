# Bot Rebrand Design: Camino Messenger Bot → Travel Token Messenger Bot

Date: 2026-07-18
Status: approved-pending-review
Repo: `travel-token-messenger-bot` (currently `github.com/chain4travel/camino-messenger-bot`)

## Context

The Travel Token Messenger ecosystem is being rebranded from "Camino
Messenger" one repo at a time. The **contracts** and **protocol** repos are
already rebranded and their artifacts published:

- Contracts Go bindings: module
  `github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts`,
  packages `ttmaccount`, `ttmaccountmanager`, `bookingtoken`,
  `bookingtokenoperator`, `erc20`, `servicefeetoken` (identifiers
  `Ttmaccount*`, abigen convention).
- Protocol: BSR module `buf.build/ttm/messenger-protocol`, namespaces
  `ttm.services.*` / `ttm.types.*`, tag `release-13`.

The **bot** consumes both. It is the last code consumer to rebrand (the
matrix-app-service follows). The bot is one Go module, so a half-renamed
module will not compile — the rebrand is therefore structured as **phased
commits that each build and pass tests**, landed as **one PR per phase** into
`dev`.

Authoritative naming decisions (from the ecosystem playbook, not
re-litigated here): brand "Travel Token Messenger" / `TravelTokenMessenger`;
identifier prefix `TTM` replaces `CM`/`CaminoMessenger`; protocol namespace
`ttm.` replaces `cmp.`; repos named `travel-token-<thing>`; Go modules under
`github.com/TravelTokenMarketplace/travel-token-messenger-<thing>`; chains
move to Base (8453) / Base Sepolia (84532), Camino configs deleted not
deprecated.

Bot-specific decisions made during brainstorming (2026-07-18):

- Env-var prefix `CMB` → **`TTMB`** (Travel Token Messenger Bot). Breaking.
- Docker publishing: **GHCR only** — drop the Docker Hub (`c4tplatform`) workflow.
- External chain4travel deps (`camino-license` lint tool, `camino-matrix-go`
  submodule / mautrix replace, `caminogoeth-compat`): **left on chain4travel**,
  out of rebrand scope, recorded in `TODOS.md`.
- Matrix homeserver `messenger.chain4travel.com`: **left as a TODO loose-end**.
- `mint.go` NFT metadata URLs (`https://camino.network` + image): **left as a
  TODO loose-end** (same category as the Matrix host).
- Module major version stays **`/v13`**, synced to protocol `release-13`.
- Landing: **PR per phase** into `dev`.

## Current-state footprint (from exploration)

- Module: `github.com/chain4travel/camino-messenger-bot/v13` (~927 self-imports).
- Protocol pulled as **buf.build gen Go SDK**:
  `buf.build/gen/go/chain4travel/camino-messenger-protocol/{grpc,protocolbuffers}/go`,
  namespaces `cmp/services/*`, `cmp/types/*` across ~298 files. 48 generated
  service-ID constants `"cmp.services.<pkg>.<v>.<Name>Service"` in
  `internal/rpc/generated/*_client.go`.
- Contracts pulled via `go.mod` require `github.com/chain4travel/camino-messenger-contracts/go/contracts`
  with a `replace` → `github.com/TravelTokenMarketplace/camino-messenger-contracts/...`
  (still `cmaccount` packages / `Cmaccount*` identifiers). ~11 non-test files.
- Bot identifiers: `CMAccount`/`cmAccount` (~390) in `pkg/cm_accounts/`;
  package `cmbcommon`; env prefix `CMB`; command/binary `camino-messenger-bot`.
- No hardcoded chain IDs (chain ID resolved at runtime from RPC). Base Sepolia
  configs already exist; two `*-camino.yaml` mainnet configs remain.
- CI: `.github/workflows/{ci,docker,ghcr,release}.yml`. Docker Hub image
  `c4tplatform/camino-messenger-bot(-plugin)`. Tags `v13.x` / `-rc.N`.
- `internal/version/version.go` hardcodes the contracts module path string +
  the label `"camino-messenger-contracts"`.

## Prerequisites (verify before Phase 2/3)

1. Protocol `release-13` pushed to GitHub **and** its buf.build gen Go SDK
   resolvable: `go get buf.build/gen/go/ttm/messenger-protocol/...@<rel-13 BSR commit>`.
2. `github.com/TravelTokenMarketplace/travel-token-messenger-contracts`
   fetchable via the Go module proxy (or reachable to pin a pseudo-version).
3. Record the exact protocol BSR commit that `release-13` maps to (used to
   pin the gen SDK versions in `go.mod` and `scripts/resolve_protocol_release.sh`).

## Phases

Each phase is one PR into `dev`, branched off the previous, and must pass
`scripts/build_test.sh` + `scripts/lint.sh` before merge. Techniques:
case-aware scripted find/replace for mechanical variants, `git mv` for
renames (history follows), regenerate generated artifacts rather than sed-ing
them.

### Phase 0 — Repo & remote setup (no code)

- Create `TravelTokenMarketplace/travel-token-messenger-bot` (private, match old).
- `git remote rename origin old`; add new `origin`; `git push origin --all && --tags`.
- Set default branch `dev` on the new repo.
- Update `.gitmodules` submodule URL only if the submodule repo moves (it does
  not — stays chain4travel).

### Phase 1 — Module path rename (isolated; builds alone)

- `go.mod` module → `github.com/TravelTokenMarketplace/travel-token-messenger-bot/v13`.
- Rewrite all ~927 `github.com/chain4travel/camino-messenger-bot/v13/...`
  self-imports. No external dep changes.
- `scripts/build.sh` LDFLAGS `-X` version paths reference the module path.
- Verify: `scripts/build_test.sh`, `scripts/lint.sh`.

### Phase 2 — Protocol repoint (`cmp` → `ttm`)

- `go.mod`: buf SDK `chain4travel/camino-messenger-protocol` →
  `ttm/messenger-protocol` (both `grpc/go` and `protocolbuffers/go`), pinned to
  the release-13 BSR commit.
- Scripts: `scripts/constants.sh`, `scripts/resolve_protocol_release.sh`,
  `scripts/generate_grpc_service_handlers.sh` — buf owner/module strings,
  `BUF_SDK_BASE`, GOPATH module paths, and the `grep -oP 'cmp\.services...'`
  extraction → `ttm\.services`.
- Import paths `cmp/services|cmp/types` → `ttm/services|ttm/types` (~298 files).
- **Regenerate** `internal/rpc/generated/*` via
  `scripts/generate_grpc_service_handlers.sh` so the 48 service-ID constants
  become `"ttm.services.*"`. Update `templates/v1|v2/*.tpl` accordingly.
  (`@custom:cmp-service` annotation is already `ttm-service` upstream.)
- `proto/buf.yaml` bot module name handled in Phase 5.
- Verify: build + test; spot-check a generated constant equals
  `ttm.services.book.v5.MintService`.

### Phase 3 — Contracts repoint (`cmaccount` → `ttmaccount`)

- `go.mod`: remove the `chain4travel/camino-messenger-contracts` require +
  `replace`; add direct require on
  `github.com/TravelTokenMarketplace/travel-token-messenger-contracts/go/contracts`.
- Update ~11 files: package selectors `cmaccount`→`ttmaccount`,
  `cmaccountmanager`→`ttmaccountmanager`; identifiers `Cmaccount*`→`Ttmaccount*`
  (`CmaccountMetaData`, `NewCmaccount`, `CmaccountServiceAdded`, ABIs, etc.).
  Files: `pkg/cm_accounts/{cm_accounts,revert}.go`,
  `internal/messaging/service_registry.go`,
  `internal/eventlistener/subscriber/subscriber.go`,
  `internal/eventlistener/subscription_{token_bought,cancellation}.go`,
  `tests/e2e/blockchain/client.go`, and `internal/version/version.go`
  (module path string + `"camino-messenger-contracts"` label).
- Regenerate mocks (`scripts/mock.gen.sh`) — `mock_cm_accounts.go` renamed in Phase 4.
- Verify: build + test.

### Phase 4 — Bot-internal identifiers

- `CMAccount`→`TTMAccount`, `cmAccount`→`ttmAccount` (~390), including
  `managerCMAccountImplementationSlot`, `IsCMAccountImplementationUpToDate`.
- `git mv pkg/cm_accounts` → `pkg/ttm_accounts` (package `cmaccounts`→`ttmaccounts`);
  `git mv pkg/cmbcommon` → `pkg/ttmcommon`.
- Env prefix `CMB` → `TTMB`: `config/config_reader.go` `envPrefix`,
  `CMB_PARTNER_PLUGIN_MOCK_*` (`pp-mock/server/server.go`), e2e `CMB_*` flags.
- Command/binary `camino-messenger-bot` → `travel-token-messenger-bot`:
  `git mv cmd/camino_messenger_bot.go`; `Use`/`Short`/`SuggestFor`
  (drop/rename `cmb`, `camino-*` aliases).
- Regenerate mocks. Verify: build + test; `bot --help` shows new command name.

### Phase 5 — Infra / CI / Docker

- **Delete** `.github/workflows/docker.yml` (Docker Hub / `c4tplatform`).
- `.github/workflows/ghcr.yml`: image path follows the renamed repo
  (`ghcr.io/traveltokenmarketplace/travel-token-messenger-bot`); build args
  `CAMINO_BOT_*` → `TTM_BOT_*`.
- `.github/workflows/ci.yml`: drop the temp Docker Hub build tag / `c4tplatform`;
  remove the stale `c4t` PR-trigger branch.
- `.github/workflows/release.yml`: artifact + binary name
  `travel-token-messenger-bot-linux-amd64-<tag>.tar.gz`, `./build/travel-token-messenger-bot`.
- `Dockerfile`, `Dockerfile.plugin`: WORKDIR/ENTRYPOINT/COPY paths + build args.
- `docker-compose.yml`: image names.
- `scripts/build.sh`: output binary name, error strings; `CAMINOBOT_PATH`→
  `TTMBOT_PATH` across `scripts/*.sh`.
- `proto/buf.yaml`: module → `buf.build/ttm/messenger-bot`.
- Verify: `scripts/build.sh` produces `build/travel-token-messenger-bot`;
  `docker build` for both images.

### Phase 6 — Docs / config / prose

- `README.MD`: title, "Camino Messenger Account"/"CMP" headers, links
  (`docs.camino.network`, `suite.camino.network`, `buf.build/chain4travel/...`);
  the protocol/contracts GitHub links already use the TTM org.
- `.gitpod.yml`: docs URL + build path.
- Delete the two `*-camino.yaml` mainnet example configs; rename remaining
  `examples/config/camino-messenger-bot-*.yaml` off the `camino-` prefix.
- Leave (record in `TODOS.md`): Base Sepolia contract addresses (pending
  deployment), Matrix host `messenger.chain4travel.com`, `mint.go`
  camino.network NFT metadata URLs.
- Final sweep: `grep -ri camino --exclude-dir={.git,vendor,build} .` — expected
  leftovers only the documented loose-ends and external chain4travel deps
  (`camino-license`, `camino-matrix-go`, `caminogoeth-compat`).

## Out of scope / deliberately unchanged

- `camino-license` lint tool, `camino-matrix-go` submodule + `maunium.net/go/mautrix`
  local replace, `caminogoeth-compat` — external chain4travel repos.
- `header.yaml` copyright — already updated to "Travel Token Marketplace".
- Base Sepolia contract addresses / Matrix host / `mint.go` URLs — loose-ends.

## Verification (end-to-end)

Per phase: `scripts/build_test.sh` (unit), `scripts/lint.sh`, `scripts/shellcheck.sh`,
`.github/workflows/check-clean-branch.sh` (codegen is deterministic). After
Phase 6: `scripts/e2e.sh` (full harness: blockchain + matrix + bot +
partner_plugin), and the final `grep -ri camino` sweep. Confirm a built bot
starts against a Base Sepolia RPC and resolves service names as
`ttm.services.*` end-to-end once the fresh contracts deployment addresses are
available.
