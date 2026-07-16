import csv
import json
import os
from pathlib import Path

OUTPUTS_DIR = Path("outputs")
OUT_PATH = OUTPUTS_DIR / "outputs_manifest.csv"

def find_one(pool_dir: Path, prefix: str):
    files = sorted(pool_dir.glob(f"{prefix}*.csv"))
    return files[0] if files else None

def count_csv_rows(path: Path):
    if path is None or not path.exists():
        return 0

    with path.open("r", encoding="utf-8") as f:
        # minus header
        return max(sum(1 for _ in f) - 1, 0)

def read_max_validation(path: Path):
    if path is None or not path.exists():
        return "", ""

    max_diff_bps = 0.0
    max_tick_delta = 0

    with path.open("r", encoding="utf-8") as f:
        reader = csv.DictReader(f)

        for row in reader:
            diff = float(row.get("amount_out_diff_bps", "0") or "0")
            tick_delta = abs(int(row.get("tick_delta", "0") or "0"))

            max_diff_bps = max(max_diff_bps, diff)
            max_tick_delta = max(max_tick_delta, tick_delta)

    return str(max_diff_bps), str(max_tick_delta)

def main():
    rows = []

    for pool_dir in sorted(OUTPUTS_DIR.iterdir()):
        if not pool_dir.is_dir():
            continue

        config_path = pool_dir / "pool_config.json"
        if not config_path.exists():
            continue

        with config_path.open("r", encoding="utf-8") as f:
            cfg = json.load(f)

        summary_path = find_one(pool_dir, "snapshot_batch_summary_")
        positions_path = find_one(pool_dir, "snapshot_batch_positions_")
        diagnostics_path = find_one(pool_dir, "snapshot_batch_diagnostics_")
        validation_path = find_one(pool_dir, "swap_validation_")

        snapshot_count = count_csv_rows(summary_path)
        observation_count = count_csv_rows(positions_path)
        diagnostics_count = count_csv_rows(diagnostics_path)
        validation_samples = count_csv_rows(validation_path)

        max_diff_bps, max_tick_delta = read_max_validation(validation_path)

        rows.append({
            "pool_name": cfg.get("pool_name", pool_dir.name),
            "pool_address": cfg.get("pool_address", ""),
            "pool_fee": cfg.get("pool_fee", ""),
            "token0_symbol": cfg.get("token0_symbol", ""),
            "token1_symbol": cfg.get("token1_symbol", ""),
            "token0_decimals": cfg.get("token0_decimals", ""),
            "token1_decimals": cfg.get("token1_decimals", ""),
            "latest_block": cfg.get("latest_block", ""),
            "lookback_blocks": cfg.get("lookback_blocks", ""),
            "step_blocks": cfg.get("step_blocks", ""),
            "max_snapshots_configured": cfg.get("max_snapshots", ""),
            "position_limit": cfg.get("position_limit", ""),
            "snapshot_count": snapshot_count,
            "diagnostics_count": diagnostics_count,
            "observation_count": observation_count,
            "validation_samples": validation_samples,
            "max_amount_out_diff_bps": max_diff_bps,
            "max_tick_delta": max_tick_delta,
            "summary_file": summary_path.name if summary_path else "",
            "positions_file": positions_path.name if positions_path else "",
            "diagnostics_file": diagnostics_path.name if diagnostics_path else "",
            "validation_file": validation_path.name if validation_path else "",
        })

    fieldnames = [
        "pool_name",
        "pool_address",
        "pool_fee",
        "token0_symbol",
        "token1_symbol",
        "token0_decimals",
        "token1_decimals",
        "latest_block",
        "lookback_blocks",
        "step_blocks",
        "max_snapshots_configured",
        "position_limit",
        "snapshot_count",
        "diagnostics_count",
        "observation_count",
        "validation_samples",
        "max_amount_out_diff_bps",
        "max_tick_delta",
        "summary_file",
        "positions_file",
        "diagnostics_file",
        "validation_file",
    ]

    with OUT_PATH.open("w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)

    print(f"manifest written: {OUT_PATH}")
    print(f"pools: {len(rows)}")

if __name__ == "__main__":
    main()