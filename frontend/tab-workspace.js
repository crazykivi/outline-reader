"use strict";

class TabWorkspace {
    constructor() {
        this.main = this.createPane();
        this.split = this.createPane();
        this.drag = { tabId: null, pane: null, token: 0 };
        this.revision = 0;
    }

    createPane() {
        return {
            tabs: [],
            pinned: [],
            activeId: null,
            histories: new Map(),
            scrollPositions: new Map(),
        };
    }

    getPane(name) {
        return name === "main" ? this.main : name === "split" ? this.split : null;
    }

    ownerOf(tabId) {
        const mainHas = this.main.tabs.includes(tabId);
        const splitHas = this.split.tabs.includes(tabId);
        if (mainHas === splitHas) return null;
        return mainHas ? "main" : "split";
    }

    beginDrag(tabId, pane) {
        if (this.ownerOf(tabId) !== pane) return false;
        this.drag = { tabId, pane, token: this.drag.token + 1 };
        return true;
    }

    consumeDrag(targetPane) {
        const session = this.drag;
        this.drag = { tabId: null, pane: null, token: session.token };
        if (!session.tabId || !session.pane || session.pane === targetPane) return null;
        if (this.ownerOf(session.tabId) !== session.pane) return null;
        return session;
    }

    endDrag(tabId, pane) {
        if (this.drag.tabId === tabId && this.drag.pane === pane) {
            this.drag = { tabId: null, pane: null, token: this.drag.token };
        }
    }

    normalize(getDocumentOID, isEmptyTab, createReplacement) {
        for (const paneName of ["main", "split"]) {
            const pane = this.getPane(paneName);
            pane.tabs = [...new Set(pane.tabs.filter((id) => typeof id === "string" && id))];
            pane.pinned = [...new Set(pane.pinned.filter((id) => pane.tabs.includes(id)))];
        }

        const seen = new Set(this.main.tabs);
        const remap = new Map();
        const uniqueSplit = [];
        for (const oldId of this.split.tabs) {
            if (!seen.has(oldId)) {
                seen.add(oldId);
                uniqueSplit.push(oldId);
                continue;
            }
            const documentOID = getDocumentOID(oldId);
            const nextId = createReplacement("split", oldId, documentOID, isEmptyTab(oldId));
            remap.set(oldId, nextId);
            if (!seen.has(nextId)) {
                seen.add(nextId);
                uniqueSplit.push(nextId);
            }
        }
        this.split.tabs = uniqueSplit;
        this.split.pinned = this.split.pinned.map((id) => remap.get(id) || id).filter((id) => this.split.tabs.includes(id));
        if (remap.has(this.split.activeId)) this.split.activeId = remap.get(this.split.activeId);

        for (const paneName of ["main", "split"]) {
            const pane = this.getPane(paneName);
            const pinned = new Set(pane.pinned);
            pane.tabs = [
                ...pane.tabs.filter((id) => pinned.has(id)),
                ...pane.tabs.filter((id) => !pinned.has(id)),
            ];
            if (!pane.tabs.includes(pane.activeId)) pane.activeId = pane.tabs[pane.tabs.length - 1] || null;
            for (const id of [...pane.histories.keys()]) if (!pane.tabs.includes(id)) pane.histories.delete(id);
            for (const id of [...pane.scrollPositions.keys()]) if (!pane.tabs.includes(id)) pane.scrollPositions.delete(id);
        }
        this.revision++;
        return this.assert();
    }

    move({ tabId, sourcePane, targetPane, targetTabId = null, after = false, isEmptyTab, createEmpty }) {
        if (!tabId || sourcePane === targetPane) return null;
        if (this.ownerOf(tabId) !== sourcePane) return null;
        const source = this.getPane(sourcePane);
        const target = this.getPane(targetPane);
        if (!source || !target) return null;

        const sourceIndex = source.tabs.indexOf(tabId);
        const wasActive = source.activeId === tabId;
        const wasPinned = source.pinned.includes(tabId);
        const history = source.histories.get(tabId);
        const scroll = source.scrollPositions.get(tabId);

        source.tabs.splice(sourceIndex, 1);
        source.pinned = source.pinned.filter((id) => id !== tabId);
        source.histories.delete(tabId);
        source.scrollPositions.delete(tabId);

        const placeholder = target.tabs.length === 1 && isEmptyTab(target.tabs[0]) && !target.pinned.includes(target.tabs[0])
            ? target.tabs[0]
            : null;
        if (placeholder) {
            target.tabs.splice(0, 1);
            target.histories.delete(placeholder);
            target.scrollPositions.delete(placeholder);
        }

        const targetIndex = targetTabId ? target.tabs.indexOf(targetTabId) : -1;
        const pinnedCount = target.tabs.filter((id) => target.pinned.includes(id)).length;
        const insertAt = targetIndex < 0
            ? (wasPinned ? pinnedCount : target.tabs.length)
            : targetIndex + (after ? 1 : 0);
        target.tabs.splice(insertAt, 0, tabId);
        if (wasPinned) target.pinned.push(tabId);
        if (history) target.histories.set(tabId, history);
        if (scroll !== undefined) target.scrollPositions.set(tabId, scroll);

        if (wasActive) {
            source.activeId = source.tabs[Math.min(sourceIndex, source.tabs.length - 1)] || null;
            if (!source.activeId) {
                source.activeId = createEmpty(sourcePane);
                source.tabs.push(source.activeId);
            }
        }
        target.activeId = tabId;
        this.revision++;
        this.assert();
        return { tabId, sourcePane, targetPane, sourceWasActive: wasActive, sourceActiveId: source.activeId, targetActiveId: tabId, wasPinned };
    }

    reorder(tabId, paneName, targetTabId, after) {
        const pane = this.getPane(paneName);
        if (!pane || this.ownerOf(tabId) !== paneName || !targetTabId || tabId === targetTabId) return false;
        if (pane.pinned.includes(tabId) !== pane.pinned.includes(targetTabId)) return false;
        const from = pane.tabs.indexOf(tabId);
        if (from < 0) return false;
        pane.tabs.splice(from, 1);
        const to = pane.tabs.indexOf(targetTabId);
        if (to < 0) return false;
        pane.tabs.splice(to + (after ? 1 : 0), 0, tabId);
        this.normalizeOrder(pane);
        this.revision++;
        this.assert();
        return true;
    }

    normalizeOrder(pane) {
        const pinned = new Set(pane.pinned);
        pane.tabs = [...pane.tabs.filter((id) => pinned.has(id)), ...pane.tabs.filter((id) => !pinned.has(id))];
    }

    assert() {
        const all = [...this.main.tabs, ...this.split.tabs];
        if (new Set(all).size !== all.length) throw new Error("TabWorkspace invariant: tab ID belongs to more than one pane");
        for (const pane of [this.main, this.split]) {
            if (pane.pinned.some((id) => !pane.tabs.includes(id))) throw new Error("TabWorkspace invariant: pinned tab is not open");
            if (pane.activeId && !pane.tabs.includes(pane.activeId)) throw new Error("TabWorkspace invariant: active tab is not open");
        }
        return true;
    }
}

if (typeof module !== "undefined" && module.exports) module.exports = TabWorkspace;
