const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const Module = require('node:module');
const originalLoad = Module._load;
const vscode = { workspace: { isTrusted: false, workspaceFolders: [{uri:{fsPath:process.cwd()}}], getConfiguration() { throw new Error('untrusted configuration accessed'); } }, window: {showWarningMessage(){}} };
Module._load = function(name,...args) { return name === 'vscode' ? vscode : originalLoad.call(this,name,...args); };
const { LintRunner, ensureBinaryPath } = require('../dist/lintRunner');
Module._load = originalLoad;

test('untrusted workspace does not execute configured commands',async () => {
 const runner = new LintRunner({}, {}, {appendLine(){}}, false);
 assert.equal(await runner.execute(false),null);
});

test('configured command is resolved from PATH before downloading',async () => {
 const dir = fs.mkdtempSync(path.join(os.tmpdir(),'iflint-path-'));
 const name = process.platform === 'win32' ? 'custom-iflint.cmd' : 'custom-iflint';
 const binary = path.join(dir,name);
 fs.writeFileSync(binary,'#!/bin/sh\nexit 0\n',{mode:0o755});
 const previous = process.env.PATH;
 process.env.PATH = dir;
 try { assert.equal(await ensureBinaryPath(name,'',dir+'-workspace'),binary); }
 finally { process.env.PATH = previous; fs.rmSync(dir,{recursive:true,force:true}); }
});

// The runner receives a real Git diff and communicates over stdin with an
// executable process. Only the VS Code host API is substituted.
function trustedWorkspace(dir, binary, extra = {}) {
 vscode.workspace.isTrusted = true;
 vscode.workspace.workspaceFolders = [{uri:{fsPath:dir}}];
 vscode.workspace.getConfiguration = () => ({get(key, fallback) { return ({binary, diffCommand:'git diff', skipDirectories:[], ...extra})[key] ?? fallback; }});
}
function tempWorkspace(t) {
 const cp = require('node:child_process');
 const dir = fs.mkdtempSync(path.join(os.tmpdir(),'iflint runner ü '));
 t.after(() => fs.rmSync(dir,{recursive:true,force:true}));
 const gitEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('GIT_')));
 Object.assign(gitEnv,{GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:path.join(dir,'nonexistent-global-config')});
 const git = args => cp.execFileSync('git',args,{cwd:dir,env:gitEnv,timeout:10000});
 for (const args of [['init','-q'],['config','user.name','Test'],['config','user.email','test@example.invalid']]) git(args);
 fs.writeFileSync(path.join(dir,'source ü.txt'),'before\n');
 git(['add','.']); git(['commit','-qm','baseline']);
 fs.writeFileSync(path.join(dir,'source ü.txt'),'after λ\n');
 return dir;
}
function executable(dir, body) {
 const binary = path.join(dir,'lint process');
 fs.writeFileSync(binary,`#!${process.execPath}\n${body}`,{mode:0o755});
 return binary;
}
function runner() { return new LintRunner({clear(){}}, {show(){}}, {appendLine(){}}, false); }

test('runner passes a real Unicode Git diff through stdin and parses findings', {skip:process.platform === 'win32'}, async t => {
 const dir = tempWorkspace(t);
 const binary = executable(dir,`let input='';process.stdin.setEncoding('utf8');process.stdin.on('data',s=>input+=s);process.stdin.on('end',()=>{if(!input.includes('after λ'))process.exit(2);console.log(JSON.stringify({errors:[{ruleId:'then_missing',severity:'error',file:'source ü.txt',line:2,message:"expected changes in 'target ü.txt#shared label' but none found"}]}));process.exitCode=1;});`);
 trustedWorkspace(dir,binary);
 const result = await runner().execute(false);
 assert.equal(result.ok,false); assert.equal(result.workspaceRoot,dir);
 assert.equal(result.findings[0].file,'source ü.txt'); assert.equal(result.findings[0].targetPath,'target ü.txt'); assert.equal(result.findings[0].targetLabel,'shared label');
});

test('runner reports operational process failures instead of clearing diagnostics', {skip:process.platform === 'win32'},async t => {
 const dir = tempWorkspace(t); const binary = executable(dir,`console.error('configuration rejected');process.exit(2);`);
 trustedWorkspace(dir,binary); await assert.rejects(runner().execute(false),/configuration rejected/);
});

test('runner sends --fix and forces JSON after configured format', {skip:process.platform === 'win32'},async t => {
 const dir = tempWorkspace(t);const binary = executable(dir,`const fs=require('fs');fs.writeFileSync('arguments.json',JSON.stringify(process.argv.slice(2)));process.stdin.resume();process.stdin.on('end',()=>console.log(JSON.stringify({errors:[]})));`);
 trustedWorkspace(dir,binary,{args:['--format=text']});const result=await runner().execute(true);assert.equal(result.ok,true);
 const args=JSON.parse(fs.readFileSync(path.join(dir,'arguments.json'),'utf8'));assert.equal(args.filter(arg=>arg==='--fix').length,1);assert.equal(args.at(-1),'-');assert.ok(args.lastIndexOf('--format=json')>args.indexOf('--format=text'));
});

test('runner exposes malformed JSON as a parse finding', {skip:process.platform === 'win32'},async t => {
 const dir=tempWorkspace(t);trustedWorkspace(dir,executable(dir,`process.stdin.resume();process.stdin.on('end',()=>console.log('malformed'));`));
 const result=await runner().execute(false);assert.equal(result.ok,false);assert.equal(result.findings[0].ruleId,'parse_error');
});

test('runner propagates diff command failure',async t => {
 const dir=tempWorkspace(t);trustedWorkspace(dir,'unused',{diffCommand:'git definitely-not-a-command'});
 await assert.rejects(runner().execute(false));
});

test('extension runs the real CLI and fixes an apostrophe target path', {timeout:120000},async t => {
 const cp=require('node:child_process');const dir=tempWorkspace(t);const buildDir=fs.mkdtempSync(path.join(os.tmpdir(),'iflint cli '));t.after(()=>fs.rmSync(buildDir,{recursive:true,force:true}));
 const binary=path.join(buildDir,process.platform==='win32'?'iflint.exe':'iflint');cp.execFileSync('go',['build','-o',binary,'./cmd'],{cwd:path.resolve(__dirname,'../..'),timeout:90000});
 const target="owner's target.go";
 fs.writeFileSync(path.join(dir,'.ifttt-lint.yaml'),'directives:\n  prefix: SENTRY\n');
 const source=value=>`// SENTRY.IfChange("shared label")\nvar value = "${value}"\n// SENTRY.ThenChange("${target}#shared label")\n`;
 fs.writeFileSync(path.join(dir,'source.go'),source('old'));fs.writeFileSync(path.join(dir,target),'// SENTRY.Label("shared label")\nvar target = "old"\n// SENTRY.EndLabel\n');
 cp.execFileSync('git',['add','.'],{cwd:dir,timeout:10000});cp.execFileSync('git',['-c','core.hooksPath=','commit','-qm','contract baseline'],{cwd:dir,timeout:10000});fs.writeFileSync(path.join(dir,'source.go'),source('new'));
 trustedWorkspace(dir,binary);const lint=runner();const result=await lint.execute(false);assert.equal(result.ok,false);assert.equal(result.findings.length,1);assert.equal(result.findings[0].targetPath,target);assert.equal(result.findings[0].targetLabel,'shared label');
 await lint.execute(true);const fixed=fs.readFileSync(path.join(dir,target),'utf8');assert.match(fixed,/TODO\(iflint\)/);await lint.execute(true);assert.equal(fs.readFileSync(path.join(dir,target),'utf8'),fixed);assert.equal(fs.existsSync(path.join(dir,'owner')),false);
});


test('native backend forwards VCS and revision without shell interpolation', {skip:process.platform === 'win32'},async t => {
 const dir=tempWorkspace(t);const binary=executable(dir,`require('fs').writeFileSync('arguments.json',JSON.stringify(process.argv.slice(2)));console.log(JSON.stringify({errors:[]}));`);
 trustedWorkspace(dir,binary,{diffCommand:'',vcs:'jj',revision:'main..@; touch unwanted',diffMode:'working-tree'});
 const result=await runner().execute(false);assert.equal(result.ok,true);
 const args=JSON.parse(fs.readFileSync(path.join(dir,'arguments.json'),'utf8'));
 assert.deepEqual(args.slice(-4),['--vcs','jj','--diff','main..@; touch unwanted']);assert.equal(args.includes('-'),false);assert.equal(fs.existsSync(path.join(dir,'unwanted')),false);
});

test('native runner uses active editor workspace and scoped configuration', {skip:process.platform === 'win32'},async t => {
 const first=tempWorkspace(t);const active=tempWorkspace(t);const binary=executable(active,`console.log(JSON.stringify({errors:[]}));`);
 trustedWorkspace(active,binary,{diffCommand:''});const selected={uri:{fsPath:active}};
 vscode.workspace.workspaceFolders=[{uri:{fsPath:first}},selected];vscode.window.activeTextEditor={document:{uri:{fsPath:path.join(active,'source ü.txt')}}};
 vscode.workspace.getWorkspaceFolder=()=>selected;
 const original=vscode.workspace.getConfiguration;vscode.workspace.getConfiguration=(section,uri)=>{assert.equal(uri,selected.uri);return original();};
 t.after(()=>{delete vscode.window.activeTextEditor;delete vscode.workspace.getWorkspaceFolder;});
 const result=await runner().execute(false);assert.equal(result.workspaceRoot,active);
});

test('newer lint results cannot be overwritten by an older concurrent run', async () => {
 const published=[];let current=[];const lint=new LintRunner({publish(findings){current=findings;published.push(findings[0].message);},getFindings(){return current;},clear(){}},{show(){}},{appendLine(){}},false);
 let resolveFirst;let count=0;lint.execute=()=>++count===1?new Promise(resolve=>resolveFirst=resolve):Promise.resolve({findings:[{message:'new'}],ok:true,workspaceRoot:process.cwd()});
 const first=lint.run();await lint.run();resolveFirst({findings:[{message:'old'}],ok:true,workspaceRoot:process.cwd()});await first;assert.deepEqual(published,['new']);
});

test('operational errors retain previous diagnostics', async () => {
 let cleared=false;const lint=new LintRunner({clear(){cleared=true;}},{show(){}},{appendLine(){}},false);
 vscode.window.showErrorMessage=()=>{};lint.execute=async()=>{throw new Error('jj unavailable');};await lint.run();assert.equal(cleared,false);
});

let realBinary;
function buildRealCLI(t) {
 if (!realBinary) {
  const cp=require('node:child_process');const buildDir=fs.mkdtempSync(path.join(os.tmpdir(),'iflint native cli '));
  realBinary=path.join(buildDir,process.platform==='win32'?'iflint.exe':'iflint');
  cp.execFileSync('go',['build','-o',realBinary,'./cmd'],{cwd:path.resolve(__dirname,'../..'),timeout:90000});
  process.on('exit',()=>fs.rmSync(buildDir,{recursive:true,force:true}));
 }
 return realBinary;
}
function contractFixture(dir) {
 const cp=require('node:child_process');
 const source=value=>`// LINT.IfChange(shared)\nvar source = "${value}"\n// LINT.ThenChange(//target.go:shared)\n`;
 fs.writeFileSync(path.join(dir,'source.go'),source('old'));fs.writeFileSync(path.join(dir,'target.go'),'// LINT.IfChange(shared)\nvar target = "old"\n// LINT.ThenChange()\n');
 cp.execFileSync('git',['add','.'],{cwd:dir,timeout:10000});cp.execFileSync('git',['-c','core.hooksPath=','commit','-qm','contract baseline'],{cwd:dir,timeout:10000});
 fs.writeFileSync(path.join(dir,'source.go'),source('new'));
}

test('default native extension finds unconfigured standard LINT Git violations and staged selection', {timeout:120000},async t => {
 const cp=require('node:child_process');const dir=tempWorkspace(t);contractFixture(dir);
 assert.equal(fs.existsSync(path.join(dir,'.ifttt-lint.yaml')),false);
 trustedWorkspace(dir,buildRealCLI(t),{diffCommand:''});let result=await runner().execute(false);assert.equal(result.ok,false);assert.equal(result.findings[0].targetPath,'target.go');assert.equal(result.findings[0].targetLabel,'shared');
 trustedWorkspace(dir,realBinary,{diffCommand:'',diffMode:'staged'});result=await runner().execute(false);assert.equal(result.ok,true);
 cp.execFileSync('git',['add','source.go'],{cwd:dir,timeout:10000});result=await runner().execute(false);assert.equal(result.ok,false);
 trustedWorkspace(dir,realBinary,{diffCommand:'',revision:'HEAD'});result=await runner().execute(false);assert.equal(result.ok,false);
});

const jjCommand=process.env.IFLINT_JJ_BINARY||'jj';
const hasJJ=require('node:child_process').spawnSync(jjCommand,['--version']).status===0;
test('default native extension finds unconfigured standard LINT with jj and accepts real revsets', {skip:!hasJJ,timeout:120000},async t => {
 const cp=require('node:child_process');const dir=tempWorkspace(t);contractFixture(dir);
 const cleanEnv=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('JJ_')&&!key.startsWith('GIT_')));
 Object.assign(cleanEnv,{JJ_CONFIG:''});
 cp.execFileSync(jjCommand,['git','init','--colocate'],{cwd:dir,env:cleanEnv,timeout:20000});
 for (const [key,value] of [['user.name','Test'],['user.email','test@example.invalid']]) cp.execFileSync(jjCommand,['config','set','--repo',key,value],{cwd:dir,env:cleanEnv,timeout:10000});
 // Backend uses jj on PATH; tests permit an explicitly installed binary.
 const previousPath=process.env.PATH;if (path.isAbsolute(jjCommand)) process.env.PATH=path.dirname(jjCommand)+path.delimiter+previousPath;
 t.after(()=>{process.env.PATH=previousPath;});
 assert.equal(fs.existsSync(path.join(dir,'.ifttt-lint.yaml')),false);
 trustedWorkspace(dir,buildRealCLI(t),{diffCommand:''});let result=await runner().execute(false);assert.equal(result.ok,false);assert.equal(result.findings[0].targetPath,'target.go');assert.equal(result.findings[0].targetLabel,'shared');
 trustedWorkspace(dir,realBinary,{diffCommand:'',vcs:'jj',revision:'@'});result=await runner().execute(false);assert.equal(result.ok,false);
 trustedWorkspace(dir,realBinary,{diffCommand:'',vcs:'jj',diffMode:'staged'});await assert.rejects(runner().execute(false),/stag(?:ed|ing)/i);
});

test('unrelated JSON objects cannot report a clean lint result', () => {
 const parsed=runner().parse(JSON.stringify({message:'wrong executable output'}));
 assert.equal(parsed.ok,false);assert.equal(parsed.findings[0].ruleId,'parse_error');
});

test('relative working directories resolve from the selected workspace folder', {skip:process.platform==='win32'},async t=>{
 const dir=tempWorkspace(t);const nested=path.join(dir,'nested workspace');fs.mkdirSync(nested);
 const binary=executable(dir,`console.log(JSON.stringify({errors:[{file:'source.go',line:1,severity:'warning',message:process.cwd()}]}));`);
 for(const [configured,expected] of [['.',dir],['nested workspace',nested],[nested,nested]]) {
  trustedWorkspace(dir,binary,{diffCommand:'',workingDirectory:configured});
  const result=await runner().execute(false);
  assert.equal(result.workspaceRoot,expected);assert.equal(result.findings[0].message,fs.realpathSync(expected));
 }
});


test('applyFix executes and refreshes the explicit finding resource',async()=>{
 vscode.window.showInformationMessage=()=>{};
 const lint=runner();const uri={fsPath:path.resolve('/tmp/finding.go')};const calls=[];
 lint.execute=async(fix,resource)=>{calls.push([fix,resource]);return {findings:[],ok:true,workspaceRoot:'/tmp'};};
 lint.run=async resource=>calls.push(['refresh',resource]);
 await lint.applyFix(uri);
 assert.deepEqual(calls,[[true,uri],['refresh',uri]]);
});

test('runner accepts an absolute report workspaceRoot with a legacy cwd fallback', {skip:process.platform==='win32'},async t=>{
 const dir=tempWorkspace(t);const root=path.resolve('/tmp/reported-root');
 for(const [metadata,expected] of [[root,root],['relative-root',dir],[42,dir],[undefined,dir]]) {
  const binary=executable(dir,`console.log(JSON.stringify({errors:[],workspaceRoot:${JSON.stringify(metadata) ?? 'undefined'}}));`);
  trustedWorkspace(dir,binary,{diffCommand:''});const result=await runner().execute(false);assert.equal(result.workspaceRoot,expected);
 }
});

test('suppressed findings stay clean in object and legacy array reports',()=>{
 const finding={file:'source.go',line:1,severity:'error',ruleId:'then_missing',message:'ignored',suppressed:true};
 for(const report of [{errors:[],suppressed:[finding]},{errors:[finding]},[finding]]) {
  const parsed=runner().parse(JSON.stringify(report));assert.equal(parsed.ok,true);assert.equal(parsed.findings.length,0);
 }
});

function twoRootRunner(t,results) {
 const original={trusted:vscode.workspace.isTrusted,folders:vscode.workspace.workspaceFolders,getFolder:vscode.workspace.getWorkspaceFolder,configuration:vscode.workspace.getConfiguration};
 const first={uri:{fsPath:path.resolve('/tmp/first')}};const second={uri:{fsPath:path.resolve('/tmp/second')}};
 vscode.workspace.isTrusted=true;vscode.workspace.workspaceFolders=[first,second];vscode.workspace.getWorkspaceFolder=uri=>uri.fsPath.startsWith(second.uri.fsPath)?second:first;
 vscode.workspace.getConfiguration=()=>({get(_key,fallback){return fallback;}});vscode.window.setStatusBarMessage=()=>{};
 t.after(()=>{vscode.workspace.isTrusted=original.trusted;vscode.workspace.workspaceFolders=original.folders;vscode.workspace.getWorkspaceFolder=original.getFolder;vscode.workspace.getConfiguration=original.configuration;});
 const reports=new Map();const status={show(){}};
 const lint=new LintRunner({publish(findings,root){reports.set(root,findings);},getFindings(){return [...reports.values()].flat();}},status,{appendLine(){}},false);
 lint.execute=(_fix,resource)=>results(resource);
 return {lint,reports,status,first:first.uri,second:second.uri};
}

test('concurrent independent roots both publish even when the first finishes last',async t=>{
 let finishFirst;const setup=twoRootRunner(t,resource=>resource.fsPath.endsWith('first')?new Promise(resolve=>finishFirst=resolve):Promise.resolve({findings:[],ok:true,workspaceRoot:resource.fsPath}));
 const pending=setup.lint.run(setup.first);await setup.lint.run(setup.second);
 finishFirst({findings:[{message:'first',severity:'error'}],ok:false,workspaceRoot:setup.first.fsPath});await pending;
 assert.equal(setup.reports.get(setup.first.fsPath)?.length,1);assert.equal(setup.reports.get(setup.second.fsPath)?.length,0);
});

test('newer alias reports cannot be overwritten by an older request for the same resolved root',async t=>{
 let finishFirst;const common=path.resolve('/tmp/shared-root');const setup=twoRootRunner(t,resource=>resource.fsPath.endsWith('first')?new Promise(resolve=>finishFirst=resolve):Promise.resolve({findings:[{message:'new'}],ok:true,workspaceRoot:common}));
 const pending=setup.lint.run(setup.first);await setup.lint.run(setup.second);finishFirst({findings:[{message:'old'}],ok:true,workspaceRoot:common});await pending;
 assert.equal(setup.reports.get(common)[0].message,'new');
});

test('a clean second root keeps the aggregate status in error while the first has findings',async t=>{
 const setup=twoRootRunner(t,resource=>Promise.resolve({findings:resource.fsPath.endsWith('first')?[{message:'first',severity:'error'}]:[],ok:!resource.fsPath.endsWith('first'),workspaceRoot:resource.fsPath}));
 await setup.lint.run(setup.first);await setup.lint.run(setup.second);assert.match(setup.status.text,/\$\(error\).*1 issue/);
});

test('changing workingDirectory in one folder invalidates its pending old report',async t=>{
 let finishFirst;let calls=0;const setup=twoRootRunner(t,()=>++calls===1?new Promise(resolve=>finishFirst=resolve):Promise.resolve({findings:[],ok:true,workspaceRoot:'/tmp/new-cwd'}));
 let workingDirectory='old';vscode.workspace.getConfiguration=()=>({get(key,fallback){return key==='workingDirectory'?workingDirectory:fallback;}});
 const pending=setup.lint.run(setup.first);workingDirectory='new';await setup.lint.run(setup.first);finishFirst({findings:[{message:'old',severity:'error'}],ok:false,workspaceRoot:'/tmp/old-cwd'});await pending;
 assert.equal(setup.reports.has('/tmp/old-cwd'),false);assert.equal(setup.reports.has('/tmp/new-cwd'),true);
});

test('removing a workspace invalidates its pending report',async t=>{
 let finish;const setup=twoRootRunner(t,()=>new Promise(resolve=>finish=resolve));let removed;
 setup.lint.diagnostics.removeOwner=uri=>removed=uri;
 const pending=setup.lint.run(setup.first);setup.lint.removeWorkspace(setup.first);finish({findings:[{message:'stale',severity:'error'}],ok:false,workspaceRoot:setup.first.fsPath});await pending;
 assert.equal(removed,setup.first);assert.equal(setup.reports.size,0);
});

test('change-set runner resolves manifest from configured cwd and bypasses native selection', {skip:process.platform==='win32'},async t=>{
 const dir=tempWorkspace(t);const nested=path.join(dir,'nested');fs.mkdirSync(nested);
 const binary=executable(dir,`require('fs').writeFileSync('arguments.json',JSON.stringify(process.argv.slice(2)));let input='';process.stdin.on('data',s=>input+=s);process.stdin.on('end',()=>{if(input)process.exit(2);console.log(JSON.stringify({errors:[]}));});`);
 trustedWorkspace(dir,binary,{diffCommand:'',changeSet:'../snapshots.yml',workingDirectory:'nested',vcs:'jj',revision:'@',diffMode:'staged',skipDirectories:['ignored']});
 assert.equal((await runner().execute(false)).ok,true);
 const args=JSON.parse(fs.readFileSync(path.join(nested,'arguments.json'),'utf8'));
 assert.deepEqual(args,['--format=json','--skip-dir','ignored','--change-set',path.join(dir,'snapshots.yml')]);
});

test('change-set rejects custom diff commands and fixes before launching commands',async t=>{
 const dir=tempWorkspace(t);
 trustedWorkspace(dir,'unused',{changeSet:'snapshots.yml',diffCommand:'touch unwanted'});
 await assert.rejects(runner().execute(false),/change.?set.*diffCommand/i);
 assert.equal(fs.existsSync(path.join(dir,'unwanted')),false);
 trustedWorkspace(dir,'unused',{changeSet:'snapshots.yml',diffCommand:''});
 await assert.rejects(runner().execute(true),/change.?set.*fix/i);
 trustedWorkspace(dir,'unused',{changeSet:'snapshots.yml',diffCommand:'',args:['--fix']});
 await assert.rejects(runner().execute(false),/change.?set.*fix/i);
});

test('snapshot findings preserve repository and immutable revision metadata',()=>{
 const parsed=runner().parse(JSON.stringify({errors:[{file:'/checkout/source.go',line:3,ruleId:'then_missing',message:'co-change required',repository:'source',baseRevision:'abcdef',headRevision:'123456',targetPath:'github://team/target/target.go'}]}));
 assert.equal(parsed.findings[0].repository,'source');assert.equal(parsed.findings[0].baseRevision,'abcdef');assert.equal(parsed.findings[0].headRevision,'123456');
 assert.equal(parsed.findings[0].targetPath,'github://team/target/target.go');
});

test('change-set report identity follows the canonical manifest and retains invocation cwd', {skip:process.platform==='win32'},async t=>{
 const dir=tempWorkspace(t);const binary=executable(dir,`console.log(JSON.stringify({errors:[]}));`);const manifest=path.join(dir,'changes.yml');fs.writeFileSync(manifest,'version: 1\n');const alias=path.join(dir,'alias.yml');fs.symlinkSync(manifest,alias);
 const nested=path.join(dir,'nested');fs.mkdirSync(nested);
 for(const configured of ['.', 'nested']) {
  trustedWorkspace(dir,binary,{changeSet:alias,diffCommand:'',workingDirectory:configured});
  const result=await runner().execute(false);assert.equal(result.reportId,'change-set:'+fs.realpathSync(manifest));assert.equal(result.workspaceRoot,path.resolve(dir,configured));
 }
});

test('shared-manifest runs publish one identity and older runs cannot restore stale reports',async t=>{
 let finishFirst;const reportId='change-set:/tmp/shared.yml';const setup=twoRootRunner(t,resource=>resource.fsPath.endsWith('first')?new Promise(resolve=>finishFirst=resolve):Promise.resolve({findings:[],ok:true,workspaceRoot:resource.fsPath,reportId}));
 const publications=[];setup.lint.diagnostics.publish=(findings,root,owner,identity)=>{publications.push({findings,root,owner,identity});};
 const pending=setup.lint.run(setup.first);await setup.lint.run(setup.second);
 finishFirst({findings:[{message:'stale',headRevision:'old'}],ok:false,workspaceRoot:setup.first.fsPath,reportId});await pending;
 assert.equal(publications.length,1);assert.equal(publications[0].identity,reportId);assert.equal(publications[0].root,setup.second.fsPath);assert.equal(publications[0].owner,setup.second);
});
