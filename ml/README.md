# ml/: FourHour offline data pipeline

Python only. Its only output is JSON in `../artifacts/` (CONTRACTS.md section 8). As of Gate 1
no artifact is generated yet: this directory holds the ingest code, event reconstruction,
the train/test split guard and the artifact schema check.

## Setup and tests

```bash
cd ml
uv venv --python 3.14 .venv
uv pip install --python .venv/bin/python -r pyproject.toml --group dev
.venv/bin/python -m pytest
```

Versions are pinned in `pyproject.toml` (tested on Python 3.14.7, 2026-11-11).

## Data layout (nothing here is committed)

| Path | Contents | In git? |
|---|---|---|
| `data/raw/eaglei/` | National EAGLE-I CSVs, `MCC.csv`, `DQI.csv`, `coverage_history.csv` | No (root `.gitignore`) |
| `data/raw/esmi/` | ESMI files, downloaded by hand (see below) | No (root `.gitignore`) |
| `data/derived/eaglei/` | `eaglei_{year}_TX_FL_IL.parquet` + `.manifest.json` (source MD5, SHA-256, rows) | No (`ml/.gitignore`, until S6) |

## Downloading EAGLE-I

`download.py` is resumable. Re-run the same command after any interruption. Finished years
are skipped, and a partial file resumes from its `.part` file. Each file is checked against
the MD5 that figshare publishes. Each year is then filtered to Texas, Florida and Illinois as
Parquet, and the national CSV is kept unless you pass `--delete-raw`.

Years 2014-2023 total 8.80 GB of downloads. To run them in the background so the download
survives closing the terminal (`caffeinate` keeps the Mac awake):

```bash
cd "/Users/karandoshi/Desktop/Karan Doshi/Projects/Hackathons/paypal/fourhour/ml" && nohup caffeinate -i .venv/bin/python download.py --years 2014-2023 > data/raw/download_2014_2023.log 2>&1 &
```

To watch progress, run `tail -f data/raw/download_2014_2023.log`. The command exits 0 when every year is done. It exits 1 and lists the failed years otherwise; re-run it to resume.

### Source

* Figshare article 24237376, version 4 (modified 2026-02-25), doi:10.6084/m9.figshare.24237376.v4.
  Page: https://figshare.com/articles/dataset/The_Environment_for_Analysis_of_Geo-Located_Energy_Information_s_Recorded_Electricity_Outages_2014-2022/24237376
  API: https://api.figshare.com/v2/articles/24237376. Files download from `https://ndownloader.figshare.com/files/{id}` without login.

| File | Bytes | MD5 |
|---|---:|---|
| eaglei_outages_2014.csv | 78,056,250 | c7bfcf4b7fd26292f8d8801a26e8c52f |
| eaglei_outages_2015.csv | 598,963,737 | 774d837e6a0d4f20aab5c82448538d37 |
| eaglei_outages_2016.csv | 619,779,314 | 8e4e85e81db474a708278aabef1198ec |
| eaglei_outages_2017.csv | 698,796,929 | 4faaeeb3b3e8e2d13d0e9238617ce3ce |
| eaglei_outages_2018.csv | 999,210,542 | 726543e4a392e220108b035ba4c52e43 |
| eaglei_outages_2019.csv | 1,102,778,792 | 0ff77af0106486eb7bdc6c5051bd916a |
| eaglei_outages_2020.csv | 1,170,699,724 | 3b1e76e7ff0445da2b9a2fe4819c0e88 |
| eaglei_outages_2021.csv | 1,141,058,232 | b5a5912be793ca22dddcc73ca2c3379d |
| eaglei_outages_2022.csv | 1,187,596,176 | 0cd04f23ac90ec4ec20ce3a63dcfa25a |
| eaglei_outages_2023.csv | 1,199,752,423 | 4ef3f9290884b4151a43333d28f67369 |
| eaglei_outages_2024.csv | 1,444,846,424 | 13183facf56c196c061a238303c87973 |
| eaglei_outages_2025.csv | 1,402,347,695 | cd2feb1282a42fb048cb6885398bc1cc |
| MCC.csv | 40,584 | 7e30d47d44cae46b3342cdabf7c84b7f |
| DQI.csv | 4,338 | fca42544e0c3ee4a191985d1c6c19bc4 |
| coverage_history.csv | 11,965 | 33ef0c8549a95bcc1ef52b61c2463271 |
| Uri_Map.R, DQI_processing.R | 2,249 / 7,201 | (scripts, not used) |

### Actual column names

Headers were read from each file: 2024 in full, and the other years from their first bytes using an HTTP Range request.

| File | Header |
|---|---|
| 2014-2022, 2025 | `fips_code,county,state,customers_out,run_start_time` |
| 2023 | `fips_code,county,state,sum,run_start_time` (outage count is named `sum`) |
| 2024 | `fips_code,county,state,customers_out,run_start_time,total_customers` |
| MCC.csv | `County_FIPS,Customers` (UTF-8 BOM; FIPS has no leading zero, e.g. `1001`) |
| DQI.csv | `fema,year,success_rate,percent_enabled,spatial_precision,cust_coverage,max_covered,total_customers,DQI` |
| coverage_history.csv | `year,state,total_customers,min_covered,max_covered,min_pct_covered,max_pct_covered` |

`fourhour_ml/eaglei.py` maps these headers onto one schema: `fips_code` (5-digit string), `county`, `state`,
`customers_out` (float, nullable), `run_start_time`, and `total_customers` (null outside 2024). Any other header raises an error.

### What the data shows so far (2024 and file headers only)

* The 2014 file starts at 2014-11-01, so the training period has only two months of 2014.
* The 2020 file has blank `customers_out` values (seen in its first row).
* The 2024 file publishes explicit zero rows. A missing 2024 row is a scrape gap, not a zero. SPEC 5.1 says zeros are not recorded; that is still to be checked for 2014-2023.
* The timezone of `run_start_time` is not stated in the figshare metadata. Times are kept as published.
* Denominators disagree. For Harris, MCC gives 1,827,686 and the 2024 `total_customers` gives 1,344,930. In 81 Harris rows, `customers_out` is above `total_customers` (peak 1,660,703). The code uses MCC.
* The big utility's feed drops out during large events. The county count falls from about 1.1M to about 2,300 for 45-75 minutes, then returns. The 30-minute bridge does not cover this, so Hurricane Beryl reconstructs as two events. See the S3 output and the lead's open question.

## ESMI (India)

* Harvard Dataverse doi:10.7910/DVN/CLLZZM, version 2.0, released 2021-02-18.
  Page: https://dataverse.harvard.edu/dataset.xhtml?persistentId=doi:10.7910/DVN/CLLZZM
* Downloads need a guestbook response (guestbook 251), even through the API (`"You may not download this file without the required Guestbook response for guestbookID 251."`).
  A person downloads the files in a browser and puts them, unrenamed, in `ml/data/raw/esmi/`.
* The structure below comes from Dataverse's public DDI metadata (`/api/access/datafile/{id}/metadata/ddi`), not from the files:

| File | Bytes | Rows | Columns |
|---|---:|---:|---|
| ESMI location information.tab | 56,731 | 528 | `Location name, From date, To date, District, State, Category, Connection Type` |
| ESMI minute-wise voltage data 2014.tab | 15,311,456 | 58,976 | `Location name, Date, Hour, Min 0 ... Min 59` (63) |
| ... 2015.tab | 205,062,222 | 766,649 | same |
| ... 2016 Jan-June.tab | 208,964,288 | 778,400 | same |
| ... 2016 July-Dec.tab | 235,647,532 | 874,773 | same |
| ... 2017 Jan-April.tab | 187,685,638 | 696,857 | same |
| ... 2017 May-Aug.tab | 181,410,105 | 681,422 | same |
| ... 2017 Sept-Dec.tab | 168,156,428 | 632,196 | same |
| ... 2018 Jan-May.tab | 232,186,851 | 882,826 | same |
| ... 2018 June-Aug.tab | 141,815,974 | 542,518 | same |
| ... 2018 Sep-Dec.tab | 180,982,884 | 687,237 | same |
| ESMI voltage data 2019 Jan-June.csv | 241,193,056 | not ingested | unknown until opened |
| ESMI voltage data 2019 July-Dec.csv | 186,876,236 | not ingested | unknown until opened |
| About the initiative.pdf | 644,407 | | |

Voltage values range from 0 to 350 in the 2014 file's metadata. Which value means "no supply" must be checked in `About the initiative.pdf`.

## Licence and terms text (recorded, not interpreted; S6 is for the humans)

Neither download page could be read by automated fetch on 2026-11-11. The figshare page
returned HTTP 403. The Dataverse page returned an empty bot-check response (HTTP 202,
0 bytes). The text below comes from each record's official API, not from the page.
**A human must read both pages for S6.**

* **EAGLE-I**, from https://api.figshare.com/v2/articles/24237376, field `license`:
  `{"value": 1, "name": "CC BY 4.0", "url": "https://creativecommons.org/licenses/by/4.0/"}`.
  Citation field: "Brelsford, Christa; Tennille, Sarah; Myers, Aaron; Chinthavali, Supriya; Tansakul, Varisara; Denman, Matthew; et al. (2023). The Environment for Analysis of Geo-Located Energy Information’s Recorded Electricity Outages 2014-2025. figshare. Dataset. https://doi.org/10.6084/m9.figshare.24237376.v4"
  Description (verbatim, HTML tags removed): "The core of the provided dataset includes eight years of power outage information at the county level from 2014 to 2025 at 15-minute intervals collected from utility’s public outage maps on their websites by the EAGLE-I program at ORNL. Three supplementary files are included to augment the power outage data files. The first file includes the customer coverage rate of each state from 2018-2022. The second file provides the modeled number of electric customers per county as of 2022. The third presents our Data Quality Index and the four sub-components by year by FEMA Region for 2018-2022. UPDATE 2/16/2023: Added 2023 outage data and Uri_Map.R and DQI_processing.R files have been added. They were used to create graphics in associated works. UPDATE 4/10/2025: Added 2024 outage data. UPDATE 2/24/2026: Added 2025 outage data."
* **ESMI**, from https://dataverse.harvard.edu/api/datasets/:persistentId/?persistentId=doi:10.7910/DVN/CLLZZM, `latestVersion`:
  `license`: `{"name": "CC0 1.0", "uri": "http://creativecommons.org/publicdomain/zero/1.0", "rightsIdentifier": "CC0-1.0"}`. `termsOfUse`: null. `termsOfAccess`: null. `fileAccessRequest`: false. `guestbookId`: 251.
  The guestbook's questions and any text it shows were not seen.

## Code map

| Module | What it does |
|---|---|
| `fourhour_ml/config.py` | Paths, `TRAIN_YEARS=(2014,2022)`, `TEST_YEARS=(2023,2024)`, `SEED=20261111`, demo counties, `GAP_BRIDGE_MINUTES=30` |
| `fourhour_ml/split.py` | `split_by_year` (the only split), `@train_only` guard, `LeakageError` |
| `fourhour_ml/eaglei.py` | File registry, streaming reader, header normalisation, state filter, MCC loader |
| `fourhour_ml/fetch.py` | Resumable, MD5-verified download; per-year filter + manifest |
| `fourhour_ml/events.py` | Event reconstruction (SPEC 5.1), interval coverage, missing-data runs |
| `fourhour_ml/esmi.py` | ESMI readers and per-district counts (not yet run on real files) |
| `fourhour_ml/schema.py` | Artifact schema check, `make_provenance`, `write_artifact` (validates first; writes nothing on failure) |
| `download.py` | CLI for the downloads |

### Event reconstruction rules (`events.py`)

Each row at time `t` covers `[t, t+15m)`, and `share = customers_out / MCC customers`. An interval is above when `share >= threshold`. Missing and null rows are never above. Above intervals merge when the gap between them is 30 minutes or less, whether the gap is below-threshold rows, missing rows, or both. The gap time counts in the duration, and each event reports how many intervals it bridged of each kind. `end = last above t + 15m`.

### Split guard

Every fitting, tuning or threshold function must be decorated with `@train_only`. The decorator refuses any DataFrame or Series argument with a row outside 2014-2022. It reads the year from a `year` column, any datetime column, or a DatetimeIndex. A frame with no time information is refused. A test fails if any function named `fit_*`, `tune_*`, `choose_*`, `select_threshold*` or `grid_search*` in `fourhour_ml` lacks the guard. Note that `split_by_year` drops 2025: it is neither train nor test.

### Schema check

`validate_artifact(kind, doc)` enforces every key CONTRACTS.md section 8 names, including the full provenance block, `train_years == [2014, 2022]` and `test_years == [2023, 2024]`. It also enforces these rules:

* Nulls are allowed only when `provenance.notes` gives a reason.
* No NaN or infinity.
* No artifact may mention a placeholder.

Where section 8 describes contents without naming keys, the check enforces only what is named. `schema.UNSPECIFIED` lists those gaps.

## Regenerating artifacts

Not yet. There is no artifact-generating code in Gate 1. The single command will be documented here when phase 4 adds it.
