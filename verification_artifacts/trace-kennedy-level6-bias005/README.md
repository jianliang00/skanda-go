# Kennedy Level 6 Bias 0.05 Stream Trace

Generated on 2026-06-13, Asia/Shanghai.

Command:

```shell
scripts/stream_trace.sh /tmp/skanda-public-corpus-canterbury/corpora/canterbury/kennedy.xls 6 0.05 verification_artifacts/trace-kennedy-level6-bias005
```

The trace compares the C++ and Go compressed streams for the largest positive Canterbury size delta observed in the public-corpus matrix.

| Metric | C++ | Go |
|---|---:|---:|
| Compressed size | 78692 | 80234 |
| Compressed blocks | 12 | 15 |
| Final raw blocks | 1 | 1 |

Both streams are valid and mutually decodable. The first structural difference is block splitting: the C++ stream includes two 196608-byte compressed blocks, while the Go stream mostly uses 65536-byte compressed blocks and one 131072-byte compressed block. This points future ratio-alignment work at block splitter model decisions before deeper entropy bitstream differences.
