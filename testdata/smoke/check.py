"""Runs INSIDE a public.ecr.aws/lambda/python image (see .mise/tasks/test/smoke).

Usage: check.py <terraform-output.json> <arch> (the `artifacts` output)

For every case of <arch> in the manifest: unpack the function zip to /var/task
and the layer zip (if the case needs one) to /opt exactly like Lambda does,
then run the case's check in a fresh interpreter (sys.path as the Lambda
runtime sets it: /var/task, /opt/python) and print one PASS/FAIL line.
Exits non-zero if any case fails.
"""

import json
import os
import platform
import shutil
import subprocess
import sys
import zipfile

ARCH_MACHINE = {"x86_64": "x86_64", "arm64": "aarch64"}

# case id -> (layer case id | None, python snippet run with Lambda's sys.path).
CHECKS = {
    "numpy-function": (
        None,
        """
import handler, numpy
assert handler.handler({}, None) == {"sum": 6}, handler.handler({}, None)
assert numpy.__file__.startswith("/var/task/numpy/"), numpy.__file__
""",
    ),
    "numpy-layer": (
        "self",
        """
import numpy
assert numpy.__file__.startswith("/opt/python/numpy/"), numpy.__file__
assert int(numpy.arange(10).sum()) == 45
""",
    ),
    "layer-user": (
        "numpy-layer",
        """
import handler, numpy
out = handler.handler({}, None)
assert out["sum"] == 10, out
assert numpy.__file__.startswith("/opt/python/numpy/"), numpy.__file__
""",
    ),
    "code-only": (
        None,
        """
import handler
out = handler.lambda_handler({"name": "smoke"}, None)
assert out["statusCode"] == 200 and "smoke" in out["body"], out
""",
    ),
    "workspace": (
        None,
        """
import wsapp, wslib, pathdep, six
for m in (wsapp, wslib, pathdep, six):
    assert m.__file__.startswith("/var/task/"), m.__file__
for absent in ("wsdev", "wsextra", "wsother"):
    try:
        __import__(absent)
    except ImportError:
        continue
    raise AssertionError(absent + " must not be shipped")
""",
    ),
    "packaged": (
        None,
        """
import demoapp, six
from importlib import resources
assert demoapp.handler({}, None) == {"ok": True}
text = resources.files("demoapp").joinpath("templates/hello.txt").read_text()
assert text.startswith("hello from package data"), text
assert demoapp.__file__.startswith("/var/task/demoapp/"), demoapp.__file__
""",
    ),
}


def wipe(path):
    os.makedirs(path, exist_ok=True)
    for entry in os.listdir(path):
        full = os.path.join(path, entry)
        if os.path.isdir(full) and not os.path.islink(full):
            shutil.rmtree(full)
        else:
            os.remove(full)


def unpack(zip_path, dest):
    with zipfile.ZipFile(zip_path) as z:
        z.extractall(dest)


def main():
    manifest = json.load(open(sys.argv[1]))
    arch = sys.argv[2]
    assert platform.machine() == ARCH_MACHINE[arch], (
        f"container is {platform.machine()}, expected {ARCH_MACHINE[arch]} for {arch}"
    )
    by_id = {a["id"]: a for a in manifest.values() if a["arch"] == arch}
    failed = 0
    for case_id, (layer, snippet) in CHECKS.items():
        art = by_id[case_id]
        wipe("/var/task")
        wipe("/opt")
        if layer == "self":
            unpack(art["path"], "/opt")
        else:
            unpack(art["path"], "/var/task")
            if layer:
                unpack(by_id[layer]["path"], "/opt")
        env = dict(os.environ, PYTHONPATH="/var/task:/opt/python", PYTHONDONTWRITEBYTECODE="1")
        proc = subprocess.run(
            [sys.executable, "-c", snippet],
            cwd="/var/task",
            env=env,
            capture_output=True,
            text=True,
        )
        ok = proc.returncode == 0
        failed += not ok
        print(f"{'PASS' if ok else 'FAIL'} {case_id} {arch} (python {platform.python_version()})")
        if not ok:
            print(proc.stdout + proc.stderr)
    sys.exit(1 if failed else 0)


main()
