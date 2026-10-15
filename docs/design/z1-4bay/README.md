# Z1 4-Bay 机箱工业设计 · v0.3（2026-10-15）

基于 HARDWARE_SPEC.md 已定决策（4 盘位 / N150 / 极简「留白」风格）的机箱设计：图纸 + 参数化 CAD 模型 + BOM。

## 文件

| 文件 | 内容 |
|------|------|
| `Z1-4Bay-机箱设计图.html` | 主图纸 v0.3：三视图(标注)、剖面风道、爆炸图、托架细节、CMF、渲染图、BOM、§8 原型版布局 |
| `BOM.csv` | 量产 BOM（1000 台批价，合计 ¥1182/台，模具一次性 ≈¥6.3 万） |
| `renders/*.png` | AI 概念渲染（万相 wan2.2-t2i-flash）：front34=主视觉(与规格一致)、hero=家居氛围、tray=托架特写、exploded=爆炸概念 |
| `cad/build_model.py` | FreeCAD 1.0 参数化建模脚本（headless，`freecadcmd build_model.py` 可重跑） |
| `cad/z1-4bay-prototype.FCStd` | FreeCAD 源文件（原型版 1:1） |
| `cad/assembly.step` | 装配体 STEP（可导入 Fusion360/SolidWorks） |
| `cad/stl/*.stl` | 打印件：shell_main / base_plate / drive_tray(×4) / backplane_bracket / fan_mount |
| `cad/preview*.png` | 快速网格预览（matplotlib，非正式渲染） |
| `cad/check_mesh.py` | STL 水密性校验脚本（freecadcmd 运行） |

## 两档尺寸（v0.3 修订）

| 版本 | 布局 | 外形 | 体量 | 说明 |
|------|------|------|------|------|
| **原型版**（已建模） | ITX 现货板卧式底置 | **186 × 184 × 210 mm** | ≈7.2L | 与群晖 DS423+ 相当；FreeCAD/STL 即此版 |
| **量产愿景版**（§1-§4 图纸） | 定制紧凑板(≤150×140)侧置 | 179 × 162 × 217 mm | ≈6.3L | 待自研板落地 |

主板查证结论（2026-10-15）：市面 N150 NAS ODM 板（CWWK CW-M2 / M6 / 各 6×SATA 款）**全部为 170×170mm Mini-ITX**，无紧凑现货板 → 原型必须走卧式底置。

两档共用：托架（120×153×29.5 免工具）、背板、风道理念、前脸设计语言、CMF。

## 原型版打印说明

- 主壳倒置打印（开口朝下），186×210 底面在 220 床身内；建议 PETG，壁厚 3.0
- 托架 ×4：PLA+ 或 PETG，开口朝上打，支撑少
- 底板/背板支架/风扇架：PETG
- 全部 STL 已通过 FreeCAD 水密校验（isSolid=True）
- 手板预算对应 PURCHASE_LIST §八（代打印 ~¥90-140）

## 已完成

- [x] 尺寸物理核算 + HARDWARE_SPEC.md §1/§6.2 同步修订（2026-10-15）
- [x] ODM 主板尺寸查证（ITX 170×170）
- [x] FreeCAD 1:1 参数化模型 + 5 件 STL + STEP 装配体 + 水密校验
- [x] BOM（量产版 25 项）

## 下一步

1. 3D 打印手板 → 装 ITX 板实测装配公差与走线
2. 4 盘满载热验证（盘温 ≤45°C、噪音 ≤25dB@1m）
3. 背板打样（嘉立创 120×40 双面板，验证热插拔浪涌/LED 链路）
4. 托架公差调整：当前导轨单边间隙 0.2mm，FDM 手板试装后按实测放宽到 0.3-0.4mm
5. 正式渲染（可导入 STEP 到 Blender/Fusion360 出营销图）

> 渲染图为 AI 概念稿（家居氛围图中盘位数为示意），精确外观以 HTML 工程图与 CAD 模型为准。
