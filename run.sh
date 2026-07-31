make backfill-swaps-<pool>

rm -rf outputs/<pool>

make analyze-<pool> && \
make freeze-controls-<pool>
