# Go 重写 C++ 压缩算法库生产可用验证 Checklist

## 0. 基线定义 Checklist

> 目标：先明确“生产可用”到底要对齐什么，否则后续测试无法判定通过或失败。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| 明确算法兼容目标 | 确认 Go 版本是完全兼容 C++ 格式，还是仅功能等价 | 文档明确写出兼容策略 | `compatibility.md` |
| 明确是否要求压缩输出 byte-to-byte 一致 | 对同一输入分别用 C++ 和 Go 压缩，确认是否要求压缩结果完全一致 | 如果格式允许多种合法编码，则不强制 byte-to-byte；但必须互相可解压 | `compatibility.md` |
| 明确支持的压缩参数 | 列出压缩等级、window size、block size、dictionary、checksum、streaming 等参数 | Go 版本参数覆盖 C++ 生产使用范围 | 参数矩阵 |
| 明确支持的数据格式版本 | 列出历史线上所有压缩格式版本 | Go 解压器必须能解所有仍在线上存在的数据版本 | 格式版本表 |
| 明确性能 SLA | 定义压缩吞吐、解压吞吐、P99 延迟、内存、压缩率门槛 | SLA 有量化指标 | `performance_sla.md` |
| 明确失败语义 | 定义损坏输入、截断输入、非法 header、checksum 错误时返回什么错误 | Go 行为与 C++ 或新规范一致 | 错误码 / error 规范 |
| 明确上线回滚策略 | 确定是否支持降级到 C++ 版本、是否支持双写、是否支持 shadow 验证 | 有明确回滚开关 | 灰度方案 |

---

## 1. 测试数据集 Checklist

> 目标：构建一套覆盖公开 benchmark、线上真实数据、边界数据、恶意数据的测试 corpus。

公开压缩 benchmark 可以作为基础测试集，例如 Canterbury Corpus、Calgary Corpus、Silesia Corpus、enwik8/enwik9；其中 Canterbury 常用于比较无损压缩方法，Silesia 包含更现代且更大范围的数据类型，enwik8/enwik9 常用于大文本压缩 benchmark。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| 引入 Canterbury Corpus | 下载并加入测试仓库或 CI 缓存 | 所有文件 round-trip 通过 | `testdata/canterbury/` |
| 引入 Silesia Corpus | 下载并加入 benchmark 数据源 | 所有文件 round-trip 通过，性能数据可记录 | `testdata/silesia/` |
| 引入 enwik8 | 使用 100MB Wikipedia 文本测试大文本场景 | round-trip 通过，性能达标 | `testdata/enwik8` |
| 可选引入 enwik9 | 用于 1GB 大文件压力测试 | 大文件压缩/解压无 panic、无 OOM、性能达标 | `testdata/enwik9` |
| 构造小文件集合 | 空文件、1 byte、2 bytes、3 bytes、短字符串 | 全部 round-trip 通过 | `testdata/edge/small/` |
| 构造重复数据集合 | 全 0、全 1、全 `A`、周期 pattern | round-trip 通过，压缩率符合预期 | `testdata/edge/repeated/` |
| 构造高熵数据集合 | 随机字节、加密数据、已压缩数据 | 不要求明显压缩，但必须正确解压 | `testdata/edge/random/` |
| 构造结构化文本集合 | JSON、HTML、XML、日志、CSV、配置文件 | round-trip 通过，压缩率记录 | `testdata/structured/` |
| 构造二进制集合 | protobuf、图片、sqlite/db dump、wasm、可执行文件 | round-trip 通过 | `testdata/binary/` |
| 收集线上真实样本 | 从生产环境采样不同业务、不同大小、不同类型 payload | 样本脱敏后可进入回归测试 | `testdata/production/` |
| 收集历史压缩产物 | 收集 C++ 历史版本压缩出来的数据 | Go 解压器 100% 解压成功 | `testdata/legacy_compressed/` |
| 构造损坏压缩流 | 截断、随机翻转 bit、非法 header、错误 checksum | 不 panic、不 OOM，返回预期错误 | `testdata/corrupt/` |
| 构造压缩炸弹样本 | 高压缩比、小输入大输出样本 | 解压受限策略生效 | `testdata/bomb/` |

---

## 2. 基础正确性 Checklist

> 目标：验证 Go 实现自身的压缩和解压逻辑正确。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| Round-trip 测试 | 对所有测试输入执行 `decompress(compress(input))` | 输出与原始输入 byte-to-byte 一致 | `TestRoundTripCorpus` |
| 空输入测试 | 压缩和解压空 byte slice | 不 panic，解压结果为空 | 单测 |
| 小输入测试 | 测试 1、2、3、4、8、16、32 byte 输入 | round-trip 通过 | 单测 |
| block 边界测试 | 输入大小覆盖 block size -1、block size、block size +1 | round-trip 通过 | 单测 |
| window 边界测试 | 构造刚好跨 window 的重复数据 | round-trip 通过 | 单测 |
| 最大大小测试 | 测试允许范围内最大输入 | 不 panic、不 OOM，结果正确 | 压力测试 |
| checksum 正确性 | 开启 checksum 时篡改数据 | 解压必须失败，并返回明确错误 | 单测 |
| dictionary 正确性 | 使用正确/错误 dictionary 解压 | 正确 dictionary 成功；错误 dictionary 失败 | 单测 |
| 多压缩等级测试 | 覆盖所有生产使用的压缩等级 | 每个等级 round-trip 通过 | 参数化单测 |
| 多参数组合测试 | 覆盖 block size、window size、checksum、dictionary 等组合 | 所有生产组合通过 | 参数化单测 |
| 流式写入测试 | 分片写入 compressor | 解压结果等于原始输入 | streaming 单测 |
| 流式读取测试 | 分片读取 decompressor | 输出完整且顺序正确 | streaming 单测 |
| flush 行为测试 | 测试 flush 后数据是否可被解压 | flush 语义符合规范 | streaming 单测 |
| close 行为测试 | 未 close、重复 close、close 后写入 | 行为符合规范，不 panic | streaming 单测 |
| partial read 测试 | 下游 reader 每次只读 1 byte / 随机长度 | 解压结果正确 | streaming 单测 |

---

## 3. C++ 与 Go 兼容性 Checklist

> 目标：确认 Go 重写版不会破坏现有 C++ 生产数据和上下游系统。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| C++ 压缩 → Go 解压 | 用 C++ 压缩全部 corpus，再用 Go 解压 | 解压结果与原始输入完全一致 | 兼容性测试报告 |
| Go 压缩 → C++ 解压 | 用 Go 压缩全部 corpus，再用 C++ 解压 | 解压结果与原始输入完全一致 | 兼容性测试报告 |
| 历史 C++ 数据解压 | 用 Go 解压历史线上压缩产物 | 100% 成功，输出符合预期 | legacy 测试报告 |
| 多版本 C++ 兼容 | 覆盖仍在线上存在的 C++ 压缩格式版本 | Go 解压器全部兼容 | 版本兼容矩阵 |
| 参数兼容 | 用 C++ 的所有生产参数压缩，Go 解压 | 全部成功 | 参数兼容报告 |
| 字典兼容 | C++ 使用 dictionary 压缩，Go 使用同字典解压 | 全部成功 | dictionary 测试 |
| checksum 兼容 | C++ 开启 checksum，Go 解压校验 | 正确数据成功，损坏数据失败 | checksum 测试 |
| 错误行为兼容 | 同一损坏输入分别给 C++ 和 Go 解压 | Go 返回错误类别符合预期 | 错误兼容报告 |
| byte-to-byte 可选验证 | 如果业务要求确定性压缩，则比较 C++ 和 Go 压缩输出 | 要求一致时必须完全一致；不要求一致时跳过 | determinism 测试 |
| 交叉版本矩阵 | C++ old/new 与 Go old/new 互压互解 | 上线期间涉及的版本组合全部通过 | 交叉兼容矩阵 |

---

## 4. 差分测试 Checklist

> 目标：用 C++ 作为 oracle，持续发现 Go 实现偏差。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| corpus 差分测试 | 对每个输入同时跑 C++ 和 Go | 解压输出一致 | diff 测试 |
| 随机输入差分测试 | 生成随机 byte slice，分别用 C++ 和 Go round-trip | 输出一致 | randomized diff |
| 参数组合差分测试 | 随机生成生产允许的参数组合 | C++ 和 Go 行为一致或符合兼容规范 | 参数 diff |
| 错误输入差分测试 | 同一 corrupt stream 分别给 C++ 和 Go | Go 不 panic；错误类型符合规范 | error diff |
| 压缩率差分 | 比较 Go 和 C++ 压缩后大小 | 劣化不超过 SLA，例如 ≤ 3% | ratio diff 报告 |
| 性能差分 | 比较 Go 和 C++ 压缩/解压耗时 | 达到性能 SLA | perf diff 报告 |
| 内存差分 | 比较 Go 和 C++ 峰值内存 | 达到资源 SLA | memory diff 报告 |

---

## 5. Fuzzing Checklist

> 目标：发现人工测试难以覆盖的边界输入、畸形压缩流、安全问题和 panic。

Go fuzzing 适合压缩库这种 property-based 场景，例如验证压缩后再解压能恢复原始输入；成熟 Go 压缩库也会持续进行 fuzz 测试，以确保 decoder 面对任意输入不会崩溃或越界。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| Round-trip fuzz | 随机生成原始输入，执行 compress + decompress | 永远不 panic；成功压缩时解压结果等于原始输入 | `FuzzRoundTrip` |
| Decompress fuzz | 随机生成压缩流，直接调用 decompress | 不 panic、不 OOM、不无限循环 | `FuzzDecompress` |
| Streaming fuzz | 随机 chunk size 写入/读取 | 输出正确或返回预期错误 | `FuzzStreaming` |
| Dictionary fuzz | 随机输入 + 随机 dictionary | 不 panic，行为符合规范 | `FuzzDictionary` |
| Header fuzz | 随机生成 header/block metadata | 不 panic，非法输入返回错误 | `FuzzHeader` |
| Corrupt fuzz | 基于合法压缩流随机 bit flip、truncate、splice | 不 panic，错误可控 | `FuzzCorruptStream` |
| 参数 fuzz | 随机压缩参数组合 | 非法参数返回错误；合法参数 round-trip 成功 | `FuzzOptions` |
| 资源限制 fuzz | 对解压输出大小、内存、block 数设置限制 | 超限时及时失败，不 OOM | `FuzzResourceLimit` |
| 持续 fuzz | 在 CI/nightly 中持续运行 | 连续 N 小时无新 crash | fuzz 报告 |
| fuzz crash 回归 | fuzz 找到的 crash 加入 seed corpus | 所有历史 crash case 不复现 | `testdata/fuzz/regression/` |

建议最低门槛：

| 阶段 | Fuzz 运行时间 |
|---|---|
| 每次 PR | 1 - 5 分钟 smoke fuzz |
| 每日 nightly | 1 - 4 小时 |
| 发布前 | 12 - 24 小时 |
| 高风险改动 | 24 小时以上 |

---

## 6. 性能 Benchmark Checklist

> 目标：确认 Go 版本不仅正确，而且满足生产性能和资源要求。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| 压缩吞吐 benchmark | 对 corpus 执行 Go benchmark | MB/s 达到 SLA | benchmark 报告 |
| 解压吞吐 benchmark | 对 corpus 压缩产物执行解压 benchmark | MB/s 达到 SLA | benchmark 报告 |
| 压缩率 benchmark | 比较压缩前后大小 | 压缩率达到 SLA | ratio 报告 |
| 内存分配 benchmark | 使用 `b.ReportAllocs()` | allocs/op、B/op 不超过 SLA | Go benchmark 输出 |
| GC 压力测试 | 高并发长时间压缩/解压 | GC pause、heap 增长可控 | pprof / trace |
| 大文件 benchmark | 对 enwik8/enwik9 或生产大文件测试 | 不 OOM，性能达标 | 大文件报告 |
| 小文件 benchmark | 对大量小 payload 测试 | P99 延迟达标 | latency 报告 |
| 多压缩等级 benchmark | 每个生产等级单独测 | 每个等级都有明确性能数据 | 等级性能表 |
| dictionary benchmark | 测试有/无 dictionary 的性能和压缩率 | 达到业务预期 | dictionary 报告 |
| streaming benchmark | 测试不同 chunk size | chunk size 敏感性可接受 | streaming perf 报告 |
| 并发 benchmark | 多 goroutine 并发压缩/解压 | 无 data race，吞吐符合预期 | 并发报告 |
| 长时间 soak test | 连续运行数小时 | 无内存泄漏、无性能退化 | soak 报告 |

建议记录这些指标：

```text
input_name
input_size
compressed_size_cpp
compressed_size_go
ratio_cpp
ratio_go
compress_MBps_cpp
compress_MBps_go
decompress_MBps_cpp
decompress_MBps_go
allocs_per_op_go
bytes_per_op_go
p50_latency
p95_latency
p99_latency
peak_rss
```

---

## 7. 资源与安全 Checklist

> 目标：防止生产环境中出现 OOM、CPU 打满、解压炸弹、panic、死循环等问题。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| 解压输出大小限制 | 设置 max decompressed size | 超限返回明确错误 | 单测 |
| 压缩输入大小限制 | 设置 max input size 或流式处理策略 | 超限行为符合规范 | 单测 |
| block 数量限制 | 构造大量小 block 压缩流 | 不无限循环，不 OOM | 安全测试 |
| dictionary 大小限制 | 输入超大 dictionary | 返回错误或受控处理 | 单测 |
| header size 限制 | 构造异常 header | 不 OOM，不 panic | fuzz/单测 |
| offset 越界保护 | 构造非法 back-reference | 返回错误，不越界 | 单测 |
| checksum 错误保护 | 篡改 payload/checksum | 解压失败且错误明确 | 单测 |
| truncated stream 保护 | 截断任意位置 | 返回 EOF/格式错误，不 panic | 单测 |
| zip-bomb 类测试 | 小压缩输入对应巨大输出 | 达到限制后失败 | 安全测试 |
| CPU 消耗保护 | 构造极端慢路径输入 | 耗时不超过阈值 | 压力测试 |
| panic recover 测试 | 在 fuzz 和 corrupt corpus 中验证 | 生产 API 不应 panic | fuzz 报告 |
| data race 检查 | `go test -race` | 无 race | CI 报告 |
| 静态检查 | `go vet`、`staticcheck` | 无高风险问题 | CI 报告 |
| unsafe 使用审计 | 如果使用 `unsafe`，逐处审计 | 有明确边界和测试覆盖 | unsafe 审计记录 |

---

## 8. API 行为 Checklist

> 目标：确认 Go 库不仅算法正确，API 也适合生产使用。

| 检查项 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| API 参数校验 | 对非法参数调用 API | 返回明确错误，不 panic | 单测 |
| nil 输入处理 | nil reader/writer/options | 行为符合 Go 习惯或明确返回错误 | 单测 |
| 错误包装 | 检查错误是否可 `errors.Is/As` | 调用方可识别错误类别 | 错误测试 |
| 并发安全说明 | 明确 encoder/decoder 是否可并发复用 | 文档清晰，测试覆盖 | API 文档 |
| buffer 复用行为 | 测试输入/输出 buffer aliasing | 不产生数据污染 | 单测 |
| close 幂等性 | 多次 close | 不 panic，行为明确 | 单测 |
| reset 行为 | Encoder/Decoder Reset 后复用 | 状态干净，结果正确 | 单测 |
| context 支持 | 如果支持 context/cancel | cancel 后及时返回 | 单测 |
| reader/writer 错误传播 | 底层 IO 返回错误 | 上层正确传播 | 单测 |
| partial write 处理 | writer 短写或返回错误 | 正确处理 | 单测 |
| 文档示例可运行 | `go test` 跑 Example | Example 全部通过 | Go doc example |

---

## 9. CI/CD 门禁 Checklist

> 目标：把验证固化成自动化流程，避免依赖人工判断。

| 阶段 | 必跑项目 | 通过标准 |
|---|---|---|
| 每次 PR | 单测、round-trip 小 corpus、C++/Go 基础兼容、race、staticcheck | 全部通过 |
| 每次 PR | 小规模 benchmark | 性能不低于基线阈值 |
| 每次 PR | smoke fuzz | 无 crash |
| 每日 nightly | 全量 corpus、Silesia、enwik8、生产样本 | 全部通过 |
| 每日 nightly | 长时间 fuzz | 无新 crash |
| 每日 nightly | C++/Go 全量差分 | 全部通过 |
| 发布前 | enwik9 或超大文件测试 | 不 OOM，性能达标 |
| 发布前 | 12-24h fuzz | 无 crash |
| 发布前 | 压力测试 / soak test | 无泄漏、无退化 |
| 发布前 | 兼容性矩阵 | 全部通过 |
| 发布前 | benchmark 对比报告 | 达到 SLA |
| 发布前 | 安全测试 | 无高危问题 |

建议 CI 输出至少包含：

```text
correctness: pass/fail
compatibility_cpp_to_go: pass/fail
compatibility_go_to_cpp: pass/fail
legacy_data_decode: pass/fail
fuzz_status: pass/fail
compression_ratio_regression: pass/fail
compress_speed_regression: pass/fail
decompress_speed_regression: pass/fail
memory_regression: pass/fail
race_check: pass/fail
security_limits: pass/fail
```

---

## 10. 灰度上线 Checklist

> 目标：在真实生产环境中验证 Go 库，而不是只在离线测试中验证。

| 阶段 | 具体操作 | 通过标准 | 产物 |
|---|---|---|---|
| Shadow 解压 | 生产请求仍使用 C++，旁路用 Go 解压并比较结果 | mismatch = 0 | shadow 报告 |
| Shadow 压缩 | 生产仍使用 C++ 输出，旁路用 Go 压缩并验证可解压 | Go 压缩结果 round-trip 成功 | shadow 报告 |
| 双写验证 | 同时生成 C++ 和 Go 压缩结果，线上不消费 Go 结果 | Go 结果可被 C++/Go 解压 | 双写报告 |
| 小流量读路径切换 | 1% 流量使用 Go 解压 | 错误率、延迟、资源无异常 | 灰度报告 |
| 小流量写路径切换 | 1% 流量使用 Go 压缩 | 下游解压无异常 | 灰度报告 |
| 扩大灰度 | 1% → 5% → 10% → 25% → 50% → 100% | 每阶段指标稳定 | 灰度记录 |
| 实时监控 | 监控错误率、panic、延迟、内存、压缩率 | 无异常告警 | dashboard |
| 快速回滚 | 保留 C++ fallback 开关 | 出现异常可分钟级回滚 | rollback 记录 |
| 数据兼容观察期 | 新 Go 压缩数据上线后观察历史消费者 | 无兼容问题 | 观察报告 |

生产监控建议至少包括：

```text
compress_success_count
compress_error_count
decompress_success_count
decompress_error_count
corrupt_input_count
checksum_error_count
unsupported_version_count
panic_count
compression_ratio_p50/p95/p99
compress_latency_p50/p95/p99
decompress_latency_p50/p95/p99
memory_usage
gc_pause
fallback_to_cpp_count
go_cpp_mismatch_count
```

---

# 建议的最终验收标准

可以把下面这组标准作为“允许进入生产”的硬门槛。

## A. 正确性门槛

| 项目 | 通过标准 |
|---|---|
| 公开 corpus round-trip | 100% 通过 |
| 生产 corpus round-trip | 100% 通过 |
| 边界输入 round-trip | 100% 通过 |
| C++ 压缩 → Go 解压 | 100% 通过 |
| Go 压缩 → C++ 解压 | 100% 通过，除非明确不要求旧版消费新数据 |
| 历史线上压缩数据 | 100% 可由 Go 解压 |
| 损坏输入 | 0 panic，0 OOM，全部返回受控错误 |
| streaming 模式 | 所有 chunk size 测试通过 |
| dictionary 模式 | 所有生产字典测试通过 |

## B. 稳定性门槛

| 项目 | 通过标准 |
|---|---|
| PR smoke fuzz | 无 crash |
| 发布前 fuzz | 至少 12-24 小时无 crash |
| race 检查 | 0 race |
| soak test | 长时间运行无泄漏、无性能退化 |
| panic | 所有测试和灰度中 panic = 0 |
| 解压炸弹 | 资源限制生效，不 OOM |

## C. 性能门槛

建议根据业务设定具体阈值，例如：

| 项目 | 示例通过标准 |
|---|---|
| 压缩吞吐 | 不低于 C++ 版本 95% |
| 解压吞吐 | 不低于 C++ 版本 95% |
| 压缩率 | 相对 C++ 劣化不超过 3% |
| P99 压缩延迟 | 不超过当前生产 SLA |
| P99 解压延迟 | 不超过当前生产 SLA |
| 峰值内存 | 不超过 C++ 版本 1.5x，或不超过业务预算 |
| Go allocs/op | 不超过设定阈值 |
| GC pause | 不影响服务 SLA |

## D. 上线门槛

| 项目 | 通过标准 |
|---|---|
| Shadow 解压 | mismatch = 0 |
| Shadow 压缩 | Go 结果可被 Go/C++ 正确解压 |
| 1% 灰度 | 错误率、延迟、资源无异常 |
| 逐步放量 | 每阶段指标稳定 |
| 回滚能力 | 验证可用 |
| 监控指标 | 已接入并有告警 |
| 文档 | API、兼容性、错误语义、性能报告齐全 |

---

# 发布前最终 Checklist

上线前建议逐项打勾：

```text
[ ] 已明确 Go 库与 C++ 库的兼容目标
[ ] 已明确是否要求压缩输出 byte-to-byte 一致
[ ] 已覆盖所有生产使用的压缩参数
[ ] 已覆盖所有历史线上压缩格式版本
[ ] Canterbury Corpus round-trip 100% 通过
[ ] Silesia Corpus round-trip 100% 通过
[ ] enwik8 / 大文本测试通过
[ ] 生产真实样本 round-trip 100% 通过
[ ] 历史 C++ 压缩数据 Go 解压 100% 通过
[ ] C++ 压缩 → Go 解压 100% 通过
[ ] Go 压缩 → C++ 解压 100% 通过
[ ] 所有边界输入测试通过
[ ] 所有 streaming 测试通过
[ ] 所有 dictionary 测试通过
[ ] 所有 checksum 测试通过
[ ] 所有 corrupt input 测试不 panic、不 OOM
[ ] 解压输出大小限制已实现并测试通过
[ ] zip-bomb / decompression-bomb 类测试通过
[ ] Go fuzzing 已持续运行，发布前无 crash
[ ] 所有 fuzz crash case 已加入回归测试
[ ] go test -race 通过
[ ] go vet / staticcheck 通过
[ ] benchmark 与 C++ 对比达标
[ ] 压缩率劣化在 SLA 范围内
[ ] 压缩吞吐达标
[ ] 解压吞吐达标
[ ] P99 延迟达标
[ ] 内存峰值达标
[ ] GC 压力可接受
[ ] 长时间 soak test 通过
[ ] API 错误语义已文档化
[ ] 监控指标已接入
[ ] 告警规则已配置
[ ] fallback / rollback 开关已验证
[ ] shadow 解压 mismatch = 0
[ ] shadow 压缩验证通过
[ ] 小流量灰度通过
[ ] 放量计划已明确
[ ] 回滚预案已评审
[ ] 最终验收报告已归档
```

