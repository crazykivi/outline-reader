"use strict";

const KnowledgeMap = (() => {
    const SVG_NS = "http://www.w3.org/2000/svg";
    const NODE_RADIUS = 8;
    const COLLECTION_RADIUS = 17;
    const LEVEL_GAP = 150;
    const COLLECTION_RADIUS_FROM_CENTER = 130;
    const LABEL_GAP = 14;

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
        $("knowledgeMapSearch").value = "";
        searchQuery = "";
        render();
        requestAnimationFrame(fit);
        $("knowledgeMapCanvas").focus({ preventScroll: true });
    }

    function close() {
        $("knowledgeMap").classList.remove("is-open");
        $("knowledgeMap").setAttribute("aria-hidden", "true");
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

    function countDocuments(collections) {
        let total = 0;
        const walk = (nodes) => (nodes || []).forEach((node) => {
            total++;
            walk(node.children);
        });
        collections.forEach((collection) => walk(collection.nodes));
        return total;
    }

    function subtreeWeight(node) {
        if (!node.children || !node.children.length) return 1;
        return node.children.reduce((sum, child) => sum + subtreeWeight(child), 0);
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
        empty.hidden = documentCount > 0;

        if (!documentCount) {
            bounds = { width: 1200, height: 800 };
            updateInspector();
            applyTransform();
            return;
        }

        const nodes = [];
        const edges = [];
        const center = { x: 0, y: 0 };
        const weights = collections.map((collection) =>
            (collection.nodes || []).reduce((sum, node) => sum + subtreeWeight(node), 0)
        );
        const totalWeight = Math.max(1, weights.reduce((sum, weight) => sum + weight, 0));
        let angleCursor = -Math.PI / 2;

        collections.forEach((collection, index) => {
            const roots = collection.nodes || [];
            const weight = Math.max(1, weights[index]);
            const sector = Math.PI * 2 * weight / totalWeight;
            const collectionAngle = angleCursor + sector / 2;
            const collectionNode = {
                key: "collection:" + collection.id,
                type: "collection",
                id: collection.id,
                title: collection.name,
                x: center.x + Math.cos(collectionAngle) * COLLECTION_RADIUS_FROM_CENTER,
                y: center.y + Math.sin(collectionAngle) * COLLECTION_RADIUS_FROM_CENTER,
                radius: COLLECTION_RADIUS,
            };
            nodes.push(collectionNode);

            let rootCursor = angleCursor;
            roots.forEach((root) => {
                const rootWeight = subtreeWeight(root);
                const rootSector = sector * rootWeight / weight;
                const rootAngle = rootCursor + rootSector / 2;
                placeDocument(root, collectionNode, 1, rootAngle, rootSector, nodes, edges);
                rootCursor += rootSector;
            });
            angleCursor += sector;
        });

        const padding = 180;
        const minX = Math.min(...nodes.map((node) => node.x - (node.radius || NODE_RADIUS)));
        const maxX = Math.max(...nodes.map((node) => node.x + (node.radius || NODE_RADIUS)));
        const minY = Math.min(...nodes.map((node) => node.y - (node.radius || NODE_RADIUS)));
        const maxY = Math.max(...nodes.map((node) => node.y + (node.radius || NODE_RADIUS)));
        const offsetX = padding - minX;
        const offsetY = padding - minY;
        nodes.forEach((node) => { node.x += offsetX; node.y += offsetY; });
        bounds = { width: maxX - minX + padding * 2, height: maxY - minY + padding * 2 };

        edges.forEach(({ parent, child }) => {
            const dx = child.x - parent.x;
            const dy = child.y - parent.y;
            const distance = Math.max(1, Math.hypot(dx, dy));
            const start = (parent.radius || NODE_RADIUS) + 2;
            const end = (child.radius || NODE_RADIUS) + 2;
            const x1 = parent.x + dx / distance * start;
            const y1 = parent.y + dy / distance * start;
            const x2 = child.x - dx / distance * end;
            const y2 = child.y - dy / distance * end;
            const bend = Math.min(44, distance * .22);
            viewport.appendChild(svgElement("path", {
                class: "knowledge-map-edge",
                d: "M " + x1 + " " + y1 + " Q " + (x1 + dx / 2 - dy / distance * bend) + " " +
                    (y1 + dy / 2 + dx / distance * bend) + " " + x2 + " " + y2,
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

    function placeDocument(doc, parent, depth, angle, sector, nodes, edges) {
        const distance = LEVEL_GAP * depth;
        const node = {
            key: "doc:" + doc.oid,
            type: "document",
            id: doc.oid,
            title: doc.title || "Без названия",
            x: parent.x + Math.cos(angle) * distance,
            y: parent.y + Math.sin(angle) * distance,
            radius: NODE_RADIUS,
            depth,
        };
        nodes.push(node);
        edges.push({ parent, child: node });

        const children = doc.children || [];
        if (!children.length) return;
        let childCursor = angle - sector / 2;
        const childWeight = children.reduce((sum, child) => sum + subtreeWeight(child), 0);
        children.forEach((child) => {
            const childSector = sector * subtreeWeight(child) / Math.max(1, childWeight);
            const childAngle = childCursor + childSector / 2;
            placeDocument(child, node, depth + 1, childAngle, childSector, nodes, edges);
            childCursor += childSector;
        });
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

        group.appendChild(svgElement("circle", {
            class: "node-dot",
            cx: 0, cy: 0, r: node.radius,
        }));
        if (node.type === "collection") {
            group.appendChild(svgElement("text", {
                class: "node-label collection-label",
                x: 0, y: COLLECTION_RADIUS + 22, "text-anchor": "middle",
            }, truncate(node.title, 30)));
        } else {
            group.appendChild(svgElement("text", {
                class: "node-label",
                x: node.radius + LABEL_GAP,
                y: 4,
            }, truncate(node.title, 34)));
        }

        group.addEventListener("click", () => {
            if (node.type !== "document") return;
            selectedOID = node.id;
            render();
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
        icon.textContent = "●";
        const title = document.createElement("strong");
        title.textContent = node.title;
        const description = document.createElement("p");
        description.textContent = "Документ · выберите узел или откройте его двойным щелчком";
        selection.append(icon, title, description);
        button.disabled = false;
    }

    async function openSelected() {
        if (!selectedOID || !nodesByKey.has("doc:" + selectedOID)) return;
        close();
        await openDocument(selectedOID);
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
            (canvas.clientWidth - 120) / bounds.width,
            (canvas.clientHeight - 120) / bounds.height,
            1
        );
        transform.scale = Math.max(.12, Math.min(1, scale));
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
        const next = Math.max(.12, Math.min(2.5, transform.scale * (event.deltaY < 0 ? 1.12 : .89)));
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
