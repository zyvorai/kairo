#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
make check
OUT="${1:-../kairo-github-ready.zip}"
rm -f "$OUT"
python3 - "$ROOT" "$OUT" <<'PY'
import os, sys, zipfile
root, out = sys.argv[1], sys.argv[2]
exclude_dirs={'.git','bin','.idea','.vscode'}
with zipfile.ZipFile(out,'w',zipfile.ZIP_DEFLATED) as z:
    for dp, dns, fns in os.walk(root):
        dns[:] = [d for d in dns if d not in exclude_dirs]
        for fn in fns:
            if fn.endswith('.zip') or fn=='.DS_Store': continue
            p=os.path.join(dp,fn)
            arc=os.path.join('kairo',os.path.relpath(p,root))
            z.write(p,arc)
print(out)
PY
