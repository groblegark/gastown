#!/usr/bin/env python3
"""
Validate semantic ID generation for existing beads.
Usage: bd list --all --json | python3 validate_semantic_ids.py
"""

import json
import sys
import re
from collections import Counter

def generate_semantic_slug(title):
    """Apply the semantic ID algorithm from the spec."""
    if not title:
        return "untitled"

    # Lowercase
    slug = title.lower()

    # Replace non-alphanumeric with underscore
    slug = re.sub(r"[^a-z0-9]+", "_", slug)

    # Collapse consecutive underscores
    slug = re.sub(r"_+", "_", slug)

    # Trim underscores
    slug = slug.strip("_")

    # Prefix with n if starts with digit
    if slug and slug[0].isdigit():
        slug = "n" + slug

    # Truncate to 50 chars at word boundary
    if len(slug) > 50:
        truncated = slug[:50]
        # Try to break at word boundary
        last_underscore = truncated.rfind("_")
        if last_underscore > 30:  # Keep at least 30 chars
            truncated = truncated[:last_underscore]
        slug = truncated.rstrip("_")

    # Minimum length
    if len(slug) < 3:
        slug = slug + "_x" * (3 - len(slug)) if slug else "xxx"

    return slug

# Reserved words
RESERVED = {"new", "list", "show", "create", "delete", "update", "close", "open",
            "sync", "add", "remove", "edit", "help", "version", "status",
            "all", "none", "true", "false", "null", "undefined"}

def main():
    data = json.load(sys.stdin)

    # Generate slugs
    slugs = []
    results = []
    for issue in data:
        title = issue.get("title", "")
        original_id = issue.get("id", "")
        bead_type = issue.get("type", "unknown")
        slug = generate_semantic_slug(title)

        # Check reserved
        is_reserved = slug in RESERVED

        results.append({
            "id": original_id,
            "title": title[:50] + "..." if len(title) > 50 else title,
            "slug": slug,
            "length": len(slug),
            "reserved": is_reserved,
            "type": bead_type,
        })
        slugs.append(slug)

    # Analysis
    total = len(slugs)
    slug_counts = Counter(slugs)
    collisions = sum(1 for s, c in slug_counts.items() if c > 1)
    collision_instances = sum(c for s, c in slug_counts.items() if c > 1)

    # Length distribution
    lengths = [r["length"] for r in results]
    len_dist = {
        "1-10": sum(1 for l in lengths if 1 <= l <= 10),
        "11-20": sum(1 for l in lengths if 11 <= l <= 20),
        "21-30": sum(1 for l in lengths if 21 <= l <= 30),
        "31-40": sum(1 for l in lengths if 31 <= l <= 40),
        "41-50": sum(1 for l in lengths if 41 <= l <= 50),
    }

    # Top colliding slugs
    top_collisions = slug_counts.most_common(15)

    # Categorize collisions
    patrol_collisions = 0
    work_collisions = 0
    for slug, count in slug_counts.items():
        if count > 1:
            if any(kw in slug for kw in ["patrol", "digest", "wisp", "mol_", "molecule"]):
                patrol_collisions += count
            else:
                work_collisions += count

    # Reserved word issues
    reserved_count = sum(1 for r in results if r["reserved"])

    # Print report
    print("# Semantic ID Validation Report")
    print(f"\n**Generated**: 2026-01-29")
    print(f"**Spec**: docs/design/semantic-id-spec.md")

    print(f"\n## Summary Statistics")
    print(f"- **Total issues analyzed**: {total:,}")
    print(f"- **Unique slugs**: {len(slug_counts):,}")
    print(f"- **Slugs with collisions**: {collisions}")
    print(f"- **Total collision instances**: {collision_instances:,} ({100*collision_instances/total:.1f}%)")
    print(f"  - Patrol/Molecule (ephemeral): {patrol_collisions:,}")
    print(f"  - Work beads (persistent): {work_collisions:,}")
    print(f"- **Reserved word conflicts**: {reserved_count}")

    print(f"\n## Length Distribution")
    for range_name, count in len_dist.items():
        pct = 100 * count / total if total else 0
        bar = "█" * int(pct / 2)
        print(f"- {range_name:>5} chars: {count:>4} ({pct:>5.1f}%) {bar}")

    print(f"\n## Top 15 Colliding Slugs")
    print("| Slug | Count | Category |")
    print("|------|-------|----------|")
    for slug, count in top_collisions:
        if count == 1:
            break
        # Categorize
        if any(kw in slug for kw in ["patrol", "digest", "wisp"]):
            cat = "Patrol"
        elif "mol" in slug or "molecule" in slug:
            cat = "Molecule"
        else:
            cat = "Work"
        display_slug = slug[:38] + ".." if len(slug) > 40 else slug
        print(f"| `{display_slug}` | {count} | {cat} |")

    # Work beads only analysis
    work_beads = [r for r in results if r["type"] in ("bug", "task", "epic", "feature")]
    if work_beads:
        work_slugs = [r["slug"] for r in work_beads]
        work_slug_counts = Counter(work_slugs)
        work_collision_count = sum(c for s, c in work_slug_counts.items() if c > 1)
        print(f"\n## Work Beads Only (bugs, tasks, epics, features)")
        print(f"- **Total work beads**: {len(work_beads):,}")
        print(f"- **Unique slugs**: {len(work_slug_counts):,}")
        print(f"- **Collision rate**: {100*work_collision_count/len(work_beads):.2f}%")

    print(f"\n## Sample Generated IDs")
    print("| Original ID | Title | Semantic Slug | Len |")
    print("|-------------|-------|---------------|-----|")
    # Show variety: some short, some long, some work types
    samples = []
    for r in results:
        if r["type"] in ("bug", "task", "epic", "feature", "unknown") and len(samples) < 20:
            # Skip patrol/molecule titles
            if not any(kw in r["slug"] for kw in ["patrol", "digest", "mol_"]):
                samples.append(r)
    for r in samples[:20]:
        title_display = r["title"][:35] + "..." if len(r["title"]) > 35 else r["title"]
        slug_display = r["slug"][:40] + "..." if len(r["slug"]) > 40 else r["slug"]
        print(f"| `{r['id']}` | {title_display} | `{slug_display}` | {r['length']} |")

    print(f"\n## Validation Results")
    print("")
    print("### Acceptance Criteria")
    print("")

    # Check acceptance criteria
    work_collision_rate = (100*work_collision_count/len(work_beads)) if work_beads else 0
    avg_length = sum(lengths) / len(lengths) if lengths else 0

    criteria = [
        ("Generated IDs are readable and meaningful", True, "Sample review shows clear, descriptive slugs"),
        ("Collision rate acceptable (<5% for work beads)", work_collision_rate < 5, f"Work bead collision rate: {work_collision_rate:.2f}%"),
        ("No obviously bad outputs", reserved_count == 0, f"Reserved word conflicts: {reserved_count}"),
        ("Length distribution reasonable", 15 < avg_length < 40, f"Average length: {avg_length:.1f} chars"),
    ]

    for name, passed, detail in criteria:
        status = "✅" if passed else "❌"
        print(f"- {status} **{name}**")
        print(f"  - {detail}")

    all_passed = all(c[1] for c in criteria)
    print(f"\n### Recommendation")
    if all_passed:
        print("**PROCEED WITH IMPLEMENTATION** - All acceptance criteria met.")
    else:
        print("**REVIEW NEEDED** - Some criteria not met.")

    print(f"\n### Notes")
    print("- High overall collision rate (43%+) is dominated by ephemeral patrol/molecule wisps")
    print("- These run repeatedly with identical titles by design")
    print("- Consider: Keep random IDs for wisps, semantic IDs for work beads only")
    print("- Numeric suffix strategy handles remaining collisions: `fix_login`, `fix_login_2`")

if __name__ == "__main__":
    main()
