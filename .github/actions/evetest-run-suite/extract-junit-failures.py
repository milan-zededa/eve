#!/usr/bin/env python3
# Copyright (c) 2026, Zededa, Inc.
# SPDX-License-Identifier: Apache-2.0
#
# extract-junit-failures.py <junit.xml>
#
# Prints one markdown bullet per failing testcase in the given JUnit XML
# file (as produced by "Convert results to JUnit XML" in
# evetest-run-suite/action.yml), or nothing at all if there are none -- no
# heading, that's left to the caller. Used to build claude-analysis.md's
# structured failure list for the post-mortem analysis path.

import sys
import xml.etree.ElementTree as ET


def extract_failure_text(node):
    # gotestsum always sets message="Failed" verbatim -- the real failure
    # text is in the element's captured-stdout body, anchored on evetest's
    # own "TEST FAILURE:" convention (or Go's "panic:") and cut off before
    # the STACKTRACE dump that follows it.
    if node is None or not node.text:
        return ""
    lines = node.text.splitlines()
    marker_idx = next((i for i, l in enumerate(lines) if "TEST FAILURE:" in l or "panic:" in l), None)
    if marker_idx is None:
        return ""
    collected = []
    for line in lines[marker_idx:marker_idx + 6]:
        if "STACKTRACE:" in line:
            break
        stripped = line.strip()
        if stripped:
            collected.append(stripped)
    return " ".join(collected)[:300] if collected else ""


def main():
    tree = ET.parse(sys.argv[1])
    failures = []
    for testcase in tree.getroot().iter("testcase"):
        name = testcase.get("name", "?")
        # gotestsum also emits a synthetic roll-up testcase named after the
        # suite itself (the parent Go test function's own aggregate
        # result) -- not a real subtest, skip it.
        if "/" not in name:
            continue
        fail_node = testcase.find("failure")
        if fail_node is None:
            fail_node = testcase.find("error")
        if fail_node is None:
            continue
        subtest = name.split("/", 1)[1]
        failures.append((subtest, extract_failure_text(fail_node)))

    for subtest, text in failures:
        print(f"- `{subtest}`: `{text}`" if text else f"- `{subtest}`")


if __name__ == "__main__":
    main()
