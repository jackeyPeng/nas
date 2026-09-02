#!/usr/bin/env python3
"""
i18n 语言包同步检查脚本
用法: python3 scripts/check-i18n.py [--fix]

检查内容:
1. zh-CN.json 与 en-US.json 的 key 是否一致
2. 是否有 key 在源语言存在但翻译缺失
3. 是否有 key 在翻译中存在但源语言已删除（多余 key）
4. 可选：自动在 en-US.json 中补充缺失的 key（标记为待翻译）

约定:
- zh-CN.json 是源语言（source of truth）
- 所有其他语言包应与 zh-CN.json 保持 key 结构一致
"""

import json
import sys
import os
from collections import OrderedDict

I18N_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'web', 'frontend', 'i18n')
SOURCE = 'zh-CN'
TARGETS = ['en-US', 'ja-JP', 'zh-TW']  # 扩展语言列表

def flatten_keys(obj, prefix=''):
    """递归展开所有 key 路径"""
    keys = {}
    for k, v in obj.items():
        full_key = f"{prefix}.{k}" if prefix else k
        if isinstance(v, dict):
            keys.update(flatten_keys(v, full_key))
        else:
            keys[full_key] = v
    return keys

def load_json(name):
    path = os.path.join(I18N_DIR, f'{name}.json')
    if not os.path.exists(path):
        return None
    with open(path, 'r') as f:
        return json.load(f)

def main():
    fix_mode = '--fix' in sys.argv

    source = load_json(SOURCE)
    if not source:
        print(f"❌ 源语言包 {SOURCE}.json 不存在: {I18N_DIR}")
        sys.exit(1)

    source_keys = flatten_keys(source)
    print(f"📖 {SOURCE}.json: {len(source_keys)} keys")

    all_ok = True
    existing_targets = []

    for target in TARGETS:
        target_data = load_json(target)
        if not target_data:
            continue
        existing_targets.append(target)

        target_keys = flatten_keys(target_data)
        print(f"   {target}.json: {len(target_keys)} keys")

        missing = set(source_keys.keys()) - set(target_keys.keys())
        extra = set(target_keys.keys()) - set(source_keys.keys())

        if missing:
            all_ok = False
            print(f"\n   ⚠️  {target} 缺少 {len(missing)} 个 key:")
            for k in sorted(missing):
                print(f"      - {k} = \"{source_keys[k]}\"")

            if fix_mode:
                # 补充缺失的 key 到目标语言包
                update_nested(target_data, source, missing)
                save_json(target, target_data)
                print(f"   ✅ 已自动补充 {len(missing)} 个 key 到 {target}.json（标记为 TODO）")

        if extra:
            all_ok = False
            print(f"\n   ⚠️  {target} 有 {len(extra)} 个多余 key（源语言中已删除）:")
            for k in sorted(extra):
                print(f"      - {k} = \"{target_keys[k]}\"")
            if not fix_mode:
                print(f"   提示: 使用 --fix 自动删除多余 key")

    if not existing_targets:
        print("\n⚠️  没有找到任何目标语言包")
        print(f"   请在 {I18N_DIR}/ 下创建 en-US.json 等文件")
        sys.exit(1)

    if all_ok:
        print(f"\n✅ 所有语言包 key 一致！")
    else:
        if not fix_mode:
            print(f"\n💡 使用 --fix 自动修复")
        sys.exit(1 if not fix_mode else 0)

def update_nested(target_data, source_data, missing_keys):
    """将缺失的 key 补充到目标语言包的嵌套结构中"""
    for key in missing_keys:
        parts = key.split('.')
        current_target = target_data
        current_source = source_data

        # 遍历到倒数第二层
        for part in parts[:-1]:
            if part not in current_target:
                current_target[part] = OrderedDict()
            current_target = current_target[part]
            current_source = current_source[part]

        # 在最后一层设置值
        source_val = current_source[parts[-1]]
        current_target[parts[-1]] = f"TODO: {source_val}"

def save_json(name, data):
    path = os.path.join(I18N_DIR, f'{name}.json')
    with open(path, 'w') as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
    print(f"   💾 已保存 {name}.json")

if __name__ == '__main__':
    main()