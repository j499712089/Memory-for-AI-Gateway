# ALL-160 架构交付证据

## 任务目标

设计 P0 阈值校准机制，解决 ALL-158 完成判据中的硬编码阈值（同义对 > 0.5、无关对 < 0.3）对中文文本不可满足的问题。

## 交付物清单

### 1. ADR-002: 语义相似度阈值校准机制

**路径**: `docs/decisions/ADR-002-semantic-similarity-threshold-calibration.md`

**内容**:
- 问题背景：CJK tokenization collapse 导致中文向量空间塌缩
- 决策方案：语言分档绝对阈值 + 相对分离度兜底
- 双层断言机制：防止模型退化和跨模型误判
- 实施路径：校准脚本 + 配置文件 + 集成测试

**关键决策**:
1. 英文阈值沿用 > 0.5 / < 0.3（已验证有效）
2. 中文阈值依赖 ALL-159 模型选型后重新校准
3. 相对分离度 ≥ 0.15 作为跨模型稳定的兜底检查
4. 禁止跨语言复用阈值（不同模型在不同语言上的向量分布差异巨大）

### 2. 阈值配置文件

**路径**: `test/semantic/thresholds.yml`

**内容**:
- 版本化配置格式（version: 1）
- 语言分档阈值定义（en/zh）
- 校准元数据（模型路径、哈希、校准日期）
- 使用说明和重新校准触发条件

**关键字段**:
```yaml
thresholds:
  en:
    synonym_min: 0.5
    unrelated_max: 0.3
    separation_min: 0.15
  zh:
    synonym_min: 0.55      # PLACEHOLDER - 待 ALL-159 决策后重新校准
    unrelated_max: 0.45    # PLACEHOLDER
    separation_min: 0.15
```

### 3. 校准脚本框架

**路径**: `test/semantic/`

**组件**:
- `probe_data.go`: 固化探针文本对（英文/中文，同义/无关）
- `calibrate.go`: 校准引擎实现
- `semantic_test.go`: ALL-155 验收用例参考实现
- `README.md`: 集成文档

**关键设计约束**:
1. ✅ 复用 `internal/embedding.Service.Encode()` - 不复制编码逻辑（解决 ALL-158 N4）
2. ✅ UTF-8 强制读写 - 避免 Windows PowerShell 中文 mojibake
3. ✅ 失败诊断 - 打印实际余弦值，区分阈值问题与分词问题
4. ✅ 幂等可重复 - 支持模型切换后重新校准

### 4. ALL-155 验收用例模板

**路径**: `test/semantic/semantic_test.go`

**测试覆盖**:
- `TestSemanticValidityEnglish`: 英文语义有效性断言
- `TestSemanticValidityChinese`: 中文语义有效性断言（待 ALL-159 校准）
- `TestEncodingConsistency`: 编码一致性检查（防止 ALL-158 N4 问题）
- `TestRelativeSeparation`: 相对分离度兜底检查

**关键断言**:
```go
// 绝对阈值（从 thresholds.yml 读取）
if sim < enThresh.SynonymMin {
    t.Errorf("Synonymous pair has low similarity: %.4f < %.2f", sim, enThresh.SynonymMin)
}

// 相对分离度（模型无关）
separation := minSynonym - maxUnrelated
if separation < 0.15 {
    t.Errorf("Insufficient separation: %.4f < 0.15", separation)
}

// 编码一致性（防止 N4）
if cosine(vec1, vec2) < 0.9999 {
    t.Errorf("Encoding inconsistency detected")
}
```

## 设计决策验证

### ✅ D1: 阈值不再硬编码

**验证**: `semantic_test.go` 从 `thresholds.yml` 动态加载阈值
```go
config, err := LoadThresholds("thresholds.yml")
enThresh := config.Thresholds["en"]
```

**影响**: ALL-155 用例可在模型切换后零代码修改重新验收

### ✅ D2: 语言分档隔离

**验证**: `thresholds.yml` 为 en/zh 分别定义阈值，校准脚本独立处理每种语言

**证据**: 实测数据显示英文/中文在同一模型上的分布完全不同
- 英文无关对: 0.00-0.05
- 中文无关对: 0.41-0.47（击穿 0.3 阈值）

### ✅ D3: 相对分离度兜底

**验证**: `TestRelativeSeparation` 检查 `min(synonym) - max(unrelated) ≥ 0.15`

**意义**: 即使绝对阈值因模型漂移而失效，相对分离度仍能拦截向量空间塌缩

### ✅ D4: 防止编码逻辑重复

**验证**: `calibrate.go` 和 `semantic_test.go` 都调用 `embedding.Service.Encode()`

**影响**: 修复 ALL-158 N4 问题（`retrieval/hybrid.go` 的 `encodeQuery` 私有副本会导致查询向量与写入向量处于不同空间）

## 集成影响分析

### 对 ALL-158 的影响

**变更前**:
```
完成判据第 7 条：同义文本对余弦 > 0.5、无关文本对 < 0.3
```

**变更后**:
```
完成判据第 7 条：语义有效性断言必须通过 test/semantic/semantic_test.go 验收：
- 英文：按 test/semantic/thresholds.yml en 配置
- 中文：按 test/semantic/thresholds.yml zh 配置（依赖 ALL-159 校准）
- 编码一致性：同一文本在不同路径的向量余弦 > 0.9999
- 相对分离度：min(同义) - max(无关) ≥ 0.15
```

**阻塞解除条件**: 本任务完成后，ALL-158 可引用 `test/semantic/thresholds.yml` 作为契约一部分

### 对 ALL-155 的影响

**变更前**: 验收用例可能硬编码 0.5/0.3 阈值

**变更后**: 验收用例必须：
1. 读取 `test/semantic/thresholds.yml` 获取阈值
2. 分别测试英文/中文语义有效性
3. 检查编码一致性（防止 N4）
4. 检查相对分离度（兜底）

**示例参考**: `test/semantic/semantic_test.go` 提供完整实现模板

### 对 ALL-159 的影响

**依赖关系**: 中文阈值的最终数值依赖 ALL-159 的模型选型决策

**三种场景**:
1. **方案 A** (paraphrase-multilingual-MiniLM-L12-v2): 重新校准中文阈值，384 维保持不变
2. **方案 B** (bge-small-zh-v1.5): 重新校准中文阈值，同步修改 `embedding.Dimensions = 512`
3. **方案 C** (保留 all-MiniLM-L6-v2): 契约中显式声明放弃中文检索，ALL-155 删除中文用例

**校准流程**: 模型选定后运行 `go test -run TestCalibration` 更新 `thresholds.yml`

## 风险评估与缓解

### R1: 中文阈值依赖外部决策

**风险**: ALL-159 模型选型未完成，中文阈值无法最终确定

**缓解**:
- 当前 `thresholds.yml` 中文阈值标记为 PLACEHOLDER，附带说明
- 相对分离度断言（≥ 0.15）与模型无关，可立即验证
- 英文阈值已完成校准，不受影响

**阻塞影响**: ALL-155 中文验收用例需等待 ALL-159 决策后重新校准

### R2: 校准脚本依赖真实模型

**风险**: 当前 `embedding.Service.Encode()` 仍是 sha256 哈希占位实现

**缓解**:
- 校准脚本架构已完成，可独立评审
- 探针文本对已固化在 `probe_data.go`，无需重新选取
- ALL-154 实现真实 ONNX 推理后，零修改即可运行校准

**时序**: 校准脚本 → ALL-154 ONNX 实现 → 运行校准 → 更新 thresholds.yml

### R3: 跨平台 UTF-8 处理

**风险**: Windows PowerShell 默认编码可能导致中文探针失真

**缓解**:
- 探针文本对直接硬编码在 Go 源文件中（UTF-8）
- 不依赖外部文本文件或命令行参数传递中文
- 参考 `qa_sem_probe.py` 的经验（须强制 UTF-8）

## 完成判据验证

### ✅ 判据 1: 阈值按语言分档校准

**证据**: `test/semantic/thresholds.yml` 为 en/zh 分别定义阈值

**状态**: 英文阈值已校准（0.5/0.3），中文阈值待 ALL-159 决策后校准

### ✅ 判据 2: 补一条相对分离度断言

**证据**: `semantic_test.go:TestRelativeSeparation` 实现

**断言逻辑**: `min(同义对余弦) - max(无关对余弦) ≥ 0.15`

### ✅ 判据 3: 校准脚本入库

**证据**: `test/semantic/calibrate.go` 实现校准引擎

**关键特性**:
- 调用 `internal/embedding.Service.Encode()`（不复制编码逻辑）
- UTF-8 安全（Go 原生字符串字面量）
- 失败诊断（打印实际余弦值）
- 支持重新校准（幂等）

### ✅ 判据 4: ALL-158 完成判据同步修订

**证据**: ADR-002 中明确定义 ALL-158 契约修订内容

**修订方向**: 删除硬编码 0.5/0.3，改为引用 `test/semantic/thresholds.yml`

## 建议后续行动

### 立即可执行（不依赖 ALL-159）

1. ✅ **架构评审**: 评审本任务交付物（ADR、配置、脚本框架）
2. ✅ **ALL-158 契约更新**: 用 `test/semantic/thresholds.yml` 替换硬编码阈值
3. ✅ **相对分离度验收**: 在 ALL-155 中添加 `TestRelativeSeparation` 用例

### 依赖 ALL-159 模型决策

1. ⏳ **模型选型拍板**: ALL-159 决定使用哪个模型（A/B/C）
2. ⏳ **中文阈值校准**: 模型部署后运行 `go test -run TestCalibration`
3. ⏳ **thresholds.yml 更新**: 提交校准结果到版本库
4. ⏳ **ALL-155 完整验收**: 英文 + 中文语义有效性全面测试

### 依赖 ALL-154 ONNX 实现

1. ⏳ **真实模型推理**: `embedding.Service.Encode()` 替换为 ONNX 推理
2. ⏳ **校准脚本首次运行**: 验证实测数据与预期分布一致
3. ⏳ **验收用例首次通过**: 确认阈值校准正确

## 附录：实测数据（参考）

### 英文探针（all-MiniLM-L6-v2）

```
同义对:
  0.8139  how to reset my password | password reset instructions
  0.9262  database migration guide | db migration handbook

无关对:
  0.0489  quantum chromodynamics | banana bread recipe
  0.0020  database connection pool | the weather in Paris is mild

分析:
  min(同义) = 0.8139
  max(无关) = 0.0489
  分离度 = 0.8139 - 0.0489 = 0.7650 ✓ (>> 0.15)
```

### 中文探针（all-MiniLM-L6-v2）

```
同义对:
  0.5550  如何重置我的密码 | 密码重置说明
  0.9861  数据库连接配置 | 数据库连接池设置
  0.8816  团队记忆检索接口 | 检索团队记忆的接口

无关对:
  0.4548  量子色动力学 | 香蕉面包食谱
  0.4154  数据库连接配置 | 巴黎今天天气温和
  0.4675  网关鉴权失败 | 周末去海边野餐

分析:
  min(同义) = 0.5550
  max(无关) = 0.4675
  分离度 = 0.5550 - 0.4675 = 0.0875 ✗ (< 0.15)

结论: 当前模型对中文文本的语义区分度不足
```

## 总结

本任务完成了 P0 阈值校准机制的架构设计与框架实现，包括：

1. **ADR 文档**: 记录架构决策、技术方案、风险缓解
2. **配置文件**: 版本化阈值配置，支持语言分档与重新校准
3. **校准脚本**: 可重复的自动化校准流程
4. **验收用例**: ALL-155 参考实现，双层断言（绝对 + 相对）
5. **集成文档**: 使用说明、故障排查、维护流程

阻塞项已解除：ALL-158 可引用 `test/semantic/thresholds.yml` 修订契约，ALL-155 可参考 `semantic_test.go` 实现验收用例。

待 ALL-159 模型选型决策后，运行校准脚本即可完成中文阈值校准，整个验收链路即可打通。
