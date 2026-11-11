# Contracts

| File | Contents |
|---|---|
| `SmallBank.sol`, `SmallBank.json` | SmallBank as an EVM contract (two balances per customer, the six procedures) and its compiled artifact (ABI and bytecode), used by `d16smallbank` and the SmallBank unit test |
| `D16Router.sol`, `D16Router.json` | Uniswap-V2-style router for fabric-x-evm's `UniswapV2Pair` (constant product, 0.3% fee, slippage bound) and its compiled artifact, used by `d16dex` and the router unit tests |
| `compile_smallbank.js`, `compile.js` | Recompile the two contracts with `solc` 0.8.20 (`npm install solc@0.8.20`, then `node compile_smallbank.js` and `node compile.js` in this directory) |

The workloads of the paper were generated with the included compiled artifacts.
