"""Second step of the two-organization overlay (11/05, stage S10b): -retry-mismatch.

With two organizations, a transaction whose two endorsements disagree (the endorsers were at different block heights;
the gateway reports "ProposalResponsePayloads do not match" and never submits it) is resubmitted, re-endorsed, like an
MVCC conflict. Other submission failures (no endorsement because execution reverted, ...) stay failures.
  gateway/core/batch_submitter.go : SubmitFailedErrHook(hash, err), called instead of SubmitFailedHook when set
  integration/perf/replay_json_dataset_test.go : flag -retry-mismatch installs that hook; mismatches count as MVCC
                                                 conflicts for the client's retry loop; EPR MISMATCH RETRY line
"""
import io, os

H = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(H, "out")

b = io.open(os.path.join(H, "batch_submitter.go"), encoding="utf-8").read()
old = "var SubmitFailedHook func(hash common.Hash)\n"
assert b.count(old) == 1
b = b.replace(old, old + "\n// SubmitFailedErrHook, if set, is called instead of SubmitFailedHook with the hash and the submission error\n"
                         "// (D16 two-organization runs: the harness retries endorsements that disagree).\n"
                         "var SubmitFailedErrHook func(hash common.Hash, err error)\n")
old = ("\t\t\t\tif SubmitFailedHook != nil {\n"
       "\t\t\t\t\tSubmitFailedHook(end.Hash)\n"
       "\t\t\t\t}\n")
assert b.count(old) == 1
b = b.replace(old, "\t\t\t\tif SubmitFailedErrHook != nil {\n"
                   "\t\t\t\t\tSubmitFailedErrHook(end.Hash, err)\n"
                   "\t\t\t\t} else if SubmitFailedHook != nil {\n"
                   "\t\t\t\t\tSubmitFailedHook(end.Hash)\n"
                   "\t\t\t\t}\n")
io.open(os.path.join(OUT, "batch_submitter.go"), "w", encoding="utf-8", newline="\n").write(b)

p = os.path.join(OUT, "replay_json_dataset_test.go")
r = io.open(p, encoding="utf-8").read()
assert "retry-mismatch" not in r
old = 'var extraEndorsers = flag.String('
assert r.count(old) == 1
r = r.replace(old, 'var retryMismatch = flag.Bool("retry-mismatch", false, "D16 two-organization runs: resubmit transactions whose endorsements disagree, like MVCC conflicts")\n\n'
                   '// eprMismatchRetries counts the endorsement disagreements resubmitted (-retry-mismatch).\n'
                   'var eprMismatchRetries atomic.Int64\n\n'
                   '// hasSub reports whether s contains sub.\n'
                   'func hasSub(s, sub string) bool {\n'
                   '\tfor i := 0; i+len(sub) <= len(s); i++ {\n'
                   '\t\tif s[i:i+len(sub)] == sub {\n'
                   '\t\t\treturn true\n'
                   '\t\t}\n'
                   '\t}\n'
                   '\treturn false\n'
                   '}\n\n' + old, 1)
old = "\tdefer func() { gwcore.SubmitFailedHook = nil }()\n"
assert r.count(old) == 1
r = r.replace(old, old +
              "\tif *retryMismatch {\n"
              "\t\t// D16 two-organization runs: endorsements that disagree (endorsers at different block heights) are\n"
              "\t\t// resubmitted like MVCC conflicts; other submission failures (e.g. reverts) stay failures.\n"
              "\t\tgwcore.SubmitFailedErrHook = func(h common.Hash, err error) {\n"
              "\t\t\tst := blocks.StatusEndorsementPolicyFailure\n"
              "\t\t\tif err != nil && hasSub(err.Error(), \"do not match\") {\n"
              "\t\t\t\tst = blocks.StatusMVCCConflict\n"
              "\t\t\t\teprMismatchRetries.Add(1)\n"
              "\t\t\t}\n"
              "\t\t\t_ = tracker.Observe(h, st)\n"
              "\t\t}\n"
              "\t\tdefer func() { gwcore.SubmitFailedErrHook = nil }()\n"
              "\t}\n", 1)
old = "func eprReport(t *testing.T) {\n"
assert r.count(old) == 1
r = r.replace(old, old + "\tif r := eprMismatchRetries.Load(); r > 0 {\n\t\tt.Logf(\"EPR MISMATCH RETRY resubmissions=%d\", r)\n\t}\n", 1)
io.open(p, "w", encoding="utf-8", newline="\n").write(r)
print("ok")
