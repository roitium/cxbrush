# 原始 Python POC（归档）

这是项目最早期用来验证「学习通答题页内联字体还原」的 Python 原型，已被
`internal/cx` 的 Go 实现取代。保留原因：

- `extract.py` 里保留了最早的字形提取、栅格化与比对参数，方便对照；
- `selftest.py` 是最初的回归脚本（对一批 HTML 做还原并核对结果）；
- 协议细节已整理进 [docs/protocol.md](../../protocol.md)。

## 文件

| 文件 | 说明 |
| --- | --- |
| `extract.py` | 抓答题页 / 解析 HTML / 字体还原 / 输出 JSON |
| `selftest.py` | 对样本批量还原并校验 |
| `requirements.txt` | Python 依赖 |

## 使用

脚本不再随主流程维护，依赖需要自己装：

```bash
pip install -r docs/archive/poc/requirements.txt

python3 docs/archive/poc/extract.py --html /tmp/work.html \
  --ref-font internal/assets/NotoSansCJKsc-DemiLight.otf \
  -o /tmp/work.json --md-out /tmp/work.md
```

字体已经内嵌在 Go 二进制里，`internal/assets/NotoSansCJKsc-DemiLight.otf`
（Noto Sans CJK，SIL OFL 1.1）可以直接给脚本当 `--ref-font`。

> 归档目录不含 `cache/`、`out/` 等运行产物；需要的话由脚本重新生成。
