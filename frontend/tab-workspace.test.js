"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const TabWorkspace = require("./tab-workspace.js");

function workspace() {
    return new TabWorkspace();
}

function isEmpty(id) {
    return id.startsWith("empty:");
}

function documentOID(id) {
    if (isEmpty(id)) return null;
    const separator = id.indexOf(":");
    return separator >= 0 ? id.slice(separator + 1) : id;
}

function replacement(pane, id, oid, empty) {
    return empty ? "empty:split-recovered" : "duplicate:split:" + oid;
}

test("cross-pane move transfers one ID and replaces the empty target tab", () => {
    const state = workspace();
    state.main.tabs = ["doc:a", "doc:b"];
    state.main.activeId = "doc:a";
    state.main.histories.set("doc:a", { history: ["a", "b"], historyIndex: 1 });
    state.main.scrollPositions.set("doc:a", 125);
    state.split.tabs = ["empty:split-start"];
    state.split.activeId = "empty:split-start";

    const moved = state.move({
        tabId: "doc:a",
        sourcePane: "main",
        targetPane: "split",
        isEmptyTab: isEmpty,
        createEmpty: (pane) => "empty:" + pane + "-fallback",
    });

    assert.equal(moved.tabId, "doc:a");
    assert.deepEqual(state.main.tabs, ["doc:b"]);
    assert.deepEqual(state.split.tabs, ["doc:a"]);
    assert.equal(state.main.activeId, "doc:b");
    assert.equal(state.split.activeId, "doc:a");
    assert.deepEqual(state.split.histories.get("doc:a"), { history: ["a", "b"], historyIndex: 1 });
    assert.equal(state.split.scrollPositions.get("doc:a"), 125);
    assert.equal(state.assert(), true);
});

test("a stale second transfer cannot duplicate a tab or leave it in its source", () => {
    const state = workspace();
    state.main.tabs = ["doc:a"];
    state.main.activeId = "doc:a";
    state.split.tabs = ["empty:split-start"];
    state.split.activeId = "empty:split-start";

    const first = state.move({
        tabId: "doc:a", sourcePane: "main", targetPane: "split",
        isEmptyTab: isEmpty, createEmpty: () => "empty:main-fallback",
    });
    const second = state.move({
        tabId: "doc:a", sourcePane: "main", targetPane: "split",
        isEmptyTab: isEmpty, createEmpty: () => "empty:main-fallback-2",
    });

    assert.ok(first);
    assert.equal(second, null);
    assert.deepEqual(state.main.tabs, ["empty:main-fallback"]);
    assert.deepEqual(state.split.tabs, ["doc:a"]);
    assert.equal(state.assert(), true);
});

test("normalization repairs persisted IDs shared by both panes", () => {
    const state = workspace();
    state.main.tabs = ["doc:a"];
    state.main.activeId = "doc:a";
    state.split.tabs = ["doc:a", "doc:b"];
    state.split.pinned = ["doc:a"];
    state.split.activeId = "doc:a";

    state.normalize(documentOID, isEmpty, replacement);

    assert.deepEqual(state.main.tabs, ["doc:a"]);
    assert.deepEqual(state.split.tabs, ["duplicate:split:a", "doc:b"]);
    assert.deepEqual(state.split.pinned, ["duplicate:split:a"]);
    assert.equal(state.split.activeId, "duplicate:split:a");
    assert.equal(state.assert(), true);
});

test("reordering stays inside its pane and preserves pinned grouping", () => {
    const state = workspace();
    state.main.tabs = ["pin:a", "doc:b", "doc:c"];
    state.main.pinned = ["pin:a"];
    state.main.activeId = "doc:b";

    assert.equal(state.reorder("doc:c", "main", "doc:b", false), true);
    assert.deepEqual(state.main.tabs, ["pin:a", "doc:c", "doc:b"]);
    assert.equal(state.ownerOf("doc:c"), "main");
    assert.equal(state.assert(), true);
});

test("a drag session can be consumed only once", () => {
    const state = workspace();
    state.main.tabs = ["doc:a"];
    state.main.activeId = "doc:a";

    assert.equal(state.beginDrag("doc:a", "main"), true);
    assert.deepEqual(state.consumeDrag("split"), { tabId: "doc:a", pane: "main", token: 1 });
    assert.equal(state.consumeDrag("split"), null);
});

test("opening the same document in the other pane allocates a different tab ID", () => {
    const state = workspace();
    state.main.tabs = ["doc:a"];
    state.main.activeId = "doc:a";
    state.split.tabs = ["empty:split"];
    state.split.activeId = "empty:split";

    const selected = state.selectDocumentTab({
        paneName: "split",
        documentOID: "doc:a",
        source: "tree",
        getDocumentOID: documentOID,
        isEmptyTab: isEmpty,
        createDuplicate: (oid) => "duplicate:split:" + oid,
    });

    assert.equal(selected.tabId, "duplicate:split:doc:a");
    assert.deepEqual(state.main.tabs, ["doc:a"]);
    assert.deepEqual(state.split.tabs, ["duplicate:split:doc:a"]);
    assert.equal(state.ownerOf("doc:a"), "main");
    assert.equal(state.ownerOf("duplicate:split:doc:a"), "split");
    assert.equal(state.assert(), true);
});

test("closing the split view merges tabs and their metadata exactly once", () => {
    const state = workspace();
    state.main.tabs = ["main:a"];
    state.main.activeId = "main:a";
    state.split.tabs = ["split:b", "split:c"];
    state.split.pinned = ["split:b"];
    state.split.activeId = "split:c";
    state.split.histories.set("split:c", { history: ["c", "d"], historyIndex: 1 });
    state.split.scrollPositions.set("split:c", 88);

    state.mergePane("split", "main", (id) => "copy:" + id);

    assert.deepEqual(state.main.tabs, ["split:b", "main:a", "split:c"]);
    assert.deepEqual(state.main.pinned, ["split:b"]);
    assert.equal(state.main.activeId, "split:c");
    assert.deepEqual(state.main.histories.get("split:c"), { history: ["c", "d"], historyIndex: 1 });
    assert.equal(state.main.scrollPositions.get("split:c"), 88);
    assert.deepEqual(state.split.tabs, []);
    assert.equal(state.split.activeId, null);
    assert.equal(state.assert(), true);
});

test("closing unpinned tabs removes their metadata and selects a pinned tab", () => {
    const state = workspace();
    state.main.tabs = ["pin:a", "doc:b"];
    state.main.pinned = ["pin:a"];
    state.main.activeId = "doc:b";
    state.main.histories.set("doc:b", { history: ["b"], historyIndex: 0 });
    state.main.scrollPositions.set("doc:b", 42);

    const result = state.closeUnpinned("main");

    assert.equal(result.activeWasClosed, true);
    assert.deepEqual(state.main.tabs, ["pin:a"]);
    assert.equal(state.main.activeId, "pin:a");
    assert.equal(state.main.histories.has("doc:b"), false);
    assert.equal(state.main.scrollPositions.has("doc:b"), false);
    assert.equal(state.assert(), true);
});
