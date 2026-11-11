#!/usr/bin/env bash
# srv_statecheck_ns.sh (11/05): srv_statecheck.sh on the table of namespace $NS (default basic)
# Post-run ledger check of the USDC balances in namespace "basic": conservation (sum equals genesis), no
# raw EPRD encodings stored, no out-of-range words. Usage: srv_statecheck.sh <label>
docker exec test-committer psql -U postgres -h localhost -p 5433 -At -F ' ' -c "
SELECT '$1',
  COUNT(*) FILTER (WHERE length(value)=32) AS words,
  COALESCE(SUM(('x'||encode(substring(value FROM 25 FOR 8),'hex'))::bit(64)::bigint::numeric) FILTER (WHERE length(value)=32 AND substring(value FROM 1 FOR 24)=decode(repeat('00',24),'hex')),0) AS total,
  COUNT(*) FILTER (WHERE length(value)=32 AND substring(value FROM 1 FOR 24)<>decode(repeat('00',24),'hex')) AS highbytes,
  COUNT(*) FILTER (WHERE substring(value FROM 1 FOR 4)='EPRD'::bytea) AS raw_deltas,
  COUNT(*) FILTER (WHERE length(value)=0) AS empty_vals,
  (SELECT COUNT(*) FROM tx_status WHERE status=1) AS committed
FROM ns_${NS:-basic} WHERE key LIKE 'str:0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48:%'::bytea;" 2>&1
