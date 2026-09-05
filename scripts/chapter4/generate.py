#!/usr/bin/env python3
from __future__ import annotations

import argparse
import glob
import json
import math
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable

import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
from scipy.stats import spearmanr


POOL_ORDER = [
    "usdc_usdt_001",
    "usdc_weth_005",
    "usdc_weth_030",
    "wbtc_weth_030",
    "weth_usdt_005",
]

SNAPSHOT_BASELINES = {
    "active_liquidity_share": "Active liquidity share",
    # "position_liquidity": "Position liquidity",
    "normalized_liquidity_density": "Normalized liquidity density",
    "range_width": "Range width",
    "distance_to_nearest_edge": "Distance to nearest edge",
}

BURN_BASELINES = {
    "immediate_total_lsis_bps": "Immediate LSIS",
    "active_removal_share": "Active removal share",
    "removal_fraction": "Removal fraction",
    "liquidity_removed": "Removed liquidity",
    "removed_liquidity_density": "Removed liquidity density",
    "range_width": "Range width",
    "normalized_distance_outside_range": "Distance outside range",
}

SCENARIO_LABELS = {
    "top_1_by_lsis": "Top 1",
    "top_3_by_lsis": "Top 3",
    "top_5_by_lsis": "Top 5",
    "target_1000_bps_active_liquidity_by_lsis": "10% active share",
    "target_2500_bps_active_liquidity_by_lsis": "25% active share",
}

STATUS_ORDER = [
    "primary_robust",
    "primary_supported",
    "primary_inconclusive",
    "balance_blocked",
]


@dataclass(frozen=True)
class Pool:
    key: str
    path: Path
    label: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Generate Chapter 4 figures/tables from CLMM LSIS outputs."
    )
    parser.add_argument("--outputs-dir", type=Path, default=Path("outputs"))
    parser.add_argument(
        "--output-dir", type=Path, default=Path("artifacts/chapter4")
    )
    return parser.parse_args()


def one_file(pool: Pool, pattern: str, *, exclude: Iterable[str] = ()) -> Path:
    matches = []
    for raw in glob.glob(str(pool.path / pattern)):
        path = Path(raw)
        if any(token in path.name for token in exclude):
            continue
        matches.append(path)
    if len(matches) != 1:
        raise RuntimeError(
            f"expected one file for {pool.key}/{pattern}, found {len(matches)}: {matches}"
        )
    return matches[0]


def load_pools(outputs_dir: Path) -> list[Pool]:
    pools: list[Pool] = []
    for key in POOL_ORDER:
        path = outputs_dir / key
        if not path.is_dir():
            raise RuntimeError(f"missing pool output directory: {path}")
        with (path / "pool_config.json").open() as fh:
            cfg = json.load(fh)
        fee_pct = float(cfg["pool_fee"]) / 10_000.0
        label = f'{cfg["token0_symbol"]}/{cfg["token1_symbol"]} {fee_pct:.2f}%'
        pools.append(Pool(key=key, path=path, label=label))
    return pools


def numeric(series: pd.Series) -> pd.Series:
    return pd.to_numeric(series, errors="coerce")


def safe_spearman(x: pd.Series, y: pd.Series) -> float:
    x = numeric(x)
    y = numeric(y)
    mask = x.notna() & y.notna()
    if mask.sum() < 5:
        return math.nan
    x = x[mask]
    y = y[mask]
    if x.nunique() < 2 or y.nunique() < 2:
        return math.nan
    return float(spearmanr(x, y).statistic)


def save_figure(fig: plt.Figure, figures_dir: Path, stem: str) -> None:
    fig.tight_layout()
    fig.savefig(figures_dir / f"{stem}.png", dpi=300, bbox_inches="tight")
    fig.savefig(figures_dir / f"{stem}.pdf", bbox_inches="tight")
    plt.close(fig)


def validation_table(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "swap_validation_*_summary.csv"))
        row = df.iloc[0]
        rows.append(
            {
                "pool": pool.label,
                "clean_samples": int(row["clean_samples"]),
                "exact_match_percent": float(row["exact_match_percent"]),
                "amount_out_exact_percent": float(row["amount_out_exact_percent"]),
                "sqrt_price_exact_percent": float(row["sqrt_price_exact_percent"]),
                "tick_exact_percent": float(row["tick_exact_percent"]),
                "mean_amount_out_diff_bps": float(row["mean_amount_out_diff_bps"]),
                "mean_sqrt_price_diff_bps": float(row["mean_sqrt_price_diff_bps"]),
            }
        )
    return pd.DataFrame(rows)


def base_piauc(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "snapshot_batch_summary_*.csv"))
        for direction, col in [
            ("zeroForOne", "zero_for_one_base_auc_bps"),
            ("oneForZero", "one_for_zero_base_auc_bps"),
        ]:
            values = numeric(df[col]).dropna()
            rows.append(
                {
                    "pool": pool.label,
                    "direction": direction,
                    "snapshot_count": int(values.size),
                    "mean_piauc_bps": float(values.mean()),
                    "median_piauc_bps": float(values.median()),
                    "q1_piauc_bps": float(values.quantile(0.25)),
                    "q3_piauc_bps": float(values.quantile(0.75)),
                }
            )
    return pd.DataFrame(rows)


def plot_base_piauc(df: pd.DataFrame, figures_dir: Path) -> None:
    pools = df["pool"].drop_duplicates().tolist()
    x = np.arange(len(pools))
    width = 0.36
    fig, ax = plt.subplots(figsize=(9.5, 5.2))
    for offset, direction in [(-width / 2, "zeroForOne"), (width / 2, "oneForZero")]:
        g = df[df["direction"] == direction].set_index("pool").loc[pools]
        ax.bar(x + offset, g["mean_piauc_bps"], width, label=direction)
    ax.set_ylabel("Mean baseline PIAUC (bps)")
    ax.set_xticks(x, pools, rotation=20, ha="right")
    ax.set_title("Baseline price-impact area across pools")
    ax.legend()
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, "fig_01_base_piauc_by_pool")


def concentration_table(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "snapshot_batch_diagnostics_*.csv"))
        rows.append(
            {
                "pool": pool.label,
                "snapshot_count": len(df),
                "mean_top1_lsis_share": numeric(df["top1_lsis_share"]).mean(),
                "mean_top3_lsis_share": numeric(df["top3_lsis_share"]).mean(),
                "mean_lsis_hhi": numeric(df["total_lsis_hhi_topk"]).mean(),
                "mean_effective_lsis_positions": numeric(
                    df["effective_lsis_positions_topk"]
                ).mean(),
                "mean_lsis_gini": numeric(df["total_lsis_gini_topk"]).mean(),
            }
        )
    return pd.DataFrame(rows)


def plot_concentration(df: pd.DataFrame, figures_dir: Path) -> None:
    pools = df["pool"].tolist()
    x = np.arange(len(pools))
    width = 0.36
    fig, ax = plt.subplots(figsize=(9.5, 5.2))
    ax.bar(x - width / 2, 100 * df["mean_top1_lsis_share"], width, label="Top 1")
    ax.bar(x + width / 2, 100 * df["mean_top3_lsis_share"], width, label="Top 3")
    ax.set_ylabel("Mean share of total LSIS (%)")
    ax.set_xticks(x, pools, rotation=20, ha="right")
    ax.set_title("Concentration of stability impact")
    ax.legend()
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, "fig_02_lsis_concentration_by_pool")


def snapshot_baseline_correlations(pools: list[Pool]) -> tuple[pd.DataFrame, pd.DataFrame]:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "snapshot_batch_positions_*.csv"))
        for snapshot_index, g in df.groupby("snapshot_index"):
            for metric, label in SNAPSHOT_BASELINES.items():
                rows.append(
                    {
                        "pool": pool.label,
                        "snapshot_index": int(snapshot_index),
                        "baseline": label,
                        "baseline_column": metric,
                        "spearman_rho": safe_spearman(g[metric], g["total_lsis_bps"]),
                        "position_count": len(g),
                    }
                )
    detail = pd.DataFrame(rows)
    summary = (
        detail.groupby(["pool", "baseline", "baseline_column"], dropna=False)[
            "spearman_rho"
        ]
        .agg(
            snapshot_count="count",
            mean="mean",
            median="median",
            q1=lambda s: s.quantile(0.25),
            q3=lambda s: s.quantile(0.75),
        )
        .reset_index()
    )
    return detail, summary


def plot_snapshot_baselines(detail: pd.DataFrame, figures_dir: Path) -> pd.DataFrame:
    summary = (
        detail.groupby(["baseline", "baseline_column"])["spearman_rho"]
        .agg(
            strata="count",
            mean="mean",
            median="median",
            q1=lambda s: s.quantile(0.25),
            q3=lambda s: s.quantile(0.75),
        )
        .reset_index()
        .sort_values("median", ascending=False)
    )
    fig, ax = plt.subplots(figsize=(9.5, 5.4))
    x = np.arange(len(summary))
    med = summary["median"].to_numpy()
    lower = med - summary["q1"].to_numpy()
    upper = summary["q3"].to_numpy() - med
    ax.bar(x, med)
    ax.errorbar(x, med, yerr=np.vstack([lower, upper]), fmt="none", capsize=4)
    ax.axhline(0, linewidth=1)
    ax.set_ylabel("Median within-snapshot Spearman rho with total LSIS")
    ax.set_xticks(x, summary["baseline"], rotation=25, ha="right")
    ax.set_title("How closely simple position proxies reproduce the LSIS ranking")
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, "fig_03_snapshot_baseline_rank_association")
    return summary


def joint_removal_table(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "snapshot_batch_joint_removal_*.csv"))
        if "skipped" in df.columns:
            df = df[~df["skipped"].astype(str).str.lower().eq("true")]
        for scenario, g in df.groupby("scenario_id"):
            amp = numeric(g["total_amplification_ratio"]).replace([np.inf, -np.inf], np.nan)
            interaction = numeric(g["total_interaction_lsis_bps"])
            rows.append(
                {
                    "pool": pool.label,
                    "scenario_id": scenario,
                    "scenario": SCENARIO_LABELS.get(scenario, scenario),
                    "snapshot_count": len(g),
                    "mean_amplification_ratio": amp.mean(),
                    "median_amplification_ratio": amp.median(),
                    "mean_interaction_lsis_bps": interaction.mean(),
                    "median_interaction_lsis_bps": interaction.median(),
                }
            )
    return pd.DataFrame(rows)


def plot_joint_removal(df: pd.DataFrame, figures_dir: Path) -> None:
    scenario_order = [SCENARIO_LABELS[k] for k in SCENARIO_LABELS]
    pools = df["pool"].drop_duplicates().tolist()
    x = np.arange(len(scenario_order))
    width = 0.15
    fig, ax = plt.subplots(figsize=(10.5, 5.5))
    for idx, pool in enumerate(pools):
        g = df[df["pool"] == pool].set_index("scenario").reindex(scenario_order)
        offset = (idx - (len(pools) - 1) / 2) * width
        ax.bar(x + offset, g["mean_amplification_ratio"], width, label=pool)
    ax.axhline(1.0, linewidth=1)
    ax.set_ylabel("Mean total amplification ratio")
    ax.set_xticks(x, scenario_order, rotation=20, ha="right")
    ax.set_title("Non-additivity under joint liquidity removal")
    ax.legend(fontsize=8)
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, "fig_04_joint_removal_amplification")


def burn_samples_table(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "burn_event_study_*_summary.csv"))
        r = df.iloc[0]
        rows.append(
            {
                "pool": pool.label,
                "candidate_events": int(r["candidate_events"]),
                "analyzed_events": int(r["analyzed_events"]),
                "active_burn_samples": int(r["active_burn_samples"]),
                "inactive_burn_samples": int(r["inactive_burn_samples"]),
                "sample_target_reached": bool(r["sample_target_reached"]),
                "minimum_spacing_blocks": int(r["minimum_spacing_blocks"]),
                "sample_yield_percent": float(r["sample_yield_percent"]),
            }
        )
    return pd.DataFrame(rows)


def load_burn_observations(pool: Pool) -> pd.DataFrame:
    path = one_file(
        pool,
        "burn_regression_*_observations.csv",
        exclude=("counterfactual", "temporal_placebo"),
    )
    return pd.read_csv(path)


def load_counterfactual_observations(pool: Pool) -> pd.DataFrame:
    return pd.read_csv(one_file(pool, "burn_regression_*_counterfactual_observations.csv"))


def burn_associations(pools: list[Pool], outcome_kind: str) -> pd.DataFrame:
    rows = []
    for pool in pools:
        obs = load_burn_observations(pool)
        if outcome_kind == "realized":
            merged = obs.copy()
            merged["analysis_outcome"] = numeric(
                merged["total_realized_deterioration_bps"]
            )
        elif outcome_kind == "mechanical":
            cf = load_counterfactual_observations(pool)
            cols = [
                "event_id",
                "horizon_label",
                "min_total_mechanical_deterioration_bps",
                "max_total_mechanical_deterioration_bps",
            ]
            merged = obs.merge(cf[cols], on=["event_id", "horizon_label"], how="inner")
            merged["analysis_outcome"] = (
                numeric(merged["min_total_mechanical_deterioration_bps"])
                + numeric(merged["max_total_mechanical_deterioration_bps"])
            ) / 2.0
        else:
            raise ValueError(outcome_kind)

        for (horizon_label, horizon_blocks), g in merged.groupby(
            ["horizon_label", "horizon_blocks"]
        ):
            for predictor, label in BURN_BASELINES.items():
                rho = safe_spearman(g[predictor], g["analysis_outcome"])
                rows.append(
                    {
                        "pool": pool.label,
                        "horizon_label": horizon_label,
                        "horizon_blocks": int(horizon_blocks),
                        "predictor": label,
                        "predictor_column": predictor,
                        "spearman_rho": rho,
                        "observation_count": int(
                            (numeric(g[predictor]).notna() & g["analysis_outcome"].notna()).sum()
                        ),
                    }
                )
    detail = pd.DataFrame(rows)

    winners: dict[str, int] = {label: 0 for label in BURN_BASELINES.values()}
    for _, g in detail.groupby(["pool", "horizon_label"]):
        valid = g.dropna(subset=["spearman_rho"])
        if valid.empty:
            continue
        winner = valid.loc[valid["spearman_rho"].abs().idxmax(), "predictor"]
        winners[winner] += 1

    summary = (
        detail.groupby(["predictor", "predictor_column"])["spearman_rho"]
        .agg(
            strata="count",
            mean="mean",
            median="median",
            q1=lambda s: s.quantile(0.25),
            q3=lambda s: s.quantile(0.75),
            minimum="min",
            maximum="max",
        )
        .reset_index()
    )
    summary["strongest_absolute_association_strata"] = summary["predictor"].map(winners)
    summary["outcome_kind"] = outcome_kind
    return detail, summary


def plot_burn_association_summary(
    summary: pd.DataFrame, figures_dir: Path, stem: str, title: str
) -> None:
    summary = summary.sort_values("median", ascending=False).reset_index(drop=True)
    fig, ax = plt.subplots(figsize=(10, 5.5))
    x = np.arange(len(summary))
    med = summary["median"].to_numpy()
    lower = med - summary["q1"].to_numpy()
    upper = summary["q3"].to_numpy() - med
    ax.bar(x, med)
    ax.errorbar(x, med, yerr=np.vstack([lower, upper]), fmt="none", capsize=4)
    ax.axhline(0, linewidth=1)
    ax.set_ylabel("Median Spearman rho across pool × horizon strata")
    ax.set_xticks(x, summary["predictor"], rotation=25, ha="right")
    ax.set_title(title)
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, stem)


def deterioration_by_horizon(pools: list[Pool]) -> tuple[pd.DataFrame, pd.DataFrame]:
    realized_rows = []
    mechanical_rows = []
    for pool in pools:
        obs = load_burn_observations(pool)
        for (label, blocks), g in obs.groupby(["horizon_label", "horizon_blocks"]):
            values = numeric(g["total_realized_deterioration_bps"]).dropna()
            realized_rows.append(
                {
                    "pool": pool.label,
                    "horizon_label": label,
                    "horizon_blocks": int(blocks),
                    "mean_deterioration_bps": values.mean(),
                    "median_deterioration_bps": values.median(),
                    "q1_deterioration_bps": values.quantile(0.25),
                    "q3_deterioration_bps": values.quantile(0.75),
                    "observation_count": len(values),
                }
            )

        cf = load_counterfactual_observations(pool).copy()
        cf["midpoint"] = (
            numeric(cf["min_total_mechanical_deterioration_bps"])
            + numeric(cf["max_total_mechanical_deterioration_bps"])
        ) / 2.0
        for (label, blocks), g in cf.groupby(["horizon_label", "horizon_blocks"]):
            values = numeric(g["midpoint"]).dropna()
            mechanical_rows.append(
                {
                    "pool": pool.label,
                    "horizon_label": label,
                    "horizon_blocks": int(blocks),
                    "mean_deterioration_bps": values.mean(),
                    "median_deterioration_bps": values.median(),
                    "q1_deterioration_bps": values.quantile(0.25),
                    "q3_deterioration_bps": values.quantile(0.75),
                    "observation_count": len(values),
                }
            )
    return pd.DataFrame(realized_rows), pd.DataFrame(mechanical_rows)


def plot_deterioration_by_horizon(
    df: pd.DataFrame, figures_dir: Path, stem: str, title: str
) -> None:
    fig, ax = plt.subplots(figsize=(9.5, 5.3))
    for pool, g in df.groupby("pool", sort=False):
        g = g.sort_values("horizon_blocks")
        ax.plot(
            g["horizon_blocks"],
            g["median_deterioration_bps"],
            marker="o",
            label=pool,
        )
    ax.set_xscale("log")
    ax.set_yscale("symlog", linthresh=0.01)
    ax.set_xlabel("Horizon (blocks, log scale)")
    ax.set_ylabel("Median total deterioration (bps, symlog scale)")
    ax.set_title(title)
    ax.legend(fontsize=8)
    ax.grid(alpha=0.25)
    save_figure(fig, figures_dir, stem)


def counterfactual_completeness(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "burn_regression_*_counterfactual_summary.csv"))
        r = df.iloc[0]
        rows.append(
            {
                "pool": pool.label,
                "expected_observations": int(r["expected_observations"]),
                "observed_observations": int(r["observed_observations"]),
                "available_observations": int(r["available_observations"]),
                "both_branches_available": int(r["both_branches_available"]),
                "single_branch_available": int(r["single_branch_available"]),
                "no_branch_available": int(r["no_branch_available"]),
                "ambiguous_swap_modes": int(r["ambiguous_swap_modes"]),
            }
        )
    return pd.DataFrame(rows)


def publication_status_table(pools: list[Pool]) -> pd.DataFrame:
    rows = []
    for pool in pools:
        df = pd.read_csv(one_file(pool, "burn_regression_*_control_v21_publication_inference.csv"))
        counts = df["publication_status"].value_counts().to_dict()
        primary = df[df["analysis_role"].astype(str).str.startswith("primary")]
        primary_counts = primary["publication_status"].value_counts().to_dict()
        rows.append(
            {
                "pool": pool.label,
                "primary_robust": int(primary_counts.get("primary_robust", 0)),
                "primary_supported": int(primary_counts.get("primary_supported", 0)),
                "primary_inconclusive": int(primary_counts.get("primary_inconclusive", 0)),
                "balance_blocked": int(primary_counts.get("balance_blocked", 0)),
                "primary_total_rows": int(len(primary)),
                "primary_eligible_rows": int(
                    pd.Series(primary.get("primary_eligible", [])).astype(str).str.lower().eq("true").sum()
                ),
                "exploratory_rows": int(counts.get("exploratory", 0)),
                "sensitivity_only_rows": int(counts.get("sensitivity_only", 0)),
            }
        )
    return pd.DataFrame(rows).fillna(0)


def plot_publication_status(df: pd.DataFrame, figures_dir: Path) -> None:
    fig, ax = plt.subplots(figsize=(9.5, 5.4))
    x = np.arange(len(df))
    bottom = np.zeros(len(df))
    for status in STATUS_ORDER:
        values = numeric(df[status]).fillna(0).to_numpy()
        ax.bar(x, values, bottom=bottom, label=status.replace("_", " "))
        bottom += values
    ax.set_ylabel("Primary inference rows")
    ax.set_xticks(x, df["pool"], rotation=20, ha="right")
    ax.set_title("Quality classification of primary evidence")
    ax.legend(fontsize=8)
    ax.grid(axis="y", alpha=0.25)
    save_figure(fig, figures_dir, "fig_09_primary_evidence_status")


def write_csv(df: pd.DataFrame, tables_dir: Path, name: str) -> None:
    df.to_csv(tables_dir / name, index=False)


def main() -> None:
    args = parse_args()
    outputs_dir = args.outputs_dir.resolve()
    output_dir = args.output_dir.resolve()
    figures_dir = output_dir / "figures"
    tables_dir = output_dir / "tables"
    figures_dir.mkdir(parents=True, exist_ok=True)
    tables_dir.mkdir(parents=True, exist_ok=True)

    pools = load_pools(outputs_dir)

    validation = validation_table(pools)
    write_csv(validation, tables_dir, "table_01_validation.csv")

    piauc = base_piauc(pools)
    write_csv(piauc, tables_dir, "table_02_base_piauc.csv")
    plot_base_piauc(piauc, figures_dir)

    concentration = concentration_table(pools)
    write_csv(concentration, tables_dir, "table_03_lsis_concentration.csv")
    plot_concentration(concentration, figures_dir)

    snapshot_detail, snapshot_pool_summary = snapshot_baseline_correlations(pools)
    write_csv(snapshot_detail, tables_dir, "table_04a_snapshot_baseline_by_snapshot.csv")
    write_csv(snapshot_pool_summary, tables_dir, "table_04b_snapshot_baseline_by_pool.csv")
    snapshot_summary = plot_snapshot_baselines(snapshot_detail, figures_dir)
    write_csv(snapshot_summary, tables_dir, "table_04c_snapshot_baseline_summary.csv")

    joint = joint_removal_table(pools)
    write_csv(joint, tables_dir, "table_05_joint_removal.csv")
    plot_joint_removal(joint, figures_dir)

    burn_samples = burn_samples_table(pools)
    write_csv(burn_samples, tables_dir, "table_06_burn_samples.csv")

    realized_detail, realized_summary = burn_associations(pools, "realized")
    write_csv(realized_detail, tables_dir, "table_07a_baseline_realized_by_stratum.csv")
    write_csv(realized_summary, tables_dir, "table_07b_baseline_realized_summary.csv")
    plot_burn_association_summary(
        realized_summary,
        figures_dir,
        "fig_05_baseline_vs_realized_deterioration",
        "Baseline association with realized post-burn deterioration",
    )

    mechanical_detail, mechanical_summary = burn_associations(pools, "mechanical")
    write_csv(mechanical_detail, tables_dir, "table_08a_baseline_mechanical_by_stratum.csv")
    write_csv(mechanical_summary, tables_dir, "table_08b_baseline_mechanical_summary.csv")
    plot_burn_association_summary(
        mechanical_summary,
        figures_dir,
        "fig_06_baseline_vs_mechanical_deterioration",
        "Baseline association with no-burn mechanical deterioration",
    )

    realized_h, mechanical_h = deterioration_by_horizon(pools)
    write_csv(realized_h, tables_dir, "table_09a_realized_deterioration_by_horizon.csv")
    write_csv(mechanical_h, tables_dir, "table_09b_mechanical_deterioration_by_horizon.csv")
    plot_deterioration_by_horizon(
        realized_h,
        figures_dir,
        "fig_07_realized_deterioration_by_horizon",
        "Realized deterioration across post-burn horizons",
    )
    plot_deterioration_by_horizon(
        mechanical_h,
        figures_dir,
        "fig_08_mechanical_deterioration_by_horizon",
        "No-burn mechanical deterioration across horizons",
    )

    cf = counterfactual_completeness(pools)
    write_csv(cf, tables_dir, "table_10_counterfactual_completeness.csv")

    pub = publication_status_table(pools)
    write_csv(pub, tables_dir, "table_11_publication_status.csv")
    plot_publication_status(pub, figures_dir)

    manifest = pd.DataFrame(
        [
            {"artifact": str(p.relative_to(output_dir)), "bytes": p.stat().st_size}
            for p in sorted(output_dir.rglob("*"))
            if p.is_file()
        ]
    )
    write_csv(manifest, tables_dir, "artifact_manifest.csv")

    print(f"Chapter 4 artifacts written to: {output_dir}")
    print(f"Figures: {len(list(figures_dir.glob('*.png')))} PNG + {len(list(figures_dir.glob('*.pdf')))} PDF")
    print(f"Tables:  {len(list(tables_dir.glob('*.csv')))} CSV")


if __name__ == "__main__":
    main()
