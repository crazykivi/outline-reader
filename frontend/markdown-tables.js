(function (root, factory) {
    const normalize = factory();
    if (typeof module !== "undefined" && module.exports) module.exports = normalize;
    root.normalizeMarkdownTables = normalize;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
    "use strict";

    function splitRow(line) {
        let value = line.trim();
        if (value.startsWith("|")) value = value.slice(1);
        if (value.endsWith("|") && !value.endsWith("\\|")) value = value.slice(0, -1);

        const cells = [];
        let cell = "";
        let codeTicks = 0;
        for (let i = 0; i < value.length; i++) {
            const char = value[i];
            if (char === "\\") {
                cell += char;
                if (i + 1 < value.length) cell += value[++i];
                continue;
            }
            if (char === "`") {
                let end = i + 1;
                while (value[end] === "`") end++;
                const count = end - i;
                if (!codeTicks) codeTicks = count;
                else if (codeTicks === count) codeTicks = 0;
                cell += value.slice(i, end);
                i = end - 1;
                continue;
            }
            if (char === "|" && !codeTicks) {
                cells.push(cell.trim());
                cell = "";
                continue;
            }
            cell += char;
        }
        cells.push(cell.trim());
        return cells;
    }

    function isDelimiter(line) {
        const cells = splitRow(line);
        return cells.length > 1 && cells.every((cell) => /^:?-{1,}:?$/.test(cell));
    }

    function normalizeRow(cells, width, emptyMarkers) {
        const result = cells.slice(0, width);
        while (result.length < width) result.push("");
        return result.map((cell) => emptyMarkers && cell === "*" ? "" : cell);
    }

    function normalizeMarkdownTables(markdown) {
        const lines = String(markdown || "").replace(/\r\n?/g, "\n").split("\n");
        const output = [];
        let fence = null;

        for (let i = 0; i < lines.length;) {
            const line = lines[i];
            const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
            if (fenceMatch) {
                const marker = fenceMatch[1][0];
                if (!fence) fence = marker;
                else if (fence === marker) fence = null;
                output.push(line);
                i++;
                continue;
            }

            if (fence || i + 1 >= lines.length || !line.includes("|") || !isDelimiter(lines[i + 1])) {
                output.push(line);
                i++;
                continue;
            }

            const header = splitRow(line);
            const delimiter = splitRow(lines[i + 1]);
            const rows = [];
            let width = Math.max(header.length, delimiter.length);
            let j = i + 2;

            while (j < lines.length && lines[j].trim() && lines[j].includes("|")) {
                const cells = splitRow(lines[j]);
                rows.push(cells);
                width = Math.max(width, cells.length);
                j++;
            }

            const normalizedHeader = normalizeRow(header, width, false);
            const normalizedDelimiter = normalizeRow(delimiter, width, false).map((cell) => cell || "---");
            const normalizedRows = rows.map((cells) => normalizeRow(cells, width, true));
            output.push("| " + normalizedHeader.join(" | ") + " |");
            output.push("| " + normalizedDelimiter.join(" | ") + " |");
            for (const cells of normalizedRows) output.push("| " + cells.join(" | ") + " |");
            i = j;
        }

        return output.join("\n");
    }

    return normalizeMarkdownTables;
});
