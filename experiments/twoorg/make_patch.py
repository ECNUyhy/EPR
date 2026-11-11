"""Build the two-organization overlay files from the server's harness sources (fetched next to this script)."""
import io, os

H = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(H, "out")
os.makedirs(OUT, exist_ok=True)

# ---- integration/test_helpers.go: exported harness constructor with endorsers of further organizations ----
s = io.open(os.path.join(H, "test_helpers.go"), encoding="utf-8").read()
anchor = "// fabricXSignerWrapper wraps a fabric-x-common signer"
assert s.count(anchor) == 1
add = '''// NewFabricXTestHarnessWithNotificationsExtra (D16, two-organization runs) is
// NewFabricXTestHarnessWithNotifications plus the endorsers of further organizations: each is
// built by factory from the endorser section of its own config file (paths relative to
// integration/, e.g. fabx-2of2-org2.yaml), runs in this process with its own KVS, is
// registered as a block handler like the local endorser (so it syncs every block), and is
// called by the gateway after the local endorser; the state primer signs with all of them.
func NewFabricXTestHarnessWithNotificationsExtra(t *testing.T, logger sdk.Logger, evmConfig execution.EVMConfig, primeDbPath string, configOverrides map[string]any, factory EndorserFactory, txQueue core.TxQueueInterface, chainFactory HandlerChainFactory, confFile string, extraEndorserFiles []string) (*TestHarness, error) {
	if len(extraEndorserFiles) == 0 {
		return NewFabricXTestHarnessWithNotifications(t, logger, evmConfig, primeDbPath, configOverrides, factory, txQueue, chainFactory, confFile)
	}
	if primeDbPath != "" && !filepath.IsAbs(primeDbPath) {
		if abs, err := filepath.Abs(primeDbPath); err == nil {
			primeDbPath = abs
		}
	}
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	_ = os.Chdir("../")

	cfg, err := config.Load(confFile)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	endorsers, err := prepareHarnessConfig(t, &cfg, &evmConfig, configOverrides, factory)
	if err != nil {
		return nil, err
	}
	for _, f := range extraEndorserFiles {
		ecfgFile, err := config.Load(f)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", f, err)
		}
		if ecfgFile.Endorser == nil {
			return nil, fmt.Errorf("%s: no endorser section", f)
		}
		ecfg := *ecfgFile.Endorser
		ecfg.Database.HistorySize = 1
		endorsers = append(endorsers, factory(t, ecfg, cfg.Network.Channel, cfg.Network.Namespace, evmConfig, cfg.Network.Protocol))
		fmt.Printf("D16 EXTRA ENDORSER %s (%s) for namespace %s\\n", ecfg.Name, f, cfg.Network.Namespace)
	}

	th, sync, err := buildTestHarness(t, logger, cfg, evmConfig, primeDbPath, false, endorsers, txQueue, chainFactory)
	if err != nil {
		return nil, err
	}

	if err := synchronizer.WaitUntilSynced(t.Context(), sync, 60*time.Second); err != nil {
		return nil, fmt.Errorf("timed out waiting for sync: %w", err)
	}

	return th, nil
}

'''
s = s.replace(anchor, add + anchor)
io.open(os.path.join(OUT, "test_helpers.go"), "w", encoding="utf-8", newline="\n").write(s)

# ---- integration/perf/replay_json_dataset_test.go: -extra-endorsers flag ----
r = io.open(os.path.join(H, "replay_json_dataset_test.go"), encoding="utf-8").read()
flag_anchor = 'var outstanding = flag.Int("outstanding", 1000, "maximum number of outstanding transactions")\n'
assert r.count(flag_anchor) == 1
r = r.replace(flag_anchor, flag_anchor +
              'var extraEndorsers = flag.String("extra-endorsers", "", "comma-separated endorser config files of further organizations, relative to integration/ (D16 two-organization runs), e.g. fabx-2of2-org2.yaml")\n\n'
              '// splitList splits a comma-separated flag value, dropping empty items.\n'
              'func splitList(v string) []string {\n'
              '\tvar out []string\n'
              '\tstart := 0\n'
              '\tfor i := 0; i <= len(v); i++ {\n'
              "\t\tif i == len(v) || v[i] == ',' {\n"
              '\t\t\tif p := v[start:i]; p != "" {\n'
              '\t\t\t\tout = append(out, p)\n'
              '\t\t\t}\n'
              '\t\t\tstart = i + 1\n'
              '\t\t}\n'
              '\t}\n'
              '\treturn out\n'
              '}\n')
call = "\tth, err := integration.NewFabricXTestHarnessWithNotifications(\n"
assert r.count(call) == 1
r = r.replace(call, "\tth, err := integration.NewFabricXTestHarnessWithNotificationsExtra(\n")
tail = "\t\tperfHandlerChain(tracker),\n\t\tgwConfig)\n"
assert r.count(tail) == 1, r.count(tail)
r = r.replace(tail, "\t\tperfHandlerChain(tracker),\n\t\tgwConfig,\n\t\tsplitList(*extraEndorsers))\n")
io.open(os.path.join(OUT, "replay_json_dataset_test.go"), "w", encoding="utf-8", newline="\n").write(r)
print("ok")
