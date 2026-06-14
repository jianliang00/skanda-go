# Verification Artifacts

These files are generated evidence for the Skanda Go verification report.

| Artifact | Source | Purpose |
|---|---|---|
| `canterbury-all-levels-matrix.csv` | Current build, `PUBLIC_CORPORA=canterbury`, all levels and biases | C++/Go mutual decompression and current Canterbury `3%` size gate. |
| `perf-compare-focused-optimized.csv` | Current build, `PERF_ITERATIONS=50 LEVELS='0 6 10' BIASES='0.05'` focused Canterbury performance command | Raw C++/Go focused throughput, size, allocation, fresh decompression, reuse compression, and reuse decode rows. It keeps legacy speed threshold fields and includes signed speed-delta fields for report tables and charts. |
| `perf-compare-focused-summary.csv` | Derived from focused performance rows | Report table with signed compression, reuse compression, fresh decompression, and reuse decode deltas. |
| `focused-size-delta-signed.csv` | Derived from focused performance rows | Signed focused compressed-size deltas. |
| `compression-delta-distribution.csv` | Derived from available matrix artifacts | Counts of Go-larger, Go-smaller, and equal outputs. |
| `compression-size-top-deltas.csv` | Derived from available public matrix artifacts | Largest positive and negative size deltas. |
| `charts/*.svg` and `charts/*.png` | `scripts/generate_report_charts.py`, derived from CSV artifacts | Report charts. Focused performance charts use centered signed Go-vs-C++ throughput deltas with `0%` as parity. Negative bars extend to the Go-slower side and positive bars extend to the Go-faster side. |

Silesia and enwik8 matrix files are archived evidence and should be rerun for the current build before release.
