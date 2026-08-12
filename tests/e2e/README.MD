## Setting up environment

```sh
./scripts/build.sh
./scripts/build_partner_plugin_mock.sh
```

## Running tests

### Dependencies

#### Linux
```sh
sudo apt install libclang-dev build-essential
```

#### macOS
```sh
brew install rust
brew install llvm
```

### Running from terminal

The e2e.sh script does:

* Download the latest released dependencies
* Build and execute the end2end tests

Usage:

```sh
./scripts/e2e.sh [--clean] [--debug] [--filter "TESTNAME"] [--foundry "FOUNDRY_VERSION"] [--camino-conduit "CAMINO_CONDUIT_VERSION"] [--asb "ASB_VERSION"]
```
e.g.
```sh
./scripts/e2e.sh --clean --debug --camino-conduit "latest" --asb "latest"
```
or to download from the dev branch
```sh
./scripts/e2e.sh --clean --debug --camino-conduit "dev" --asb "dev"
```
Different dependencies might require different go versions or otherwise will fail to build. In this case, manually delete the dependency from `build/dependencies` folder, use gvm to adjust the version and re-run the script with just the dependency.
```sh
gvm use go1.25
./scripts/e2e.sh --debug --asb "dev"
```

The chain itself is no longer a downloaded dependency: `e2e.sh` provisions a
single [anvil](https://book.getfoundry.sh/anvil/) process (from `anvil` on
`PATH` if present, otherwise a pinned Foundry release) and runs it started
fresh for the duration of the run - there is no bootstrapping, no validator
set, and no genesis to configure.

Flags (All optional):

* `--clean` Always re-download dependencies instead of re-using already existing ones
* `--debug` Adds extensive log output for the end-to-end tests, including JSON requests and responses for each test-case
* `--filter` Comma-separated list of top-level test names to execute instead of all e.g. "PingV1,AccommodationV3".
* `--foundry` Overrides the Foundry (anvil) release to download; if unspecified, the pinned default version is used and its checksum is verified. Requesting a different version skips checksum verification (with a warning) unless `TTMB_FOUNDRY_SHA256` is set in the environment - see the `provision_anvil` function in `scripts/e2e.sh`. Ignored entirely if `anvil` is already on `PATH`.
* `--camino-conduit` Overrides the version to download; if unspecified, the latest version will be used.
* `--asb` Overrides the version to download; if unspecified, the latest version will be used.
### Running from VS Code

For VS Code to work correctly with e2e, following settings must be added because of build constraints:

```json
    "gopls": {
         "build.buildFlags": [
             "-tags=e2e"
         ]
     },
    "go.useLanguageServer": true,
```

Example launch json debug config can be found [here](../../examples/debug-launch-configs/vs_code_launch.json).


### Running from GoLand


Example runConfiguration .xml file can be found [here](../../examples/debug-launch-configs/goland_e2e.xml)


## Troubleshooting
The logs of tests are stored inside `/tmp/ttm-e2e` folder. The chain's own log
(anvil's stdout/stderr) is written to `<dataDir>/chain/anvil.log`:
```sh
find /tmp/ttm-e2e -name "anvil.log" 2>/dev/null
```
