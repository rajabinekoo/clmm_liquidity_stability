# Chapter 4 figures and tables

This script builds thesis/paper-ready descriptive artifacts directly from the
five analyzer output directories. It does not modify the research outputs.

## Install

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r scripts/chapter4/requirements.txt
```

## Run

If analyzer outputs live under `outputs/`:

```bash
make chapter4-figures
```

Or run directly:

```bash
python3 scripts/chapter4/generate.py \
  --outputs-dir outputs \
  --output-dir artifacts/chapter4
```

Every figure is emitted as both PNG (300 dpi) and vector PDF. CSV tables are
written to `artifacts/chapter4/tables`.

## Baseline comparison

The baseline analysis is intentionally descriptive rather than causal.

Snapshot-level comparison asks how closely simple position proxies reproduce
the LSIS ranking within each reconstructed snapshot. The proxies are active
liquidity share, raw position liquidity, normalized liquidity density, range
width, and distance to the nearest range edge.

Burn-level comparison asks how strongly immediate LSIS and simple withdrawal
proxies are rank-associated with two future outcomes across each pool x horizon
stratum:

- realized total PIAUC deterioration;
- midpoint of the no-burn mechanical deterioration envelope.

The burn-level summary reports median Spearman rho across strata, the IQR, and
how often each proxy has the largest absolute Spearman association within a
stratum. These results should be described as comparative associations, not as
proof of universal predictive superiority or causality.
