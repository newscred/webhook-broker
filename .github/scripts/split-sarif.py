import json
import os

INPUT_FILE = "snyk.sarif"
OUTPUT_DIR = "snyk-split-runs"

os.makedirs(OUTPUT_DIR, exist_ok=True)

with open(INPUT_FILE, "r") as f:
    sarif = json.load(f)

for i, run in enumerate(sarif.get("runs", [])):
    split_file = os.path.join(OUTPUT_DIR, f"snyk_{i}.sarif")
    with open(split_file, "w") as out:
        print("RUN:", i, run)
        json.dump({
            "version": sarif["version"],
            "runs": [run]
        }, out)
