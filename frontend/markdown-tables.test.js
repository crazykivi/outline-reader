"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const normalizeMarkdownTables = require("./markdown-tables.js");

test("pads rows with missing cells and preserves the table width", () => {
    const source = [
        "| Машина | Ресурс | Логин | Пароль |",
        "| --- | --- | --- | --- |",
        "| Srv-prod | ssh | | |",
        "| Srv-prod2 | portainer | admin | |",
    ].join("\n");

    const result = normalizeMarkdownTables(source).split("\n");
    assert.equal(result.length, 4);
    assert.equal(result[2], "| Srv-prod | ssh |  |  |");
    assert.equal(result[3], "| Srv-prod2 | portainer | admin |  |");
});

test("converts Outline empty-cell markers without dropping columns", () => {
    const source = "| Машина | Ресурс | Логин | Пароль |\n| --- | --- | --- | --- |\n| Srv-prod | ssh | * | * |";
    assert.equal(
        normalizeMarkdownTables(source).split("\n")[2],
        "| Srv-prod | ssh |  |  |"
    );
});

test("expands the table when a row contains additional cells", () => {
    const source = "| A | B |\n| --- | --- |\n| one | two | three |";
    const result = normalizeMarkdownTables(source).split("\n");
    assert.equal(result[0], "| A | B |  |");
    assert.equal(result[1], "| --- | --- | --- |");
    assert.equal(result[2], "| one | two | three |");
});

test("does not split escaped or code-span pipes", () => {
    const source = "| Name | Value |\n| --- | --- |\n| a\\|b | `x|y` |";
    assert.equal(normalizeMarkdownTables(source), source);
});

test("leaves pipe examples inside fenced code blocks untouched", () => {
    const source = "```md\n| A | B |\n| --- | --- |\n| one |\n```";
    assert.equal(normalizeMarkdownTables(source), source);
});

test("leaves ordinary prose untouched", () => {
    const source = "A | B is just prose here.\n\nAnother paragraph.";
    assert.equal(normalizeMarkdownTables(source), source);
});
