# Original baseline sources (verbatim, Apache-2.0)

- `sigmod20/`: FabricSharp, branch `sigmod20` of https://github.com/ooibc88/FabricSharp, commit
  fc639da4cd36aa85f3cb8cd9924e24d061ff2146, `orderer/common/blockcutter/scheduler/{sharpscheduler,common}`.
- `master/fppscheduler/`: Fabric++ scheduler shipped with FabricSharp (branch `master`,
  `orderer/common/blockcutter/scheduler/fppscheduler`). HTFabric (CIKM'24, Sec. 5.1) used this code as
  Fabric++, whose original code is not public.

`../port_orig_baselines.py` derives the ported packages from these files with a fixed list of textual
edits (package paths, logger, the rwset parsing that Fabric-X replaces); every edit is listed there.
