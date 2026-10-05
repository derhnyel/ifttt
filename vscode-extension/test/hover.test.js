const assert = require('node:assert/strict');
const { test, before, after } = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const cp = require('node:child_process');
const Module = require('node:module');

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ifttt-hover-'));
const binary = path.join(root, process.platform === 'win32' ? 'ifttt.exe' : 'ifttt');
let args = [];
class MarkdownString {
 constructor() { this.value = ''; }
 appendText(text) { this.value += text; }
 appendMarkdown(text) { this.value += text; }
 appendCodeblock(text) { this.value += text; }
}
const folder = { uri: { fsPath: root } };
const vscode = {
 MarkdownString, Hover: class { constructor(contents) { this.contents = contents; } }, Range: class {},
 languages: { getDiagnostics: () => [] },
 workspace: { isTrusted: true, workspaceFolders: [folder], getWorkspaceFolder: () => folder,
  getConfiguration: () => ({ get: (key, fallback) => ({ binary, args })[key] ?? fallback }) }
};
const load = Module._load;
Module._load = function(name, ...rest) { return name === 'vscode' ? vscode : load.call(this, name, ...rest); };
const { FindingHoverProvider } = require('../dist/hover');
Module._load = load;
before(() => cp.execFileSync('go', ['build', '-o', binary, './cmd/ifttt'], { cwd: path.resolve(__dirname, '../..'), timeout: 90000 }));
after(() => fs.rmSync(root, { recursive: true, force: true }));
function document(label, extension = '.go') {
 const text = `${extension === '.tmpl' ? '##' : '//'} LINT.IfChange(${label})\nbody\n// LINT.ThenChange()\n`;
 return { uri: { scheme: 'file', fsPath: path.join(root, 'source' + extension) }, version: 1,
  lineAt: () => ({ text: text.split('\n')[0] }), getText: () => text };
}

// LINT.IfChange(hover_behavior)
test('reopened documents cannot reuse another editor buffer inspection', async () => {
 const provider = new FindingHoverProvider({});
 assert.match((await provider.provideHover(document('FIRST'), {line: 0})).contents.value, /Argument: FIRST/);
 assert.match((await provider.provideHover(document('SECOND'), {line: 0})).contents.value, /Argument: SECOND/);
});

test('parser overrides refresh cached hover without forwarding lint or fix flags', async () => {
 const provider = new FindingHoverProvider({});
 const doc = document('CUSTOM', '.tmpl');
 assert.equal(await provider.provideHover(doc, {line: 0}), undefined);
 try {
  for (const setting of [['--comment-style', '.tmpl=##', '--fix'], ['--comment-style=.tmpl=##', '--format=text']]) {
   args = setting;
   assert.match((await provider.provideHover(doc, {line: 0})).contents.value, /Argument: CUSTOM/);
  }
 } finally { args = []; }
});

test('untrusted and cancelled hovers do not read editor contents or run commands', async () => {
 const provider = new FindingHoverProvider({});
 const doc = document('PRIVATE');
 doc.getText = () => { throw new Error('untrusted contents accessed'); };
 vscode.workspace.isTrusted = false;
 try { assert.equal(await provider.provideHover(doc, {line: 0}), undefined); }
 finally { vscode.workspace.isTrusted = true; }
 assert.equal(await provider.provideHover(doc, {line: 0}, {isCancellationRequested: true}), undefined);
});
// LINT.ThenChange(//vscode-extension/src/hover.ts:directive_hover)
