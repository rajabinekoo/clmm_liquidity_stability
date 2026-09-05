#!/usr/bin/env python3
"""Compare D50/D100/D300 snapshot structural-stress outputs.

The script intentionally uses only Python's standard library so it can run on
research machines without adding analysis dependencies.
"""

from __future__ import annotations

import argparse
import csv
import glob
import math
import os
import statistics
from collections import defaultdict
from dataclasses import dataclass
from typing import Dict, Iterable, List, Mapping, Optional, Sequence, Tuple

DOMAINS = ("d50", "d100", "d300")
POOLS = (
    "usdc_usdt_001",
    "usdc_weth_005",
    "usdc_weth_030",
    "wbtc_weth_030",
    "weth_usdt_005",
)
DOMAIN_PAIRS = (("d50", "d100"), ("d50", "d300"), ("d100", "d300"))


@dataclass(frozen=True)
class PositionRow:
    block: int
    position_key: str
    active_share: float
    lsis: float
    lsis_rank: int


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default="stress_outputs")
    parser.add_argument("--out", default="stress_outputs/comparison")
    return parser.parse_args()


def find_one(pattern: str) -> Optional[str]:
    matches = sorted(glob.glob(pattern))
    if not matches:
        return None
    if len(matches) > 1:
        raise RuntimeError(f"expected one file for {pattern!r}, found {len(matches)}")
    return matches[0]


def read_csv(path: Optional[str]) -> List[dict]:
    if path is None or not os.path.exists(path):
        return []
    with open(path, "r", newline="", encoding="utf-8") as handle:
        return list(csv.DictReader(handle))


def to_float(value: str) -> Optional[float]:
    try:
        parsed = float(value)
    except (TypeError, ValueError):
        return None
    return parsed if math.isfinite(parsed) else None


def mean(values: Sequence[float]) -> Optional[float]:
    return statistics.fmean(values) if values else None


def median(values: Sequence[float]) -> Optional[float]:
    return statistics.median(values) if values else None


def fmt(value: Optional[float]) -> str:
    if value is None or not math.isfinite(value):
        return ""
    return f"{value:.12g}"


def average_ranks(values: Sequence[float]) -> List[float]:
    order = sorted(range(len(values)), key=lambda i: values[i])
    ranks = [0.0] * len(values)
    i = 0
    while i < len(order):
        j = i + 1
        while j < len(order) and values[order[j]] == values[order[i]]:
            j += 1
        avg = ((i + 1) + j) / 2.0
        for k in range(i, j):
            ranks[order[k]] = avg
        i = j
    return ranks


def pearson(xs: Sequence[float], ys: Sequence[float]) -> Optional[float]:
    if len(xs) != len(ys) or len(xs) < 2:
        return None
    mx = statistics.fmean(xs)
    my = statistics.fmean(ys)
    dx = [x - mx for x in xs]
    dy = [y - my for y in ys]
    sx = sum(v * v for v in dx)
    sy = sum(v * v for v in dy)
    if sx == 0 or sy == 0:
        return None
    return sum(a * b for a, b in zip(dx, dy)) / math.sqrt(sx * sy)


def spearman(xs: Sequence[float], ys: Sequence[float]) -> Optional[float]:
    if len(xs) != len(ys) or len(xs) < 2:
        return None
    return pearson(average_ranks(xs), average_ranks(ys))


def grouped_positions(rows: Iterable[dict]) -> Dict[int, List[PositionRow]]:
    grouped: Dict[int, List[PositionRow]] = defaultdict(list)
    for row in rows:
        active = to_float(row.get("active_liquidity_share", ""))
        lsis = to_float(row.get("total_lsis_bps", ""))
        if active is None or lsis is None:
            continue
        grouped[int(row["block_number"])].append(
            PositionRow(
                block=int(row["block_number"]),
                position_key=row["position_key"],
                active_share=active,
                lsis=lsis,
                lsis_rank=int(row["rank"]),
            )
        )
    return grouped


def snapshot_metrics(rows: Sequence[PositionRow]) -> Optional[dict]:
    if not rows:
        return None

    by_lsis = sorted(rows, key=lambda r: (r.lsis_rank, r.position_key))
    max_active = max(row.active_share for row in rows)
    active_top_candidates = {row.position_key for row in rows if row.active_share == max_active}
    top1_mismatch = by_lsis[0].position_key not in active_top_candidates

    by_active = sorted(rows, key=lambda r: (-r.active_share, r.position_key))
    top3_overlap = None
    if len(rows) >= 3:
        lsis_top3 = {row.position_key for row in by_lsis[:3]}
        active_top3 = {row.position_key for row in by_active[:3]}
        top3_overlap = len(lsis_top3 & active_top3) / 3.0

    rho = None
    if len(rows) >= 5:
        rho = spearman(
            [row.lsis for row in rows],
            [row.active_share for row in rows],
        )

    total_lsis = sum(max(row.lsis, 0.0) for row in rows)
    top1_share = None
    top3_share = None
    if total_lsis > 0:
        top1_share = max(by_lsis[0].lsis, 0.0) / total_lsis
        if len(rows) >= 3:
            top3_share = sum(max(row.lsis, 0.0) for row in by_lsis[:3]) / total_lsis

    return {
        "spearman": rho,
        "top1_mismatch": top1_mismatch,
        "top3_overlap": top3_overlap,
        "top1_lsis_share": top1_share,
        "top3_lsis_share": top3_share,
    }


def aggregate_snapshot_metrics(grouped: Mapping[int, Sequence[PositionRow]], blocks: Optional[set] = None) -> dict:
    metrics = []
    for block, rows in grouped.items():
        if blocks is not None and block not in blocks:
            continue
        metric = snapshot_metrics(rows)
        if metric is not None:
            metrics.append(metric)

    rhos = [m["spearman"] for m in metrics if m["spearman"] is not None]
    top1 = [m["top1_lsis_share"] for m in metrics if m["top1_lsis_share"] is not None]
    top3 = [m["top3_lsis_share"] for m in metrics if m["top3_lsis_share"] is not None]
    top3_overlaps = [m["top3_overlap"] for m in metrics if m["top3_overlap"] is not None]
    mismatch = sum(1 for m in metrics if m["top1_mismatch"])

    return {
        "eligible_snapshots": len(metrics),
        "valid_spearman_snapshots": len(rhos),
        "mean_spearman_lsis_vs_active_share": mean(rhos),
        "median_spearman_lsis_vs_active_share": median(rhos),
        "minimum_spearman_lsis_vs_active_share": min(rhos) if rhos else None,
        "top1_mismatch_count": mismatch,
        "top1_mismatch_percent": (100.0 * mismatch / len(metrics)) if metrics else None,
        "top3_overlap_snapshots": len(top3_overlaps),
        "mean_top3_overlap_fraction": mean(top3_overlaps),
        "mean_top1_lsis_share": mean(top1),
        "mean_top3_lsis_share": mean(top3),
    }


def load_domain_pool(root: str, domain: str, pool: str) -> dict:
    base = os.path.join(root, domain, pool)
    positions = read_csv(find_one(os.path.join(base, "snapshot_batch_positions_*.csv")))
    joint = read_csv(find_one(os.path.join(base, "snapshot_batch_joint_removal_*.csv")))
    audit = read_csv(find_one(os.path.join(base, "analysis_amount_grid_*_audit.csv")))
    metadata = read_csv(os.path.join(base, "stress_snapshot_run_metadata.csv"))
    return {
        "positions": grouped_positions(positions),
        "joint": joint,
        "audit": audit,
        "metadata": metadata[0] if metadata else {},
    }


def joint_metrics(rows: Sequence[dict], scenario_id: str) -> dict:
    values = []
    total_rows = 0
    skipped = 0
    for row in rows:
        if row.get("scenario_id") != scenario_id:
            continue
        total_rows += 1
        if row.get("skipped", "").lower() == "true":
            skipped += 1
            continue
        value = to_float(row.get("total_amplification_ratio", ""))
        if value is not None and value > 0:
            values.append(value)
    return {
        "rows": total_rows,
        "eligible": len(values),
        "skipped": skipped,
        "mean": mean(values),
        "median": median(values),
        "minimum": min(values) if values else None,
        "gt1": sum(1 for value in values if value > 1.0),
    }


def reachability_rows(domain: str, pool: str, audit: Sequence[dict]) -> List[dict]:
    grouped: Dict[Tuple[str, str], dict] = {}
    states = set()
    for row in audit:
        block = row.get("block_number", "")
        states.add(block)
        key = (row.get("target_impact_bps", ""), row.get("direction", ""))
        bucket = grouped.setdefault(key, {"states": set(), "available": set(), "status": defaultdict(int)})
        bucket["states"].add(block)
        bucket["status"][row.get("status", "")] += 1
        if row.get("available", "").lower() == "true":
            bucket["available"].add(block)

    result = []
    for (target, direction), bucket in sorted(grouped.items(), key=lambda kv: (float(kv[0][0]), kv[0][1])):
        total = len(bucket["states"])
        available = len(bucket["available"])
        result.append({
            "domain": domain,
            "pool": pool,
            "target_impact_bps": target,
            "direction": direction,
            "states_attempted": total,
            "states_available": available,
            "availability_percent": (100.0 * available / total) if total else None,
            "status_counts": ";".join(f"{k}:{v}" for k, v in sorted(bucket["status"].items())),
        })
    return result


def rank_stability(left: Mapping[int, Sequence[PositionRow]], right: Mapping[int, Sequence[PositionRow]]) -> dict:
    common_blocks = sorted(set(left) & set(right))
    rhos = []
    top1_same = 0
    top3_overlaps = []
    eligible = 0

    for block in common_blocks:
        lmap = {row.position_key: row for row in left[block]}
        rmap = {row.position_key: row for row in right[block]}
        keys = sorted(set(lmap) & set(rmap))
        if len(keys) < 5:
            continue
        rho = spearman([lmap[k].lsis for k in keys], [rmap[k].lsis for k in keys])
        if rho is None:
            continue
        eligible += 1
        rhos.append(rho)

        ltop = min(left[block], key=lambda row: (row.lsis_rank, row.position_key)).position_key
        rtop = min(right[block], key=lambda row: (row.lsis_rank, row.position_key)).position_key
        if ltop == rtop:
            top1_same += 1

        l3 = {row.position_key for row in sorted(left[block], key=lambda row: (row.lsis_rank, row.position_key))[:3]}
        r3 = {row.position_key for row in sorted(right[block], key=lambda row: (row.lsis_rank, row.position_key))[:3]}
        top3_overlaps.append(len(l3 & r3) / 3.0)

    return {
        "common_blocks": len(common_blocks),
        "eligible_snapshots": eligible,
        "mean_spearman_lsis_rank_stability": mean(rhos),
        "median_spearman_lsis_rank_stability": median(rhos),
        "minimum_spearman_lsis_rank_stability": min(rhos) if rhos else None,
        "top1_same_count": top1_same,
        "top1_same_percent": (100.0 * top1_same / eligible) if eligible else None,
        "mean_top3_overlap_fraction": mean(top3_overlaps),
    }


def write_rows(path: str, fieldnames: Sequence[str], rows: Sequence[Mapping[str, object]]) -> None:
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=fieldnames)
        writer.writeheader()
        for row in rows:
            writer.writerow({key: fmt(value) if isinstance(value, float) else value for key, value in row.items()})


def main() -> None:
    args = parse_args()
    os.makedirs(args.out, exist_ok=True)

    data = {}
    for domain in DOMAINS:
        for pool in POOLS:
            data[(domain, pool)] = load_domain_pool(args.root, domain, pool)

    reach_rows = []
    pool_rows = []
    for domain in DOMAINS:
        for pool in POOLS:
            item = data[(domain, pool)]
            reach_rows.extend(reachability_rows(domain, pool, item["audit"]))
            agg = aggregate_snapshot_metrics(item["positions"])
            top3 = joint_metrics(item["joint"], "top_3_by_lsis")
            top5 = joint_metrics(item["joint"], "top_5_by_lsis")
            meta = item["metadata"]
            pool_rows.append({
                "domain": domain,
                "pool": pool,
                "targets_bps": meta.get("target_impacts_bps", ""),
                "grid_unique_states": int(meta.get("resolved_unique_states", "0") or 0),
                "grid_complete_states": int(meta.get("complete_grid_states", "0") or 0),
                "grid_incomplete_states": int(meta.get("incomplete_grid_states", "0") or 0),
                "analyzed_snapshots": int(meta.get("analyzed_snapshots", "0") or 0),
                **agg,
                "top3_joint_eligible": top3["eligible"],
                "top3_amp_mean": top3["mean"],
                "top3_amp_median": top3["median"],
                "top3_amp_min": top3["minimum"],
                "top3_amp_gt1_count": top3["gt1"],
                "top5_joint_eligible": top5["eligible"],
                "top5_amp_mean": top5["mean"],
                "top5_amp_median": top5["median"],
                "top5_amp_min": top5["minimum"],
                "top5_amp_gt1_count": top5["gt1"],
            })

    common_rows = []
    for pool in POOLS:
        complete_sets = [set(data[(domain, pool)]["positions"]) for domain in DOMAINS]
        common = set.intersection(*complete_sets) if complete_sets else set()
        for domain in DOMAINS:
            agg = aggregate_snapshot_metrics(data[(domain, pool)]["positions"], common)
            common_rows.append({
                "pool": pool,
                "domain": domain,
                "common_complete_snapshots_all_domains": len(common),
                **agg,
            })

    stability_rows = []
    for pool in POOLS:
        for left, right in DOMAIN_PAIRS:
            metrics = rank_stability(data[(left, pool)]["positions"], data[(right, pool)]["positions"])
            stability_rows.append({
                "pool": pool,
                "left_domain": left,
                "right_domain": right,
                **metrics,
            })

    write_rows(
        os.path.join(args.out, "stress_domain_reachability.csv"),
        ["domain", "pool", "target_impact_bps", "direction", "states_attempted", "states_available", "availability_percent", "status_counts"],
        reach_rows,
    )
    write_rows(
        os.path.join(args.out, "stress_domain_pool_summary.csv"),
        list(pool_rows[0].keys()) if pool_rows else [],
        pool_rows,
    )
    write_rows(
        os.path.join(args.out, "stress_domain_common_complete_summary.csv"),
        list(common_rows[0].keys()) if common_rows else [],
        common_rows,
    )
    write_rows(
        os.path.join(args.out, "stress_domain_rank_stability.csv"),
        list(stability_rows[0].keys()) if stability_rows else [],
        stability_rows,
    )

    report_path = os.path.join(args.out, "STRESS_DOMAIN_REPORT.txt")
    with open(report_path, "w", encoding="utf-8") as handle:
        handle.write("D50 / D100 / D300 structural stress-domain comparison\n")
        handle.write("====================================================\n\n")
        for pool in POOLS:
            handle.write(f"{pool}\n")
            for domain in DOMAINS:
                row = next(r for r in pool_rows if r["pool"] == pool and r["domain"] == domain)
                handle.write(
                    f"  {domain}: complete={row['analyzed_snapshots']}, "
                    f"rho_size_mean={fmt(row['mean_spearman_lsis_vs_active_share'])}, "
                    f"rho_size_median={fmt(row['median_spearman_lsis_vs_active_share'])}, "
                    f"top1_mismatch={row['top1_mismatch_count']}/{row['eligible_snapshots']}, "
                    f"top3_overlap={fmt(row['mean_top3_overlap_fraction'])}, "
                    f"top3_amp_med={fmt(row['top3_amp_median'])}, "
                    f"top5_amp_med={fmt(row['top5_amp_median'])}\n"
                )
            for left, right in DOMAIN_PAIRS:
                row = next(r for r in stability_rows if r["pool"] == pool and r["left_domain"] == left and r["right_domain"] == right)
                handle.write(
                    f"  rank {left}->{right}: n={row['eligible_snapshots']}, "
                    f"rho_med={fmt(row['median_spearman_lsis_rank_stability'])}, "
                    f"top1_same={fmt(row['top1_same_percent'])}%, "
                    f"top3_overlap={fmt(row['mean_top3_overlap_fraction'])}\n"
                )
            handle.write("\n")

    print(f"wrote comparison outputs to {args.out}")
    print(f"quick report: {report_path}")


if __name__ == "__main__":
    main()
