#!/usr/bin/env bash
# Replay acceptance (F1, ADR-0401): for the last N first-parent commits of a
# PiCode checkout, the contract in testdata/picode-contract.json must select
# every gate scripts/ci-scope.mjs selects (or the full gate), never fewer.
#   scripts/replay-picode.sh <picode checkout> [N] [flow binary]
set -euo pipefail
repo=${1:?picode checkout}; n=${2:-300}; flow=${3:-flow}
here=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
git -C "$repo" log --first-parent -n "$n" --format=%H main > "$tmp/commits"
while read -r c; do
  git -C "$repo" diff --name-only --no-renames "$c^1" "$c" 2>/dev/null \
    | python3 -c 'import sys,json;print(json.dumps([l.strip() for l in sys.stdin if l.strip()]))'
done < "$tmp/commits" > "$tmp/sets.jsonl"
node --input-type=module -e "
import { localScope } from '$repo/scripts/ci-scope.mjs';
import { readFileSync } from 'node:fs';
for (const l of readFileSync('$tmp/sets.jsonl','utf8').split('\n')) { if (!l) continue;
  const s = localScope(JSON.parse(l)); console.log(JSON.stringify(s)); }" > "$tmp/ci.jsonl"
"$flow" scope --contract "$here/testdata/picode-contract.json" --batch < "$tmp/sets.jsonl" > "$tmp/flow.jsonl"
python3 - "$tmp" <<'PY'
import json,sys
d=sys.argv[1]
ci=[json.loads(l) for l in open(d+"/ci.jsonl")]; fl=[json.loads(l) for l in open(d+"/flow.jsonl")]
sets=[json.loads(l) for l in open(d+"/sets.jsonl")]
same=superset=fewer=0; bad=[]
m={"go":"go","web":"web","packages":"js","docs":"docs","desktop":"desktop","metadata":"meta"}
for i,(c,f) in enumerate(zip(ci,fl)):
    if c["full"]:
        need=None
    else:
        need={m[k] for k in m if c[k]}
        if c["web"]: need.add("js")   # ci-scoped.sh runs test-js for web too
    if f["full"]:
        if need is None: same+=1
        else: superset+=1
        continue
    if need is None:
        fewer+=1; bad.append((i,sets[i][:5],"ci full, flow "+",".join(f["checks"]))); continue
    got=set(f["checks"])
    if need<=got:
        if need==got: same+=1
        else: superset+=1
    else:
        fewer+=1; bad.append((i,sets[i][:5],"missing "+",".join(sorted(need-got))))
print(f"commits={len(ci)} same={same} superset={superset} fewer={fewer}")
for b in bad[:10]: print("FEWER", b)
sys.exit(1 if fewer else 0)
PY
