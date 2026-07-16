import csv
import json
from pathlib import Path
from decimal import Decimal, InvalidOperation

OUTPUTS_DIR = Path("outputs")
PAPER_DIR = OUTPUTS_DIR / "paper_tables"

POOL_LABELS = {
    "usdc_usdt_001": "USDC/USDT 0.01%",
    "usdc_weth_005": "USDC/WETH 0.05%",
    "usdc_weth_030": "USDC/WETH 0.30%",
    "wbtc_weth_030": "WBTC/WETH 0.30%",
    "weth_usdt_005": "WETH/USDT 0.05%",
}

CORRELATION_METRICS = [
    "active_liquidity_share",
    "range_width",
    "distance_to_nearest_edge",
    "normalized_liquidity_density",
]


def D(value, default=Decimal("0")):
    if value is None or value == "":
        return default
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError):
        return default


def fmt_decimal(value, digits=3):
    value = D(value)
    q = Decimal(10) ** -digits
    return str(value.quantize(q))


def fmt_pct(value, digits=1):
    value = D(value) * Decimal(100)
    q = Decimal(10) ** -digits
    return str(value.quantize(q)) + "%"


def read_csv(path):
    with path.open("r", encoding="utf-8", newline="") as f:
        return list(csv.DictReader(f))


def write_csv(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def write_markdown(path, rows, fieldnames):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", encoding="utf-8") as f:
        f.write("| " + " | ".join(fieldnames) + " |\n")
        f.write("| " + " | ".join(["---"] * len(fieldnames)) + " |\n")
        for row in rows:
            f.write("| " + " | ".join(str(row.get(k, "")) for k in fieldnames) + " |\n")


def find_one(pool_dir, prefix):
    files = sorted(pool_dir.glob(prefix + "*.csv"))
    return files[0] if files else None


def row_count(path):
    if not path:
        return 0
    with path.open("r", encoding="utf-8") as f:
        return max(sum(1 for _ in f) - 1, 0)


def load_pools():
    pools = []

    for pool_dir in sorted(OUTPUTS_DIR.iterdir()):
        if not pool_dir.is_dir() or pool_dir.name == "paper_tables":
            continue

        config_path = pool_dir / "pool_config.json"
        if not config_path.exists():
            continue

        cfg = json.loads(config_path.read_text(encoding="utf-8"))
        pool_name = cfg.get("pool_name", pool_dir.name)

        pools.append({
            "pool_dir": pool_dir,
            "pool_name": pool_name,
            "pool_label": POOL_LABELS.get(pool_name, pool_name),
            "cfg": cfg,
            "summary": find_one(pool_dir, "snapshot_batch_summary_"),
            "positions": find_one(pool_dir, "snapshot_batch_positions_"),
            "diagnostics": find_one(pool_dir, "snapshot_batch_diagnostics_"),
            "empirical": find_one(pool_dir, "empirical_summary_"),
            "correlations": find_one(pool_dir, "snapshot_batch_position_correlations_"),
            "ranges": find_one(pool_dir, "snapshot_batch_ranges_"),
            "validation": find_one(pool_dir, "swap_validation_"),
        })

    return pools


def read_empirical_means(path):
    rows = read_csv(path)
    return {r["metric"]: D(r["mean"]) for r in rows}


def validation_stats(path):
    rows = read_csv(path)
    max_diff = Decimal("0")
    max_tick = 0

    for r in rows:
        max_diff = max(max_diff, D(r.get("amount_out_diff_bps")))
        max_tick = max(max_tick, abs(int(r.get("tick_delta") or 0)))

    return len(rows), max_diff, max_tick


def table_1_dataset_validation(pools):
    rows = []

    for p in pools:
        cfg = p["cfg"]
        samples, max_diff, max_tick = validation_stats(p["validation"])

        latest_block = ""
        summary_rows = read_csv(p["summary"])
        if summary_rows:
            latest_block = summary_rows[0].get("block_number", "")

        rows.append({
            "Pool": p["pool_label"],
            "Fee tier": cfg.get("pool_fee", ""),
            "Token0/Token1": f"{cfg.get('token0_symbol', '')}/{cfg.get('token1_symbol', '')}",
            "Latest block": latest_block,
            "Snapshots": row_count(p["summary"]),
            "Top-K obs.": row_count(p["positions"]),
            "Validation swaps": samples,
            "Max diff (bps)": fmt_decimal(max_diff, 9),
            "Max tick Δ": max_tick,
        })

    return rows


def table_2_pool_empirical_summary(pools):
    rows = []

    for p in pools:
        means = read_empirical_means(p["empirical"])

        rows.append({
            "Pool": p["pool_label"],
            "ZFO PIAUC": fmt_decimal(means.get("zero_for_one_base_auc_bps"), 2),
            "OFZ PIAUC": fmt_decimal(means.get("one_for_zero_base_auc_bps"), 2),
            "Top1 LSIS share": fmt_pct(means.get("top1_lsis_share"), 1),
            "Top3 LSIS share": fmt_pct(means.get("top3_lsis_share"), 1),
            "Eff. LSIS positions": fmt_decimal(means.get("effective_lsis_positions_topk"), 2),
            "LSIS Gini": fmt_decimal(means.get("total_lsis_gini_topk"), 3),
        })

    return rows


def table_3_lsis_vs_active_concentration(pools):
    rows = []

    for p in pools:
        means = read_empirical_means(p["empirical"])

        rows.append({
            "Pool": p["pool_label"],
            "Top1 active": fmt_pct(means.get("top1_active_share"), 1),
            "Top1 LSIS": fmt_pct(means.get("top1_lsis_share"), 1),
            "Δ Top1": fmt_pct(D(means.get("top1_lsis_share")) - D(means.get("top1_active_share")), 1),
            "Top3 active": fmt_pct(means.get("top3_active_share"), 1),
            "Top3 LSIS": fmt_pct(means.get("top3_lsis_share"), 1),
            "Δ Top3": fmt_pct(D(means.get("top3_lsis_share")) - D(means.get("top3_active_share")), 1),
            "Active HHI": fmt_decimal(means.get("active_hhi_normalized_topk"), 3),
            "LSIS HHI": fmt_decimal(means.get("total_lsis_hhi_topk"), 3),
        })

    return rows


def table_4_baseline_correlations(pools, target="total_lsis_bps"):
    rows = []

    for p in pools:
        corr_rows = read_csv(p["correlations"])
        corr_by_metric = {
            r["metric"]: r
            for r in corr_rows
            if r.get("target") == target
        }

        row = {"Pool": p["pool_label"]}

        for metric in CORRELATION_METRICS:
            r = corr_by_metric.get(metric)
            row[f"{metric} Pearson"] = fmt_decimal(r.get("pearson") if r else "", 3)
            row[f"{metric} Spearman"] = fmt_decimal(r.get("spearman") if r else "", 3)

        rows.append(row)

    return rows


def table_5_persistent_ranges(pools, top_n=3):
    rows = []

    for p in pools:
        ranges = read_csv(p["ranges"])
        ranges.sort(
            key=lambda r: (
                int(r.get("rank1_count") or 0),
                D(r.get("avg_total_lsis_bps")),
            ),
            reverse=True,
        )

        for r in ranges[:top_n]:
            rows.append({
                "Pool": p["pool_label"],
                "Range": r.get("range_key", ""),
                "Appearances": r.get("appearances", ""),
                "Rank-1 count": r.get("rank1_count", ""),
                "Avg rank": fmt_decimal(r.get("avg_rank"), 2),
                "Avg active share": fmt_pct(r.get("avg_active_liquidity_share"), 2),
                "Avg total LSIS": fmt_decimal(r.get("avg_total_lsis_bps"), 2),
                "Max total LSIS": fmt_decimal(r.get("max_total_lsis_bps"), 2),
            })

    return rows


def overall_claim_counts(pools):
    total_snapshots = 0
    total_observations = 0
    top1_lsis_gt_active = 0
    top3_lsis_gt_active = 0
    lsis_hhi_gt_active_hhi = 0
    lsis_gini_gt_active_gini = 0

    for p in pools:
        rows = read_csv(p["diagnostics"])
        total_snapshots += len(rows)
        total_observations += row_count(p["positions"])

        for r in rows:
            if D(r.get("top1_lsis_share")) > D(r.get("top1_active_share")):
                top1_lsis_gt_active += 1
            if D(r.get("top3_lsis_share")) > D(r.get("top3_active_share")):
                top3_lsis_gt_active += 1
            if D(r.get("total_lsis_hhi_topk")) > D(r.get("active_hhi_normalized_topk")):
                lsis_hhi_gt_active_hhi += 1
            if D(r.get("total_lsis_gini_topk")) > D(r.get("active_share_gini_topk")):
                lsis_gini_gt_active_gini += 1

    return [{
        "total_pools": len(pools),
        "total_snapshots": total_snapshots,
        "total_observations": total_observations,
        "top1_lsis_share_gt_top1_active_share": f"{top1_lsis_gt_active}/{total_snapshots}",
        "top3_lsis_share_gt_top3_active_share": f"{top3_lsis_gt_active}/{total_snapshots}",
        "lsis_hhi_gt_active_hhi": f"{lsis_hhi_gt_active_hhi}/{total_snapshots}",
        "lsis_gini_gt_active_gini": f"{lsis_gini_gt_active_gini}/{total_snapshots}",
    }]


def main():
    pools = load_pools()
    if not pools:
        raise SystemExit("No pool outputs found. Run from repo root where outputs/ exists.")

    tables = [
        ("table_1_dataset_validation", table_1_dataset_validation(pools)),
        ("table_2_pool_empirical_summary", table_2_pool_empirical_summary(pools)),
        ("table_3_lsis_vs_active_concentration", table_3_lsis_vs_active_concentration(pools)),
        ("table_4_baseline_correlations_total_lsis", table_4_baseline_correlations(pools)),
        ("table_5_persistent_load_bearing_ranges", table_5_persistent_ranges(pools, top_n=3)),
        ("overall_claim_counts", overall_claim_counts(pools)),
    ]

    for name, rows in tables:
        if not rows:
            continue

        fieldnames = list(rows[0].keys())
        write_csv(PAPER_DIR / f"{name}.csv", rows, fieldnames)
        write_markdown(PAPER_DIR / f"{name}.md", rows, fieldnames)

    print(f"wrote paper tables to: {PAPER_DIR}")

    for name, rows in tables:
        print(f"- {name}: {len(rows)} rows")


if __name__ == "__main__":
    main()