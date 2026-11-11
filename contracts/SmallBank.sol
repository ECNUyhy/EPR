// SPDX-License-Identifier: Apache-2.0
pragma solidity ^0.8.20;

/// SmallBank (H-Store / Blockbench / Fabric++ / FabricSharp / HTFabric benchmark) as an EVM contract for D16.
/// Two balances per customer (checking, savings). Unsigned balances; the generator funds every account
/// generously so that, as in the original benchmark, transactions only fail on genuine insufficient funds.
contract SmallBank {
    mapping(uint256 => uint256) public checking; // slot 0
    mapping(uint256 => uint256) public savings;  // slot 1

    /// genesis helper (used only by the generator)
    function init(uint256 from, uint256 n, uint256 c, uint256 s) external {
        for (uint256 i = from; i < from + n; i++) {
            checking[i] = c;
            savings[i] = s;
        }
    }

    /// Balance: read both balances (read-only).
    function balance(uint256 a) external view returns (uint256) {
        return checking[a] + savings[a];
    }

    /// DepositChecking: checking[a] += v.
    function depositChecking(uint256 a, uint256 v) external {
        checking[a] += v;
    }

    /// TransactSavings: savings[a] += v (v may be negative; insufficient savings reverts).
    function transactSavings(uint256 a, int256 v) external {
        if (v >= 0) {
            savings[a] += uint256(v);
        } else {
            uint256 w = uint256(-v);
            require(savings[a] >= w, "SAVINGS");
            savings[a] -= w;
        }
    }

    /// Amalgamate: move all of a's funds into b's checking account.
    function amalgamate(uint256 a, uint256 b) external {
        uint256 t = savings[a] + checking[a];
        savings[a] = 0;
        checking[a] = 0;
        checking[b] += t;
    }

    /// WriteCheck: debit checking by v, plus a penalty of 1 if the total balance does not cover v.
    function writeCheck(uint256 a, uint256 v) external {
        uint256 c = checking[a];
        if (savings[a] + c < v) {
            v += 1;
        }
        require(c >= v, "CHECKING");
        checking[a] = c - v;
    }

    /// SendPayment: move v from a's checking to b's checking.
    function sendPayment(uint256 a, uint256 b, uint256 v) external {
        require(checking[a] >= v, "FUNDS");
        checking[a] -= v;
        checking[b] += v;
    }
}
