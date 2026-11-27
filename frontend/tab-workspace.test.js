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
