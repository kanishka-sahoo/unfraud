#!/usr/bin/env bash
# Checks the case invariants against stored Decisions; every count but the
# total should be 0:
#   unexplained       Cases without Contributions
#   wrongly_credited  credited to the Revision although the Provisional
#                     Decision was as strict (ties go to the earlier Stage)
#   not_strictest     action differs from the stricter of the two Stages
set -euo pipefail
kubectl -n unfraud exec cases-1 -c postgres -- psql -U postgres -d unfraud -c "
select c.profile, count(*) as cases,
       count(*) filter (where c.contributions is null) as unexplained,
       count(*) filter (where c.flagged_by = 2 and p.action >= c.action) as wrongly_credited,
       count(*) filter (where c.action <> greatest(p.action, coalesce(r.action, 0))) as not_strictest
from cases c
join decisions p on p.profile = c.profile and p.event_id = c.event_id and p.stage = 1
left join decisions r on r.profile = c.profile and r.event_id = c.event_id and r.stage = 2
group by 1 order by 1"
