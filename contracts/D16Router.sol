// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// D16 evaluation: minimal Uniswap-V2-style router for fabric-x-evm's UniswapV2Pair. One transaction moves
// the input tokens into the pair, quotes the output from the current reserves (constant product, 0.3% fee)
// and swaps, reverting if the quote is below the user's slippage bound.

interface IERC20Min {
    function transferFrom(address from, address to, uint256 value) external returns (bool);
}

interface IPairMin {
    function token0() external view returns (address);
    function getReserves() external view returns (uint112, uint112);
    function swap(uint256 amount0Out, uint256 amount1Out, address to, bytes calldata data) external;
}

contract D16Router {
    function swapExactIn(address pair, address tokenIn, uint256 amountIn, uint256 amountOutMin) external {
        require(IERC20Min(tokenIn).transferFrom(msg.sender, pair, amountIn), "TRANSFER_IN");
        (uint112 r0, uint112 r1) = IPairMin(pair).getReserves();
        bool zeroIn = tokenIn == IPairMin(pair).token0();
        (uint256 rIn, uint256 rOut) = zeroIn ? (uint256(r0), uint256(r1)) : (uint256(r1), uint256(r0));
        uint256 inWithFee = amountIn * 997;
        uint256 amountOut = (inWithFee * rOut) / (rIn * 1000 + inWithFee);
        require(amountOut >= amountOutMin, "SLIPPAGE");
        if (zeroIn) {
            IPairMin(pair).swap(0, amountOut, msg.sender, "");
        } else {
            IPairMin(pair).swap(amountOut, 0, msg.sender, "");
        }
    }
}
