"use strict";

const KnowledgeMap = (() => {
    const SVG_NS = "http://www.w3.org/2000/svg";
    const NODE_RADIUS = 9;
    const COLLECTION_RADIUS = 18;
    const MIN_RING_RADIUS = 250;
    const LABEL_WIDTH = 220;
    const LABEL_GAP = 28;
    const RING_GAP = 190;

    let getCollections = () => [];
    let getCurrentOID = () => null;
    let openDocument = async () => {};
    let selectedOID = null;
    let searchQuery = "";
    let transform = { x: 0, y: 0, scale: 1 };
    let bounds = { width: 1200, height: 800 };
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
                hidePopover();
            }
        });

        const canvas = $("knowledgeMapCanvas");
        canvas.addEventListener("wheel", onWheel, { passive: false });
        canvas.addEventListener("pointerdown", onPointerDown);
        canvas.addEventListener("pointermove", onPointerMove);
        canvas.addEventListener("pointerup", endPan);
        canvas.addEventListener("pointercancel", endPan);
        canvas.addEventListener("click", (event) => {
            if (!event.target.closest(".knowledge-map-node") && !event.target.closest(".knowledge-map-popover")) {
                hidePopover();
            }
        });
        canvas.addEventListener("dblclick", (event) => {
            const node = event.target.closest("[data-oid]");
            if (!node || !node.dataset.oid) return;
            close();
            void openDocument(node.dataset.oid);
        });
        $("knowledgeMapPopoverClose").addEventListener("click", hidePopover);
        document.addEventListener("keydown", (event) => {
            if (event.key === "Escape" && $("knowledgeMap").classList.contains("is-open")) {
                if (!$("knowledgeMapPopover").hidden) {
                    hidePopover();
                    return;
                }
                close();
            }
        });
        resizeObserver = new ResizeObserver(scheduleRender);
        resizeObserver.observe(canvas);
    }

    function open() {
        const map = $("knowledgeMap");
        map.classList.add("is-open");
        map.setAttribute("aria-hidden", "false");
        selectedOID = getCurrentOID() || selectedOID;
        $("knowledgeMapSearch").value = "";
        searchQuery = "";
        hidePopover();
        render();
        requestAnimationFrame(fit);
        $("knowledgeMapCanvas").focus({ preventScroll: true });
    }

    function close() {
        $("knowledgeMap").classList.remove("is-open");
        $("knowledgeMap").setAttribute("aria-hidden", "true");
        hidePopover();
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

    function flattenCollection(collection) {
        const levels = [];
        const nodes = [];
        const walk = (items, depth, parent) => {
            if (!levels[depth]) levels[depth] = [];
            (items || []).forEach((doc) => {
                const node = {
                    key: "doc:" + doc.oid,
                    type: "document",
                    id: doc.oid,
                    title: doc.title || "Без названия",
                    depth,
                    parentTitle: parent ? parent.title : "",
                    collectionTitle: collection.name || "Коллекция",
                    childrenCount: countDocuments(doc.children || []),
                    radius: NODE_RADIUS,
                };
                levels[depth].push(node);
                nodes.push(node);
                walk(doc.children, depth + 1, node);
            });
        };
        walk(collection.nodes, 1, null);
        return { levels, nodes };
    }

    function countDocuments(items) {
        let total = 0;
        (items || []).forEach((item) => {
            total += 1 + countDocuments(item.children);
        });
        return total;
    }

    function layoutCollection(collection, index, totalCollections, maxClusterRadius) {
        const flattened = flattenCollection(collection);
        const levels = flattened.levels;
        const edges = [];
        const byID = new Map(flattened.nodes.map((node) => [node.id, node]));
        const parentByID = new Map();

        const walkParents = (items, parent) => (items || []).forEach((doc) => {
            if (parent) parentByID.set(doc.oid, parent.oid);
            walkParents(doc.children, doc);
        });
        walkParents(collection.nodes, null);

        let clusterRadius = MIN_RING_RADIUS;
        levels.forEach((level, levelIndex) => {
            if (!level || !level.length) return;
            const radius = Math.max(MIN_RING_RADIUS + levelIndex * RING_GAP,
                level.length * (LABEL_WIDTH + LABEL_GAP) / (Math.PI * 2));
            clusterRadius = Math.max(clusterRadius, radius + 70);
            level.forEach((node, nodeIndex) => {
                const angle = -Math.PI / 2 + (Math.PI * 2 * (nodeIndex + .5) / level.length);
                node.x = Math.cos(angle) * radius;
                node.y = Math.sin(angle) * radius;
                const parentID = parentByID.get(node.id);
                const parent = parentID ? byID.get(parentID) : null;
                edges.push({ parent, child: node });
            });
        });

        const collectionNode = {
            key: "collection:" + collection.id,
            type: "collection",
            id: collection.id,
            title: collection.name || "Коллекция",
            collectionTitle: collection.name || "Коллекция",
            documentCount: flattened.nodes.length,
            x: 0,
            y: 0,
            radius: COLLECTION_RADIUS,
        };

        return { nodes: [collectionNode, ...flattened.nodes], edges, clusterRadius };
    }

    function render() {
        const collections = getCollections() || [];
        const viewport = $("knowledgeMapViewport");
        const empty = $("knowledgeMapEmpty");
        const meta = $("knowledgeMapMeta");
        viewport.replaceChildren();
        nodesByKey = new Map();

        const documentCount = collections.reduce((sum, collection) => sum + countDocuments(collection.nodes), 0);
        meta.textContent = collections.length + " коллекций · " + documentCount + " документов";
        empty.hidden = documentCount > 0;

        if (!documentCount) {
            bounds = { width: 1200, height: 800 };
            applyTransform();
            return;
        }

        const layouts = collections.map((collection, index) => layoutCollection(collection, index, collections.length));
        const maxClusterRadius = Math.max(...layouts.map((layout) => layout.clusterRadius), MIN_RING_RADIUS);
        const centerRadius = layouts.length <= 1 ? 0 :
            (maxClusterRadius + 120) / Math.sin(Math.PI / layouts.length);
        const nodes = [];
        const edges = [];

        layouts.forEach((layout, index) => {
            const collectionAngle = layouts.length <= 1 ? 0 :
                -Math.PI / 2 + Math.PI * 2 * index / layouts.length;
            const centerX = Math.cos(collectionAngle) * centerRadius;
            const centerY = Math.sin(collectionAngle) * centerRadius;

            layout.nodes.forEach((node) => {
                node.x += centerX;
                node.y += centerY;
                nodes.push(node);
                nodesByKey.set(node.key, node);
            });
            layout.edges.forEach((edge) => edges.push(edge));
        });

        const minX = Math.min(...nodes.map((node) => node.x - (node.type === "collection" ? COLLECTION_RADIUS : LABEL_WIDTH / 2)));
        const maxX = Math.max(...nodes.map((node) => node.x + (node.type === "collection" ? COLLECTION_RADIUS : LABEL_WIDTH / 2)));
        const minY = Math.min(...nodes.map((node) => node.y - (node.type === "collection" ? COLLECTION_RADIUS : NODE_RADIUS)));
        const maxY = Math.max(...nodes.map((node) => node.y + (node.type === "collection" ? COLLECTION_RADIUS + 48 : NODE_RADIUS + 56)));
        const padding = 180;
        const offsetX = padding - minX;
        const offsetY = padding - minY;
        nodes.forEach((node) => { node.x += offsetX; node.y += offsetY; });
        bounds = { width: maxX - minX + padding * 2, height: maxY - minY + padding * 2 };

        edges.forEach(({ parent, child, collectionId }) => {
            const actualParent = parent
                ? nodesByKey.get(parent.key)
                : nodesByKey.get("collection:" + collectionId);
            const actualChild = nodesByKey.get(child.key);
            if (actualParent && actualChild) drawEdge(viewport, actualParent, actualChild);
        });

        nodes.forEach((node) => viewport.appendChild(renderNode(node)));
        applyTransform();
    }

    function drawEdge(viewport, parent, child) {
        const dx = child.x - parent.x;
        const dy = child.y - parent.y;
        const distance = Math.max(1, Math.hypot(dx, dy));
        const start = parent.radius + 3;
        const end = child.radius + 3;
        const x1 = parent.x + dx / distance * start;
        const y1 = parent.y + dy / distance * start;
        const x2 = child.x - dx / distance * end;
        const y2 = child.y - dy / distance * end;
        const bend = Math.min(34, distance * .12);
        viewport.appendChild(svgElement("path", {
            class: "knowledge-map-edge",
            d: "M " + x1 + " " + y1 + " Q " + (x1 + dx / 2 - dy / distance * bend) + " " +
                (y1 + dy / 2 + dx / distance * bend) + " " + x2 + " " + y2,
        }));
    }

    function renderNode(node) {
        const current = node.type === "document" && node.id === getCurrentOID();
        const selected = node.type === "document" && node.id === selectedOID;
        const matches = searchQuery && node.title.toLocaleLowerCase().includes(searchQuery);
        const group = svgElement("g", {
            class: "knowledge-map-node " + node.type + (current ? " current" : "") +
                (selected ? " selected" : "") + (matches ? " search-match" : ""),
            transform: "translate(" + node.x + " " + node.y + ")",
            tabindex: "0",
            role: "button",
            "aria-label": node.title,
            "data-oid": node.type === "document" ? node.id : "",
            "data-node-key": node.key,
        });

        group.appendChild(svgElement("circle", {
            class: "node-dot",
            cx: 0, cy: 0, r: node.radius,
        }));

        const labelText = truncate(node.title, 29);
        const labelWidth = Math.max(92, Math.min(LABEL_WIDTH, labelText.length * 7.1 + 24));
        const labelY = node.type === "collection" ? COLLECTION_RADIUS + 13 : NODE_RADIUS + 12;
        const labelHeight = 27;
        group.appendChild(svgElement("rect", {
            class: "node-label-bg",
            x: -labelWidth / 2,
            y: labelY,
            width: labelWidth,
            height: labelHeight,
            rx: 7,
        }));
        group.appendChild(svgElement("text", {
            class: "node-label",
            x: 0,
            y: labelY + 18,
            "text-anchor": "middle",
        }, labelText));

        group.addEventListener("click", (event) => {
            event.stopPropagation();
            if (node.type === "document") selectedOID = node.id;
            render();
            showPopover(node, event.clientX, event.clientY);
        });
        group.addEventListener("keydown", (event) => {
            if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                selectedOID = node.type === "document" ? node.id : null;
                render();
                const rect = group.getBoundingClientRect();
                showPopover(node, rect.left + rect.width / 2, rect.top + rect.height / 2);
            }
        });
        return group;
    }

    function showPopover(node, clientX, clientY) {
        const popover = $("knowledgeMapPopover");
        const canvas = $("knowledgeMapCanvas");
        const rect = canvas.getBoundingClientRect();
        const title = $("knowledgeMapPopoverTitle");
        const type = $("knowledgeMapPopoverType");
        const details = $("knowledgeMapPopoverDetails");
        title.textContent = node.title;
        type.textContent = node.type === "collection" ? "Коллекция" : "Документ";
        details.replaceChildren();

        const rows = node.type === "collection"
            ? [["Документов", String(node.documentCount)]]
            : [
                ["Коллекция", node.collectionTitle],
                ["Родитель", node.parentTitle || "Корневой документ"],
                ["Вложенных документов", String(node.childrenCount)],
            ];
        rows.forEach(([label, value]) => {
            const row = document.createElement("div");
            row.className = "knowledge-map-popover-row";
            const name = document.createElement("span");
            name.textContent = label;
            const content = document.createElement("strong");
            content.textContent = value;
            row.append(name, content);
            details.appendChild(row);
        });

        popover.hidden = false;
        const width = popover.offsetWidth || 290;
        const height = popover.offsetHeight || 150;
        const left = Math.max(12, Math.min(clientX - rect.left + 14, rect.width - width - 12));
        const top = Math.max(12, Math.min(clientY - rect.top + 14, rect.height - height - 12));
        popover.style.left = left + "px";
        popover.style.top = top + "px";
    }

    function hidePopover() {
        const popover = $("knowledgeMapPopover");
        if (popover) popover.hidden = true;
    }

    function truncate(value, max) {
        const text = String(value || "");
        return text.length > max ? text.slice(0, max - 1) + "…" : text;
    }

    function findDocument(query) {
        let match = null;
        const visit = (items) => {
            for (const node of items || []) {
                if ((node.title || "").toLocaleLowerCase().includes(query)) {
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
        render();
        const node = nodesByKey.get("doc:" + match.oid);
        if (!node) return;
        const canvas = $("knowledgeMapCanvas");
        transform.x = canvas.clientWidth / 2 - node.x * transform.scale;
        transform.y = canvas.clientHeight / 2 - node.y * transform.scale;
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
        transform.scale = Math.max(.08, Math.min(1, scale));
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
        const next = Math.max(.08, Math.min(2.5, transform.scale * (event.deltaY < 0 ? 1.12 : .89)));
        const ratio = next / transform.scale;
        transform.x = px - (px - transform.x) * ratio;
        transform.y = py - (py - transform.y) * ratio;
        transform.scale = next;
        applyTransform();
    }

    function onPointerDown(event) {
        if (event.button !== 0 || event.target.closest(".knowledge-map-node") ||
            event.target.closest(".knowledge-map-popover")) return;
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
