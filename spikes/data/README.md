# Data spikes

Run from the repo root after setting up `ml/.venv` (see `ml/README.md`).

* `s3_eaglei.py`: run `ml/.venv/bin/python spikes/data/s3_eaglei.py`. It downloads the EAGLE-I 2024 file (1.44 GB) and MCC.csv if they are missing, then prints the columns, the 2024 row counts for the three demo counties, and Harris County's 4-hour-or-more events at a PLACEHOLDER threshold. **Pass:** it ends with `S3 PASS` and prints a non-empty event list. 2024 is a test year, so nothing it prints may feed any fitting or threshold choice.
* `s4_esmi.py`: run `ml/.venv/bin/python spikes/data/s4_esmi.py`. It needs the ESMI files downloaded by hand into `ml/data/raw/esmi/`, because Dataverse requires a guestbook form. It prints the file structure, the time span, premises per district, and the districts passing the proposed cut-off (median of 10 or more monitors reporting per day in both halves of the period). **Pass:** it ends with `S4 PASS` (exit 0). Exit 2 means the files are missing; exit 1 means no district passes.
