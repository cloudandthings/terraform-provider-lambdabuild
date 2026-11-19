"""Generate the fake Packages of the hermetic test fixtures.

Run with `mise run fixtures:gen`. Output is byte-for-byte deterministic
(fixed timestamps, sorted entries), so regenerating leaves the committed
files and the hashes in each fixture's uv.lock unchanged. After changing
what a Package contains, re-lock the fixture (`uv lock` in its folder) and
update the golden hashes.

  compiled-deps/simple/fakeext   one Linux wheel per architecture, standing
                                 in for a compiled Package such as numpy
  sdist-only/simple/fakesdist    a Package with only a source distribution
  sbom-licences/simple/*         wheels covering the SBOM licence and purl
                                 rules
"""

import base64
import gzip
import hashlib
import io
import os
import sys
import tarfile
import zipfile


def write_wheel(index_dir, name, version, tag, files, metadata_extra="", metadata_version="2.1"):
    """Write a wheel into index_dir/<project>/ and return (filename, sha256)."""
    dist = name.replace("-", "_")
    di = f"{dist}-{version}.dist-info"
    files = dict(files)
    files[f"{di}/METADATA"] = f"Metadata-Version: {metadata_version}\nName: {name}\nVersion: {version}\n{metadata_extra}".encode()
    purelib = "false"  # as the original generator wrote it; uv ignores it for --target
    files[f"{di}/WHEEL"] = f"Wheel-Version: 1.0\nGenerator: fixture\nRoot-Is-Purelib: {purelib}\nTag: {tag}\n".encode()
    rec = ""
    for n, b in files.items():
        h = base64.urlsafe_b64encode(hashlib.sha256(b).digest()).rstrip(b"=").decode()
        rec += f"{n},sha256={h},{len(b)}\n"
    rec += f"{di}/RECORD,,\n"
    files[f"{di}/RECORD"] = rec.encode()

    project = name.lower().replace("_", "-")
    os.makedirs(os.path.join(index_dir, project), exist_ok=True)
    fn = f"{dist}-{version}-{tag}.whl"
    path = os.path.join(index_dir, project, fn)
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        for n, b in files.items():
            zi = zipfile.ZipInfo(n, (1980, 1, 1, 0, 0, 0))
            zi.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(zi, b)
    with open(path, "rb") as f:
        return fn, hashlib.sha256(f.read()).hexdigest()


def write_index(index_dir, project, links):
    with open(os.path.join(index_dir, project, "index.html"), "w") as f:
        f.write("<!DOCTYPE html>\n<html><body>\n")
        for fn, h in links:
            f.write(f'<a href="{fn}#sha256={h}">{fn}</a>\n')
        f.write("</body></html>\n")


def compiled_deps(root):
    index = os.path.join(root, "compiled-deps", "simple")
    links = []
    for arch in ("x86_64", "aarch64"):
        links.append(write_wheel(index, "fakeext", "1.0", f"cp312-cp312-manylinux_2_28_{arch}", {
            "fakeext/__init__.py": f"ARCH = '{arch}'\n".encode(),
            "fakeext/_native.so": f"fake {arch} shared object\n".encode(),
        }))
    write_index(index, "fakeext", links)


def sdist_only(root):
    pkginfo = b"Metadata-Version: 2.2\nName: fakesdist\nVersion: 1.0\n"
    index = os.path.join(root, "sdist-only", "simple")
    os.makedirs(os.path.join(index, "fakesdist"), exist_ok=True)
    path = os.path.join(index, "fakesdist", "fakesdist-1.0.tar.gz")
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w") as t:
        for n, b in {
            "fakesdist-1.0/PKG-INFO": pkginfo,
            "fakesdist-1.0/pyproject.toml": b'[project]\nname = "fakesdist"\nversion = "1.0"\n',
        }.items():
            ti = tarfile.TarInfo(n)
            ti.size = len(b)
            ti.mtime = 0
            t.addfile(ti, io.BytesIO(b))
    with open(path, "wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as g:
            g.write(buf.getvalue())
    with open(path, "rb") as f:
        h = hashlib.sha256(f.read()).hexdigest()
    write_index(index, "fakesdist", [("fakesdist-1.0.tar.gz", h)])


def sbom_licences(root):
    index = os.path.join(root, "sbom-licences", "simple")
    any_tag = "py3-none-any"
    for name, project, extra in (
        # License-Expression wins over the free-text License field; the
        # mixed-case, underscored name tests purl normalisation.
        ("Zeta_Pkg", "zeta-pkg", "License-Expression: MIT OR Apache-2.0\nLicense: ignored free text\n"),
        # "UNKNOWN" is no licence.
        ("unknownlic", "unknownlic", "License: UNKNOWN\n"),
        # No licence metadata at all.
        ("nolic", "nolic", ""),
    ):
        module = project.replace("-", "_")
        link = write_wheel(index, name, "1.0", any_tag, {f"{module}.py": f"NAME = '{name}'\n".encode()}, extra, "2.4")
        write_index(index, project, [link])


if __name__ == "__main__":
    root = sys.argv[1]
    compiled_deps(root)
    sdist_only(root)
    sbom_licences(root)
