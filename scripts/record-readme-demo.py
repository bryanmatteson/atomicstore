#!/usr/bin/env python3
"""Record real conditional writes and lease handoff on FileStorage."""
import os
from pathlib import Path
import shutil
import subprocess

root = Path(__file__).resolve().parents[1]
output = Path(os.environ.get("README_RECORD_OUTPUT", Path.home() / "Documents/Developer/conditional-store/readme")).expanduser().resolve()
output.mkdir(parents=True, exist_ok=True)
binary = output / "atomicstore-demo"
build = ["go", "build", "-o", str(binary), "./examples/readme"]
limiter = Path.home() / ".local/bin/agent-build"
subprocess.run(([str(limiter), "--"] if limiter.exists() else []) + build, cwd=root, check=True)
text = subprocess.check_output([str(binary)], text=True)
if "VERIFIED" not in text:
    raise SystemExit("The storage demo did not finish.")
(output / "transcript.txt").write_text(text)
tape = output / "demo.tape"
tape.write_text((root / "docs/assets/demo.tape").read_text().replace("Output docs/assets/demo.gif", "Output demo.gif"))
subprocess.run(prefix + ["vhs", str(tape)], cwd=output, env=dict(os.environ, README_DEMO_BIN=str(binary)), check=True, timeout=180)
shutil.copyfile(output / "demo.gif", root / "docs/assets/demo.gif")
print("Recorded docs/assets/demo.gif")
