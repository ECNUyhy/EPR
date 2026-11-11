#!/usr/bin/env bash
# Post-run SmallBank ledger check: balances that are not valid non-negative int64 words (an overdraft
# under unguarded deltas wraps to a huge uint256 word or sets high bytes). Usage: srv_sbcheck.sh <label>
BANK=${SB_BANK:-0xa4AD55ac1e6F09de44624A1C8B536619Aa01a7A9}
docker exec test-committer psql -U postgres -h localhost -p 5433 -At -F ' ' -c "
SELECT '$1',
  COUNT(*) AS balances,
  COUNT(*) FILTER (WHERE length(value)=32 AND (substring(value FROM 1 FOR 24)<>decode(repeat('00',24),'hex') OR get_byte(value,24)>=128)) AS overdrawn,
  COUNT(*) FILTER (WHERE substring(value FROM 1 FOR 4)='EPRD'::bytea OR substring(value FROM 1 FOR 4)='EPRP'::bytea) AS raw_encoded
FROM ns_basic WHERE key LIKE 'str:$BANK:%'::bytea;" 2>&1
