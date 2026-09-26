"""Checks or finishes a sync of the app's string catalog. Run by strings.sh.

usage: strings_catalog.py sync|check <Localizable.xcstrings> <app-stringsdata-list> <kit-stringsdata-dir>

sync:  adds EmberKit keys the catalog lacks as "manual" entries (with the
       source comment), then rewrites the file in the repo's JSON format.
check: exits 1 when an extracted key is missing from the catalog, or a
       format string (or an inflected one) has no translator comment.
       Warns (without failing) about catalog keys no code uses any more.
"""

import glob
import json
import os
import re
import sys

KIT_COMMENT = "From EmberKit (not extracted automatically; kept in step by StringCatalogTests)."
# A printf specifier (%@, %lld, %1$@…) or automatic grammar agreement.
NEEDS_COMMENT = re.compile(r"%(\d+\$)?(@|l{0,2}[dfu]|[dfu])|\^\[")


def load_keys(paths):
    """Key -> first non-empty source comment, over the given .stringsdata files."""
    keys = {}
    for path in paths:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
        for table, items in data.get("tables", {}).items():
            if table != "Localizable":
                continue
            for item in items:
                comment = item.get("comment") or ""
                if not keys.get(item["key"]):
                    keys[item["key"]] = comment
    return keys


def write(catalog_path, catalog):
    with open(catalog_path, "w", encoding="utf-8") as f:
        f.write(json.dumps(catalog, indent=2, separators=(",", " : "), sort_keys=True, ensure_ascii=False) + "\n")


def main():
    mode, catalog_path, app_list, kit_dir = sys.argv[1:5]
    with open(app_list, encoding="utf-8") as f:
        app = load_keys([line.strip() for line in f if line.strip()])
    kit = load_keys(sorted(glob.glob(os.path.join(kit_dir, "*.stringsdata"))))
    with open(catalog_path, encoding="utf-8") as f:
        catalog = json.load(f)
    strings = catalog.setdefault("strings", {})

    if mode == "sync":
        for key, comment in kit.items():
            if key not in strings:
                strings[key] = {"comment": comment or KIT_COMMENT, "extractionState": "manual"}
        write(catalog_path, catalog)
        print(f"strings_catalog: {len(strings)} keys ({len(app)} app, {len(kit)} EmberKit)", file=sys.stderr)

    problems = []
    for key in sorted(set(app) - set(strings)):
        problems.append(f"missing from the catalog (app): {key!r}")
    for key in sorted(set(kit) - set(strings)):
        problems.append(f"missing from the catalog (EmberKit): {key!r}")
    for key in sorted(set(app) | set(kit)):
        entry = strings.get(key)
        if entry is None or entry.get("shouldTranslate") is False:
            continue
        if NEEDS_COMMENT.search(key) and not (entry.get("comment") or app.get(key) or kit.get(key)):
            problems.append(f"format string without a translator comment: {key!r}")
    # Reverse drift: keys whose code is gone. A warning, not a failure: a
    # translator may still want the old text, and sync never deletes.
    extracted = set(app) | set(kit)
    stale = sorted(k for k, e in strings.items() if k not in extracted and e.get("extractionState") != "stale")
    marked = sorted(k for k, e in strings.items() if e.get("extractionState") == "stale")
    for key in stale + marked:
        print(f"warning: not used in code any more: {key!r}", file=sys.stderr)

    if problems:
        print("\n".join(problems), file=sys.stderr)
        print("Run scripts/strings.sh sync, then add comments to the new entries.", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
