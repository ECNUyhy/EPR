# Baselines

All baselines run in the same Fabric-X stack, replay harness, datasets and genesis state as EPR. The table in
the top-level README lists how each one is selected. This directory holds what is needed to check the ports
of the two baselines whose code is public.

## Original sources (`orig_src/`)

Verbatim copies, see `orig_src/README.md`:

- `orig_src/sigmod20/`: FabricSharp, branch `sigmod20` of `github.com/ooibc88/FabricSharp`, commit
  `fc639da4cd36aa85f3cb8cd9924e24d061ff2146`, directories `orderer/common/blockcutter/scheduler/sharpscheduler`
  and `.../scheduler/common`.
- `orig_src/master/fppscheduler/`: the Fabric++ scheduler shipped with FabricSharp (branch `master`,
  `orderer/common/blockcutter/scheduler/fppscheduler`).

## Port and list of edits (`port_orig_baselines.py`)

`python port_orig_baselines.py` writes the ported packages to `ported/fabricsharp/` from `orig_src/`. Every
textual edit is listed in the script and must match exactly once, so a changed original makes the port fail.
The edits replace package paths, the orderer's logger and configuration lookups, and the parsing of Fabric 2.x
read-write sets that Fabric-X replaces; the scheduling algorithms are unchanged. After `gofmt`, the output is
identical to `../src/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp/`.

## Original unit tests

`common/ds_test.go`, `common/mv_store_test.go` and `sharpscheduler/scheduler_test.go` are FabricSharp's
original unit tests, ported without edits. In the patched committer tree:

    go test ./service/coordinator/dependencygraph/fabricsharp/...

## Integration into the coordinator

| Baseline | Glue | Tests |
|---|---|---|
| FabricSharp | `epr_fabricsharp_orig.go` (the authors' scheduler, `FX_FABRICSHARP=1`), `epr_fabricsharp.go` (selection, snapshot heights) | `epr_fabricsharp_orig_test.go`, `epr_fabricsharp_test.go` |
| Fabric++ | `epr_fpp_orig.go` (`FX_REORDER=1`), `epr_reorder.go` (selection, early aborts) | `epr_fpp_orig_test.go`, `epr_reorder_test.go` |
| HTFabric | `epr_ward.go` (WARD ordering, `FX_WARD=1`); `service/vc/epr_reexec.go` (re-execution, early gossip); `d16reexec_v2` (TSP-partitioned re-execution, `-parallel`) | `epr_ward_test.go` (the example of HTFabric's Figure 4), `service/vc/epr_reexec_test.go` |
| XOX | `service/vc/epr_reexec.go`; `d16reexec_v2` (sequential re-execution) | `service/vc/epr_reexec_test.go` |

Paths without a directory are in `../src/fabric-x-committer/service/coordinator/dependencygraph/`;
`d16reexec_v2` is in `../src/fabric-x-evm/`.
