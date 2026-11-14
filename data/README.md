# Local Data Directory

Raw CSV files are gitignored so the repository stays compact.

- `data/input/` — Near real-time global 48-hour CSV snapshots downloaded from NASA FIRMS (`MODIS_C6_1_Global_48h.csv`, `J2_VIIRS_C2_Global_48h.csv`).
- `data/archive/` — Historical Standard Processing (SP) country archive CSVs retrieved from NASA FIRMS Country Yearly Summary archives (`scripts/fetch_firms_archive.py`).
