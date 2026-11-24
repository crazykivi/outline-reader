"use strict";

const KnowledgeMap = (() => {
    const SVG_NS = "http://www.w3.org/2000/svg";
    const NODE_WIDTH = 232;
    const NODE_HEIGHT = 58;
    const LEVEL_GAP = 286;
    const ROW_GAP = 92;
    const COLLECTION_GAP = 110;

    let getCollections = () => [];
    let getCurrentOID = () => null;
    let openDocument = async () => {};
    let selectedOID = null;
    let expanded = new Set();
    let collapsedCollections = new Set();
    let searchQuery = "";
    let transform = { x: 48, y: 48, scale: 1 };
    let bounds = { width: 1000, height: 700 };
    let nodesByKey = new Map();
    let renderScheduled = false;
    let resizeObserver = null;
    let initialized = false;
    let drag = null;

    const $ = (id) => document.getElementById(id);

    function init(options) {
        if (initialized) return;
        initialized = true;
        getCollections = options.getCollections;
        getCurrentOID = options.getCurrentOID;
        openDocument = options.openDocument;

        $("knowledgeMapBack").addEventListener("click", close);
        $("knowledgeMapFit").addEventListener("click", fit);
        $("knowledgeMapCollapse").addEventListener("click", collapseAll);
        $("knowledgeMapOpen").addEventListener("click", openSelected);
        $("knowledgeMapSearch").addEventListener("input", (event) => {
            searchQuery = event.target.value.trim().toLocaleLowerCase();
            render();
            if (searchQuery) focusFirstMatch();
        });
        $("knowledgeMapSearch").addEventListener("keydown", (event) => {
            if (event.key === "Enter") {
                event.preventDefault();
                focusFirstMatch();
            }
            if (event.key === "Escape") {
                event.stopPropagation();
                event.target.value = "";
                searchQuery = "";
                render();
            }
        });

        const canvas = $("knowledgeMapCanvas");
        canvas.addEventListener("wheel", onWheel, { passive: false });
        canvas.addEventListener("pointerdown", onPointerDown);
        canvas.addEventListener("pointermove", onPointerMove);
        canvas.addEventListener("pointerup", endPan);
        canvas.addEventListener("pointercancel", endPan);
        canvas.addEventListener("dblclick", (event) => {
            const node = event.target.closest("[data-oid]");
            if (node && node.dataset.oid) void openDocument(node.dataset.oid);
        });
        document.addEventListener("keydown", (event) => {
            if (event.key === "Escape" && $("knowledgeMap").classList.contains("is-open")) close();
        });
        resizeObserver = new ResizeObserver(scheduleRender);
        resizeObserver.observe(canvas);
    }

    function open() {
        const map = $("knowledgeMap");
        map.classList.add("is-open");
        map.setAttribute("aria-hidden", "false");
        selectedOID = getCurrentOID() || selectedOID;
        if (selectedOID) expandPath(selectedOID);
        $("knowledgeMapSearch").value = "";
        searchQuery = "";
        render();
        requestAnimationFrame(fit);
        $("knowledgeMapCanvas").focus({ preventScroll: true });
    }

    function close() {
        const map = $("knowledgeMap");
        map.classList.remove("is-open");
        map.setAttribute("aria-hidden", "true");
    }

    function collapseAll() {
        expanded.clear();
        render();
        fit();
    }

    function scheduleRender() {
        if (renderScheduled) return;
        renderScheduled = true;
        requestAnimationFrame(() => {
            renderScheduled = false;
            render();
        });
    }

    function svgElement(name, attributes = {}, text = "") {
        const element = document.createElementNS(SVG_NS, name);
        Object.entries(attributes).forEach(([key, value]) => element.setAttribute(key, String(value)));
        if (text) element.textContent = text;
        return element;
    }

    function leafCount(node) {
        if (!node.children || !node.children.length || !expanded.has(node.oid)) return 1;
        return node.children.reduce((sum, child) => sum + leafCount(child), 0);
    }

    function render() {
        const collections = getCollections() || [];
        const viewport = $("knowledgeMapViewport");
        const empty = $("knowledgeMapEmpty");
        const meta = $("knowledgeMapMeta");
        viewport.replaceChildren();
        nodesByKey = new Map();

        const documentCount = countDocuments(collections);
        meta.textContent = collections.length + " коллекций · " + documentCount + " документов";
        empty.hidden = collections.length > 0;
        $("knowledgeMapCollapse").disabled = expanded.size === 0;
        $("knowledgeMapOpen").disabled = !selectedOID || !nodesByKey.has("doc:" + selectedOID);

        if (!collections.length) {
            bounds = { width: 900, height: 600 };
            applyTransform();
            updateInspector();
            return;
        }

        const edges = [];
        const nodes = [];
        let cursorY = 50;

        collections.forEach((collection) => {
            const roots = collection.nodes || [];
            const isCollapsed = collapsedCollections.has(collection.id);
            const rows = isCollapsed ? 1 : roots.reduce((sum, node) => sum + leafCount(node), 0);
            const collectionHeight = Math.max(1, rows) * ROW_GAP;
            const centerY = cursorY + collectionHeight / 2;
            const collectionNode = {
                key: "collection:" + collection.id,
                type: "collection",
                id: collection.id,
                title: collection.name,
                subtitle: roots.length + (roots.length === 1 ? " документ верхнего уровня" : " документов верхнего уровня"),
                x: 38,
                y: centerY - NODE_HEIGHT / 2,
                depth: 0,
                childrenCount: roots.length,
                isCollapsed,
            };
            nodes.push(collectionNode);

            let row = 0;
            const placeDocument = (doc, parent, depth) => {
                const count = leafCount(doc);
                const y = cursorY + (row + count / 2) * ROW_GAP - NODE_HEIGHT / 2;
                const node = {
                    key: "doc:" + doc.oid,
                    type: "document",
                    id: doc.oid,
                    title: doc.title || "Без названия",
                    subtitle: depth === 1 ? collection.name : "Вложенный документ",
                    x: 38 + depth * LEVEL_GAP,
                    y,
                    depth,
                    childrenCount: (doc.children || []).length,
                };
                nodes.push(node);
                edges.push({ parent, child: node });
                if (doc.children && doc.children.length && expanded.has(doc.oid)) {
                    doc.children.forEach((child) => placeDocument(child, node, depth + 1));
                } else {
                    row += count;
                }
            };
            if (!isCollapsed) roots.forEach((doc) => placeDocument(doc, collectionNode, 1));
            cursorY += collectionHeight + COLLECTION_GAP;
        });

        const maxX = nodes.reduce((max, node) => Math.max(max, node.x + NODE_WIDTH), 0);
        bounds = { width: maxX + 90, height: Math.max(cursorY + 20, 500) };

        edges.forEach(({ parent, child }) => {
            const x1 = parent.x + NODE_WIDTH;
            const y1 = parent.y + NODE_HEIGHT / 2;
            const x2 = child.x;
            const y2 = child.y + NODE_HEIGHT / 2;
            const middle = x1 + Math.max(32, (x2 - x1) * .48);
            viewport.appendChild(svgElement("path", {
                class: "knowledge-map-edge",
                d: "M " + x1 + " " + y1 + " C " + middle + " " + y1 + ", " + middle + " " + y2 + ", " + x2 + " " + y2,
            }));
        });

        nodes.forEach((node) => {
            nodesByKey.set(node.key, node);
            viewport.appendChild(renderNode(node));
        });

        $("knowledgeMapOpen").disabled = !selectedOID || !nodesByKey.has("doc:" + selectedOID);
        updateInspector();
        applyTransform();
    }

    function countDocuments(collections) {
        let total = 0;
        const walk = (nodes) => (nodes || []).forEach((node) => {
            total++;
            walk(node.children);
        });
        collections.forEach((collection) => walk(collection.nodes));
        return total;
    }

    function renderNode(node) {
        const current = node.type === "document" && node.id === getCurrentOID();
        const selected = node.type === "document" && node.id === selectedOID;
        const matches = searchQuery && node.title.toLocaleLowerCase().includes(searchQuery);
        const group = svgElement("g", {
            class: "knowledge-map-node " + node.type + (current ? " current" : "") +
                (selected ? " selected" : "") + (matches ? " search-match" : ""),
            transform: "translate(" + node.x + " " + node.y + ")",
            tabindex: node.type === "document" ? "0" : "-1",
            role: node.type === "document" ? "button" : "group",
            "aria-label": node.title,
            "data-oid": node.type === "document" ? node.id : "",
        });
        group.appendChild(svgElement("rect", { class: "node-card", x: 0, y: 0, width: NODE_WIDTH, height: NODE_HEIGHT, rx: 10 }));
        group.appendChild(svgElement("rect", { class: "node-marker", x: 12, y: 14, width: 4, height: 30, rx: 2 }));
        group.appendChild(svgElement("text", { class: "node-title", x: 26, y: 25 }, truncate(node.title, 29)));
        group.appendChild(svgElement("text", { class: "node-subtitle", x: 26, y: 43 }, truncate(node.subtitle, 34)));

        if (node.childrenCount > 0) {
            const isExpanded = node.type === "collection" ? !node.isCollapsed : expanded.has(node.id);
            const toggleAttribute = node.type === "collection" ? "data-toggle-collection" : "data-toggle";
            group.appendChild(svgElement("circle", {
                class: "node-toggle", cx: NODE_WIDTH - 16, cy: NODE_HEIGHT / 2, r: 11,
                [toggleAttribute]: node.id,
            }));
            group.appendChild(svgElement("text", {
                class: "node-toggle-text", x: NODE_WIDTH - 16, y: NODE_HEIGHT / 2 + .5,
                [toggleAttribute]: node.id,
            }, isExpanded ? "−" : "+"));
            group.appendChild(svgElement("text", {
                class: "node-subtitle", x: NODE_WIDTH - 34, y: NODE_HEIGHT / 2 + 3, "text-anchor": "end",
            }, String(node.childrenCount)));
        }

        group.addEventListener("click", (event) => {
            const collectionToggle = event.target.closest("[data-toggle-collection]");
            if (collectionToggle) {
                const id = collectionToggle.getAttribute("data-toggle-collection");
                if (collapsedCollections.has(id)) collapsedCollections.delete(id);
                else collapsedCollections.add(id);
                render();
                return;
            }
            const toggle = event.target.closest("[data-toggle]");
            if (toggle) {
                const oid = toggle.getAttribute("data-toggle");
                if (expanded.has(oid)) expanded.delete(oid); else expanded.add(oid);
                render();
                return;
            }
            if (node.type === "document") {
                selectedOID = node.id;
                render();
            }
        });
        group.addEventListener("keydown", (event) => {
            if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                if (node.type === "document") {
                    selectedOID = node.id;
                    render();
                }
            }
        });
        return group;
    }

    function truncate(value, max) {
        const text = String(value || "");
        return text.length > max ? text.slice(0, max - 1) + "…" : text;
    }

    function updateInspector() {
        const node = selectedOID ? nodesByKey.get("doc:" + selectedOID) : null;
        const selection = $("knowledgeMapSelection");
        const button = $("knowledgeMapOpen");
        selection.replaceChildren();
        if (!node) {
            const strong = document.createElement("strong");
            strong.textContent = "Выберите документ на карте";
            const description = document.createElement("p");
            description.textContent = "Нажмите на узел, чтобы выбрать документ. Двойной щелчок откроет его.";
            selection.append(strong, description);
            button.disabled = true;
            return;
        }
        const icon = document.createElement("span");
        icon.className = "knowledge-map-selection-icon";
        icon.textContent = "▤";
        const title = document.createElement("strong");
        title.textContent = node.title;
        const description = document.createElement("p");
        description.textContent = node.subtitle;
        selection.append(icon, title, description);
        button.disabled = false;
    }

    async function openSelected() {
        if (!selectedOID || !nodesByKey.has("doc:" + selectedOID)) return;
        close();
        await openDocument(selectedOID);
    }

    function expandPath(oid) {
        const visit = (nodes, ancestors) => {
            for (const node of nodes || []) {
                if (node.oid === oid) {
                    ancestors.forEach((id) => expanded.add(id));
                    return true;
                }
                if (visit(node.children, [...ancestors, node.oid])) return true;
            }
            return false;
        };
        (getCollections() || []).some((collection) => {
            const found = visit(collection.nodes, []);
            if (found) collapsedCollections.delete(collection.id);
            return found;
        });
    }

    function findDocument(query) {
        let match = null;
        const visit = (nodes) => {
            for (const node of nodes || []) {
                if (node.title.toLocaleLowerCase().includes(query)) {
                    match = node;
                    return true;
                }
                if (visit(node.children)) return true;
            }
            return false;
        };
        for (const collection of getCollections() || []) {
            if (visit(collection.nodes)) break;
        }
        return match;
    }

    function focusFirstMatch() {
        if (!searchQuery) return;
        const match = findDocument(searchQuery);
        if (!match) return;
        selectedOID = match.oid;
        expandPath(match.oid);
        render();
        const positioned = nodesByKey.get("doc:" + match.oid);
        if (!positioned) return;
        const canvas = $("knowledgeMapCanvas");
        transform.x = canvas.clientWidth / 2 - (positioned.x + NODE_WIDTH / 2) * transform.scale;
        transform.y = canvas.clientHeight / 2 - (positioned.y + NODE_HEIGHT / 2) * transform.scale;
        applyTransform();
    }

    function fit() {
        const canvas = $("knowledgeMapCanvas");
        if (!canvas.clientWidth || !canvas.clientHeight) return;
        const scale = Math.min(
            (canvas.clientWidth - 100) / bounds.width,
            (canvas.clientHeight - 100) / bounds.height,
            1
        );
        transform.scale = Math.max(.18, Math.min(1, scale));
        transform.x = (canvas.clientWidth - bounds.width * transform.scale) / 2;
        transform.y = (canvas.clientHeight - bounds.height * transform.scale) / 2;
        applyTransform();
    }

    function applyTransform() {
        $("knowledgeMapViewport").setAttribute("transform",
            "translate(" + transform.x + " " + transform.y + ") scale(" + transform.scale + ")");
    }

    function onWheel(event) {
        event.preventDefault();
        const rect = $("knowledgeMapCanvas").getBoundingClientRect();
        const px = event.clientX - rect.left;
        const py = event.clientY - rect.top;
        const next = Math.max(.18, Math.min(2.2, transform.scale * (event.deltaY < 0 ? 1.12 : .89)));
        const ratio = next / transform.scale;
        transform.x = px - (px - transform.x) * ratio;
        transform.y = py - (py - transform.y) * ratio;
        transform.scale = next;
        applyTransform();
    }

    function onPointerDown(event) {
        if (event.button !== 0 || event.target.closest(".knowledge-map-node")) return;
        const canvas = $("knowledgeMapCanvas");
        drag = { x: event.clientX, y: event.clientY, tx: transform.x, ty: transform.y, pointerId: event.pointerId };
        canvas.setPointerCapture(event.pointerId);
        canvas.classList.add("is-panning");
    }

    function onPointerMove(event) {
        if (!drag || drag.pointerId !== event.pointerId) return;
        transform.x = drag.tx + event.clientX - drag.x;
        transform.y = drag.ty + event.clientY - drag.y;
        applyTransform();
    }

    function endPan(event) {
        if (!drag || drag.pointerId !== event.pointerId) return;
        drag = null;
        $("knowledgeMapCanvas").classList.remove("is-panning");
    }

    return { init, open, close };
})();
