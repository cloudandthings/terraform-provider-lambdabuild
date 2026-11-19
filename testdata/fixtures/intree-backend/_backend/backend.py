"""Minimal PEP 517 backend for tests.

Builds a wheel of the `intree` package and, like setuptools, leaves build
leftovers in the source tree (the current directory): build/ and
intree.egg-info/.
"""

import base64
import hashlib
import os
import zipfile

NAME, VERSION = "intree", "0.1.0"
DIST_INFO = f"{NAME}-{VERSION}.dist-info"


def _leftovers():
    os.makedirs("build/lib/intree", exist_ok=True)
    with open("build/lib/intree/__init__.py", "w") as f:
        f.write("leftover\n")
    os.makedirs(f"{NAME}.egg-info", exist_ok=True)
    with open(f"{NAME}.egg-info/PKG-INFO", "w") as f:
        f.write("leftover\n")


def _record_line(name, data):
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
    return f"{name},sha256={digest},{len(data)}"


def build_wheel(wheel_directory, config_settings=None, metadata_directory=None):
    _leftovers()
    files = {}
    for root, _, names in os.walk(NAME):
        for n in sorted(names):
            if n.endswith(".py") or n.endswith(".txt"):
                p = os.path.join(root, n)
                with open(p, "rb") as f:
                    files[p.replace(os.sep, "/")] = f.read()
    files[f"{DIST_INFO}/METADATA"] = f"Metadata-Version: 2.1\nName: {NAME}\nVersion: {VERSION}\n".encode()
    files[f"{DIST_INFO}/WHEEL"] = b"Wheel-Version: 1.0\nGenerator: intree-backend\nRoot-Is-Purelib: true\nTag: py3-none-any\n"
    record = [_record_line(n, d) for n, d in sorted(files.items())] + [f"{DIST_INFO}/RECORD,,"]
    files[f"{DIST_INFO}/RECORD"] = ("\n".join(record) + "\n").encode()
    wheel = f"{NAME}-{VERSION}-py3-none-any.whl"
    with zipfile.ZipFile(os.path.join(wheel_directory, wheel), "w") as z:
        for n in sorted(files):
            z.writestr(zipfile.ZipInfo(n, date_time=(1980, 1, 1, 0, 0, 0)), files[n])
    return wheel


def get_requires_for_build_wheel(config_settings=None):
    return []
