"""Resumable, checksum-verified downloads of EAGLE-I files, and the yearly filter step."""

from __future__ import annotations

import json
import time
import urllib.error
import urllib.request
from pathlib import Path

from .config import DERIVED_DIR, KEEP_STATES, RAW_DIR, STATE_ABBR
from .eaglei import AUX_FILES, YEAR_FILES, RemoteFile, file_md5, file_sha256, filter_year_to_parquet

USER_AGENT = "Mozilla/5.0 (compatible; data-downloader/1.0)"
EAGLEI_RAW = RAW_DIR / "eaglei"
EAGLEI_DERIVED = DERIVED_DIR / "eaglei"


def log(msg: str) -> None:
    print(time.strftime("%Y-%m-%d %H:%M:%S"), msg, flush=True)


def download(remote: RemoteFile, dest: Path, retries: int = 8, chunk: int = 1 << 20) -> Path:
    """Download ``remote`` to ``dest``, resuming a ``.part`` file. Verifies size and MD5.

    An existing ``dest`` with the right size is trusted (MD5 checked once, then skipped
    by the caller's manifest). A wrong checksum deletes the file and raises.
    """
    dest.parent.mkdir(parents=True, exist_ok=True)
    if dest.exists() and dest.stat().st_size == remote.size:
        return dest
    part = dest.with_name(dest.name + ".part")
    for attempt in range(1, retries + 1):
        have = part.stat().st_size if part.exists() else 0
        if have > remote.size:
            part.unlink()
            have = 0
        if have == remote.size:
            break
        req = urllib.request.Request(remote.url, headers={"User-Agent": USER_AGENT})
        if have:
            req.add_header("Range", f"bytes={have}-")
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                if have and resp.status != 206:
                    log(f"{remote.name}: server ignored Range (status {resp.status}); restarting")
                    have = 0
                mode = "ab" if have else "wb"
                last = time.time()
                with open(part, mode) as f:
                    while buf := resp.read(chunk):
                        f.write(buf)
                        have += len(buf)
                        if time.time() - last > 30:
                            log(f"{remote.name}: {have / 1e6:,.0f} / {remote.size / 1e6:,.0f} MB")
                            last = time.time()
            if have == remote.size:
                break
            log(f"{remote.name}: connection ended at {have} of {remote.size} bytes; resuming")
        except (urllib.error.URLError, TimeoutError, ConnectionError, OSError) as e:
            wait = min(300, 5 * 2**attempt)
            log(f"{remote.name}: attempt {attempt} failed ({e}); retrying in {wait}s")
            time.sleep(wait)
    else:
        raise RuntimeError(f"{remote.name}: gave up after {retries} attempts")
    md5 = file_md5(part)
    if md5 != remote.md5:
        part.unlink()
        raise RuntimeError(f"{remote.name}: MD5 {md5} != published {remote.md5}; deleted")
    part.replace(dest)
    return dest


def fetch_aux() -> dict[str, Path]:
    return {k: download(r, EAGLEI_RAW / r.name) for k, r in AUX_FILES.items()}


def parquet_path(year: int) -> Path:
    return EAGLEI_DERIVED / f"eaglei_{year}_{'_'.join(STATE_ABBR[s] for s in KEEP_STATES)}.parquet"


def manifest_path(year: int) -> Path:
    return parquet_path(year).with_suffix(".manifest.json")


def fetch_and_filter_year(year: int, delete_raw: bool = False) -> dict:
    """Download one yearly file (resuming), verify it, filter to KEEP_STATES, write Parquet
    and a manifest with the source checksums. Skips work already done."""
    remote = YEAR_FILES[year]
    out = parquet_path(year)
    man = manifest_path(year)
    if out.exists() and man.exists():
        m = json.loads(man.read_text())
        if m.get("source_md5") == remote.md5:
            log(f"{year}: already filtered ({m['rows']:,} rows), skipping")
            return m
    raw = download(remote, EAGLEI_RAW / remote.name)
    log(f"{year}: downloaded {raw.stat().st_size:,} bytes; checksumming and filtering")
    md5 = file_md5(raw)
    if md5 != remote.md5:
        raise RuntimeError(f"{year}: MD5 {md5} != published {remote.md5}")
    stats = filter_year_to_parquet(raw, out)
    m = {
        "year": year,
        "source_name": remote.name,
        "source_url": remote.url,
        "source_bytes": remote.size,
        "source_md5": md5,
        "source_sha256": file_sha256(raw),
        "source_header": stats["header"],
        "states": list(KEEP_STATES),
        "rows": stats["rows"],
        "parquet_bytes": stats["bytes"],
        "raw_deleted": delete_raw,
    }
    man.write_text(json.dumps(m, indent=2) + "\n")
    log(f"{year}: wrote {out.name} ({stats['rows']:,} rows, {stats['bytes']:,} bytes)")
    if delete_raw:
        raw.unlink()
        log(f"{year}: deleted national file {raw.name}")
    return m
