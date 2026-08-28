#!/usr/bin/env python3
"""Rewrite swag's output into a spec openapi-python-client can generate from.

swag v2.0.0-rc5 emits request bodies as `oneOf: [{type: object}, {$ref}]`, which
generates as `Union[Any, Model]`, and declares bearer auth as an apiKey header.
Both are fixed here rather than in the annotations because they are the
generator's doing, not ours -- and docs/swagger.json is rewritten on every build.
"""

import json
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent
SRC = ROOT.parent / "docs" / "swagger.json"
DST = ROOT / "openapi.json"

GO_PKG_PREFIX = "main."
REF_PREFIX = "#/components/schemas/"

def strip_ref(ref: str) -> str:
    if ref.startswith(REF_PREFIX):
        name = ref[len(REF_PREFIX) :]
        return REF_PREFIX + name.removeprefix(GO_PKG_PREFIX)
    return ref

def collapsed_ref(node: dict[str, Any]) -> str | None:
    """The $ref of a `oneOf` that is one real schema plus swag's bare objects."""
    members = node.get("oneOf")
    if not isinstance(members, list):
        return None
    refs = [m for m in members if isinstance(m, dict) and "$ref" in m]
    bare = [m for m in members if isinstance(m, dict) and m.get("type") == "object" and len(m) == 1]
    if len(refs) == 1 and len(refs) + len(bare) == len(members):
        return refs[0]["$ref"]
    return None

def walk(node: Any) -> Any:
    if isinstance(node, list):
        return [walk(n) for n in node]
    if not isinstance(node, dict):
        return node

    ref = collapsed_ref(node)
    if ref is not None:
        rest = {k: walk(v) for k, v in node.items() if k != "oneOf"}
        return {**rest, "$ref": strip_ref(ref)}

    return {k: (strip_ref(v) if k == "$ref" and isinstance(v, str) else walk(v)) for k, v in node.items()}

def main() -> int:
    if not SRC.exists():
        print(f"no spec at {SRC}; run the control server once so swag regenerates it", file=sys.stderr)
        return 1

    spec: dict[str, Any] = walk(json.loads(SRC.read_text()))

    schemas = spec.get("components", {}).get("schemas", {})
    spec["components"]["schemas"] = {k.removeprefix(GO_PKG_PREFIX): v for k, v in schemas.items()}

    schemes = spec.get("components", {}).get("securitySchemes", {})
    if "BearerAuth" in schemes:
        schemes["BearerAuth"] = {"type": "http", "scheme": "bearer"}

    if not spec.get("externalDocs", {}).get("url"):
        spec.pop("externalDocs", None)

    DST.write_text(json.dumps(spec, indent=2, sort_keys=True) + "\n")
    print(f"wrote {DST.relative_to(ROOT.parent)}")
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
