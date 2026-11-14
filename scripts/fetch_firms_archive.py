"""Extract single-country historical CSVs from NASA FIRMS yearly ZIP bundles.

Uses HTTP byte-range requests to inspect the remote ZIP central directory and
download only the compressed entry for the requested country instead of
fetching multi-hundred-megabyte global archives.
"""

import argparse
import os
import struct
import sys
import urllib.request
import zlib

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_OUT_DIR = os.path.join(REPO_ROOT, "data", "archive")

DEFAULT_ARCHIVES = [
    # MODIS Collection 6.1 (2010-2025)
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2010_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2013_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2015_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2018_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2020_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2022_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2023_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2024_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/modis_2025_all_countries.zip",
    # VIIRS S-NPP 375m (2013-2024)
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2013_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2015_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2018_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2020_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2022_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2023_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-snpp_2024_all_countries.zip",
    # VIIRS NOAA-20 / JPSS-1 375m (2018-2025)
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2018_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2020_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2022_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2023_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2024_all_countries.zip",
    "https://firms.modaps.eosdis.nasa.gov/data/country/zips/viirs-jpss1_2025_all_countries.zip",
]


def fetch_range(url: str, start: int, end: int) -> bytes:
    """Read an inclusive byte range from a remote archive."""
    req = urllib.request.Request(
        url,
        headers={
            "Range": f"bytes={start}-{end}",
            "User-Agent": "PyroSync-ArchiveFetcher/1.0",
        },
    )
    with urllib.request.urlopen(req, timeout=45) as resp:
        return resp.read()


def get_total_size(url: str) -> int:
    """Resolve total remote byte length via a 1-byte Content-Range probe."""
    req = urllib.request.Request(
        url,
        headers={
            "Range": "bytes=0-0",
            "User-Agent": "PyroSync-ArchiveFetcher/1.0",
        },
    )
    with urllib.request.urlopen(req, timeout=45) as resp:
        cr = resp.headers.get("Content-Range", "")
        if "/" in cr:
            return int(cr.split("/")[-1])
        return int(resp.headers.get("Content-Length", "0"))


def extract_country(url: str, country: str, out_dir: str, overwrite: bool = False) -> None:
    """Extract CSV entries matching country from a remote ZIP archive."""
    os.makedirs(out_dir, exist_ok=True)
    total_size = get_total_size(url)

    tail_size = min(131072, total_size)
    tail = fetch_range(url, total_size - tail_size, total_size - 1)
    eocd_pos = tail.rfind(b"PK\x05\x06")
    if eocd_pos == -1:
        raise RuntimeError("ZIP end-of-central-directory signature not found")

    cd_size, cd_offset = struct.unpack("<II", tail[eocd_pos + 12 : eocd_pos + 20])
    cd_data = fetch_range(url, cd_offset, cd_offset + cd_size - 1)

    pos = 0
    matches = []
    target = country.lower()
    while pos + 46 <= len(cd_data):
        if cd_data[pos : pos + 4] != b"PK\x01\x02":
            break
        comp_method, _, _, _, comp_size, uncomp_size, fname_len, extra_len, comment_len = (
            struct.unpack("<HHHIIIHHH", cd_data[pos + 10 : pos + 34])
        )
        local_hdr_offset = struct.unpack("<I", cd_data[pos + 42 : pos + 46])[0]
        fname = cd_data[pos + 46 : pos + 46 + fname_len].decode("utf-8", errors="replace")
        if target in fname.lower() and fname.lower().endswith(".csv"):
            matches.append((fname, comp_method, comp_size, uncomp_size, local_hdr_offset))
        pos += 46 + fname_len + extra_len + comment_len

    if not matches:
        print(f"[skip] no entry matching {country!r} in {os.path.basename(url)}")
        return

    for fname, comp_method, comp_size, uncomp_size, local_hdr_offset in matches:
        out_path = os.path.join(out_dir, os.path.basename(fname))
        if os.path.exists(out_path) and not overwrite:
            print(f"[exists] {out_path} ({os.path.getsize(out_path) / 1024:.1f} KB)")
            continue

        chunk = fetch_range(url, local_hdr_offset, local_hdr_offset + 30 + 256 + comp_size)
        if chunk[:4] != b"PK\x03\x04":
            raise RuntimeError(f"invalid local header for {fname}")
        loc_fname_len, loc_extra_len = struct.unpack("<HH", chunk[26:30])
        data_start = 30 + loc_fname_len + loc_extra_len
        comp_bytes = chunk[data_start : data_start + comp_size]
        raw_csv = zlib.decompress(comp_bytes, -zlib.MAX_WBITS) if comp_method == 8 else comp_bytes

        with open(out_path, "wb") as f:
            f.write(raw_csv)
        print(
            f"[saved] {out_path} ({len(raw_csv) / 1024:.1f} KB from {comp_size / 1024:.1f} KB compressed; expected {uncomp_size / 1024:.1f} KB)"
        )


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch country CSVs from FIRMS yearly country ZIPs.")
    parser.add_argument("--country", default="Pakistan", help="Country substring to extract (default: Pakistan)")
    parser.add_argument("--out", default=DEFAULT_OUT_DIR, help="Output directory")
    parser.add_argument("--url", action="append", help="Specific archive URL (repeatable)")
    parser.add_argument("--overwrite", action="store_true", help="Overwrite existing CSV files")
    args = parser.parse_args()

    urls = args.url if args.url else DEFAULT_ARCHIVES
    failures = 0
    for url in urls:
        try:
            extract_country(url, args.country, args.out, overwrite=args.overwrite)
        except Exception as exc:
            failures += 1
            print(f"[error] {url}: {exc}", file=sys.stderr)
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main())
