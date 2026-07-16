import csv
from decimal import Decimal
from pathlib import Path

import matplotlib.pyplot as plt

TABLES_DIR = Path("outputs/paper_tables")
FIGURES_DIR = Path("outputs/paper_figures")

FIGURES_DIR.mkdir(parents=True, exist_ok=True)


def D(value):
    if value is None or value == "":
        return Decimal("0")

    value = str(value).replace("%", "").strip()

    try:
        return Decimal(value)
    except Exception:
        return Decimal("0")


def read_csv(name):
    path = TABLES_DIR / name

    with path.open("r", encoding="utf-8", newline="") as f:
        return list(csv.DictReader(f))


def short_pool_name(name):
    return (
        name.replace("USDC/WETH ", "U/W ")
        .replace("WBTC/WETH ", "B/W ")
        .replace("WETH/USDT ", "W/U ")
        .replace("USDC/USDT ", "U/T ")
    )


def savefig(name):
    plt.tight_layout()
    plt.savefig(FIGURES_DIR / f"{name}.png", dpi=300)
    plt.savefig(FIGURES_DIR / f"{name}.pdf")
    plt.close()


def figure_2_directional_piauc():
    rows = read_csv("table_2_pool_empirical_summary.csv")

    pools = [short_pool_name(r["Pool"]) for r in rows]
    zfo = [float(D(r["ZFO PIAUC"])) for r in rows]
    ofz = [float(D(r["OFZ PIAUC"])) for r in rows]

    x = list(range(len(pools)))
    width = 0.38

    plt.figure(figsize=(9, 4.8))
    plt.bar([i - width / 2 for i in x], zfo, width, label="token0 → token1")
    plt.bar([i + width / 2 for i in x], ofz, width, label="token1 → token0")

    plt.xticks(x, pools, rotation=20, ha="right")
    plt.ylabel("Mean PIAUC (bps)")
    plt.title("Directional price-impact AUC across pools")
    plt.yscale("log")
    plt.legend()

    savefig("figure_2_directional_piauc")


def figure_3_concentration_gap():
    rows = read_csv("table_3_lsis_vs_active_concentration.csv")

    pools = [short_pool_name(r["Pool"]) for r in rows]
    top1_active = [float(D(r["Top1 active"])) for r in rows]
    top1_lsis = [float(D(r["Top1 LSIS"])) for r in rows]
    top3_active = [float(D(r["Top3 active"])) for r in rows]
    top3_lsis = [float(D(r["Top3 LSIS"])) for r in rows]

    x = list(range(len(pools)))
    width = 0.2

    plt.figure(figsize=(10, 5))
    plt.bar([i - 1.5 * width for i in x], top1_active, width, label="Top1 active")
    plt.bar([i - 0.5 * width for i in x], top1_lsis, width, label="Top1 LSIS")
    plt.bar([i + 0.5 * width for i in x], top3_active, width, label="Top3 active")
    plt.bar([i + 1.5 * width for i in x], top3_lsis, width, label="Top3 LSIS")

    plt.xticks(x, pools, rotation=20, ha="right")
    plt.ylabel("Share (%)")
    plt.title("LSIS concentration compared with active-liquidity concentration")
    plt.legend()

    savefig("figure_3_lsis_vs_active_concentration")


def figure_4_effective_lsis_positions():
    rows = read_csv("table_2_pool_empirical_summary.csv")

    pools = [short_pool_name(r["Pool"]) for r in rows]
    effective_positions = [float(D(r["Eff. LSIS positions"])) for r in rows]

    plt.figure(figsize=(8, 4.5))
    plt.bar(pools, effective_positions)

    plt.xticks(rotation=20, ha="right")
    plt.ylabel("Effective LSIS positions")
    plt.title("Effective number of load-bearing positions by pool")

    savefig("figure_4_effective_lsis_positions")


def figure_5_persistent_ranges():
    rows = read_csv("table_5_persistent_load_bearing_ranges.csv")

    # Keep only the strongest range per pool for the main figure.
    best_by_pool = {}

    for row in rows:
        pool = row["Pool"]

        if pool not in best_by_pool:
            best_by_pool[pool] = row
            continue

        current = best_by_pool[pool]

        row_rank1 = int(row["Rank-1 count"] or 0)
        cur_rank1 = int(current["Rank-1 count"] or 0)

        row_lsis = D(row["Avg total LSIS"])
        cur_lsis = D(current["Avg total LSIS"])

        if (row_rank1, row_lsis) > (cur_rank1, cur_lsis):
            best_by_pool[pool] = row

    selected = list(best_by_pool.values())

    labels = [
        f"{short_pool_name(r['Pool'])}\n{r['Range']}"
        for r in selected
    ]
    rank1_counts = [int(r["Rank-1 count"] or 0) for r in selected]
    appearances = [int(r["Appearances"] or 0) for r in selected]

    x = list(range(len(selected)))
    width = 0.35

    plt.figure(figsize=(10, 5))
    plt.bar([i - width / 2 for i in x], appearances, width, label="Appearances")
    plt.bar([i + width / 2 for i in x], rank1_counts, width, label="Rank-1 count")

    plt.xticks(x, labels, rotation=20, ha="right")
    plt.ylabel("Snapshot count")
    plt.title("Persistence of the strongest load-bearing range per pool")
    plt.legend()

    savefig("figure_5_persistent_load_bearing_ranges")


def main():
    figure_2_directional_piauc()
    figure_3_concentration_gap()
    figure_4_effective_lsis_positions()
    figure_5_persistent_ranges()

    print(f"figures written to: {FIGURES_DIR}")


if __name__ == "__main__":
    main()