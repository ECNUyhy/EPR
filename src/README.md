# Source files

Every file that `../patches/` adds to the upstream trees, at its upstream path, so that the code can be read
without applying the patches. The content is identical to that of the patches. Changes to existing upstream
files (hooks in the coordinator, the validator, the executor, the replica store and the harness) are only in
the patches.

| Directory | Upstream tree |
|---|---|
| `fabric-x-committer/` | Fabric-X committer v1.0.3 |
| `fabric-x-evm/` | fabric-x-evm `c42a92c` |
| `fabric-x-evm-two-org/` | fabric-x-evm with the two-organization patch |
