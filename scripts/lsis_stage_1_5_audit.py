#!/usr/bin/env python3
from __future__ import annotations

import argparse
import glob
import hashlib
import itertools
import json
import math
import shutil
import sys
import zipfile
from dataclasses import dataclass
from pathlib import Path

import numpy as np
import pandas as pd


POOL_ORDER = [
    "usdc_usdt_001",
    "usdc_weth_005",
    "usdc_weth_030",
    "wbtc_weth_030",
    "weth_usdt_005",
]

FALLBACK_LABELS = {
    "usdc_usdt_001": "USDC/USDT 0.01%",
    "usdc_weth_005": "USDC/WETH 0.05%",
    "usdc_weth_030": "USDC/WETH 0.30%",
    "wbtc_weth_030": "WBTC/WETH 0.30%",
    "weth_usdt_005": "WETH/USDT 0.05%",
}

BURN_PREDICTORS = {
    "immediate_total_lsis_bps": "Immediate LSIS",
    "active_removal_share": "Active removal share",
    "liquidity_removed": "Removed liquidity",
    "removed_liquidity_density": "Removed liquidity density",
    "removal_fraction": "Removal fraction",
    "range_width": "Range width",
    "normalized_distance_outside_range": "Distance outside range",
}


@dataclass(frozen=True)
class Pool:
    key: str
    path: Path
    label: str


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description=(
            "Stage 1.5 audit for the CLMM LSIS paper. "
            "Reads existing outputs only; it does not rerun or modify the Go analysis."
        )
    )
    p.add_argument(
        "--outputs",
        type=Path,
        default=Path("outputs"),
        help="Path to the existing outputs directory (default: ./outputs)",
    )
    p.add_argument(
        "--out",
        type=Path,
        default=Path("stage_1_5"),
        help="Directory for generated audit artifacts (default: ./stage_1_5)",
    )
    p.add_argument(
        "--near-share-gap",
        type=float,
        default=0.10,
        help=(
            "Maximum relative active-share gap for divergence-pair search. "
            "0.10 means <=10%% (default: 0.10)."
        ),
    )
    p.add_argument(
        "--top-candidates",
        type=int,
        default=500,
        help="Maximum near-share divergence pairs to write (default: 500)",
    )
    return p.parse_args()


def as_bool(s: pd.Series) -> pd.Series:
    return s.astype(str).str.strip().str.lower().isin({"true", "1", "yes"})


def numeric(s: pd.Series) -> pd.Series:
    return pd.to_numeric(s, errors="coerce")


def safe_spearman(x: pd.Series, y: pd.Series, min_n: int = 5) -> float:
    x = numeric(x)
    y = numeric(y)
    mask = x.notna() & y.notna()
    if int(mask.sum()) < min_n:
        return math.nan
    xx = x[mask]
    yy = y[mask]
    if xx.nunique() < 2 or yy.nunique() < 2:
        return math.nan
    # Spearman = Pearson correlation of average ranks. This avoids repeatedly
    # invoking scipy and keeps the script dependent only on pandas/numpy.
    rx = xx.rank(method="average").to_numpy(dtype=float)
    ry = yy.rank(method="average").to_numpy(dtype=float)
    if np.std(rx) == 0 or np.std(ry) == 0:
        return math.nan
    return float(np.corrcoef(rx, ry)[0, 1])


def one_file(pool: Pool, pattern: str, exclude: tuple[str, ...] = ()) -> Path:
    matches = [
        Path(p)
        for p in glob.glob(str(pool.path / pattern))
        if not any(token in Path(p).name for token in exclude)
    ]
    if len(matches) != 1:
        raise RuntimeError(
            f"expected exactly one file for {pool.key}/{pattern}, "
            f"found {len(matches)}: {[m.name for m in matches]}"
        )
    return matches[0]


def pool_label(path: Path, key: str) -> str:
    cfg_path = path / "pool_config.json"
    if not cfg_path.exists():
        return FALLBACK_LABELS.get(key, key)
    try:
        with cfg_path.open() as fh:
            cfg = json.load(fh)
        fee_pct = float(cfg["pool_fee"]) / 10_000.0
        return f'{cfg["token0_symbol"]}/{cfg["token1_symbol"]} {fee_pct:.2f}%'
    except Exception:
        return FALLBACK_LABELS.get(key, key)


def load_pools(outputs: Path) -> list[Pool]:
    outputs = outputs.resolve()
    if not outputs.is_dir():
        raise RuntimeError(f"outputs directory does not exist: {outputs}")
    pools: list[Pool] = []
    missing: list[str] = []
    for key in POOL_ORDER:
        p = outputs / key
        if not p.is_dir():
            missing.append(key)
            continue
        pools.append(Pool(key=key, path=p, label=pool_label(p, key)))
    if missing:
        raise RuntimeError(
            "missing expected pool directories: " + ", ".join(missing)
        )
    return pools


def require_columns(df: pd.DataFrame, cols: list[str], source: Path) -> None:
    missing = [c for c in cols if c not in df.columns]
    if missing:
        raise RuntimeError(
            f"{source.name} is missing required columns: {missing}"
        )


def write_csv(df: pd.DataFrame, out: Path, name: str) -> Path:
    path = out / name
    df.to_csv(path, index=False)
    return path


def file_sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def add_input_manifest(rows: list[dict], pool: Pool, kind: str, path: Path) -> None:
    rows.append(
        {
            "pool": pool.label,
            "pool_key": pool.key,
            "input_kind": kind,
            "file_name": path.name,
            "size_bytes": path.stat().st_size,
            "sha256": file_sha256(path),
        }
    )


def load_positions(pool: Pool, manifest: list[dict]) -> pd.DataFrame:
    path = one_file(pool, "snapshot_batch_positions_*.csv")
    add_input_manifest(manifest, pool, "snapshot_positions", path)
    df = pd.read_csv(path)
    require_columns(
        df,
        [
            "snapshot_index", "block_number", "current_tick", "rank",
            "position_label", "position_key", "tick_lower", "tick_upper",
            "position_liquidity", "active_liquidity",
            "active_liquidity_share", "range_width",
            "distance_to_nearest_edge", "zero_for_one_lsis_bps",
            "one_for_zero_lsis_bps", "total_lsis_bps",
            "zero_for_one_base_auc_bps", "one_for_zero_base_auc_bps",
        ],
        path,
    )
    df["pool"] = pool.label
    df["pool_key"] = pool.key
    return df


def load_burn_samples(pool: Pool, manifest: list[dict]) -> pd.DataFrame:
    path = one_file(pool, "burn_event_study_*_samples.csv")
    add_input_manifest(manifest, pool, "burn_samples", path)
    df = pd.read_csv(path)
    require_columns(
        df,
        [
            "event_id", "block_number", "log_index", "current_tick",
            "tick_lower", "tick_upper", "range_location",
            "burn_range_active", "distance_outside_range",
            "active_removal_share", "liquidity_removed",
            "removal_fraction", "zero_for_one_lsis_bps",
            "one_for_zero_lsis_bps", "total_lsis_bps",
        ],
        path,
    )
    df["pool"] = pool.label
    df["pool_key"] = pool.key
    df["burn_range_active_bool"] = as_bool(df["burn_range_active"])
    return df


def load_joint(pool: Pool, manifest: list[dict]) -> pd.DataFrame:
    path = one_file(pool, "snapshot_batch_joint_removal_*.csv")
    add_input_manifest(manifest, pool, "joint_removal", path)
    df = pd.read_csv(path)
    require_columns(
        df,
        [
            "snapshot_index", "scenario_id", "scenario_kind",
            "requested_active_liquidity_share_bps",
            "removed_position_count", "removed_active_liquidity_share",
            "skipped", "sum_individual_total_lsis_bps",
            "joint_total_lsis_bps", "total_interaction_lsis_bps",
            "total_amplification_ratio",
        ],
        path,
    )
    df["pool"] = pool.label
    df["pool_key"] = pool.key
    df["skipped_bool"] = as_bool(df["skipped"])
    return df


def load_main_observations(pool: Pool, manifest: list[dict]) -> pd.DataFrame:
    path = one_file(
        pool,
        "burn_regression_*_observations.csv",
        exclude=("counterfactual", "temporal_placebo", "control_v2"),
    )
    add_input_manifest(manifest, pool, "burn_regression_observations", path)
    df = pd.read_csv(path)
    require_columns(
        df,
        [
            "event_id", "horizon_label", "horizon_blocks",
            "burn_range_active", "immediate_total_lsis_bps",
            "active_removal_share", "liquidity_removed",
            "removed_liquidity_density", "removal_fraction",
            "range_width", "normalized_distance_outside_range",
            "total_realized_deterioration_bps",
        ],
        path,
    )
    df["pool"] = pool.label
    df["pool_key"] = pool.key
    df["burn_range_active_bool"] = as_bool(df["burn_range_active"])
    return df


def load_counterfactual(pool: Pool, manifest: list[dict]) -> pd.DataFrame:
    path = one_file(pool, "burn_regression_*_counterfactual_observations.csv")
    add_input_manifest(manifest, pool, "counterfactual_observations", path)
    df = pd.read_csv(path)
    require_columns(
        df,
        [
            "event_id", "horizon_label", "horizon_blocks",
            "min_total_mechanical_deterioration_bps",
            "max_total_mechanical_deterioration_bps",
        ],
        path,
    )
    df["pool"] = pool.label
    df["pool_key"] = pool.key
    return df


def snapshot_divergence(
    pools: list[Pool],
    positions_by_pool: dict[str, pd.DataFrame],
) -> tuple[pd.DataFrame, pd.DataFrame, pd.DataFrame]:
    snapshot_rows: list[dict] = []
    redundancy_rows: list[dict] = []

    for pool in pools:
        df = positions_by_pool[pool.key].copy()
        for snap, g0 in df.groupby("snapshot_index", sort=True):
            g = g0.copy()
            g["share_rank"] = numeric(g["active_liquidity_share"]).rank(
                method="min", ascending=False
            )
            g["liquidity_rank"] = numeric(g["position_liquidity"]).rank(
                method="min", ascending=False
            )
            g["lsis_rank_calc"] = numeric(g["total_lsis_bps"]).rank(
                method="min", ascending=False
            )

            rho = safe_spearman(
                g["active_liquidity_share"],
                g["total_lsis_bps"],
            )

            lsis_top1 = str(
                g.sort_values(
                    ["total_lsis_bps", "active_liquidity_share"],
                    ascending=[False, False],
                ).iloc[0]["position_key"]
            )
            share_top1 = str(
                g.sort_values(
                    ["active_liquidity_share", "total_lsis_bps"],
                    ascending=[False, False],
                ).iloc[0]["position_key"]
            )

            k = min(3, len(g))
            lsis_top3 = set(
                g.nlargest(k, "total_lsis_bps")["position_key"].astype(str)
            )
            share_top3 = set(
                g.nlargest(k, "active_liquidity_share")["position_key"].astype(str)
            )
            overlap = len(lsis_top3 & share_top3) / k if k else math.nan

            rank_delta = (
                g["share_rank"] - g["lsis_rank_calc"]
            ).abs()

            # Algebraic consistency of active share = position liquidity / pool active liquidity.
            pos_liq = g["position_liquidity"].map(lambda x: int(str(x)))
            active_liq = g["active_liquidity"].map(lambda x: int(str(x)))
            computed_share = pd.Series(
                [
                    (pl / al) if al > 0 else math.nan
                    for pl, al in zip(pos_liq, active_liq)
                ],
                index=g.index,
                dtype=float,
            )
            share_error = (
                computed_share - numeric(g["active_liquidity_share"])
            ).abs()

            share_liq_rho = safe_spearman(
                g["active_liquidity_share"],
                g["position_liquidity"],
                min_n=2,
            )
            same_rank = bool(
                np.array_equal(
                    g["share_rank"].to_numpy(),
                    g["liquidity_rank"].to_numpy(),
                )
            )

            snapshot_rows.append(
                {
                    "pool": pool.label,
                    "pool_key": pool.key,
                    "snapshot_index": int(snap),
                    "block_number": int(g.iloc[0]["block_number"]),
                    "current_tick": int(g.iloc[0]["current_tick"]),
                    "position_count": int(len(g)),
                    "spearman_lsis_vs_active_share": rho,
                    "top1_same": lsis_top1 == share_top1,
                    "top3_overlap_fraction": overlap,
                    "max_absolute_rank_difference": float(rank_delta.max()),
                    "mean_absolute_rank_difference": float(rank_delta.mean()),
                }
            )

            redundancy_rows.append(
                {
                    "pool": pool.label,
                    "pool_key": pool.key,
                    "snapshot_index": int(snap),
                    "position_count": int(len(g)),
                    "spearman_position_liquidity_vs_active_share": share_liq_rho,
                    "same_rank_order": same_rank,
                    "max_abs_active_share_formula_error": float(share_error.max()),
                }
            )

    detail = pd.DataFrame(snapshot_rows)
    redundancy = pd.DataFrame(redundancy_rows)

    summary_rows: list[dict] = []
    for (pool, pool_key), g in detail.groupby(["pool", "pool_key"], sort=False):
        valid = g["spearman_lsis_vs_active_share"].dropna()
        summary_rows.append(
            {
                "pool": pool,
                "pool_key": pool_key,
                "snapshot_count": int(len(g)),
                "valid_spearman_snapshots": int(valid.notna().sum()),
                "mean_spearman": float(valid.mean()) if len(valid) else math.nan,
                "median_spearman": float(valid.median()) if len(valid) else math.nan,
                "minimum_spearman": float(valid.min()) if len(valid) else math.nan,
                "maximum_spearman": float(valid.max()) if len(valid) else math.nan,
                "top1_mismatch_count": int((~g["top1_same"]).sum()),
                "top1_mismatch_percent": float((~g["top1_same"]).mean() * 100.0),
                "mean_top3_overlap_fraction": float(g["top3_overlap_fraction"].mean()),
                "median_top3_overlap_fraction": float(g["top3_overlap_fraction"].median()),
                "minimum_top3_overlap_fraction": float(g["top3_overlap_fraction"].min()),
                "mean_max_absolute_rank_difference": float(
                    g["max_absolute_rank_difference"].mean()
                ),
            }
        )
    return detail, pd.DataFrame(summary_rows), redundancy


def near_share_pairs(
    positions_by_pool: dict[str, pd.DataFrame],
    pools: list[Pool],
    max_rel_gap: float,
    top_n: int,
) -> pd.DataFrame:
    # Keep only a limited number of strongest pairs per snapshot. The largest
    # snapshots can contain hundreds of positions, so materializing every
    # O(n^2) pair as a Python dict is unnecessarily slow.
    rows: list[dict] = []
    per_snapshot_keep = min(25, max(5, top_n))

    for pool in pools:
        df = positions_by_pool[pool.key]
        for snap, g0 in df.groupby("snapshot_index", sort=True):
            g = g0.copy()
            g["active_liquidity_share"] = numeric(g["active_liquidity_share"])
            g["total_lsis_bps"] = numeric(g["total_lsis_bps"])
            g["share_rank"] = g["active_liquidity_share"].rank(
                method="min", ascending=False
            )
            g["lsis_rank"] = g["total_lsis_bps"].rank(
                method="min", ascending=False
            )
            g = g.sort_values(
                "active_liquidity_share", ascending=False
            ).reset_index(drop=True)

            shares = g["active_liquidity_share"].to_numpy(dtype=float)
            lsis = g["total_lsis_bps"].to_numpy(dtype=float)
            n = len(g)
            if n < 2:
                continue

            # Pair matrices, upper triangle only.
            a = shares[:, None]
            b = shares[None, :]
            denom = np.maximum(a, b)
            with np.errstate(divide="ignore", invalid="ignore"):
                gap = np.abs(a - b) / denom

            hi = np.maximum(lsis[:, None], lsis[None, :])
            lo = np.minimum(lsis[:, None], lsis[None, :])
            with np.errstate(divide="ignore", invalid="ignore"):
                ratio = hi / lo
                log_ratio = np.log10(ratio)
            log_ratio[(lo <= 0) & (hi > 0)] = 12.0

            valid = (
                np.triu(np.ones((n, n), dtype=bool), k=1)
                & np.isfinite(gap)
                & (denom > 0)
                & (gap <= max_rel_gap)
                & (hi > 0)
            )
            score = log_ratio * (1.0 - gap)
            score[~valid] = -np.inf

            flat = score.ravel()
            valid_count = int(np.isfinite(flat).sum())
            if valid_count == 0:
                continue
            keep = min(per_snapshot_keep, valid_count)
            # argpartition avoids a full O(n^2 log n) sort.
            idx = np.argpartition(flat, -keep)[-keep:]
            idx = idx[np.argsort(flat[idx])[::-1]]

            for flat_idx in idx:
                i, j = np.unravel_index(int(flat_idx), score.shape)
                aa = g.iloc[int(i)]
                bb = g.iloc[int(j)]
                share_a = float(shares[i])
                share_b = float(shares[j])
                lsis_a = float(lsis[i])
                lsis_b = float(lsis[j])
                rel_gap = float(gap[i, j])
                pair_ratio = (
                    math.inf
                    if min(lsis_a, lsis_b) <= 0 < max(lsis_a, lsis_b)
                    else max(lsis_a, lsis_b) / min(lsis_a, lsis_b)
                )

                rows.append(
                    {
                        "pool": pool.label,
                        "pool_key": pool.key,
                        "snapshot_index": int(snap),
                        "block_number": int(aa["block_number"]),
                        "current_tick": int(aa["current_tick"]),
                        "share_relative_gap": rel_gap,
                        "lsis_ratio_max_over_min": pair_ratio,
                        "lsis_absolute_difference_bps": abs(lsis_a - lsis_b),
                        "divergence_score": float(score[i, j]),

                        "a_position_label": aa["position_label"],
                        "a_position_key": aa["position_key"],
                        "a_tick_lower": int(aa["tick_lower"]),
                        "a_tick_upper": int(aa["tick_upper"]),
                        "a_range_width": int(aa["range_width"]),
                        "a_distance_to_nearest_edge": int(aa["distance_to_nearest_edge"]),
                        "a_active_share": share_a,
                        "a_share_rank": int(aa["share_rank"]),
                        "a_lsis_rank": int(aa["lsis_rank"]),
                        "a_zero_for_one_lsis_bps": float(aa["zero_for_one_lsis_bps"]),
                        "a_one_for_zero_lsis_bps": float(aa["one_for_zero_lsis_bps"]),
                        "a_total_lsis_bps": lsis_a,

                        "b_position_label": bb["position_label"],
                        "b_position_key": bb["position_key"],
                        "b_tick_lower": int(bb["tick_lower"]),
                        "b_tick_upper": int(bb["tick_upper"]),
                        "b_range_width": int(bb["range_width"]),
                        "b_distance_to_nearest_edge": int(bb["distance_to_nearest_edge"]),
                        "b_active_share": share_b,
                        "b_share_rank": int(bb["share_rank"]),
                        "b_lsis_rank": int(bb["lsis_rank"]),
                        "b_zero_for_one_lsis_bps": float(bb["zero_for_one_lsis_bps"]),
                        "b_one_for_zero_lsis_bps": float(bb["one_for_zero_lsis_bps"]),
                        "b_total_lsis_bps": lsis_b,
                    }
                )

    result = pd.DataFrame(rows)
    if result.empty:
        return result
    result = result.sort_values(
        ["divergence_score", "lsis_absolute_difference_bps"],
        ascending=[False, False],
    ).head(top_n)
    return result.reset_index(drop=True)


def extract_case(
    df: pd.DataFrame,
    snapshot_index: int,
    labels: tuple[str, str],
) -> tuple[pd.DataFrame, pd.DataFrame]:
    snap = df[df["snapshot_index"] == snapshot_index].copy()
    if snap.empty:
        raise RuntimeError(
            f"selected case snapshot {snapshot_index} not found in {df['pool_key'].iloc[0]}"
        )
    snap["share_rank"] = numeric(snap["active_liquidity_share"]).rank(
        method="min", ascending=False
    ).astype(int)
    snap["lsis_rank"] = numeric(snap["total_lsis_bps"]).rank(
        method="min", ascending=False
    ).astype(int)

    pair = snap[snap["position_label"].isin(labels)].copy()
    if len(pair) != 2:
        raise RuntimeError(
            f"selected case {snapshot_index} expected labels {labels}, found "
            f"{pair['position_label'].tolist()}"
        )

    cols = [
        "pool", "pool_key", "snapshot_index", "block_number", "current_tick",
        "position_label", "position_key", "tick_lower", "tick_upper",
        "range_width", "distance_to_lower_tick", "distance_to_upper_tick",
        "distance_to_nearest_edge", "position_liquidity",
        "active_liquidity_share", "share_rank", "lsis_rank",
        "zero_for_one_base_auc_bps", "one_for_zero_base_auc_bps",
        "zero_for_one_lsis_bps", "one_for_zero_lsis_bps",
        "total_lsis_bps", "max_directional_lsis_bps",
    ]
    depth_cols = [
        c for c in snap.columns
        if (
            c.startswith("zero_for_one_depth_")
            or c.startswith("one_for_zero_depth_")
        )
    ]
    cols += [c for c in depth_cols if c not in cols]
    cols = [c for c in cols if c in snap.columns]

    pair = pair[cols].sort_values("total_lsis_bps", ascending=False)
    snap = snap[cols].sort_values("total_lsis_bps", ascending=False)

    # Pair-level diagnostics repeated on both rows for easy audit.
    shares = numeric(pair["active_liquidity_share"]).to_numpy()
    lsis = numeric(pair["total_lsis_bps"]).to_numpy()
    share_gap = abs(shares[0] - shares[1]) / max(shares[0], shares[1])
    ratio = max(lsis) / min(lsis) if min(lsis) > 0 else math.inf
    pair.insert(0, "pair_active_share_relative_gap", share_gap)
    pair.insert(1, "pair_lsis_ratio_max_over_min", ratio)
    return snap, pair


def inactive_burn_analysis(
    pools: list[Pool],
    burns_by_pool: dict[str, pd.DataFrame],
) -> tuple[pd.DataFrame, pd.DataFrame]:
    all_rows: list[pd.DataFrame] = []
    summary_rows: list[dict] = []

    for pool in pools:
        df = burns_by_pool[pool.key].copy()
        inactive = df[~df["burn_range_active_bool"]].copy()

        inactive["inferred_range_relation"] = np.where(
            numeric(inactive["current_tick"]) < numeric(inactive["tick_lower"]),
            "range_above_current",
            np.where(
                numeric(inactive["current_tick"]) >= numeric(inactive["tick_upper"]),
                "range_below_current",
                "INCONSISTENT_ACTIVE",
            ),
        )
        inactive["toward_range_direction"] = np.where(
            inactive["inferred_range_relation"].eq("range_above_current"),
            "one_for_zero",
            "zero_for_one",
        )
        inactive["toward_range_lsis_bps"] = np.where(
            inactive["inferred_range_relation"].eq("range_above_current"),
            numeric(inactive["one_for_zero_lsis_bps"]),
            numeric(inactive["zero_for_one_lsis_bps"]),
        )
        inactive["away_from_range_lsis_bps"] = np.where(
            inactive["inferred_range_relation"].eq("range_above_current"),
            numeric(inactive["zero_for_one_lsis_bps"]),
            numeric(inactive["one_for_zero_lsis_bps"]),
        )
        inactive["positive_lsis"] = numeric(inactive["total_lsis_bps"]) > 0

        all_rows.append(inactive)

        positive = inactive[inactive["positive_lsis"]]
        summary_rows.append(
            {
                "pool": pool.label,
                "pool_key": pool.key,
                "total_burns": int(len(df)),
                "active_burns": int(df["burn_range_active_bool"].sum()),
                "inactive_burns": int(len(inactive)),
                "inactive_positive_lsis_count": int(inactive["positive_lsis"].sum()),
                "inactive_positive_lsis_percent": (
                    float(inactive["positive_lsis"].mean() * 100.0)
                    if len(inactive) else math.nan
                ),
                "inactive_active_removal_share_nonzero_count": int(
                    (numeric(inactive["active_removal_share"]).abs() > 1e-15).sum()
                ),
                "inactive_inferred_relation_inconsistent_count": int(
                    inactive["inferred_range_relation"].eq("INCONSISTENT_ACTIVE").sum()
                ),
                "inactive_positive_toward_range_lsis_count": int(
                    (numeric(inactive["toward_range_lsis_bps"]) > 0).sum()
                ),
                "inactive_positive_away_from_range_lsis_count": int(
                    (numeric(inactive["away_from_range_lsis_bps"]) > 0).sum()
                ),
                "inactive_median_total_lsis_bps": float(
                    numeric(inactive["total_lsis_bps"]).median()
                ) if len(inactive) else math.nan,
                "inactive_mean_total_lsis_bps": float(
                    numeric(inactive["total_lsis_bps"]).mean()
                ) if len(inactive) else math.nan,
                "inactive_max_total_lsis_bps": float(
                    numeric(inactive["total_lsis_bps"]).max()
                ) if len(inactive) else math.nan,
                "positive_inactive_median_total_lsis_bps": float(
                    numeric(positive["total_lsis_bps"]).median()
                ) if len(positive) else math.nan,
            }
        )

    inactive_all = pd.concat(all_rows, ignore_index=True)
    summary = pd.DataFrame(summary_rows)

    # Add total row.
    total_inactive = inactive_all
    all_burns = sum(len(burns_by_pool[p.key]) for p in pools)
    active_burns = sum(
        int(burns_by_pool[p.key]["burn_range_active_bool"].sum()) for p in pools
    )
    positive_total = total_inactive[
        numeric(total_inactive["total_lsis_bps"]) > 0
    ]
    summary = pd.concat(
        [
            summary,
            pd.DataFrame(
                [{
                    "pool": "TOTAL",
                    "pool_key": "TOTAL",
                    "total_burns": all_burns,
                    "active_burns": active_burns,
                    "inactive_burns": int(len(total_inactive)),
                    "inactive_positive_lsis_count": int(
                        (numeric(total_inactive["total_lsis_bps"]) > 0).sum()
                    ),
                    "inactive_positive_lsis_percent": float(
                        (numeric(total_inactive["total_lsis_bps"]) > 0).mean() * 100.0
                    ),
                    "inactive_active_removal_share_nonzero_count": int(
                        (numeric(total_inactive["active_removal_share"]).abs() > 1e-15).sum()
                    ),
                    "inactive_inferred_relation_inconsistent_count": int(
                        total_inactive["inferred_range_relation"]
                        .eq("INCONSISTENT_ACTIVE").sum()
                    ),
                    "inactive_positive_toward_range_lsis_count": int(
                        (numeric(total_inactive["toward_range_lsis_bps"]) > 0).sum()
                    ),
                    "inactive_positive_away_from_range_lsis_count": int(
                        (numeric(total_inactive["away_from_range_lsis_bps"]) > 0).sum()
                    ),
                    "inactive_median_total_lsis_bps": float(
                        numeric(total_inactive["total_lsis_bps"]).median()
                    ),
                    "inactive_mean_total_lsis_bps": float(
                        numeric(total_inactive["total_lsis_bps"]).mean()
                    ),
                    "inactive_max_total_lsis_bps": float(
                        numeric(total_inactive["total_lsis_bps"]).max()
                    ),
                    "positive_inactive_median_total_lsis_bps": float(
                        numeric(positive_total["total_lsis_bps"]).median()
                    ) if len(positive_total) else math.nan,
                }]
            ),
        ],
        ignore_index=True,
    )

    cols = [
        "pool", "pool_key", "event_id", "tx_hash", "block_number", "log_index",
        "current_tick", "tick_lower", "tick_upper", "range_location",
        "inferred_range_relation", "toward_range_direction",
        "distance_outside_range", "normalized_distance_outside_range",
        "liquidity_removed", "removal_fraction", "active_removal_share",
        "zero_for_one_lsis_bps", "one_for_zero_lsis_bps",
        "toward_range_lsis_bps", "away_from_range_lsis_bps",
        "total_lsis_bps", "positive_lsis",
    ]
    cols = [c for c in cols if c in inactive_all.columns]
    cases = inactive_all[cols].sort_values(
        "total_lsis_bps", ascending=False
    ).reset_index(drop=True)

    return summary, cases


def joint_topn_analysis(
    pools: list[Pool],
    joint_by_pool: dict[str, pd.DataFrame],
) -> tuple[pd.DataFrame, pd.DataFrame]:
    summary_rows: list[dict] = []
    detail_rows: list[pd.DataFrame] = []

    for pool in pools:
        df = joint_by_pool[pool.key].copy()
        top = df[
            df["scenario_id"].isin(["top_3_by_lsis", "top_5_by_lsis"])
        ].copy()
        detail_rows.append(top)

        for scenario, g0 in top.groupby("scenario_id", sort=True):
            g = g0[
                (~g0["skipped_bool"])
                & (numeric(g0["sum_individual_total_lsis_bps"]) > 0)
                & numeric(g0["total_amplification_ratio"]).notna()
            ].copy()
            amp = numeric(g["total_amplification_ratio"])
            interaction = numeric(g["total_interaction_lsis_bps"])
            summary_rows.append(
                {
                    "pool": pool.label,
                    "pool_key": pool.key,
                    "scenario_id": scenario,
                    "all_rows": int(len(g0)),
                    "valid_positive_denominator_rows": int(len(g)),
                    "amplification_gt_1_count": int((amp > 1.0).sum()),
                    "amplification_eq_1_count": int(np.isclose(amp, 1.0).sum()),
                    "amplification_lt_1_count": int((amp < 1.0).sum()),
                    "amplification_gt_1_percent": (
                        float((amp > 1.0).mean() * 100.0) if len(g) else math.nan
                    ),
                    "positive_interaction_count": int((interaction > 0).sum()),
                    "minimum_amplification_ratio": float(amp.min()) if len(g) else math.nan,
                    "q1_amplification_ratio": float(amp.quantile(0.25)) if len(g) else math.nan,
                    "median_amplification_ratio": float(amp.median()) if len(g) else math.nan,
                    "mean_amplification_ratio": float(amp.mean()) if len(g) else math.nan,
                    "q3_amplification_ratio": float(amp.quantile(0.75)) if len(g) else math.nan,
                    "maximum_amplification_ratio": float(amp.max()) if len(g) else math.nan,
                }
            )

    return pd.DataFrame(summary_rows), pd.concat(detail_rows, ignore_index=True)


def target_share_overshoot(
    pools: list[Pool],
    joint_by_pool: dict[str, pd.DataFrame],
) -> tuple[pd.DataFrame, pd.DataFrame]:
    summary_rows: list[dict] = []
    detail_rows: list[pd.DataFrame] = []

    for pool in pools:
        df = joint_by_pool[pool.key].copy()
        target = df[
            df["scenario_kind"].astype(str).eq(
                "target_active_liquidity_share_by_lsis"
            )
        ].copy()
        if target.empty:
            continue
        target["target_share"] = (
            numeric(target["requested_active_liquidity_share_bps"]) / 10_000.0
        )
        target["actual_removed_share"] = numeric(
            target["removed_active_liquidity_share"]
        )
        target["overshoot_share"] = (
            target["actual_removed_share"] - target["target_share"]
        )
        target["actual_over_target_multiple"] = (
            target["actual_removed_share"] / target["target_share"]
        )
        detail_rows.append(target)

        for scenario, g0 in target.groupby("scenario_id", sort=True):
            g = g0[~g0["skipped_bool"]].copy()
            mult = numeric(g["actual_over_target_multiple"])
            actual = numeric(g["actual_removed_share"])
            over = numeric(g["overshoot_share"])
            summary_rows.append(
                {
                    "pool": pool.label,
                    "pool_key": pool.key,
                    "scenario_id": scenario,
                    "target_share": float(g["target_share"].iloc[0]) if len(g) else math.nan,
                    "valid_rows": int(len(g)),
                    "single_position_count": int(
                        (numeric(g["removed_position_count"]) == 1).sum()
                    ),
                    "single_position_percent": (
                        float((numeric(g["removed_position_count"]) == 1).mean() * 100.0)
                        if len(g) else math.nan
                    ),
                    "median_actual_removed_share": float(actual.median()) if len(g) else math.nan,
                    "mean_actual_removed_share": float(actual.mean()) if len(g) else math.nan,
                    "minimum_actual_removed_share": float(actual.min()) if len(g) else math.nan,
                    "maximum_actual_removed_share": float(actual.max()) if len(g) else math.nan,
                    "median_overshoot_share": float(over.median()) if len(g) else math.nan,
                    "maximum_overshoot_share": float(over.max()) if len(g) else math.nan,
                    "median_actual_over_target_multiple": float(mult.median()) if len(g) else math.nan,
                    "actual_gt_2x_target_count": int((mult > 2.0).sum()),
                    "actual_gt_2x_target_percent": (
                        float((mult > 2.0).mean() * 100.0) if len(g) else math.nan
                    ),
                }
            )

    detail = (
        pd.concat(detail_rows, ignore_index=True)
        if detail_rows else pd.DataFrame()
    )
    return pd.DataFrame(summary_rows), detail


def burn_association_active_vs_all(
    pools: list[Pool],
    obs_by_pool: dict[str, pd.DataFrame],
    cf_by_pool: dict[str, pd.DataFrame],
) -> tuple[pd.DataFrame, pd.DataFrame]:
    rows: list[dict] = []

    for pool in pools:
        obs = obs_by_pool[pool.key].copy()
        cf = cf_by_pool[pool.key].copy()

        cols = [
            "event_id", "horizon_label",
            "min_total_mechanical_deterioration_bps",
            "max_total_mechanical_deterioration_bps",
        ]
        merged = obs.merge(
            cf[cols],
            on=["event_id", "horizon_label"],
            how="inner",
        )
        merged["mechanical_midpoint_deterioration_bps"] = (
            numeric(merged["min_total_mechanical_deterioration_bps"])
            + numeric(merged["max_total_mechanical_deterioration_bps"])
        ) / 2.0
        merged["realized_outcome"] = numeric(
            merged["total_realized_deterioration_bps"]
        )
        merged["mechanical_outcome"] = numeric(
            merged["mechanical_midpoint_deterioration_bps"]
        )

        subsets = {
            "all_burns": merged,
            "active_burns_only": merged[merged["burn_range_active_bool"]],
            "inactive_burns_only": merged[~merged["burn_range_active_bool"]],
        }

        for subset_name, sub in subsets.items():
            for (hlabel, hblocks), g in sub.groupby(
                ["horizon_label", "horizon_blocks"], sort=True
            ):
                for outcome_kind, outcome_col in [
                    ("realized", "realized_outcome"),
                    ("mechanical", "mechanical_outcome"),
                ]:
                    for predictor_col, predictor_label in BURN_PREDICTORS.items():
                        rho = safe_spearman(g[predictor_col], g[outcome_col])
                        mask = (
                            numeric(g[predictor_col]).notna()
                            & numeric(g[outcome_col]).notna()
                        )
                        rows.append(
                            {
                                "pool": pool.label,
                                "pool_key": pool.key,
                                "subset": subset_name,
                                "outcome_kind": outcome_kind,
                                "horizon_label": hlabel,
                                "horizon_blocks": int(hblocks),
                                "predictor": predictor_label,
                                "predictor_column": predictor_col,
                                "spearman_rho": rho,
                                "observation_count": int(mask.sum()),
                            }
                        )

    detail = pd.DataFrame(rows)
    summary_rows: list[dict] = []

    group_cols = ["subset", "outcome_kind", "predictor", "predictor_column"]
    for keys, g0 in detail.groupby(group_cols, sort=False):
        subset, outcome_kind, predictor, predictor_col = keys
        valid = g0["spearman_rho"].dropna()

        # Count wins by absolute correlation within each pool × horizon
        wins = 0
        for (_, _), stratum in detail[
            (detail["subset"] == subset)
            & (detail["outcome_kind"] == outcome_kind)
        ].groupby(["pool", "horizon_label"]):
            vv = stratum.dropna(subset=["spearman_rho"])
            if vv.empty:
                continue
            winner = vv.loc[
                vv["spearman_rho"].abs().idxmax(),
                "predictor_column",
            ]
            if winner == predictor_col:
                wins += 1

        summary_rows.append(
            {
                "subset": subset,
                "outcome_kind": outcome_kind,
                "predictor": predictor,
                "predictor_column": predictor_col,
                "valid_strata": int(len(valid)),
                "mean_spearman": float(valid.mean()) if len(valid) else math.nan,
                "median_spearman": float(valid.median()) if len(valid) else math.nan,
                "q1_spearman": float(valid.quantile(0.25)) if len(valid) else math.nan,
                "q3_spearman": float(valid.quantile(0.75)) if len(valid) else math.nan,
                "minimum_spearman": float(valid.min()) if len(valid) else math.nan,
                "maximum_spearman": float(valid.max()) if len(valid) else math.nan,
                "strongest_absolute_association_strata": int(wins),
            }
        )

    return detail, pd.DataFrame(summary_rows)


def key_mechanical_comparison(summary: pd.DataFrame) -> pd.DataFrame:
    keep = summary[
        (summary["outcome_kind"] == "mechanical")
        & summary["subset"].isin(["all_burns", "active_burns_only", "inactive_burns_only"])
        & summary["predictor_column"].isin(
            ["immediate_total_lsis_bps", "active_removal_share", "liquidity_removed"]
        )
    ].copy()
    return keep.sort_values(
        ["subset", "median_spearman"],
        ascending=[True, False],
    )


def make_readme(
    out: Path,
    divergence_summary: pd.DataFrame,
    inactive_summary: pd.DataFrame,
    joint_summary: pd.DataFrame,
    overshoot_summary: pd.DataFrame,
    mechanical_key: pd.DataFrame,
) -> None:
    lines: list[str] = []
    lines.append("CLMM LSIS — Stage 1.5 audit")
    lines.append("=" * 34)
    lines.append("")
    lines.append(
        "This package is derived ONLY from the existing outputs directory. "
        "It does not rerun the Go analyzer or modify any research result."
    )
    lines.append("")

    lines.append("1) LSIS vs active-liquidity-share divergence")
    for _, r in divergence_summary.iterrows():
        lines.append(
            f"- {r['pool']}: mean rho={r['mean_spearman']:.6f}, "
            f"min rho={r['minimum_spearman']:.6f}, "
            f"Top-1 mismatch={int(r['top1_mismatch_count'])}/{int(r['snapshot_count'])}, "
            f"mean Top-3 overlap={r['mean_top3_overlap_fraction']:.3f}"
        )
    lines.append("")

    total = inactive_summary[inactive_summary["pool"] == "TOTAL"]
    if not total.empty:
        r = total.iloc[0]
        lines.append("2) Inactive Burns")
        lines.append(
            f"- Inactive Burns: {int(r['inactive_burns'])}; "
            f"positive immediate LSIS: {int(r['inactive_positive_lsis_count'])} "
            f"({r['inactive_positive_lsis_percent']:.1f}%)."
        )
        lines.append(
            f"- Positive LSIS in the direction AWAY from the inactive range: "
            f"{int(r['inactive_positive_away_from_range_lsis_count'])}."
        )
        lines.append("")

    lines.append("3) Top-3 / Top-5 joint-removal amplification")
    for _, r in joint_summary.iterrows():
        lines.append(
            f"- {r['pool']} {r['scenario_id']}: "
            f"{int(r['amplification_gt_1_count'])}/"
            f"{int(r['valid_positive_denominator_rows'])} > 1; "
            f"min={r['minimum_amplification_ratio']:.4f}, "
            f"median={r['median_amplification_ratio']:.4f}, "
            f"mean={r['mean_amplification_ratio']:.4f}."
        )
    lines.append("")

    lines.append("4) Target-share scenario overshoot")
    for _, r in overshoot_summary.iterrows():
        lines.append(
            f"- {r['pool']} {r['scenario_id']}: target={r['target_share']:.3f}, "
            f"median actual={r['median_actual_removed_share']:.3f}, "
            f"single-position={r['single_position_percent']:.1f}%."
        )
    lines.append("")

    lines.append("5) Mechanical association: all vs active-only")
    for _, r in mechanical_key.iterrows():
        lines.append(
            f"- {r['subset']} | {r['predictor']}: "
            f"median rho={r['median_spearman']:.6f}, "
            f"mean rho={r['mean_spearman']:.6f}, "
            f"wins={int(r['strongest_absolute_association_strata'])}."
        )
    lines.append("")

    lines.append("Selected case-study files:")
    lines.append("- case_01_usdc_usdt_snapshot_41_pair.csv")
    lines.append("- case_01_usdc_usdt_snapshot_41_all_positions.csv")
    lines.append("- case_02_weth_usdt_snapshot_68_pair.csv")
    lines.append("- case_02_weth_usdt_snapshot_68_all_positions.csv")
    lines.append("")
    lines.append(
        "Send the entire generated ZIP to the reviewer/assistant; do not hand-pick rows."
    )

    (out / "README_STAGE_1_5.txt").write_text("\n".join(lines), encoding="utf-8")


def zip_dir(out: Path) -> Path:
    zip_path = out.with_suffix(".zip")
    if zip_path.exists():
        zip_path.unlink()
    with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as z:
        for path in sorted(out.rglob("*")):
            if path.is_file():
                z.write(path, path.relative_to(out.parent))
    return zip_path


def main() -> int:
    args = parse_args()
    if not (0 < args.near_share_gap < 1):
        raise RuntimeError("--near-share-gap must be inside (0,1)")
    if args.top_candidates <= 0:
        raise RuntimeError("--top-candidates must be positive")

    pools = load_pools(args.outputs)
    out = args.out.resolve()

    if out.exists():
        shutil.rmtree(out)
    out.mkdir(parents=True, exist_ok=True)

    manifest: list[dict] = []

    positions_by_pool = {
        p.key: load_positions(p, manifest)
        for p in pools
    }
    burns_by_pool = {
        p.key: load_burn_samples(p, manifest)
        for p in pools
    }
    joint_by_pool = {
        p.key: load_joint(p, manifest)
        for p in pools
    }
    obs_by_pool = {
        p.key: load_main_observations(p, manifest)
        for p in pools
    }
    cf_by_pool = {
        p.key: load_counterfactual(p, manifest)
        for p in pools
    }

    # A. Snapshot divergence and baseline redundancy.
    div_detail, div_summary, redundancy = snapshot_divergence(
        pools, positions_by_pool
    )
    write_csv(div_detail, out, "snapshot_divergence_by_snapshot.csv")
    write_csv(div_summary, out, "snapshot_divergence_by_pool.csv")
    write_csv(redundancy, out, "position_liquidity_active_share_redundancy.csv")

    candidates = near_share_pairs(
        positions_by_pool,
        pools,
        args.near_share_gap,
        args.top_candidates,
    )
    write_csv(candidates, out, "near_share_divergence_candidates.csv")

    # B. Preselected case studies from the audited outputs.
    case1_all, case1_pair = extract_case(
        positions_by_pool["usdc_usdt_001"],
        41,
        ("S41_P1", "S41_P5"),
    )
    write_csv(case1_all, out, "case_01_usdc_usdt_snapshot_41_all_positions.csv")
    write_csv(case1_pair, out, "case_01_usdc_usdt_snapshot_41_pair.csv")

    case2_all, case2_pair = extract_case(
        positions_by_pool["weth_usdt_005"],
        68,
        ("S68_P1", "S68_P3"),
    )
    write_csv(case2_all, out, "case_02_weth_usdt_snapshot_68_all_positions.csv")
    write_csv(case2_pair, out, "case_02_weth_usdt_snapshot_68_pair.csv")

    # C. Inactive Burns with positive LSIS + directional sanity check.
    inactive_summary, inactive_cases = inactive_burn_analysis(
        pools, burns_by_pool
    )
    write_csv(inactive_summary, out, "inactive_burn_summary.csv")
    write_csv(inactive_cases, out, "inactive_burn_cases.csv")
    write_csv(
        inactive_cases[inactive_cases["positive_lsis"]].head(100),
        out,
        "inactive_burn_top100_positive_lsis.csv",
    )

    # D. Joint-removal non-additivity and target-share overshoot.
    joint_summary, joint_detail = joint_topn_analysis(
        pools, joint_by_pool
    )
    write_csv(joint_summary, out, "joint_top3_top5_amplification_summary.csv")
    write_csv(joint_detail, out, "joint_top3_top5_rows.csv")

    overshoot_summary, overshoot_detail = target_share_overshoot(
        pools, joint_by_pool
    )
    write_csv(overshoot_summary, out, "target_share_overshoot_summary.csv")
    write_csv(overshoot_detail, out, "target_share_overshoot_rows.csv")

    # E. Re-run RQ6 associations from existing outputs, separating active/inactive Burns.
    assoc_detail, assoc_summary = burn_association_active_vs_all(
        pools, obs_by_pool, cf_by_pool
    )
    write_csv(assoc_detail, out, "burn_association_active_vs_all_by_stratum.csv")
    write_csv(assoc_summary, out, "burn_association_active_vs_all_summary.csv")
    mechanical_key = key_mechanical_comparison(assoc_summary)
    write_csv(
        mechanical_key,
        out,
        "mechanical_key_comparison_all_vs_active_only.csv",
    )

    # F. Full input manifest for reproducibility.
    write_csv(pd.DataFrame(manifest), out, "input_manifest.csv")

    make_readme(
        out,
        div_summary,
        inactive_summary,
        joint_summary,
        overshoot_summary,
        mechanical_key,
    )

    z = zip_dir(out)
    print(f"Stage 1.5 audit complete.")
    print(f"Directory: {out}")
    print(f"ZIP:       {z}")
    print(f"Send this ZIP without deleting any generated file.")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        raise
