const assert = require('node:assert/strict');
const {test} = require('node:test');
const path = require('node:path');
const Module = require('node:module');
const commands = new Map();
const saves = [];
const folderChanges = [];
const removedWorkspaces = [];
const runs = [];
const errors = [];
const locationCalls = [];
const selectedResources = [];
const fixes = [];
let navigationText = '// shared label mentioned here\n// SENTRY.Label("shared label")\nbody\n// SENTRY.EndLabel\n';
let locationLine = 3;
const folderA = {uri:{fsPath:path.resolve('/tmp/first'),scheme:'file'}};
const folderB = {uri:{fsPath:path.resolve('/tmp/second'),scheme:'file'}};
const disposable = () => ({dispose(){}});
class Position {constructor(line,character){Object.assign(this,{line,character});}}
class Range {constructor(start,end){Object.assign(this,{start,end});}}
class Selection extends Range {}
class EventEmitter {constructor(){this.event=disposable;} fire(){} dispose(){}}
const editor = {document:{uri:{fsPath:path.join(folderA.uri.fsPath,'source.go'),scheme:'file'}},revealRange(){}};
const vscode = {
 Uri:{file:fsPath=>({fsPath,scheme:'file'})},Position,Range,Selection,EventEmitter,StatusBarAlignment:{Left:1},TextEditorRevealType:{InCenter:1},
 CodeActionKind:{QuickFix:'quickfix'},
 window:{showInformationMessage(){},showErrorMessage:message=>errors.push(message),activeTextEditor:editor,createStatusBarItem:disposable,createOutputChannel:()=>({appendLine(){},dispose(){}}),createTreeView:disposable,showTextDocument:async()=>editor},
 workspace:{isTrusted:true,workspaceFolders:[folderA,folderB],getWorkspaceFolder:uri=>uri.fsPath.startsWith(folderB.uri.fsPath)?folderB:folderA,getConfiguration:(_section,uri)=>({get(key,fallback){return key==='runOnSave'?uri?.fsPath.startsWith(folderB.uri.fsPath):fallback;}}),onDidSaveTextDocument:fn=>{saves.push(fn);return disposable();},onDidChangeConfiguration:disposable,onDidChangeWorkspaceFolders:fn=>{folderChanges.push(fn);return disposable();},openTextDocument:async()=>({getText:()=> navigationText})},
 commands:{registerCommand:(name,fn)=>{commands.set(name,fn);return disposable();}},
 languages:{createDiagnosticCollection:()=>({dispose(){}}),registerCodeActionsProvider:disposable,registerCodeLensProvider:disposable,registerHoverProvider:disposable}
};
const original=Module._load;
Module._load=function(name,...args){if(name==='vscode')return vscode;if(name==='./lintRunner')return {LintRunner:class {run(uri){runs.push(uri);return Promise.resolve();}applyFix(uri){fixes.push(uri);return Promise.resolve();}removeWorkspace(uri){removedWorkspaces.push(uri);}setVerbose(){}},selectedWorkspaceFolder:resource=>{selectedResources.push(resource);return resource?vscode.workspace.getWorkspaceFolder(resource):folderA;},resolveWorkingDirectory:(folder,configured)=>path.resolve(folder.uri.fsPath,configured || '.'),ensureBinaryPath:async()=> 'test-ifttt',runBinary:async(_binary,args)=>{locationCalls.push(args);return {status:0,stdout:JSON.stringify({file:args[3],line:locationLine}),stderr:''};}};return original.call(this,name,...args);};
const {activate}=require('../dist/extension');
Module._load=original;
activate({subscriptions:[]});

test('save linting follows the saved document and its folder runOnSave setting',async()=>{
 const saved={uri:{fsPath:path.join(folderB.uri.fsPath,'source.go'),scheme:'file'}};
 for(const onSave of saves)await onSave(saved);
 assert.equal(runs.at(-1),saved.uri);
 const count=runs.length;
 for(const onSave of saves)await onSave(editor.document);
 assert.equal(runs.length,count,'disabled first folder must not lint on save');
});

test('folder-specific lint configuration is declared with resource scope',()=>{
 const properties=require('../package.json').contributes.configuration.properties;
 for(const name of ['binary','args','workingDirectory','diffCommand','skipDirectories','runOnSave','downloadBaseUrl','vcs','revision','diffMode','changeSet']) {
  assert.equal(properties[`iftttLint.${name}`].scope,'resource',`${name} must accept per-folder VS Code settings`);
 }
 assert.equal(properties['iftttLint.changeSet'].default,'');
});


test('scaffold invocation has the same process deadline as linting',async()=>{
 const cp=require('node:child_process');const {EventEmitter:NodeEmitter}=require('node:events');const originalSpawn=cp.spawn;let options;
 cp.spawn=(_binary,_args,opts)=>{
  options=opts;const process=new NodeEmitter();process.stderr=new NodeEmitter();
  queueMicrotask(()=>process.emit('close',0));return process;
 };
 try {
  await commands.get('iftttLint.scaffoldDirective')(path.join(folderA.uri.fsPath,'source.go'),'target.go','shared');
  assert.equal(options.timeout,30000);assert.equal(options.killSignal,'SIGKILL');
 } finally {cp.spawn=originalSpawn;}
});

test('scaffold kills a process whose error output exceeds the lint output limit',async()=>{
 const cp=require('node:child_process');const {EventEmitter:NodeEmitter}=require('node:events');const originalSpawn=cp.spawn;let killed=false;let convertedAfterOverflow=false;const previousErrors=errors.length;
 cp.spawn=()=>{
  const process=new NodeEmitter();process.stderr=new NodeEmitter();
  process.kill=signal=>{killed=signal==='SIGKILL';};
  queueMicrotask(()=>{process.stderr.emit('data','x'.repeat(16*1024*1024+1));process.stderr.emit('data',{toString(){convertedAfterOverflow=true;return 'later';}});process.emit('close',0);});return process;
 };
 try {
  await commands.get('iftttLint.scaffoldDirective')(path.join(folderA.uri.fsPath,'source.go'),'target.go','shared');
  assert.equal(killed,true);assert.equal(convertedAfterOverflow,false);assert.equal(errors.length,previousErrors+1);assert.match(errors.at(-1),/output exceeds 16 MiB/);
 } finally {cp.spawn=originalSpawn;}
});


test('label navigation uses the native parser location past prose and fenced examples',async()=>{
 navigationText='// See SENTRY.Label("shared label")\n// ```\n// SENTRY.Label("shared label")\n// ```\n// SENTRY.Label("shared label")\nbody\n// SENTRY.EndLabel\n';
 locationLine=6;
 const target=path.join(folderB.uri.fsPath,'target.go');
 await commands.get('iftttLint.jumpToLabel')(target,'shared label');
 assert.deepEqual(locationCalls.at(-1),['jump','--print-location','--',target,'shared label']);
 assert.equal(editor.selection.start.line,5);
});


test('label navigation selects the supplied target folder instead of the active editor',async()=>{
 const target=path.join(folderB.uri.fsPath,'target.go');
 await commands.get('iftttLint.jumpToLabel')(target,'shared label');
 assert.equal(selectedResources.at(-1)?.fsPath,target);
});

test('scaffolding selects the supplied source folder and reruns that resource',async()=>{
 const cp=require('node:child_process');const {EventEmitter:NodeEmitter}=require('node:events');const originalSpawn=cp.spawn;let options;
 cp.spawn=(_binary,_args,opts)=>{options=opts;const process=new NodeEmitter();process.stderr=new NodeEmitter();queueMicrotask(()=>process.emit('close',0));return process;};
 const source=path.join(folderB.uri.fsPath,'source.go');
 try {
  await commands.get('iftttLint.scaffoldDirective')(source,'target.go','shared');
  assert.equal(options.cwd,folderB.uri.fsPath);assert.equal(runs.at(-1)?.fsPath,source);
 } finally {cp.spawn=originalSpawn;}
});

test('the fix command forwards the finding resource to the runner',async()=>{
 const uri={fsPath:path.join(folderB.uri.fsPath,'source.go'),scheme:'file'};
 await commands.get('iftttLint.applyFix')(uri);
 assert.equal(fixes.at(-1),uri);
});

test('label navigation uses an explicit workspace owner for a target outside its folder',async()=>{
 const target=path.resolve('/tmp/outside/target.go');
 await commands.get('iftttLint.jumpToLabel')(target,'shared label',folderB.uri);
 assert.equal(selectedResources.at(-1),folderB.uri);
});

test('open target navigation retains an explicit workspace owner',async()=>{
 await commands.get('iftttLint.openTarget')(path.resolve('/tmp/outside/target.go'),'shared label',folderB.uri);
 assert.equal(selectedResources.at(-1),folderB.uri);
});

test('scaffolding uses an explicit workspace owner and refreshes that owner',async()=>{
 const cp=require('node:child_process');const {EventEmitter:NodeEmitter}=require('node:events');const originalSpawn=cp.spawn;let options;
 cp.spawn=(_binary,_args,opts)=>{options=opts;const process=new NodeEmitter();process.stderr=new NodeEmitter();queueMicrotask(()=>process.emit('close',0));return process;};
 try {
  await commands.get('iftttLint.scaffoldDirective')(path.resolve('/tmp/outside/source.go'),path.resolve('/tmp/outside/target.go'),'shared',folderB.uri);
  assert.equal(options.cwd,folderB.uri.fsPath);assert.equal(runs.at(-1),folderB.uri);
 } finally {cp.spawn=originalSpawn;}
});


test('removing a workspace folder retires its diagnostics and pending lint runs',()=>{
 assert.equal(folderChanges.length,1);
 folderChanges[0]({removed:[folderB],added:[]});
 assert.equal(removedWorkspaces.at(-1),folderB.uri);
});

test('snapshot mode guards manually invoked fix, scaffold and label jump commands',async t=>{
 const previous=vscode.workspace.getConfiguration;vscode.workspace.getConfiguration=(section,uri)=>({get(key,fallback){return key==='changeSet'?'snapshots.yml':previous(section,uri).get(key,fallback);}});
 t.after(()=>vscode.workspace.getConfiguration=previous);
 const before=locationCalls.length;const beforeFix=fixes.length;const beforeErrors=errors.length;
 await assert.rejects(commands.get('iftttLint.jumpToLabel')('/tmp/foreign/target.go','shared',folderB.uri),/snapshot|change.?set/i);
 await commands.get('iftttLint.applyFix')(folderB.uri);assert.equal(fixes.length,beforeFix);assert.match(errors.at(-1),/snapshot|change.?set/i);
 await commands.get('iftttLint.scaffoldDirective')('/tmp/foreign/source.go','target.go','shared',folderB.uri);assert.match(errors.at(-1),/snapshot|change.?set/i);assert.equal(errors.length,beforeErrors+2);
 assert.equal(locationCalls.length,before);
 await commands.get('iftttLint.openTarget')('/tmp/foreign/target.go',null,folderB.uri);assert.equal(editor.selection.start.line,0);
});

test('remote target commands never invoke filesystem helpers',async()=>{
 const before=locationCalls.length;const beforeErrors=errors.length;
 for(const command of ['iftttLint.openTarget','iftttLint.jumpToLabel']) await assert.rejects(commands.get(command)('github://owner/repo/file.go','shared',folderB.uri),/remote/i);
 await commands.get('iftttLint.scaffoldDirective')('/tmp/source.go','github://owner/repo/file.go','shared',folderB.uri);
 assert.equal(locationCalls.length,before);assert.equal(errors.length,beforeErrors+1);assert.match(errors.at(-1),/remote/i);
});
