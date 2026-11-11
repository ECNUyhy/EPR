# Patches

The source changes of the paper, as patches against the pinned upstream revisions. `setup.sh` applies them.

| Patch | Applies to | Contents |
|---|---|---|
| `fabric-x-committer-v1.0.3.patch` | `github.com/hyperledger/fabric-x-committer`, tag v1.0.3 | EPR in the coordinator and the validator-committer, and the ported baselines (FabricSharp, Fabric++, HTFabric's WARD ordering, the VC side of re-execution) |
| `fabric-x-evm-c42a92c.patch` | `github.com/hyperledger/fabric-x-evm`, commit `c42a92c` | EPR extraction in the endorser, replica application, the replay harness extensions, the workload generators, the serial-equivalence oracle, the coverage study and the re-execution service |
| `fabric-x-evm-two-org.patch` | fabric-x-evm with the patch above | Harness support for endorsers of further organizations (`-extra-endorsers`) and for resubmitting transactions whose endorsements disagree (`-retry-mismatch`), used by the two-organization experiment |

Files that the patches add are also in `../src/`, at their upstream paths.
