# Travel-Token-Messenger-Bot

Travel-Token-Messenger-Bot is designed to facilitate communication through various
messaging platforms. The application is modularized into distinct components,
each serving a specific purpose.

## Components

1. **Messenger**

   - The Messenger component declares a decoupled messenger interface with the
     functionalities that include sending and receiving messages asynchronously.

   Currently, the only concrete implementation is the `matrix-messenger`, which
   utilizes the [Mautrix Golang SDK](https://github.com/mautrix/go) for the
   Matrix client. Note that the `mautrix` dependency requires the `olm` C
   library during both build time and runtime.

   To update camino-matrix-go submodule run:

   ```bash
   git submodule update --init --recursive
   ```

2. **Message Processor**

   - The Message Processor utilizes the Messenger to receive and process
     messages.

3. **gRPC Server**

   - This component acts as a Message Request gateway, accepting protobuf
     messages. These requests are then forwarded to the Message Processor.

4. **gRPC Client**
   - The gRPC Client communicates with a middleware or "partner-plugin" via
     gRPC, exchanging protobuf messages. The middleware handles necessary
     conversions and interacts with the external system of the partner. It's
     important to note that the middleware is not part of this application.

## Travel Token Messenger Protocol (TTMP)

This application depends on the [travel-token-messenger-protocol](https://github.com/TravelTokenMarketplace/travel-token-messenger-protocol),
which is a separate repository. The protocol defines the protobuf messages and
services used for communication between the gRPC server and client.

Currently [release-13](https://github.com/TravelTokenMarketplace/travel-token-messenger-protocol/releases/tag/release-13)
of Travel Token Messenger Protocol is used.

Protocol Buf Schema is available [here](https://buf.build/ttm/messenger-protocol)

## Travel Token Messenger Account (TTM Account)

To run the travel-token-messenger-bot as intended you will need a **TTM Account** with
a **bot** added to it and **registered services**.

### Creating the TTM Account

If you don't already have a **TTM Account** your company should [get listed as a Partner](https://docs.camino.network/partners/partner-showroom/index.html#being-listed-as-a-partner) <!-- TODO: brand docs URL -->
and [configure its partner profile](https://docs.camino.network/partners/partner-config) <!-- TODO: brand docs URL -->
via [suite](https://suite.camino.network) <!-- TODO: brand docs URL -->. Following this process will result in
a TTM account and all requirements to run the bot on mainnet Camino. Creating the
TTM Account this way is necessary for the mainnet Camino.

For development purposes, it is also possible to configure the partner profile
on testnet Base Sepolia and a TTM Account can also be created and configured
without the company being listed as a Partner - via [travel token messenger contracts](https://github.com/TravelTokenMarketplace/travel-token-messenger-contracts/tree/dev#travel-token-messenger-account-setup)
`hardhat` tool.

To create a **TTM Account** and add the bot to it - you will need **two** (test)
**wallets** - one for the **TTM Account**, and one for the **bot**.

> [!IMPORTANT] Remember to ensure you are connected to testnet Base Sepolia if
> you are doing this for development purposes.

### Funding your wallets

> [!IMPORTANT] Funds must be available on both wallets before you can create the
> TTM account for the blockchain transaction.
> 
> Test ETH can be obtained on Base Sepolia using these [faucets](https://docs.base.org/base-chain/network-information/network-faucets).
> Note that many require mainnet activity or mainnet ETH to be held.

### Creating the TTM Account via hardhat

After you have your two wallets ready, clone the contracts repo, position inside
the repo, and run:

```
yarn install
```

And then

```
yarn hardhat account --help
yarn hardhat account create --help
yarn hardhat account bot:add --help
yarn hardhat account service:add --help
```

To see how to use the tool.

1. To **create the TTM Account** run:

```
yarn hardhat account create --private-key <0xStatic-private-key-of-the-ttm-account-creator-wallet> --network base_sepolia
```

The command will output your new **TTM Account Address** in format 0x<\...\>.
Store it and add it to your `.yml` configuration under `ttm_account_address`.
More information can be found [here](#travel-token-messenger-account-ttm-account).

2. To **add the bot** to the TTM Account run:

```
yarn hardhat account bot:add --bot <0xAddress-of-the-bot-wallet> --ttm-account  <0xTTMAccount-address> --private-key <0xstatic-private-key-of-the-ttm-account-wallet> --network base_sepolia
```

Add this **bot's wallet static private key** to your `.yml` configuration under
`bot_key`.

> [!CAUTION] Do not set up two bot instances with the same configuration (same
> `bot_key`). E.g. if you have a deployed bot running on the Base Sepolia
> network, don't start one locally. Do not start the bot locally with the
> configuration intended for the mainnet (Camino or Base).

3. Granting Withdrawer role

In case you want to withdraw funds from the TTM Account contract, you have to
grant the `WITHDRAWER_ROLE` to an address, even for the default admin (creator
of the TTM Account). To do this run:

```
yarn hardhat account role:grant --role WITHDRAWER_ROLE --ttm-account <0xTTMAccount-address> --address <0xwithdrawer-address> --private-key <0xstatic-private-key-of-the-default-admin-of-the-ttm-account> --network base_sepolia

```

4. To **register a service** supported by your TTM Account run e.g.

```
yarn hardhat account service:add --ttm-account <0xTTMAccount-address> --private-key <0xstatic-private-key-of-the-ttm-account-wallet>  --service-name ttm.services.ping.v1.PingService --fee 1 --network base_sepolia
```

> [!IMPORTANT] **After adding or removing the service(s)** it is necessary to
> **restart the bot**.


To see all the services you can support, run:

```
yarn hardhat manager services:list --network base_sepolia
```

5. To see a list of services that are supported by a specific TTM Account:

```
yarn hardhat account service:list --ttm-account <0xTTMAccount-address> --network base_sepolia
```

6. Once your bot is active, offering its services to other TTM Accounts, and
   performing regular cash-ins, it is possible to withdraw a possible surplus
   gas coins to an address with WITHDRAWER_ROLE via `hardhat` (and also via
   suite). To do it via hardhat run:

```
yarn hardhat account withdraw --amount <STRING> --ttm-account <0xTTMAccount-address> --private-key <0xstatic-private-key-of-the-ttm-account-wallet> --recipient <STRING>
```

## Examples and Docker Support

The repository includes examples demonstrating the setup of gRPC servers and
clients under the `examples` folder. Additionally, Dockerfiles and a
docker-compose.yml file are provided to simplify the deployment of a provider,
distributor bot, and a partner plugin application that mocks responses.

> [!Note] If docker-compose.yml is used, please check volumes. The configuration
> filepath should be `ttm-config/config.yaml`. An example can be copied over
> from examples/config/ and modified.

## Getting Started
### Operating systems Support
> [!Note] - Linux - Supported on **Ubuntu/Debian Linux** systems - macOS - Only
> on **Apple Silicon** chip, on Intel chip it is not supported - Windows - Not
> supported

### Install required `olm` library

Below are instructions for installing the `olm` library for Linux (Debian and
Ubuntu) and macOS (Apple Silicon):

#### Linux (Debian/Ubuntu)

1. **Update Package Lists:**

   ```bash
   sudo apt update
   ```

2. **Install Dependencies:**
   ```bash
   sudo apt install -y libolm-dev
   ```

#### macOS (Apple Silicon)

1. **Update Homebrew:**

   ```bash
   brew update
   ```

> [!TIP] If you don't have Homebrew installed, you can install it by following
> the instructions on [https://brew.sh/](https://brew.sh/).

2. **Install libolm:**
   ```bash
   brew install libolm
   ```
3. **Install GNU grep**

   MacOS ships with BSD `grep`, which does not support the `-P` flag required by
   the build scripts. Install GNU grep and add it to your PATH:
   ```bash
   brew install grep
   export PATH="$(brew --prefix)/opt/grep/libexec/gnubin:$PATH"
   ```
   Add the `export` line to your `~/.bashrc` (and put `source ~/.bashrc` in your
   `.bash_profile`) or `~/.zshrc` to make it permanent.

## Build

Use the provided build script to build the application. This will create a
binary in the `./build` directory. Minimum required go version is written in [go.mod](go.mod)

We recommend not using global go installation but rather installing [gvm](https://github.com/moovweb/gvm)
for easier go version management. After installing go, run the following command

#### Linux (Debian/Ubuntu)
```bash
./scripts/build.sh
```

#### macOS (Apple Silicon)

```bash
export CGO_CFLAGS="-I/opt/homebrew/include"
export CGO_LDFLAGS="-L/opt/homebrew/lib"
./scripts/build.sh
```
or run
```bash
set -a && source homebrew.env && set +a && ./scripts/build.sh
```

## Run

### Configuration

Use the provided example configuration files for suppliers and distributors:

```
travel-token-messenger-bot-distributor-base-sepolia.yaml
travel-token-messenger-bot-supplier-base-sepolia.yaml
```

Copy them accordingly and edit fields, mainly the private keys and TTM Account
address. Most of the default values should be fine for testing.

> [!IMPORTANT] Before choosing the configuration file, ensure that there is 100%
> clarity on whether the bot acts in the supplier role (seller of travel
> services) or whether the bot should be configured in the distributor role
> (buyer of travel services).

> [!NOTE] Keep in mind that the addresses of `bot_key` in the config file needs
> to have funds on-chain to pay for the transaction fees. (If you are using
> **MintRequest**)

### Start the Application

Start the bot using the related config file depending on your need.

**Supplier Bot:**

```bash
./build/travel-token-messenger-bot --config supplier-config.yaml
```

**Distributor Bot:**

```bash
./build/travel-token-messenger-bot --config distributor-config.yaml
```

> [!TIP] For testing and development, you can also start two bots, one supplier
> and one distributor.

In this case, make sure to **use 2 different TTM Accounts**. Feel free to explore
and customize the application based on your specific requirements. If you
encounter any issues or have questions, please refer to the documentation or
open an issue in the repository.

### Running partner plugin (pp-mock) example

Run the partner plugin. The listening port is read **only** from the
`TTMB_PARTNER_PLUGIN_MOCK_PORT` env var (pp-mock parses no CLI flags) and
defaults to `50051` when unset. Set it to match your supplier bot configuration
file's `partner_plugin_port` field, e.g.:

```bash
TTMB_PARTNER_PLUGIN_MOCK_PORT=50051 go run ./pp-mock
```

Optional environment variables:

- `TTMB_PARTNER_PLUGIN_MOCK_REALISTIC_PRICE=true` — make mint return the validated price
  (price-consistent search → validate → mint), instead of the default fixed `1` native
  coin. Fiat stays off-chain at a realistic value; native/ERC20 use a tiny base-unit
  amount for cheap testnet buys.
- `TTMB_PARTNER_PLUGIN_MOCK_TOKEN_DECIMALS="0xToken:6,..."` — per-ERC20 decimals used by
  realistic pricing (default 18). Only needed for non-18-decimal test tokens.
- `TTMB_PARTNER_PLUGIN_MOCK_BASE_UNITS=<value>` — overrides the tiny native/ERC20 base-unit
  amount used by realistic pricing (default `10533`).

See [README.MD](pp-mock/docs/README.md) for more information about partner
plugin.

## Tracing

Response metadata ("timestamps" key) contains json with traced steps.

In case of errors, some steps may be missing.

Example with all steps with Unix timestamps in milliseconds, which is important
for investigation in cases of response time issues:
```json
{
   "0-p2p_request_received": 1740518819000,
   "1-p2p_request_message_sent_to_server": 1740518819100,
   "2-p2p_request_message_received_from_server": 1740518819200,
   "3-p2p_request_message_sent_to_pp": 1740518819300,
   "4-p2p_response_message_received_from_pp": 1740518819400,
   "5-p2p_response_message_sent_to_server": 1740518819500,
   "6-p2p_response_message_received_from_server": 1740518819600,
   "7-p2p_response_sent": 1740518819700,
}
```

## E2E tests

See [README.MD](tests/e2e/README.MD) for running e2e tests.

## License

The Travel Token Messenger Bot is licensed under the terms of the [Camino Messenger License](LICENSE.md).

## Data Protection

Please take note of the [Camino Messenger Data Protection Guidelines](DATA_PROTECTION.md).
